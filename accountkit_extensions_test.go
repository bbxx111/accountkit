package accountkit_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/internal/accountsvc/introspection"
	"github.com/bbxx111/accountkit/user"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestUserExtensionsConfigurationOwnership(t *testing.T) {
	cfg := minimal()
	cfg.UserExtensions = &user.ExtensionsConfig{SchemaVersion: "v1", Schema: []byte(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{},"additionalProperties":false}`)}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	pc, err := pgxpool.ParseConfig("postgres://u:synthetic@127.0.0.1:1/db?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	var attempts atomic.Int32
	pc.BeforeConnect = func(context.Context, *pgx.ConnConfig) error { attempts.Add(1); return errors.New("forbidden") }
	pool, err := pgxpool.NewWithConfig(context.Background(), pc)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	deps := baseDeps(t)
	deps.Pool = pool
	a, err := accountkit.New(cfg, deps)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	cfg.UserExtensions.Schema[0] = 'x'
	cfg.UserExtensions.SchemaVersion = "changed"
	view := a.Config()
	if view.UserExtensions.Schema[0] != '{' || view.UserExtensions.SchemaVersion != "v1" {
		t.Fatal("New retained caller configuration")
	}
	view.UserExtensions.Schema[0] = 'y'
	view.UserExtensions.SchemaVersion = "changed"
	if a.Config().UserExtensions.Schema[0] != '{' || a.Config().UserExtensions.SchemaVersion != "v1" {
		t.Fatal("Config exposes mutable configuration")
	}
	if attempts.Load() != 0 {
		t.Fatal("constructor performed database I/O")
	}
	cfg.UserExtensions.Schema = []byte(`{}`)
	if err := cfg.Validate(); !errors.Is(err, user.ErrInvalidArgument) {
		t.Fatal(err)
	}
}

func TestUserExtensionsHTTPTokenIntrospectionPrivacy(t *testing.T) {
	ctx := context.Background()
	rdb := testRedis(t)
	cfg := minimal()
	cfg.UserExtensions = &user.ExtensionsConfig{SchemaVersion: "synthetic-schema-v1", Schema: []byte(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"synthetic_secret_key":{"type":"string"}},"additionalProperties":false}`)}
	base, pool, sent := integrationInstance(t, cfg, rdb)
	if err := base.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	pc := pool.Config()
	counter := &sessionQueryCounter{}
	pc.ConnConfig.Tracer = counter
	traced, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		t.Fatal(err)
	}
	defer traced.Close()
	auth, err := accountkit.New(base.Config(), accountkit.Deps{Pool: traced, Redis: rdb, SMSSender: sent, EmailSender: sent, Audit: audit.Noop{}, AdminVerifier: stubVerifier{}, AdminPrincipal: stubPrincipalFrom})
	if err != nil {
		t.Fatal(err)
	}
	defer auth.Close()
	enduser := auth.EndUserHandler()
	call := func(h http.Handler, method, path, body, bearer, admin string) map[string]any {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.RemoteAddr = "203.0.113.55:42"
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Device-Id", "extensions-privacy")
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		if admin != "" {
			req.Header.Set("X-Test-Admin", admin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s returned %d: %s", path, w.Code, w.Body.String())
		}
		var result map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	const target = "extensions-private@example.test"
	challengeBody := map[string]string{"channel": "EMAIL", "target": target}
	raw, _ := json.Marshal(challengeBody)
	challenge := call(enduser, "POST", "/users:sendSignInCode", string(raw), "", "")
	sent.rememberChallenge(challengeBody, challenge)
	credential, _ := json.Marshal(map[string]any{"email": map[string]string{"target": target, "code_id": sent.codeID(target), "code": sent.code(target)}})
	signed := call(enduser, "POST", "/users:signInWithCode", string(credential), "", "")
	id := signed["user_id"].(string)
	z := int64(0)
	view, err := auth.Users().PatchExtensions(ctx, id, user.ExtensionsPatch{Document: []byte(`{"synthetic_secret_key":"synthetic-secret-value"}`), ExpectedRevision: &z}, user.Meta{RequestID: "synthetic-private-write"})
	if err != nil || view.Revision != 1 {
		t.Fatalf("extension setup: %+v %v", view, err)
	}
	// Obtain a fresh real token after private data exists, rather than inspecting a pre-write JWT.
	pair, err := auth.Users().Refresh(ctx, signed["refresh_token"].(string), user.Meta{})
	if err != nil {
		t.Fatal(err)
	}
	assertPrivate := func(label string, result any) {
		t.Helper()
		raw, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{"synthetic_secret_key", "synthetic-secret-value", "extensions", "schema_version", "schema_fingerprint", "synthetic-schema-v1", "$schema", "additionalProperties", *view.SchemaFingerprint} {
			if strings.Contains(string(raw), secret) {
				t.Fatalf("%s leaked private extension information %q", label, secret)
			}
		}
	}
	me := call(enduser, "GET", "/users/me", "", pair.AccessToken, "")
	assertPrivate("consumer HTTP", me)
	admin := stubVerifier{}.Middleware()(auth.AdminHandler())
	detail := call(admin, "GET", "/users/"+id, "", "", "https://admin.example.test|synthetic-admin|operator|operator")
	assertPrivate("admin HTTP", detail)
	profiles, err := auth.Users().BatchPublicProfiles(ctx, []string{id})
	if err != nil {
		t.Fatal(err)
	}
	assertPrivate("public profiles", profiles)
	parts := strings.Split(pair.AccessToken, ".")
	if len(parts) != 3 {
		t.Fatal("invalid JWT shape")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims map[string]any
	if err = json.Unmarshal(payload, &claims); err != nil {
		t.Fatal(err)
	}
	if claims["sub"] != id {
		t.Fatal("unexpected JWT subject")
	}
	assertPrivate("actual JWT claims", claims)
	if counter.count.Load() == 0 {
		t.Fatal("query tracer did not observe the real account writes and detail reads")
	}
	counter.count.Store(0)
	principal, err := auth.Users().Authenticate(ctx, pair.AccessToken)
	if err != nil || principal.UserID != id {
		t.Fatalf("authenticate: %+v %v", principal, err)
	}
	if n := counter.count.Load(); n != 0 {
		t.Fatalf("ordinary Authenticate queried DB %d times", n)
	}
	secret := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("s", 32)))
	introspect, err := introspection.New(map[string][]string{"synthetic-service": {secret}}, auth.Users().Authenticate)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/introspect", strings.NewReader(url.Values{"token": {pair.AccessToken}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth("synthetic-service", secret)
	counter.count.Store(0)
	w := httptest.NewRecorder()
	introspect.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("introspection: %d %s", w.Code, w.Body.String())
	}
	var result map[string]any
	if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["active"] != true || result["sub"] != id {
		t.Fatalf("introspection principal: %+v", result)
	}
	assertPrivate("introspection HTTP", result)
	if n := counter.count.Load(); n != 0 {
		t.Fatalf("introspection queried DB %d times", n)
	}
}
