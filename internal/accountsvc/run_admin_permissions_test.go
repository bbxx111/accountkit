package accountsvc

import (
	"crypto/rand"
	"crypto/rsa"
	"github.com/bbxx111/accountkit"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"
)

func TestApplicationAdminRevealErrorsNoStore(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "authentication"}[enabled], func(t *testing.T) {
			serveEnvironment(t)
			t.Setenv(envPrefix+"MODE", "development")
			if enabled {
				t.Setenv(envPrefix+"ADMIN_ENABLED", "true")
				t.Setenv(envPrefix+"ADMIN_ISSUER", "http://127.0.0.1:1")
				t.Setenv(envPrefix+"ADMIN_AUDIENCE", "admin")
			}
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
			defer pool.Close()
			rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
			defer rdb.Close()
			app, err := newApplication(cfg, &dependencies{pool: pool, redis: rdb}, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if err != nil {
				t.Fatal(err)
			}
			defer app.close()
			key, _ := rsa.GenerateKey(rand.Reader, 2048)
			token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": "http://127.0.0.1:1", "aud": "admin", "sub": "synthetic", "exp": time.Now().Add(time.Hour).Unix()})
			token.Header["kid"] = "unknown"
			raw, _ := token.SignedString(key)
			for _, credential := range []string{"", raw} {
				r := httptest.NewRequest("GET", "/users/u_0000000000001/identities/i_0000000000001:reveal", nil)
				if credential != "" {
					r.Header.Set("Authorization", "Bearer "+credential)
				}
				w := httptest.NewRecorder()
				app.admin.ServeHTTP(w, r)
				want := 503
				if enabled && credential == "" {
					want = 401
				}
				if w.Code != want || w.Header().Get("Cache-Control") != "no-store" {
					t.Fatalf("status %d want %d cache=%q", w.Code, want, w.Header().Get("Cache-Control"))
				}
			}
		})
	}
}
