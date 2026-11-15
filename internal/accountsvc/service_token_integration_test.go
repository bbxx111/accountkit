package accountsvc_test

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/internal/accountsvc/serviceauth"
	"github.com/golang-jwt/jwt/v5"
)

// unifiedServiceClients explicitly replaces the configured registry.
func (f *integrationFixture) unifiedServiceClients() {
	f.t.Helper()
	f.clearServiceClients()
	f.env["ACCOUNTSVC_SERVICE_CLIENTS"] = "BUSINESS,INTRO,CLEANUP,BASIC_ONLY,BEARER_ONLY"
	for _, c := range []struct{ alias, id, methods, permissions, secret string }{
		{"BUSINESS", "business", "basic,bearer", "tokens.introspect,purged_users.read", f.secrets[1]},
		{"INTRO", "intro-only", "basic,bearer", "tokens.introspect", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{21}, 32))},
		{"CLEANUP", "cleanup-only", "basic,bearer", "purged_users.read", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{22}, 32))},
		{"BASIC_ONLY", "basic-only", "basic", "tokens.introspect,purged_users.read", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{23}, 32))},
		{"BEARER_ONLY", "bearer-only", "bearer", "tokens.introspect,purged_users.read", ""},
	} {
		prefix := "ACCOUNTSVC_SERVICE_CLIENT_" + c.alias + "_"
		if strings.Contains(c.methods, "bearer") {
			f.env[prefix+"BEARER_SUBJECTS"] = `["machine"]`
		}
		f.env[prefix+"ID"], f.env[prefix+"AUTH_METHODS"], f.env[prefix+"PERMISSIONS"] = c.id, c.methods, c.permissions
		if c.secret != "" {
			f.env[prefix+"BASIC_SECRETS"] = c.secret
			f.secrets = append(f.secrets, c.secret)
		}
	}
}

func (f *integrationFixture) clearServiceClients() {
	f.t.Helper()
	for name := range f.env {
		if name == "ACCOUNTSVC_SERVICE_CLIENTS" || strings.HasPrefix(name, "ACCOUNTSVC_SERVICE_CLIENT_") {
			delete(f.env, name)
		}
	}
}

func TestUnifiedServiceClientFixtureConfiguration(t *testing.T) {
	f := &integrationFixture{t: t, env: map[string]string{}, secrets: []string{"", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{20}, 32))}}
	f.unifiedServiceClients()
	var clients []serviceauth.ClientConfig
	for _, alias := range strings.Split(f.env["ACCOUNTSVC_SERVICE_CLIENTS"], ",") {
		prefix := "ACCOUNTSVC_SERVICE_CLIENT_" + alias + "_"
		client := serviceauth.ClientConfig{ID: f.env[prefix+"ID"], AuthMethods: strings.Split(f.env[prefix+"AUTH_METHODS"], ","), Permissions: strings.Split(f.env[prefix+"PERMISSIONS"], ",")}
		if raw := f.env[prefix+"BEARER_SUBJECTS"]; raw != "" {
			if err := json.Unmarshal([]byte(raw), &client.BearerSubjects); err != nil {
				t.Fatal(err)
			}
		}
		if raw := f.env[prefix+"BASIC_SECRETS"]; raw != "" {
			client.BasicSecrets = strings.Split(raw, ",")
		}
		clients = append(clients, client)
	}
	if _, err := serviceauth.NewRegistry(clients); err != nil {
		t.Fatalf("real service fixture must satisfy the production registry configuration: %v", err)
	}
}

func serviceBasic(id, secret string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(id+":"+secret))
}

func (f *integrationFixture) serviceRequest(p *serviceProcess, endpoint, authorization, token string, want int) (http.Header, map[string]any) {
	f.t.Helper()
	method, contentType, body := "GET", "", ""
	if endpoint == "/v1/introspect" {
		method, contentType, body = "POST", "application/x-www-form-urlencoded", "token="+token
	}
	headers, out := transportRequest(f.t, f.client, method, p.baseURL+endpoint, contentType, body, map[string]string{"Authorization": authorization}, want)
	if headers.Get("Cache-Control") != "no-store" {
		f.t.Fatalf("service response missing no-store: %v", headers)
	}
	return headers, out
}

