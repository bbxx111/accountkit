package accountsvc

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/sender"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// Only delivery is replaced: the service still authenticates an actual account
// with a token issued by the real sign-in transaction and the configured keys.
type profileHTTPDelivery struct{ code string }

func (d *profileHTTPDelivery) SendEmail(_ context.Context, _ string, message sender.CodeMessage) error {
	d.code = message.Code
	return nil
}

func TestServiceProfileAttributesHTTP(t *testing.T) {
	dsn := os.Getenv("SERVER_TEST_DB_DSN")
	if dsn == "" {
		t.Skip("SERVER_TEST_DB_DSN not set")
	}
	serveEnvironment(t)
	schema := fmt.Sprintf("svc_profile_%08x", rand.Uint32())
	t.Setenv("ACCOUNTSVC_DATABASE_URL", dsn)
	t.Setenv("ACCOUNTKIT_AUTH_SCHEMA", schema)
	t.Setenv("ACCOUNTKIT_ENABLED_IDENTITY_KINDS", "EMAIL")
	t.Setenv("ACCOUNTKIT_REQUIRED_IDENTITY_KINDS", "EMAIL")
	t.Setenv("ACCOUNTSVC_PROFILE_ATTRIBUTES_SCHEMA_FILE", profileSchemaFile(t, serviceProfileSchema))
	cfg, err := LoadConfig("serve")
	if err != nil {
		t.Fatal(err)
	}
	pc, err := accountkit.PoolConfig(cfg.DatabaseURL, cfg.Library.Schema)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(t.Context(), pc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		defer pool.Close()
		if _, err := pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	delivery := &profileHTTPDelivery{}
	setupCfg := cfg.Library
	setupCfg.AuditSource, setupCfg.AuditNamespace = "test/service-profile", "test/service-profile/resources"
	setup, err := accountkit.New(setupCfg, accountkit.Deps{Pool: pool, Redis: rdb, EmailSender: delivery, Audit: audit.Noop{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(setup.Close)
	if err := setup.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	const target = "service-profile@example.test"
	challenge, err := setup.Users().SendSignInCode(t.Context(), enum.IdentityEmail, target, user.Meta{IP: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	signed, err := setup.Users().SignInWithCode(t.Context(), user.CodeCredential{Channel: enum.IdentityEmail, Target: target, CodeID: challenge.CodeID, Code: delivery.code}, user.Device{ID: "service-profile"}, user.Meta{IP: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	newHandler := func(cfg Config) http.Handler {
		t.Helper()
		app, err := newApplication(cfg, &dependencies{pool: pool, redis: rdb}, logger)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(app.close)
		return serviceHandler(cfg, app.endUser, app.admin, app.introspect, app.purgeFeed, app.jwks, &healthState{})
	}
	call := func(h http.Handler, method, path, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+signed.Tokens.AccessToken)
		r.Header.Set("X-Request-Id", "service-profile-request")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s %s: status=%d want=%d body=%s", method, path, w.Code, status, w.Body.String())
		}
		if strings.HasPrefix(path, "/v1/users/me/profile") && (w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Request-Id") == "") {
			t.Fatal("service profile lost response metadata")
		}
		return w
	}
	h := newHandler(cfg)
	w := call(h, "GET", "/v1/users/me/profile", "", 200)
	if !strings.Contains(w.Body.String(), `"profile_attributes":{}`) {
		t.Fatal("new service account has no empty profile resource")
	}
	const document = `{"bio":"synthetic-service-private"}`
	w = call(h, "PATCH", "/v1/users/me/profile", `{"profile_attributes":`+document+`}`, 200)
	if !strings.Contains(w.Body.String(), document) || !strings.Contains(w.Body.String(), `"name":"users/`+signed.Tokens.UserID+`/profile"`) {
		t.Fatal("service PATCH did not round-trip the complete resource")
	}
	call(h, "GET", "/users/me/profile", "", 404)
	w = call(h, "GET", "/v1/users/me", "", 200)
	if strings.Contains(w.Body.String(), "profile_attributes") || strings.Contains(w.Body.String(), "synthetic-service-private") {
		t.Fatal("service ordinary user resource leaked profile attributes")
	}
	var stored string
	if err := pool.QueryRow(t.Context(), `SELECT profile_attributes->>'bio' FROM user_account WHERE id=$1`, signed.Tokens.UserID).Scan(&stored); err != nil || stored != "synthetic-service-private" {
		t.Fatalf("service PATCH not persisted: %q %v", stored, err)
	}

	// LoadConfig's empty setting disables the service capability without deleting
	// existing stored attributes; re-enabling the same snapshot restores access.
	t.Setenv("ACCOUNTSVC_PROFILE_ATTRIBUTES_SCHEMA_FILE", "")
	disabledCfg, err := LoadConfig("serve")
	if err != nil {
		t.Fatal(err)
	}
	disabled := newHandler(disabledCfg)
	for _, method := range []string{"GET", "PATCH"} {
		w = call(disabled, method, "/v1/users/me/profile", `{"profile_attributes":{}}`, 503)
		if !strings.Contains(w.Body.String(), "PROFILE_ATTRIBUTES_NOT_CONFIGURED") {
			t.Fatal("disabled service did not return the capability error")
		}
	}
	if err := pool.QueryRow(t.Context(), `SELECT profile_attributes->>'bio' FROM user_account WHERE id=$1`, signed.Tokens.UserID).Scan(&stored); err != nil || stored != "synthetic-service-private" {
		t.Fatal("disabling service configuration changed stored profile")
	}
	w = call(h, "GET", "/v1/users/me/profile", "", 200)
	if !strings.Contains(w.Body.String(), document) {
		t.Fatal("loaded service schema snapshot changed after environment update")
	}
}
