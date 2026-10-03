package accountkit_test

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
	"github.com/jackc/pgx/v5/pgxpool"
)

type sourceFixture struct {
	UserID       string `json:"user_id"`
	IdentityID   string `json:"identity_id"`
	SessionID    string `json:"session_id"`
	EventID      string `json:"event_id"`
	Phone        string `json:"phone"`
	RefreshToken string `json:"refresh_token"`
}

func seedSourceFixture(t *testing.T, pool *pgxpool.Pool, cfg accountkit.Config) (sourceFixture, string) {
	t.Helper()
	ctx := context.Background()
	raw, err := os.ReadFile("tests/testdata/source-baseline/fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	var f sourceFixture
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	ddl, err := os.ReadFile("tests/testdata/source-baseline/0001_init.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("CREATE SCHEMA " + cfg.Schema)
	// Separate source schema installation from accountkit's migrator.
	exec(string(ddl))
	exec("CREATE TABLE schema_migrations(version bigint NOT NULL PRIMARY KEY, dirty boolean NOT NULL)")
	exec("INSERT INTO schema_migrations VALUES(1,false)")
	now := time.Now().UTC().Truncate(time.Second)
	exec("INSERT INTO user_account(id,state,display_name) VALUES($1,1,'Source user')", f.UserID)
	mac := hmac.New(sha256.New, cfg.SubjectHMACKeys[1])
	mac.Write([]byte(f.Phone))
	block, err := aes.NewCipher(cfg.SubjectCipherKeys[1])
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, gcm.NonceSize()) // Synthetic fixture only.
	ct := gcm.Seal(nonce, nonce, []byte(f.Phone), nil)
	exec("INSERT INTO identity(id,user_id,kind,subject_digest,digest_key_version,subject_ciphertext,cipher_key_version,hint_prefix,hint_suffix) VALUES($1,$2,1,$3,1,$4,1,'+86138','1234')", f.IdentityID, f.UserID, hex.EncodeToString(mac.Sum(nil)), ct)
	hash := sha256.Sum256([]byte(f.RefreshToken))
	exec("INSERT INTO session(id,user_id,device_id,auth_time,refresh_token_hash,refresh_expire_time) VALUES($1,$2,'source-device',$3,$4,$5)", f.SessionID, f.UserID, now, hash[:], now.Add(24*time.Hour))
	exec("INSERT INTO audit_event(id,event_type,actor_kind,user_id,result) VALUES($1,1,1,$2,1)", f.EventID, f.UserID)
	claims, err := json.Marshal(map[string]any{"sub": f.UserID, "sid": f.SessionID, "scope": "user", "auth_time": now.Unix(), "iat": now.Unix(), "exp": now.Add(15 * time.Minute).Unix(), "iss": cfg.JWTIssuer, "aud": []string{cfg.JWTAudience}, "jti": "00112233445566778899aabbccddeeff"})
	if err != nil {
		t.Fatal(err)
	}
	b64 := base64.RawURLEncoding.EncodeToString
	signed := b64([]byte(`{"alg":"HS256","kid":"1","typ":"JWT"}`)) + "." + b64(claims)
	mac = hmac.New(sha256.New, cfg.JWTKeys[1])
	mac.Write([]byte(signed))
	return f, signed + "." + b64(mac.Sum(nil))
}

func snapshotSource(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var out string
	err := pool.QueryRow(context.Background(), `
 SELECT jsonb_build_object(
 'users',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM user_account t),
 'identities',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM identity t),
 'sessions',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM session t),
 'audit',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM audit_event t),
 'version',(SELECT jsonb_agg(to_jsonb(t)) FROM schema_migrations t))::text`).Scan(&out)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Catches renamed tables/columns, migration resets, changed encryption or token wire formats.
func TestSourceDatabaseTakeover(t *testing.T) {
	a, pool, sent := integrationInstance(t, minimal(), testRedis(t))
	f, access := seedSourceFixture(t, pool, a.Config())
	before := snapshotSource(t, pool)
	ctx := context.Background()
	for range 2 {
		if err := a.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if got := snapshotSource(t, pool); got != before {
		t.Fatal("migration changed source data or version")
	}
	principal, err := a.Users().Authenticate(ctx, access)
	if err != nil || principal.UserID != f.UserID {
		t.Fatalf("source access: %v", err)
	}
	me, err := a.Users().GetMe(ctx, f.UserID)
	if err != nil || me.ID != f.UserID {
		t.Fatalf("source user: %v", err)
	}
	revealed, err := a.Users().RevealIdentity(ctx, user.Admin{}, f.UserID, f.IdentityID, user.Meta{IP: "127.0.0.1"})
	if err != nil || revealed.Subject != f.Phone {
		t.Fatalf("source ciphertext: %v", err)
	}
	tok, err := a.Users().Refresh(ctx, f.RefreshToken, user.Meta{IP: "127.0.0.1"})
	if err != nil || tok.UserID != f.UserID || tok.Scope != "user" || tok.RefreshToken == f.RefreshToken {
		t.Fatalf("source refresh: %v", err)
	}
	// Old digest lookup must resolve the original account, never register a duplicate.
	if err := a.Users().SendSignInCode(ctx, enum.IdentityPhone, f.Phone, user.Meta{IP: "127.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	signed, err := a.Users().SignInWithCode(ctx, enum.IdentityPhone, f.Phone, sent.code(f.Phone), user.Device{ID: "takeover-device"}, user.Meta{IP: "127.0.0.1"})
	if err != nil || signed.UserID != f.UserID || signed.IsNewUser {
		t.Fatalf("source digest lookup created another account: user=%s new=%v err=%v", signed.UserID, signed.IsNewUser, err)
	}
	var users, identities int
	if err := pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM user_account),(SELECT count(*) FROM identity)").Scan(&users, &identities); err != nil {
		t.Fatal(err)
	}
	if users != 1 || identities != 1 {
		t.Fatalf("duplicate source identity: users=%d identities=%d", users, identities)
	}
}