func TestServiceIntegrationUnifiedServiceClients(t *testing.T) {
	f := newIntegration(t)
	var down atomic.Bool
	sign := f.serviceTokenIssuer(&down)
	f.unifiedServiceClients()
	basic, bearer := serviceBasic("business", f.secrets[1]), "Bearer "+sign("business")
	p := f.start(nil)
	tokens := f.login(p, "unified-service@example.test")
	access := tokens["access_token"].(string)
	for _, endpoint := range []string{"/v1/introspect", "/v1/purgedUsers"} {
		for _, authorization := range []string{basic, bearer} {
			_, out := f.serviceRequest(p, endpoint, authorization, access, 200)
			if endpoint == "/v1/introspect" && out["active"] != true {
				t.Fatalf("unified consumer introspection: %v", out)
			}
		}
	}
	for _, tc := range []struct {
		endpoint, authorization, code string
		status                        int
		challenge                     string
	}{
		{"/v1/introspect", serviceBasic("cleanup-only", f.env["ACCOUNTSVC_SERVICE_CLIENT_CLEANUP_BASIC_SECRETS"]), "insufficient_scope", 403, ""},
		{"/v1/introspect", "Bearer " + sign("cleanup-only"), "insufficient_scope", 403, `Bearer realm="accountsvc-introspection", error="insufficient_scope"`},
		{"/v1/purgedUsers", serviceBasic("intro-only", f.env["ACCOUNTSVC_SERVICE_CLIENT_INTRO_BASIC_SECRETS"]), "CLIENT_NOT_AUTHORIZED", 403, ""},
		{"/v1/purgedUsers", "Bearer " + sign("intro-only"), "CLIENT_NOT_AUTHORIZED", 403, `Bearer realm="accountsvc-purge-feed", error="insufficient_scope"`},
		{"/v1/introspect", "Bearer " + sign("basic-only"), "invalid_client", 401, "both"},
		{"/v1/purgedUsers", serviceBasic("bearer-only", f.secrets[1]), "INVALID_CLIENT", 401, "both"},
		{"/v1/introspect", "Bearer " + sign("unregistered"), "insufficient_scope", 403, `Bearer realm="accountsvc-introspection", error="insufficient_scope"`},
		{"/v1/introspect", "Bearer " + access, "invalid_token", 401, "invalid_token"},
	} {
		h, out := f.serviceRequest(p, tc.endpoint, tc.authorization, "not-a-consumer-token", tc.status)
		code, _ := out["error"].(string)
		if obj, ok := out["error"].(map[string]any); ok {
			code, _ = obj["reason"].(string)
		}
		if code != tc.code {
			t.Fatalf("service error: %v want %s", out, tc.code)
		}
		switch tc.challenge {
		case "both":
			if len(h.Values("WWW-Authenticate")) != 2 || strings.Contains(h.Get("WWW-Authenticate"), "invalid_token") {
				t.Fatalf("disabled method challenge: %v", h)
			}
		case "invalid_token":
			if !strings.Contains(strings.Join(h.Values("WWW-Authenticate"), ","), `error="invalid_token"`) {
				t.Fatalf("consumer isolation challenge: %v", h)
			}
		default:
			if h.Get("WWW-Authenticate") != tc.challenge {
				t.Fatalf("permission challenge: %v", h)
			}
		}
	}
	// Existing process keeps its immutable policy; restarting applies revocation
	// even to the exact same, still-valid service token.
	f.env["ACCOUNTSVC_SERVICE_CLIENT_BUSINESS_PERMISSIONS"] = "tokens.introspect"
	f.serviceRequest(p, "/v1/purgedUsers", bearer, "", 200)
	p.stop()
	p = f.start(nil)
	for _, authorization := range []string{basic, bearer} {
		f.serviceRequest(p, "/v1/purgedUsers", authorization, "", 403)
		f.serviceRequest(p, "/v1/introspect", authorization, access, 200)
	}
	// Disable one method while another client's Bearer keeps the shared verifier active.
	f.env["ACCOUNTSVC_SERVICE_CLIENT_BUSINESS_AUTH_METHODS"] = "basic"
	delete(f.env, "ACCOUNTSVC_SERVICE_CLIENT_BUSINESS_BEARER_SUBJECTS")
	p.stop()
	p = f.start(nil)
	f.serviceRequest(p, "/v1/introspect", bearer, access, 401)
	f.serviceRequest(p, "/v1/introspect", basic, access, 200)
	newSecret := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{24}, 32))
	f.secrets = append(f.secrets, newSecret)
	f.env["ACCOUNTSVC_SERVICE_CLIENT_BUSINESS_BASIC_SECRETS"] = newSecret + "," + f.secrets[1]
	p.stop()
	p = f.start(nil)
	f.serviceRequest(p, "/v1/introspect", basic, access, 200)
	newBasic := serviceBasic("business", newSecret)
	f.serviceRequest(p, "/v1/introspect", newBasic, access, 200)
	f.env["ACCOUNTSVC_SERVICE_CLIENT_BUSINESS_BASIC_SECRETS"] = newSecret
	p.stop()
	p = f.start(nil)
	f.serviceRequest(p, "/v1/introspect", basic, access, 401)
	f.serviceRequest(p, "/v1/introspect", newBasic, access, 200)
	// Removal of the identity applies independently from signed-token validity.
	f.env["ACCOUNTSVC_SERVICE_CLIENTS"] = "INTRO,CLEANUP,BASIC_ONLY,BEARER_ONLY"
	for _, field := range []string{"ID", "AUTH_METHODS", "PERMISSIONS", "BASIC_SECRETS"} {
		delete(f.env, "ACCOUNTSVC_SERVICE_CLIENT_BUSINESS_"+field)
	}
	p.stop()
	p = f.start(nil)
	f.serviceRequest(p, "/v1/introspect", bearer, access, 403)
	f.serviceRequest(p, "/v1/introspect", newBasic, access, 401)
	f.assertAuditPrivate()
}

