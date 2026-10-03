package adminauth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bbxx111/accountkit"
	"github.com/golang-jwt/jwt/v5"
)

type oidcFixture struct {
	t              *testing.T
	server         *httptest.Server
	key            *rsa.PrivateKey
	mu             sync.Mutex
	keys           []map[string]any
	requests       int
	fail           bool
	issuerOverride string
	jwksOverride   string
	body           string
	entered        chan struct{}
	release        chan struct{}
}

func newFixture(t *testing.T) *oidcFixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &oidcFixture{t: t, key: key}
	f.keys = []map[string]any{jwk("initial", &key.PublicKey)}
	f.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		if r.URL.Path == "/.well-known/openid-configuration" {
			issuer, address := f.server.URL, f.server.URL+"/keys"
			if f.issuerOverride != "" {
				issuer = f.issuerOverride
			}
			if f.jwksOverride != "" {
				address = f.jwksOverride
			}
			f.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": issuer, "jwks_uri": address})
			return
		}
		if r.URL.Path != "/keys" {
			f.mu.Unlock()
			http.NotFound(w, r)
			return
		}
		f.requests++
		fail, keys, body, entered, release := f.fail, f.keys, f.body, f.entered, f.release
		f.mu.Unlock()
		if entered != nil {
			entered <- struct{}{}
			<-release
		}
		if fail {
			http.Error(w, "synthetic upstream failure", 503)
			return
		}
		if body != "" {
			_, _ = w.Write([]byte(body))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": keys})
	}))
	t.Cleanup(f.server.Close)
	return f
}

func jwk(kid string, key *rsa.PublicKey) map[string]any {
	return map[string]any{"kid": kid, "kty": "RSA", "use": "sig", "alg": "RS256", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}
}

func (f *oidcFixture) config() Config { return Config{Issuer: f.server.URL, Audience: "admin-api"} }
func (f *oidcFixture) count() int     { f.mu.Lock(); defer f.mu.Unlock(); return f.requests }
func (f *oidcFixture) verifier(opts Options) *Verifier {
	f.t.Helper()
	opts.HTTPClient = f.server.Client()
	v, err := New(context.Background(), f.config(), opts)
	if err != nil {
		f.t.Fatal(err)
	}
	return v
}
func (f *oidcFixture) claims(now time.Time) jwt.MapClaims {
	return jwt.MapClaims{"iss": f.server.URL, "aud": "admin-api", "sub": "external-admin", "exp": now.Add(3 * time.Hour).Unix(), "iat": now.Unix(), "roles": []string{"operator"}, "preferred_username": "Operator"}
}
func (f *oidcFixture) token(claims jwt.MapClaims, kid string) string {
	f.t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = kid
	signed, err := token.SignedString(f.key)
	if err != nil {
		f.t.Fatal(err)
	}
	return signed
}
func request(v *Verifier, token, role string) *httptest.ResponseRecorder {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	var handler http.Handler = h
	if role != "" {
		handler = v.RequireRole(role)(handler)
	}
	handler = v.Middleware(handler)
	r := httptest.NewRequest("GET", "https://accountsvc.test/admin/v1/users", nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestJWTValidation(t *testing.T) {
	f := newFixture(t)
	now := time.Now().Truncate(time.Second)
	v := f.verifier(Options{Now: func() time.Time { return now }})
	tests := []struct {
		name string
		edit func(jwt.MapClaims)
		want int
	}{
		{"valid", func(c jwt.MapClaims) {}, 204},
		{"consumer", func(c jwt.MapClaims) { c["aud"] = "consumer-api"; c["sub"] = "u_0000000000000" }, 401},
		{"id-token", func(c jwt.MapClaims) { c["aud"] = "login-client" }, 401},
		{"wrong-issuer", func(c jwt.MapClaims) { c["iss"] = "https://wrong.test" }, 401},
		{"empty-subject", func(c jwt.MapClaims) { c["sub"] = "" }, 401},
		{"missing-subject", func(c jwt.MapClaims) { delete(c, "sub") }, 401},
		{"missing-exp", func(c jwt.MapClaims) { delete(c, "exp") }, 401},
		{"expired", func(c jwt.MapClaims) { c["exp"] = now.Add(-31 * time.Second).Unix() }, 401},
		{"expiration-leeway", func(c jwt.MapClaims) { c["exp"] = now.Add(-29 * time.Second).Unix() }, 204},
		{"future-nbf", func(c jwt.MapClaims) { c["nbf"] = now.Add(31 * time.Second).Unix() }, 401},
		{"future-iat", func(c jwt.MapClaims) { c["iat"] = now.Add(31 * time.Second).Unix() }, 401},
		{"time-leeway", func(c jwt.MapClaims) {
			c["nbf"] = now.Add(29 * time.Second).Unix()
			c["iat"] = now.Add(29 * time.Second).Unix()
		}, 204},
		{"bad-time-type", func(c jwt.MapClaims) { c["iat"] = "yesterday" }, 401},
		{"null-iat", func(c jwt.MapClaims) { c["iat"] = nil }, 401},
		{"null-nbf", func(c jwt.MapClaims) { c["nbf"] = nil }, 401},
		{"zero-iat", func(c jwt.MapClaims) { c["iat"] = 0 }, 204},
		{"missing-roles", func(c jwt.MapClaims) { delete(c, "roles") }, 204},
		{"wrong-roles-type", func(c jwt.MapClaims) { c["roles"] = "operator" }, 401},
		{"wrong-role-element", func(c jwt.MapClaims) { c["roles"] = []any{"operator", 1} }, 401},
		{"username-optional", func(c jwt.MapClaims) { delete(c, "preferred_username") }, 204},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims := f.claims(now)
			tt.edit(claims)
			w := request(v, f.token(claims, "initial"), "")
			if w.Code != tt.want {
				t.Fatalf("status %d, want %d: %s", w.Code, tt.want, w.Body.String())
			}
			if tt.want == 401 && !strings.HasPrefix(w.Header().Get("WWW-Authenticate"), "Bearer") {
				t.Fatal("missing Bearer challenge")
			}
		})
	}
	t.Run("forged-signature", func(t *testing.T) {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, f.claims(now))
		token.Header["kid"] = "initial"
		signed, err := token.SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		if w := request(v, signed, ""); w.Code != 401 {
			t.Fatalf("status %d", w.Code)
		}
	})
	t.Run("algorithm-and-remote-key-headers", func(t *testing.T) {
		for _, alg := range []jwt.SigningMethod{jwt.SigningMethodHS256, jwt.SigningMethodNone, jwt.SigningMethodRS512} {
			token := jwt.NewWithClaims(alg, f.claims(now))
			token.Header["kid"] = "initial"
			var key any = f.key
			if alg == jwt.SigningMethodHS256 {
				key = []byte("synthetic-secret")
			}
			if alg == jwt.SigningMethodNone {
				key = jwt.UnsafeAllowNoneSignatureType
			}
			signed, err := token.SignedString(key)
			if err != nil {
				t.Fatal(err)
			}
			if w := request(v, signed, ""); w.Code != 401 {
				t.Fatalf("%s status %d", alg.Alg(), w.Code)
			}
		}
		for _, header := range []string{"jku", "x5u"} {
			token := jwt.NewWithClaims(jwt.SigningMethodRS256, f.claims(now))
			token.Header["kid"] = "initial"
			token.Header[header] = "https://untrusted.test/key"
			signed, err := token.SignedString(f.key)
			if err != nil {
				t.Fatal(err)
			}
			if w := request(v, signed, ""); w.Code != 401 {
				t.Fatalf("%s status %d", header, w.Code)
			}
		}
	})
}

