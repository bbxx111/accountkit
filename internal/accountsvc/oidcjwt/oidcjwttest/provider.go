// Package oidcjwttest provides a local HTTPS OIDC provider for tests of service token
// authentication. It is imported only by tests.
package oidcjwttest

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Audience is the service audience used by Claims.
const Audience = "accountsvc-service"

// Provider serves discovery and a JWKS document signed by one RSA key under kid "initial".
type Provider struct {
	t            *testing.T
	Server       *httptest.Server
	Key          *rsa.PrivateKey
	mu           sync.Mutex
	keys         []map[string]any
	jwksRequests int
	fail         bool
	issuer       string
	block        chan struct{}
}

// New starts a provider that is closed with the test.
func New(t *testing.T) *Provider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &Provider{t: t, Key: key}
	p.keys = []map[string]any{JWK("initial", &key.PublicKey)}
	p.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		fail, issuer, block := p.fail, p.issuer, p.block
		if issuer == "" {
			issuer = p.Server.URL
		}
		if r.URL.Path == "/keys" {
			p.jwksRequests++
		}
		keys := p.keys
		p.mu.Unlock()
		if block != nil {
			<-block
		}
		if fail {
			http.Error(w, "synthetic upstream failure", http.StatusServiceUnavailable)
			return
		}
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": issuer, "jwks_uri": p.Server.URL + "/keys"})
		case "/keys":
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": keys})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(func() {
		p.Unblock()
		p.Server.Close()
	})
	return p
}

// JWK renders an RSA public key as a signing JWK.
func JWK(kid string, key *rsa.PublicKey) map[string]any {
	return map[string]any{"kid": kid, "kty": "RSA", "use": "sig", "alg": "RS256", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}
}

// URL is the issuer.
func (p *Provider) URL() string { return p.Server.URL }

// Client trusts the provider's certificate.
func (p *Provider) Client() *http.Client { return p.Server.Client() }

// SetFail makes discovery and JWKS answer 503.
func (p *Provider) SetFail(fail bool) { p.mu.Lock(); p.fail = fail; p.mu.Unlock() }

// SetIssuer overrides the issuer announced by discovery.
func (p *Provider) SetIssuer(issuer string) { p.mu.Lock(); p.issuer = issuer; p.mu.Unlock() }

// AddKey publishes another kid for the same signing key.
func (p *Provider) AddKey(kid string) {
	p.mu.Lock()
	p.keys = append(p.keys, JWK(kid, &p.Key.PublicKey))
	p.mu.Unlock()
}

// Block holds every provider response until Unblock.
func (p *Provider) Block() { p.mu.Lock(); p.block = make(chan struct{}); p.mu.Unlock() }

// Unblock releases held responses.
func (p *Provider) Unblock() {
	p.mu.Lock()
	if p.block != nil {
		close(p.block)
		p.block = nil
	}
	p.mu.Unlock()
}

// JWKSRequests counts JWKS fetches.
func (p *Provider) JWKSRequests() int { p.mu.Lock(); defer p.mu.Unlock(); return p.jwksRequests }

// Claims returns valid client-credentials claims for azp, issued at now and valid five minutes.
func (p *Provider) Claims(now time.Time, azp string) jwt.MapClaims {
	return jwt.MapClaims{"iss": p.Server.URL, "aud": Audience, "azp": azp, "exp": now.Add(5 * time.Minute).Unix(), "iat": now.Unix()}
}

// Token signs claims with RS256 under kid.
func (p *Provider) Token(claims jwt.MapClaims, kid string) string {
	p.t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = kid
	signed, err := token.SignedString(p.Key)
	if err != nil {
		p.t.Fatal(err)
	}
	return signed
}