func TestServiceIntegrationUnifiedIssuerFailure(t *testing.T) {
	f := newIntegration(t)
	var down atomic.Bool
	sign := f.serviceTokenIssuer(&down)
	f.unifiedServiceClients()
	bearer := "Bearer " + sign("business")
	basic := serviceBasic("business", f.secrets[1])
	down.Store(true)
	p := f.start(nil)
	for _, endpoint := range []string{"/v1/introspect", "/v1/purgedUsers"} {
		h, out := f.serviceRequest(p, endpoint, bearer, "invalid-consumer", 503)
		if h.Get("WWW-Authenticate") != "" {
			t.Fatalf("provider failure challenge: %v", h)
		}
		if endpoint == "/v1/introspect" && out["error"] != "temporarily_unavailable" {
			t.Fatalf("OAuth provider error: %v", out)
		}
		if endpoint == "/v1/purgedUsers" && h.Get("Retry-After") != "1" {
			t.Fatalf("feed retry header: %v", h)
		}
		f.serviceRequest(p, endpoint, basic, "invalid-consumer", 200)
	}
	f.call("GET", p.baseURL+"/readyz", "", nil, 200)
	f.call("GET", p.baseURL+"/healthz", "", nil, 200)
	p.stop()
	if !strings.Contains(p.output.String(), "service token issuer unavailable at startup") {
		t.Fatal("missing issuer startup warning")
	}
}

func TestServiceIntegrationUnifiedUnconfiguredCapabilities(t *testing.T) {
	f := newIntegration(t)
	f.clearServiceClients()
	// Assign directly because fixture extra maps treat an empty string as deletion.
	f.env["ACCOUNTSVC_SERVICE_CLIENTS"] = ""
	p := f.start(nil)
	for _, endpoint := range []string{"/v1/introspect", "/v1/purgedUsers"} {
		h, _ := f.serviceRequest(p, endpoint, "Bearer invalid", "invalid", 503)
		if h.Get("WWW-Authenticate") != "" {
			t.Fatalf("unconfigured challenge: %v", h)
		}
	}
	p.stop()
	for _, marker := range []string{"introspection is not configured", "purged user feed is not configured"} {
		if strings.Count(p.output.String(), marker) != 1 {
			t.Fatalf("capability warning count: %s", p.output.String())
		}
	}
	f.env["ACCOUNTSVC_SERVICE_CLIENTS"] = "CLEANUP"
	f.env["ACCOUNTSVC_SERVICE_CLIENT_CLEANUP_ID"] = "cleanup"
	f.env["ACCOUNTSVC_SERVICE_CLIENT_CLEANUP_AUTH_METHODS"] = "basic"
	f.env["ACCOUNTSVC_SERVICE_CLIENT_CLEANUP_PERMISSIONS"] = "purged_users.read"
	f.env["ACCOUNTSVC_SERVICE_CLIENT_CLEANUP_BASIC_SECRETS"] = f.secrets[1]
	p = f.start(nil)
	f.serviceRequest(p, "/v1/introspect", serviceBasic("cleanup", f.secrets[1]), "invalid", 503)
	f.serviceRequest(p, "/v1/purgedUsers", serviceBasic("cleanup", f.secrets[1]), "", 200)
	p.stop()
	if strings.Count(p.output.String(), "introspection is not configured") != 1 || strings.Contains(p.output.String(), "purged user feed is not configured") || !strings.Contains(p.output.String(), "PERMISSIONS") {
		t.Fatalf("single capability startup warnings: %s", p.output.String())
	}
}

