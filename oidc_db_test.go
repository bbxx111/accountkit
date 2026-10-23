package accountkit_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	mrand "math/rand/v2"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/enum"
)

// TestOIDCSignInEndToEndAgainstRealDB 通过 Config.OIDCProviders 装配真实 OIDC 校验器，
// 覆盖待补全注册、再次登录、绑定以及 Required 为空时直接注册。
func TestOIDCSignInEndToEndAgainstRealDB(t *testing.T) {
	dsn := dbDSN(t)
	ctx := context.Background()
	schema := fmt.Sprintf("authtest_%08x", mrand.Uint32())
	poolCfg, err := accountkit.PoolConfig(dsn, schema)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		pool.Close()
	})
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	jwks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{
			"kty": "RSA", "kid": "e2e", "use": "sig", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.PublicKey.E)).Bytes()),
		}}})
	}))
	defer jwks.Close()
	const issuer = "https://sso.corp.example"
	nonceSeq := 0
	idToken := func(sub, email, name string) map[string]any {
		t.Helper()
		nonceSeq++
		nonce := fmt.Sprintf("nonce-%d", nonceSeq)
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
			"iss": issuer, "aud": "corp-web", "sub": sub, "exp": time.Now().Add(10 * time.Minute).Unix(),
			"nonce": nonce, "email": email, "email_verified": true, "name": name,
		})
		tok.Header["kid"] = "e2e"
		s, err := tok.SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		return map[string]any{"kind": "CORP_SSO", "credential": map[string]string{"id_token": s, "nonce": nonce}}
	}

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	captured := &captureSender{}
	build := func(required []enum.IdentityKind) *httptest.Server {
		t.Helper()
		cfg := minimal()
		cfg.Schema = schema
		cfg.RequiredIdentityKinds = required
		cfg.MaxIdentitiesPerKind = 2
		cfg.OIDCProviders = []accountkit.OIDCProvider{{Kind: "CORP_SSO", Issuer: issuer, JWKSURL: jwks.URL, ClientIDs: []string{"corp-web"}}}
		a, err := accountkit.New(cfg, accountkit.Deps{Pool: pool, Redis: rdb, SMSSender: captured, EmailSender: captured, HTTPClient: jwks.Client()})
		if err != nil {
			t.Fatal(err)
		}
		if err := a.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		r := chi.NewRouter()
		r.Mount("/v1", a.EndUserHandler())
		srv := httptest.NewServer(r)
		t.Cleanup(srv.Close)
		return srv
	}
	srv := build(nil)
	call := func(srv *httptest.Server, path, bearer string, body any, dev string) (int, map[string]any) {
		t.Helper()
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", srv.URL+path, bytes.NewReader(b))
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		if dev != "" {
			req.Header.Set("X-Device-Id", dev)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		captured.rememberChallenge(body, out)
		return resp.StatusCode, out
	}

	// 1. 默认 Required：首次 OIDC 登录待补全，补邮箱后注册并按声明填充显示名。
	status, pending := call(srv, "/v1/users:signInWithIdp", "", idToken("corp-1", "corp1@example.test", "Corp One"), "oidc-dev-1")
	if status != 200 || pending["next_action"] != "COMPLETE_IDENTITY" || pending["hint_email"] != "corp1@example.test" {
		t.Fatalf("pending: %d %v", status, pending)
	}
	flow := pending["sign_in_completion_token"].(string)
	const mail = "corp1@example.test"
	if status, out := call(srv, "/v1/users:sendSignInCompletionCode", "", map[string]string{"sign_in_completion_token": flow, "channel": "EMAIL", "target": mail}, "oidc-dev-1"); status != 200 {
		t.Fatalf("completion code: %d %v", status, out)
	}
	status, done := call(srv, "/v1/users:completeSignIn", "", map[string]any{"sign_in_completion_token": flow, "kind": "EMAIL", "credential": map[string]string{"target": mail, "code_id": captured.codeID(mail), "code": captured.code(mail)}}, "oidc-dev-1")
	if status != 200 || done["is_new_user"] != true || done["scope"] != "user" {
		t.Fatalf("complete: %d %v", status, done)
	}
	userID := done["user_id"].(string)
	var displayName *string
	if err := pool.QueryRow(ctx, "SELECT display_name FROM user_account WHERE id=$1", userID).Scan(&displayName); err != nil || displayName == nil || *displayName != "Corp One" {
		t.Fatalf("display name: %v %v", displayName, err)
	}
	var kinds []string
	if err := pool.QueryRow(ctx, "SELECT array_agg(kind ORDER BY kind) FROM identity WHERE user_id=$1 AND delete_time IS NULL", userID).Scan(&kinds); err != nil || fmt.Sprint(kinds) != "[CORP_SSO EMAIL]" {
		t.Fatalf("identities: %v %v", kinds, err)
	}

	// 2. 同一 sub 再次登录命中同一账号。
	status, again := call(srv, "/v1/users:signInWithIdp", "", idToken("corp-1", "", "Renamed"), "oidc-dev-2")
	if status != 200 || again["user_id"] != userID || again["is_new_user"] != false {
		t.Fatalf("second sign-in: %d %v", status, again)
	}

	// 3. 已登录账号绑定另一个 OIDC 主体，之后用它登录到同一账号。
	access := again["access_token"].(string)
	if status, out := call(srv, "/v1/users/me/identities", access, idToken("corp-2", "", ""), ""); status != 201 || out["kind"] != "CORP_SSO" {
		t.Fatalf("bind: %d %v", status, out)
	}
	if status, out := call(srv, "/v1/users:signInWithIdp", "", idToken("corp-2", "", ""), "oidc-dev-3"); status != 200 || out["user_id"] != userID {
		t.Fatalf("bound sign-in: %d %v", status, out)
	}

	// 4. Required 为空时首次 OIDC 登录直接注册。
	direct := build([]enum.IdentityKind{})
	status, created := call(direct, "/v1/users:signInWithIdp", "", idToken("corp-3", "", "Direct"), "oidc-dev-4")
	if status != 200 || created["is_new_user"] != true || created["scope"] != "user" || created["user_id"] == userID {
		t.Fatalf("direct register: %d %v", status, created)
	}
	if status, out := call(direct, "/v1/users:signInWithIdp", "", map[string]any{"kind": "CORP_SSO", "credential": map[string]string{"id_token": "x"}}, "oidc-dev-5"); status != 400 || out["error"].(map[string]any)["reason"] != "CREDENTIAL_INCOMPLETE" {
		t.Fatalf("malformed credential through the real provider: %d %v", status, out)
	}
}
