package accountkit_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"time"
)

func policyWeChatConfig(t *testing.T) accountkit.Config {
	t.Helper()
	wx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"access_token":"synthetic","openid":"open-%s","unionid":"%s"}`, r.URL.Query().Get("code"), r.URL.Query().Get("code"))
	}))
	t.Cleanup(wx.Close)
	cfg := minimal()
	cfg.EnabledIdentityKinds = []enum.IdentityKind{enum.IdentityEmail, enum.IdentityWeChat}
	cfg.RequiredIdentityKinds = []enum.IdentityKind{enum.IdentityEmail}
	cfg.WeChatApps = []accountkit.WeChatApp{{AppID: "test-wechat", Secret: "synthetic"}}
	cfg.WeChatAPIBaseURL = wx.URL
	return cfg
}

func policyEmailSignIn(t *testing.T, a *accountkit.Auth, sent *captureSender, target, device string) user.SignInResult {
	t.Helper()
	ctx := context.Background()
	meta := user.Meta{IP: "127.0.0.1"}
	challenge, err := a.Users().SendSignInCode(ctx, enum.IdentityEmail, target, meta)
	if err != nil {
		t.Fatal(err)
	}
	result, err := a.Users().SignInWithCode(ctx, user.CodeCredential{Channel: enum.IdentityEmail, Target: target, CodeID: challenge.CodeID, Code: sent.code(target)}, user.Device{ID: device}, meta)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func policyIdpSignIn(t *testing.T, a *accountkit.Auth, subject string) user.SignInResult {
	t.Helper()
	result, err := a.Users().SignInWithIdp(context.Background(), user.IdpCredential{Kind: enum.IdentityWeChat, AppID: "test-wechat", Code: subject}, user.Device{ID: "policy-device", Name: "first"}, user.Meta{IP: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func policyCounts(t *testing.T, p *pgxpool.Pool, users, identities, sessions int) {
	t.Helper()
	var u, i, s int
	if err := p.QueryRow(context.Background(), "SELECT (SELECT count(*) FROM user_account),(SELECT count(*) FROM identity),(SELECT count(*) FROM session)").Scan(&u, &i, &s); err != nil {
		t.Fatal(err)
	}
	if u != users || i != identities || s != sessions {
		t.Fatalf("counts users=%d identities=%d sessions=%d want %d/%d/%d", u, i, s, users, identities, sessions)
	}
}

func TestIdentityPolicySignInCompletionAgainstRealDB(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprintf("existing=%v", existing), func(t *testing.T) {
			ctx := context.Background()
			meta := user.Meta{IP: "127.0.0.1"}
			mr := miniredis.RunT(t)
			rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
			t.Cleanup(func() { _ = rdb.Close() })
			a, p, sent := integrationInstance(t, policyWeChatConfig(t), rdb)
			if err := a.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			target := "completion@example.test"
			var uid string
			if existing {
				first := policyEmailSignIn(t, a, sent, target, "existing-device")
				if first.Tokens == nil {
					t.Fatal("email must complete account")
				}
				uid = first.Tokens.UserID
			}
			pending := policyIdpSignIn(t, a, "completion-subject")
			if pending.Pending == nil || pending.Tokens != nil {
				t.Fatal("unknown third party must return only pending")
			}
			if existing {
				policyCounts(t, p, 1, 1, 1)
			} else {
				policyCounts(t, p, 0, 0, 0)
			}
			if existing {
				mr.FastForward(61 * time.Second)
			}
			token := pending.Pending.SignInCompletionToken
			challenge, err := a.Users().SendSignInCompletionCode(ctx, token, enum.IdentityEmail, target, user.Device{}, meta)
			if err != nil {
				t.Fatal(err)
			}
			cred := user.CodeCredential{Channel: enum.IdentityEmail, Target: target, CodeID: challenge.CodeID, Code: sent.code(target)}
			result, err := a.Users().CompleteSignIn(ctx, token, cred, user.Device{}, meta)
			if err != nil || result.Tokens == nil || result.Pending != nil {
				t.Fatalf("complete: %v", err)
			}
			if result.Tokens.Scope != "user" || result.Tokens.IsNewUser == existing || (existing && result.Tokens.UserID != uid) {
				t.Fatal("wrong account, scope or creation result")
			}
			replay, err := a.Users().CompleteSignIn(ctx, token, cred, user.Device{}, meta)
			if err != nil || replay.Tokens == nil || replay.Tokens.RefreshToken != result.Tokens.RefreshToken {
				t.Fatalf("restricted replay: %v", err)
			}
			if existing {
				policyCounts(t, p, 1, 2, 2)
			} else {
				policyCounts(t, p, 1, 2, 1)
			}
		})
	}
}

func TestIdentityPolicyOnlyEmailAndUnrestrictedIdp(t *testing.T) {
	ctx := context.Background()
	cfg := minimal()
	cfg.EnabledIdentityKinds = []enum.IdentityKind{enum.IdentityEmail}
	cfg.RequiredIdentityKinds = []enum.IdentityKind{enum.IdentityEmail}
	a, p, sent := integrationInstance(t, cfg, testRedis(t))
	if err := a.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	result := policyEmailSignIn(t, a, sent, "email-only@example.test", "email-device")
	if result.Tokens == nil || result.Pending != nil || result.Tokens.Scope != "user" {
		t.Fatal("email-only login must succeed")
	}
	policyCounts(t, p, 1, 1, 1)
	cfg = policyWeChatConfig(t)
	cfg.RequiredIdentityKinds = []enum.IdentityKind{}
	b, pb, _ := integrationInstance(t, cfg, testRedis(t))
	if err := b.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	direct := policyIdpSignIn(t, b, "unrestricted")
	if direct.Tokens == nil || direct.Pending != nil || direct.Tokens.Scope != "user" || !direct.Tokens.IsNewUser {
		t.Fatal("explicit empty required permits third party registration")
	}
	policyCounts(t, pb, 1, 1, 1)
}

func TestIdentityPolicyTighteningAndFlowIsolation(t *testing.T) {
	ctx := context.Background()
	meta := user.Meta{IP: "127.0.0.1"}
	rdb := testRedis(t)
	cfg := policyWeChatConfig(t)
	cfg.RequiredIdentityKinds = []enum.IdentityKind{}
	a, p, sent := integrationInstance(t, cfg, rdb)
	if err := a.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	initial := policyIdpSignIn(t, a, "known-incomplete")
	strict := a.Config()
	strict.RequiredIdentityKinds = []enum.IdentityKind{enum.IdentityEmail}
	makeAuth := func(c accountkit.Config) *accountkit.Auth {
		t.Helper()
		b, err := accountkit.New(c, accountkit.Deps{Pool: p, Redis: rdb, SMSSender: sent, EmailSender: sent, Audit: audit.Noop{}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(b.Close)
		return b
	}
	b := makeAuth(strict)
	known := policyIdpSignIn(t, b, "known-incomplete")
	if known.Tokens == nil || known.Tokens.UserID != initial.Tokens.UserID || known.Tokens.Scope != "user:bind" {
		t.Fatal("tightened policy must restrict existing account")
	}
	fresh, err := b.Users().Refresh(ctx, known.Tokens.RefreshToken, meta)
	if err != nil || fresh.Scope != "user:bind" {
		t.Fatalf("tightened refresh: %v", err)
	}
	pending := policyIdpSignIn(t, b, "new-pending")
	if pending.Pending == nil {
		t.Fatal("pending missing")
	}
	for _, field := range []string{"prefix", "issuer", "audience"} {
		t.Run(field, func(t *testing.T) {
			c := strict
			switch field {
			case "prefix":
				c.KeyPrefix = "other:"
			case "issuer":
				c.JWTIssuer = "other"
			case "audience":
				c.JWTAudience = "other"
			}
			other := makeAuth(c)
			_, err := other.Users().SendSignInCompletionCode(ctx, pending.Pending.SignInCompletionToken, enum.IdentityEmail, "isolation@example.test", user.Device{}, meta)
			if !errors.Is(err, user.ErrSignInCompletionInvalid) {
				t.Fatalf("cross context accepted: %v", err)
			}
		})
	}
	other, op, _ := integrationInstance(t, strict, rdb)
	if err := other.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := op.Exec(ctx, "INSERT INTO identity_flow SELECT * FROM "+b.Config().Schema+".identity_flow"); err != nil {
		t.Fatal(err)
	}
	_, err = other.Users().SendSignInCompletionCode(ctx, pending.Pending.SignInCompletionToken, enum.IdentityEmail, "isolation@example.test", user.Device{}, meta)
	if !errors.Is(err, user.ErrSignInCompletionInvalid) {
		t.Fatalf("copied cross-schema flow accepted: %v", err)
	}
}

func TestAccountsProtocolMigrationAgainstRealDB(t *testing.T) {
	ctx := context.Background()
	a, _, sent := integrationInstance(t, policyWeChatConfig(t), testRedis(t))
	if err := a.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	call := func(h http.Handler, method, path, body, bearer string) (*httptest.ResponseRecorder, map[string]any) {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("X-Device-Id", "protocol-device")
		req.Header.Set("X-Test-Admin", "iss|sub|ops|operator")
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec, out
	}
	rec, pending := call(a.EndUserHandler(), "POST", "/accounts:signInWithIdp", `{"wechat":{"app_id":"test-wechat","code":"protocol-subject"}}`, "")
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" || len(pending) != 5 || pending["next_action"] != "COMPLETE_IDENTITY" {
		t.Fatalf("pending protocol status=%d fields=%d", rec.Code, len(pending))
	}
	for _, name := range []string{"sign_in_completion_token", "expire_time", "enabled_identity_kinds", "required_identity_kinds"} {
		if _, ok := pending[name]; !ok {
			t.Fatalf("pending missing %s", name)
		}
	}
	token := pending["sign_in_completion_token"].(string)
	body, _ := json.Marshal(map[string]any{"sign_in_completion_token": token, "channel": "EMAIL", "target": "protocol@example.test"})
	rec, challenge := call(a.EndUserHandler(), "POST", "/accounts:sendSignInCompletionCode", string(body), "")
	if rec.Code != 200 {
		t.Fatalf("completion send status=%d", rec.Code)
	}
	body, _ = json.Marshal(map[string]any{"sign_in_completion_token": token, "email": map[string]any{"target": "protocol@example.test", "code_id": challenge["code_id"], "code": sent.code("protocol@example.test")}})
	rec, pair := call(a.EndUserHandler(), "POST", "/accounts:completeSignIn", string(body), "")
	if rec.Code != 200 || pair["scope"] != "user" || pair["token_type"] != "Bearer" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("completion status=%d", rec.Code)
	}
	bearer := pair["access_token"].(string)
	uid := pair["user_id"].(string)
	rec, me := call(a.EndUserHandler(), "GET", "/accounts/me", "", bearer)
	if rec.Code != 200 || me["name"] != "accounts/"+uid {
		t.Fatal("account resource name did not migrate")
	}
	rec, identities := call(a.EndUserHandler(), "GET", "/accounts/me/identities", "", bearer)
	if rec.Code != 200 {
		t.Fatalf("identities status=%d", rec.Code)
	}
	for _, v := range identities["identities"].([]any) {
		if !strings.HasPrefix(v.(map[string]any)["name"].(string), "accounts/"+uid+"/identities/") {
			t.Fatal("identity resource name did not migrate")
		}
	}
	rec, sessions := call(a.EndUserHandler(), "GET", "/accounts/me/sessions", "", bearer)
	if rec.Code != 200 {
		t.Fatalf("sessions status=%d", rec.Code)
	}
	for _, v := range sessions["sessions"].([]any) {
		if !strings.HasPrefix(v.(map[string]any)["name"].(string), "accounts/"+uid+"/sessions/") {
			t.Fatal("session resource name did not migrate")
		}
	}
	deps := a.DepsForTest()
	deps.AdminVerifier = stubVerifier{}
	deps.AdminPrincipal = stubPrincipalFrom
	admin, err := accountkit.New(a.Config(), deps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	adminHandler := stubVerifier{}.Middleware()(admin.AdminHandler())
	rec, list := call(adminHandler, "GET", "/accounts", "", "")
	if rec.Code != 200 || len(list["users"].([]any)) != 1 || list["users"].([]any)[0].(map[string]any)["name"] != "accounts/"+uid {
		t.Fatalf("admin list status=%d", rec.Code)
	}
	for _, path := range []string{"/users", "/users/me", "/users/me/identities", "/users/me/sessions", "/users:signInWithIdp", "/users:sendSignInCompletionCode", "/users:completeSignIn"} {
		method := "GET"
		if strings.Contains(path, ":") {
			method = "POST"
		}
		rec, _ := call(a.EndUserHandler(), method, path, "{}", bearer)
		if rec.Code != 404 {
			t.Fatalf("old enduser %s status=%d", path, rec.Code)
		}
	}
	rec, _ = call(adminHandler, "GET", "/users/"+uid, "", "")
	if rec.Code != 404 {
		t.Fatalf("old admin status=%d", rec.Code)
	}
	rec, _ = call(a.EndUserHandler(), "POST", "/accounts/"+uid+":freeze", `{"reason":"x"}`, bearer)
	if rec.Code != 404 {
		t.Fatalf("admin route exposed to enduser status=%d", rec.Code)
	}
}