func TestServiceIntegrationUnifiedDatabaseRevocation(t *testing.T) {
	f := newIntegration(t)
	var down atomic.Bool
	sign := f.serviceTokenIssuer(&down)
	f.unifiedServiceClients()
	proxy := newRedisProxy(t, f.redisURL)
	u, err := url.Parse(f.redisURL)
	if err != nil {
		t.Fatal(err)
	}
	u.Host = proxy.ln.Addr().String()
	p := f.start(map[string]string{"ACCOUNTSVC_REDIS_URL": u.String()})
	tokens := f.login(p, "unified-revocation@example.test")
	access := tokens["access_token"].(string)
	basic, bearer := serviceBasic("business", f.secrets[1]), "Bearer "+sign("business")
	proxy.fail(true)
	for _, authorization := range []string{basic, bearer} {
		_, out := f.serviceRequest(p, "/v1/introspect", authorization, access, 200)
		if out["active"] != true {
			t.Fatalf("Redis failure altered DB-backed introspection: %v", out)
		}
	}
	f.call("POST", p.baseURL+"/v1/revoke", "", map[string]string{"token": tokens["refresh_token"].(string)}, 200)
	for _, authorization := range []string{basic, bearer} {
		_, out := f.serviceRequest(p, "/v1/introspect", authorization, access, 200)
		if out["active"] != false || len(out) != 1 {
			t.Fatalf("committed revocation not enforced: %v", out)
		}
	}
	proxy.fail(false)
	p.stop()
	f.assertAuditPrivate()
}