func TestPrincipalRolesAndForbidden(t *testing.T) {
	f := newFixture(t)
	now := time.Now().Truncate(time.Second)
	var forbidden atomic.Int32
	v := f.verifier(Options{Now: func() time.Time { return now }, OnForbidden: func(r *http.Request, p accountkit.AdminPrincipal) {
		if p.Subject != "external-admin" || p.Issuer != f.server.URL {
			t.Error("wrong audited principal")
		}
		forbidden.Add(1)
	}})
	claims := f.claims(now)
	if w := request(v, f.token(claims, "initial"), "super-admin"); w.Code != 403 {
		t.Fatalf("operator escalated: %d", w.Code)
	}
	if forbidden.Load() != 1 {
		t.Fatal("role rejection must audit exactly once")
	}
	claims["roles"] = []string{"super-admin"}
	if w := request(v, f.token(claims, "initial"), "operator"); w.Code != 204 {
		t.Fatalf("super-admin lacks operator: %d", w.Code)
	}
	h := v.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := PrincipalFrom(r.Context())
		if !ok || p.Issuer != f.server.URL || p.Subject != "external-admin" || p.Username != "Operator" {
			t.Fatalf("wrong principal: %+v %v", p, ok)
		}
	}))
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer "+f.token(claims, "initial"))
	h.ServeHTTP(httptest.NewRecorder(), r)
	for _, header := range []string{"", "Basic secret", "Bearer", "Bearer bad token", "Bearer a.b.c"} {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("Authorization", header)
		r.Header.Set("X-Admin-Role", "super-admin")
		w := httptest.NewRecorder()
		v.Middleware(v.RequireRole("operator")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("unauthenticated handler called") }))).ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatalf("header %q: %d", header, w.Code)
		}
	}
	w := httptest.NewRecorder()
	v.RequireRole("operator")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("unguarded role passed") })).ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 401 || forbidden.Load() != 1 {
		t.Fatal("unauthenticated request mis-audited")
	}
	if _, ok := PrincipalFrom(context.Background()); ok {
		t.Fatal("principal without auth")
	}
}

