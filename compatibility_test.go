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
	"github.com/bbxx111/accountkit/migrations"
	"github.com/bbxx111/accountkit/user"
	"github.com/google/uuid"
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

func seedFormatFixture(t *testing.T, pool *pgxpool.Pool, cfg accountkit.Config) (sourceFixture, string) {
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
	if err := migrations.Up(ctx, pool.Config().ConnConfig, cfg.Schema); err != nil {
		t.Fatal(err)
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	// Load historical formats onto the current complete initial schema.
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
	// Explicit synthetic projection only; this is not a legacy database upgrade.
	exec(`INSERT INTO audit_event(id,source,action,result,actor,targets,data,occur_time)
 VALUES('019a0000-0000-7000-8000-000000000001',$1,'CODE_SENT',1,
 jsonb_build_object('type','USER','namespace',$2::text,'id',$3::text),
 jsonb_build_array(jsonb_build_object('type','USER','namespace',$2::text,'id',$3::text)),
 jsonb_build_object('accountkit',jsonb_build_object('legacy_event_id',$4::text)),$5)`, cfg.AuditSource, cfg.AuditNamespace, f.UserID, f.EventID, now)
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
 'flows',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM identity_flow t),
 'version',(SELECT jsonb_agg(to_jsonb(t)) FROM schema_migrations t))::text`).Scan(&out)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Catches format regressions and repeat-migration data loss on the current initial schema.
func TestHistoricalFormatsOnInitialSchema(t *testing.T) {
	a, pool, sent := integrationInstance(t, minimal(), testRedis(t))
	f, access := seedFormatFixture(t, pool, a.Config())
	before := snapshotSource(t, pool)
	ctx := context.Background()
	for range 2 {
		if err := a.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var oldData, newData map[string]json.RawMessage
	if err := json.Unmarshal([]byte(before), &oldData); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(snapshotSource(t, pool)), &newData); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"users", "identities", "sessions", "flows", "audit", "version"} {
		if string(oldData[table]) != string(newData[table]) {
			t.Fatalf("migration changed source %s data", table)
		}
	}
	// Historical audit fixtures have no flow association; repeat migration must preserve that.
	var linkedAudit int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_event WHERE data#>>'{accountkit,flow_id}' IS NOT NULL`).Scan(&linkedAudit); err != nil || linkedAudit != 0 {
		t.Fatalf("migration rewrote historical audit flow association: %d %v", linkedAudit, err)
	}
	var version, flows int
	var dirty bool
	if err := pool.QueryRow(ctx, "SELECT version,dirty,(SELECT count(*) FROM identity_flow) FROM schema_migrations").Scan(&version, &dirty, &flows); err != nil || version != 1 || dirty || flows != 0 {
		t.Fatalf("initial version=%d dirty=%v flows=%d: %v", version, dirty, flows, err)
	}
	var mappedID uuid.UUID
	var legacy string
	var action string
	var count int
	if err := pool.QueryRow(ctx, `SELECT id,data#>>'{accountkit,legacy_event_id}',action FROM audit_event`).Scan(&mappedID, &legacy, &action); err != nil || mappedID.Version() != 7 || legacy != f.EventID || action != "CODE_SENT" {
		t.Fatalf("historical mapping: %s %q %s %v", mappedID, legacy, action, err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM audit_event").Scan(&count); err != nil || count != 1 {
		t.Fatalf("historical rows: %d %v", count, err)
	}
	stable := snapshotSource(t, pool)
	if err := a.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if snapshotSource(t, pool) != stable {
		t.Fatal("repeat migration changed current initial snapshot")
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
	challenge, err := a.Users().SendSignInCode(ctx, enum.IdentityPhone, f.Phone, user.Meta{IP: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	signed, err := a.Users().SignInWithCode(ctx, user.CodeCredential{Channel: enum.IdentityPhone, Target: f.Phone, CodeID: challenge.CodeID, Code: sent.code(f.Phone)}, user.Device{ID: "takeover-device"}, user.Meta{IP: "127.0.0.1"})
	if err != nil || signed.Tokens == nil || signed.Tokens.UserID != f.UserID || signed.Tokens.IsNewUser {
		t.Fatalf("source digest lookup did not preserve account: tokens=%v pending=%v err=%v", signed.Tokens != nil, signed.Pending != nil, err)
	}
	var users, identities int
	if err := pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM user_account),(SELECT count(*) FROM identity)").Scan(&users, &identities); err != nil {
		t.Fatal(err)
	}
	if users != 1 || identities != 1 {
		t.Fatalf("duplicate source identity: users=%d identities=%d", users, identities)
	}
}
