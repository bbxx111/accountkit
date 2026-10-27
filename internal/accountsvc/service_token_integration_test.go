package accountsvc_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

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
	f.env["ACCOUNTSVC_SERVICE_TOKEN_ISSUER"] = s.URL
	f.env["ACCOUNTSVC_SERVICE_TOKEN_AUDIENCE"] = "integration-service"
	f.env["ACCOUNTSVC_INTROSPECTION_TOKEN_CLIENTS"] = `["intro-svc"]`
	f.env["ACCOUNTSVC_PURGE_FEED_TOKEN_CLIENTS"] = `["purge-svc"]`
	return func(azp string) string {
		now := time.Now()
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": s.URL, "aud": "integration-service", "azp": azp, "exp": now.Add(5 * time.Minute).Unix(), "iat": now.Unix()})
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