// serviceTokenIssuer starts an HTTPS client-credentials issuer trusted by the service
// process and configures both interfaces to accept its tokens. down makes it answer 503.
func (f *integrationFixture) serviceTokenIssuer(down *atomic.Bool) (sign func(azp string) string) {
	f.t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		f.t.Fatal(err)
	}
	var s *httptest.Server
	s = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if down.Load() {
			http.Error(w, "issuer down", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/.well-known/openid-configuration" {
			_ = json.NewEncoder(w).Encode(map[string]string{"issuer": s.URL, "jwks_uri": s.URL + "/keys"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{"kid": "svc", "kty": "RSA", "alg": "RS256", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
	}))
	s.TLS = &tls.Config{Certificates: []tls.Certificate{f.cert}, MinVersion: tls.VersionTLS12}
	s.StartTLS()
	f.t.Cleanup(s.Close)
	f.env["ACCOUNTSVC_SERVICE_ISSUER"] = s.URL
	f.env["ACCOUNTSVC_SERVICE_AUDIENCE"] = "integration-service"
	f.env["ACCOUNTSVC_SERVICE_CLIENTS"] += ",INTRO_SVC,PURGE_SVC"
	for _, c := range []struct{ alias, id, permission string }{{"INTRO_SVC", "intro-svc", "tokens.introspect"}, {"PURGE_SVC", "purge-svc", "purged_users.read"}} {
		prefix := "ACCOUNTSVC_SERVICE_CLIENT_" + c.alias + "_"
		f.env[prefix+"BEARER_SUBJECTS"] = `["machine"]`
		f.env[prefix+"ID"], f.env[prefix+"AUTH_METHODS"], f.env[prefix+"PERMISSIONS"] = c.id, "bearer", c.permission
	}
	return func(azp string) string {
		now := time.Now()
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": s.URL, "aud": "integration-service", "azp": azp, "sub": "machine", "scope": "tokens.introspect purged_users.read identities.resolve", "exp": now.Add(5 * time.Minute).Unix(), "iat": now.Unix()})
		token.Header["kid"] = "svc"
		raw, err := token.SignedString(key)
		if err != nil {
			f.t.Fatal(err)
		}
		f.secrets = append(f.secrets, raw)
		return raw
	}
}

func TestServiceIntegrationServiceBearerTokens(t *testing.T) {
	f := newIntegration(t)
	var down atomic.Bool
	sign := f.serviceTokenIssuer(&down)
	introToken, purgeToken := sign("intro-svc"), sign("purge-svc")
	form := "token=not-a-consumer-token"
	p := f.start(nil)
	_, out := transportRequest(t, f.client, "POST", p.baseURL+"/v1/introspect", "application/x-www-form-urlencoded", form, map[string]string{"Authorization": "Bearer " + introToken}, 200)
	if out["active"] != false {
		t.Fatalf("bearer introspection: %v", out)
	}
	_, page := transportRequest(t, f.client, "GET", p.baseURL+"/v1/purgedUsers", "", "", map[string]string{"Authorization": "Bearer " + purgeToken}, 200)
	if _, ok := page["next_page_token"].(string); !ok {
		t.Fatalf("bearer purge feed page: %v", page)
	}
	h, denied := transportRequest(t, f.client, "GET", p.baseURL+"/v1/purgedUsers", "", "", map[string]string{"Authorization": "Bearer " + introToken}, 403)
	if errObj, _ := denied["error"].(map[string]any); errObj == nil || !strings.Contains(h.Get("WWW-Authenticate"), `error="insufficient_scope"`) {
		t.Fatalf("introspection client on purge feed: %v %v", h, denied)
	}
	h, _ = transportRequest(t, f.client, "POST", p.baseURL+"/v1/introspect", "application/x-www-form-urlencoded", form, map[string]string{"Authorization": "Bearer " + purgeToken}, 403)
	if !strings.Contains(h.Get("WWW-Authenticate"), `error="insufficient_scope"`) {
		t.Fatalf("purge client on introspection: %v", h)
	}
	p.stop()

	// Issuer outage: the service still starts and becomes ready; bearer calls answer 503,
	// Basic callers and consumer routes keep working.
	down.Store(true)
	p = f.start(nil)
	_, unavailable := transportRequest(t, f.client, "POST", p.baseURL+"/v1/introspect", "application/x-www-form-urlencoded", form, map[string]string{"Authorization": "Bearer " + introToken}, 503)
	if unavailable["error"] != "temporarily_unavailable" {
		t.Fatalf("bearer during issuer outage: %v", unavailable)
	}
	h, _ = transportRequest(t, f.client, "GET", p.baseURL+"/v1/purgedUsers", "", "", map[string]string{"Authorization": "Bearer " + purgeToken}, 503)
	if h.Get("Retry-After") != "1" {
		t.Fatalf("purge feed outage headers: %v", h)
	}
	basic := "Basic " + base64.StdEncoding.EncodeToString([]byte("business:"+f.secrets[1]))
	transportRequest(t, f.client, "POST", p.baseURL+"/v1/introspect", "application/x-www-form-urlencoded", form, map[string]string{"Authorization": basic}, 200)
	p.stop()
	if out := p.output.String(); !strings.Contains(out, "service token issuer unavailable at startup") {
		t.Fatal("issuer outage at startup was not logged")
	}
}

// Without any introspection callers the service still starts; the route answers 503.
func TestServiceIntegrationIntrospectionNotConfigured(t *testing.T) {
	f := newIntegration(t)
	f.clearServiceClients()
	p := f.start(nil)
	basic := "Basic " + base64.StdEncoding.EncodeToString([]byte("business:"+f.secrets[1]))
	h, out := transportRequest(t, f.client, "POST", p.baseURL+"/v1/introspect", "application/x-www-form-urlencoded", "token=not-a-consumer-token", map[string]string{"Authorization": basic}, 503)
	if out["error"] != "introspection_not_configured" || h.Get("WWW-Authenticate") != "" || h.Get("Cache-Control") != "no-store" {
		t.Fatalf("introspection not configured: %v %v", h, out)
	}
	p.stop()
	if !strings.Contains(p.output.String(), "introspection is not configured") {
		t.Fatal("unconfigured introspection was not logged at startup")
	}
}

// Removed settings must fail before dependencies or a listening socket are created.
func TestServiceIntegrationRejectsRemovedSettings(t *testing.T) {
	f := newIntegration(t)
	for _, mixed := range []bool{false, true} {
		for _, name := range []string{"ACCOUNTSVC_INTROSPECTION_BASIC_CLIENTS", "ACCOUNTSVC_INTROSPECTION_BEARER_CLIENTS", "ACCOUNTSVC_PURGE_FEED_BASIC_CLIENTS", "ACCOUNTSVC_PURGE_FEED_BEARER_CLIENTS"} {
			t.Run(name+map[bool]string{false: "/alone", true: "/mixed"}[mixed], func(t *testing.T) {
				saved := f.env
				f.env = make(map[string]string, len(saved))
				for k, v := range saved {
					f.env[k] = v
				}
				defer func() { f.env = saved }()
				if !mixed {
					f.clearServiceClients()
				}
				for _, value := range []string{"{}", "[]", "sensitive-removed-value"} {
					out, err := f.command(map[string]string{name: value})
					if err == nil || !strings.Contains(out, name) || !strings.Contains(out, "removed") || strings.Contains(out, "listener started") || strings.Contains(out, "sensitive-removed-value") {
						t.Fatalf("removed setting startup result err=%v output=%s", err, out)
					}
				}
			})
		}
	}
}