func TestRolesJSONPointer(t *testing.T) {
	f := newFixture(t)
	now := time.Now().Truncate(time.Second)
	cfg := f.config()
	cfg.RolesClaim = "/realm~1access/groups~0list/0/roles"
	cfg.UsernameClaim = "/profile/name"
	v, err := New(context.Background(), cfg, Options{HTTPClient: f.server.Client(), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	claims := f.claims(now)
	delete(claims, "roles")
	claims["realm/access"] = map[string]any{"groups~list": []any{map[string]any{"roles": []string{"operator"}}}}
	claims["profile"] = map[string]any{"name": "Nested"}
	if w := request(v, f.token(claims, "initial"), "operator"); w.Code != 204 {
		t.Fatalf("nested pointer: %d", w.Code)
	}
	delete(claims, "realm/access")
	if w := request(v, f.token(claims, "initial"), "operator"); w.Code != 403 {
		t.Fatalf("missing roles: %d", w.Code)
	}
}

func TestMalformedHeadersDoNotRefreshOrAudit(t *testing.T) {
	f := newFixture(t)
	var forbidden atomic.Int32
	v := f.verifier(Options{OnForbidden: func(*http.Request, accountkit.AdminPrincipal) { forbidden.Add(1) }})
	for _, kid := range []any{nil, 1, "", strings.Repeat("x", 257)} {
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, f.claims(time.Now()))
		if kid != nil {
			token.Header["kid"] = kid
		}
		signed, err := token.SignedString(f.key)
		if err != nil {
			t.Fatal(err)
		}
		if w := request(v, signed, "operator"); w.Code != 401 {
			t.Fatalf("invalid kid %v: %d", kid, w.Code)
		}
	}
	if w := request(v, strings.Repeat("x", 16*1024+1), "operator"); w.Code != 401 {
		t.Fatalf("oversize JWT: %d", w.Code)
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Add("Authorization", "Bearer "+f.token(f.claims(time.Now()), "initial"))
	r.Header.Add("Authorization", "Bearer "+f.token(f.claims(time.Now()), "initial"))
	w := httptest.NewRecorder()
	v.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("ambiguous header reached handler") })).ServeHTTP(w, r)
	if w.Code != 401 || f.count() != 1 || forbidden.Load() != 0 {
		t.Fatalf("malformed token caused refresh or audit: status=%d downloads=%d audits=%d", w.Code, f.count(), forbidden.Load())
	}
	// Dependency failures are not authenticated role denials either.
	f.mu.Lock()
	f.fail = true
	f.mu.Unlock()
	if w := request(v, f.token(f.claims(time.Now()), "unknown"), "operator"); w.Code != 503 || forbidden.Load() != 0 {
		t.Fatal("dependency failure misclassified as role denial")
	}
}

func TestConfigAndStartupRejectUnsafeMetadata(t *testing.T) {
	for _, cfg := range []Config{{}, {Issuer: "http://issuer.test", Audience: "admin"}, {Issuer: "https://u:p@issuer.test", Audience: "admin"}, {Issuer: "https://issuer.test?q=1", Audience: "admin"}, {Issuer: "https://issuer.test#frag", Audience: "admin"}, {Issuer: "https://issuer.test#", Audience: "admin"}, {Issuer: "https://issuer.test", Audience: " "}, {Issuer: "https://issuer.test", Audience: "admin", RolesClaim: "roles"}, {Issuer: "https://issuer.test", Audience: "admin", RolesClaim: "/bad~2pointer"}} {
		if cfg.Validate() == nil {
			t.Fatalf("unsafe config accepted: %+v", cfg)
		}
	}
	for _, name := range []string{"issuer-mismatch", "http-jwks", "weak-key", "duplicate-key", "empty-jwks", "large-response", "bad-json", "upstream-failure"} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			switch name {
			case "issuer-mismatch":
				f.issuerOverride = "https://wrong.test"
			case "http-jwks":
				f.jwksOverride = "http://insecure.test/keys"
			case "weak-key":
				key, err := rsa.GenerateKey(rand.Reader, 1024)
				if err != nil {
					t.Fatal(err)
				}
				f.keys = []map[string]any{jwk("weak", &key.PublicKey)}
			case "duplicate-key":
				f.keys = append(f.keys, f.keys[0])
			case "empty-jwks":
				f.keys = nil
			case "large-response":
				f.body = strings.Repeat(" ", 256*1024+1)
			case "bad-json":
				f.body = `{"keys":[]} {}`
			case "upstream-failure":
				f.fail = true
			}
			if _, err := New(context.Background(), f.config(), Options{HTTPClient: f.server.Client()}); err == nil {
				t.Fatal("unsafe discovery/JWKS accepted")
			}
		})
	}
}
