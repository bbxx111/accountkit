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
	"github.com/bbxx111/accountkit/migrations"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type syntheticFixture struct {
	UserID       string `json:"user_id"`
	IdentityID   string `json:"identity_id"`
	SessionID    string `json:"session_id"`
	EventID      string `json:"event_id"`
	Phone        string `json:"phone"`
	RefreshToken string `json:"refresh_token"`
}

// seedSyntheticAccount 在当前结构上写入一个合成账号、身份、会话与审计事件，并签发一枚合法 access token；仅用于测试。
func seedSyntheticAccount(t *testing.T, pool *pgxpool.Pool, cfg accountkit.Config) (syntheticFixture, string) {
	t.Helper()
	ctx := context.Background()
	raw, err := os.ReadFile("tests/testdata/synthetic/fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	var f syntheticFixture
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
	// Synthetic rows on the current initial schema.
	now := time.Now().UTC().Truncate(time.Second)
	exec("INSERT INTO user_account(id,state,display_name) VALUES($1,1,'Synthetic user')", f.UserID)
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
	exec("INSERT INTO identity(id,user_id,kind,subject_digest,digest_key_version,subject_ciphertext,cipher_key_version,hint_prefix,hint_suffix) VALUES($1,$2,'PHONE',$3,1,$4,1,'+86138','1234')", f.IdentityID, f.UserID, hex.EncodeToString(mac.Sum(nil)), ct)
	hash := sha256.Sum256([]byte(f.RefreshToken))
	exec("INSERT INTO session(id,user_id,device_id,auth_time,refresh_token_hash,refresh_expire_time) VALUES($1,$2,'synthetic-device',$3,$4,$5)", f.SessionID, f.UserID, now, hash[:], now.Add(24*time.Hour))
	// Synthetic audit row; legacy_event_id is a live field of the audit/authentication decoder.
	exec(`INSERT INTO audit_event(id,source,action,result,actor,targets,data,occur_time)
 VALUES('019a0000-0000-7000-8000-000000000001',$1,'CODE_SENT',1,
 jsonb_build_object('type','USER','namespace',$2::text,'id',$3::text),
 jsonb_build_array(jsonb_build_object('type','USER','namespace',$2::text,'id',$3::text)),
 jsonb_build_object('accountkit',jsonb_build_object('legacy_event_id',$4::text,'actor_kind','USER')),$5)`, cfg.AuditSource, cfg.AuditNamespace, f.UserID, f.EventID, now)
	claims, err := json.Marshal(map[string]any{"sub": f.UserID, "sid": f.SessionID, "scope": "user", "auth_time": now.Unix(), "iat": now.Unix(), "exp": now.Add(15 * time.Minute).Unix(), "iss": cfg.JWTIssuer, "aud": []string{cfg.JWTAudience}, "jti": "00112233445566778899aabbccddeeff"})
	if err != nil {
		t.Fatal(err)
	}
	b64 := base64.RawURLEncoding.EncodeToString
	signed := b64([]byte(`{"alg":"ES256","kid":"1","typ":"at+jwt"}`)) + "." + b64(claims)
	sig, err := jwt.SigningMethodES256.Sign(signed, cfg.JWTKeys[1])
	if err != nil {
		t.Fatal(err)
	}
	return f, signed + "." + b64(sig)
}

func snapshotSyntheticAccount(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var out string
	err := pool.QueryRow(context.Background(), `
 SELECT jsonb_build_object(
 'users',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM user_account t),
 'identities',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM identity t),
 'sessions',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM session t),
 'audit',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM audit_event t),
 'flows',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM sign_in_completion_flow t),
 'version',(SELECT jsonb_agg(to_jsonb(t)) FROM schema_migrations t))::text`).Scan(&out)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
