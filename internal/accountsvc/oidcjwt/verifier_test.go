package oidcjwt

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type provider struct {
	t              *testing.T
	server         *httptest.Server
	key            *rsa.PrivateKey
	mu             sync.Mutex
	keys           []map[string]any
	discoveries    int
	jwksRequests   int
	fail           bool
	issuerOverride string
	jwksOverride   string
	entered        chan struct{}
	release        chan struct{}
}

// newProvider serves discovery and JWKS over TLS, the production shape.
func newProvider(t *testing.T) *provider { return newProviderWith(t, false) }

// newPlainProvider serves the same endpoints over plain HTTP, as a local development IdP would.
func newPlainProvider(t *testing.T) *provider { return newProviderWith(t, true) }

func newProviderWith(t *testing.T, plain bool) *provider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &provider{t: t, key: key, keys: []map[string]any{jwk("initial", &key.PublicKey)}}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			p.discoveries++
			fail, issuer, jwksURI := p.fail, p.server.URL, p.server.URL+"/keys"
			if p.issuerOverride != "" {
				issuer = p.issuerOverride
			}
			if p.jwksOverride != "" {
				jwksURI = p.jwksOverride
			}
			p.mu.Unlock()
			if fail {
				http.Error(w, "synthetic upstream failure", http.StatusServiceUnavailable)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": issuer, "jwks_uri": jwksURI})
		case "/keys":
			p.jwksRequests++
			fail, keys, entered, release := p.fail, p.keys, p.entered, p.release
			p.mu.Unlock()
			if entered != nil {
				entered <- struct{}{}
				<-release
			}
			if fail {
				http.Error(w, "synthetic upstream failure", http.StatusServiceUnavailable)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": keys})
		default:
			p.mu.Unlock()
			http.NotFound(w, r)
		}
	})
	if plain {
		p.server = httptest.NewServer(handler)
	} else {
		p.server = httptest.NewTLSServer(handler)
	}
	t.Cleanup(p.server.Close)
	return p
}

func jwk(kid string, key *rsa.PublicKey) map[string]any {
	return map[string]any{"kid": kid, "kty": "RSA", "use": "sig", "alg": "RS256", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}
}

func (p *provider) set(f func()) { p.mu.Lock(); f(); p.mu.Unlock() }
func (p *provider) counts() (int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.discoveries, p.jwksRequests
}

func (p *provider) claims(now time.Time) jwt.MapClaims {
	return jwt.MapClaims{"iss": p.server.URL, "aud": "svc", "azp": "client", "exp": now.Add(5 * time.Minute).Unix(), "iat": now.Unix()}
}

func (p *provider) token(claims jwt.MapClaims, kid string) string {
	p.t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = kid
	signed, err := token.SignedString(p.key)
	if err != nil {
		p.t.Fatal(err)
	}
	return signed
}

type clock struct{ unix atomic.Int64 }

func newClock(t time.Time) *clock { c := &clock{}; c.unix.Store(t.Unix()); return c }
func (c *clock) now() time.Time   { return time.Unix(c.unix.Load(), 0) }
func (c *clock) set(t time.Time)  { c.unix.Store(t.Unix()) }
func (p *provider) verifier(t *testing.T, c *clock, logs *bytes.Buffer) *Verifier {
	t.Helper()
	opts := Options{HTTPClient: p.server.Client(), Now: c.now, RefreshInterval: 10 * time.Second}
	if logs != nil {
		opts.Logger = slog.New(slog.NewTextHandler(logs, nil))
	}
	v, err := New(p.server.URL, "svc", opts)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestNewIsLazyAndFirstParseDiscovers(t *testing.T) {
	p := newProvider(t)
	now := time.Now().Truncate(time.Second)
	v := p.verifier(t, newClock(now), nil)
	if d, j := p.counts(); d != 0 || j != 0 {
		t.Fatalf("New performed I/O: %d %d", d, j)
	}
	claims, err := v.Parse(context.Background(), p.token(p.claims(now), "initial"))
	if err != nil || claims["azp"] != "client" {
		t.Fatalf("lazy verification failed: %v", err)
	}
	if d, j := p.counts(); d != 1 || j != 1 {
		t.Fatalf("expected one discovery and one JWKS fetch: %d %d", d, j)
	}
}

func TestLoadDoesNotConsumeCooldown(t *testing.T) {
	p := newProvider(t)
	now := time.Now().Truncate(time.Second)
	v := p.verifier(t, newClock(now), nil)
	if err := v.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Parse(context.Background(), p.token(p.claims(now), "rotated")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown kid after load: %v", err)
	}
	if _, j := p.counts(); j != 2 {
		t.Fatalf("first unknown kid after Load must refresh immediately: %d", j)
	}
}

func TestCooldownAfterSuccessfulAttempt(t *testing.T) {
	p := newProvider(t)
	now := time.Now().Truncate(time.Second)
	c := newClock(now)
	v := p.verifier(t, c, nil)
	unknown := p.token(p.claims(now), "rotated")
	for range 20 {
		if _, err := v.Parse(context.Background(), unknown); !errors.Is(err, ErrInvalid) {
			t.Fatalf("unknown kid within cooldown after success must be invalid: %v", err)
		}
	}
	if d, j := p.counts(); d != 1 || j != 1 {
		t.Fatalf("random kids amplified provider requests: %d %d", d, j)
	}
	p.set(func() { p.keys = append(p.keys, jwk("rotated", &p.key.PublicKey)) })
	c.set(now.Add(9 * time.Second))
	if _, err := v.Parse(context.Background(), unknown); !errors.Is(err, ErrInvalid) {
		t.Fatalf("9s must still throttle: %v", err)
	}
	c.set(now.Add(10 * time.Second))
	if _, err := v.Parse(context.Background(), unknown); err != nil {
		t.Fatalf("rotated key not accepted after cooldown: %v", err)
	}
}

func TestCooldownAfterFailedAttempt(t *testing.T) {
	p := newProvider(t)
	now := time.Now().Truncate(time.Second)
	c := newClock(now)
	v := p.verifier(t, c, nil)
	known := p.token(p.claims(now), "initial")
	if _, err := v.Parse(context.Background(), known); err != nil {
		t.Fatal(err)
	}
	p.set(func() { p.fail = true })
	c.set(now.Add(20 * time.Second))
	missing := p.token(p.claims(now), "missing")
	if _, err := v.Parse(context.Background(), missing); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("failed refresh must be unavailable: %v", err)
	}
	_, before := p.counts()
	for range 10 {
		if _, err := v.Parse(context.Background(), missing); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("failure within cooldown must stay unavailable: %v", err)
		}
	}
	if _, after := p.counts(); after != before {
		t.Fatal("failure cooldown re-fetched")
	}
	if _, err := v.Parse(context.Background(), known); err != nil {
		t.Fatalf("cached key must survive provider failure: %v", err)
	}
	c.set(now.Add(20*time.Second + time.Hour))
	if _, err := v.Parse(context.Background(), known); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expired key must not authenticate when refresh fails: %v", err)
	}
	p.set(func() { p.fail = false })
	c.set(now.Add(40*time.Second + time.Hour))
	if _, err := v.Parse(context.Background(), p.token(p.claims(c.now()), "initial")); err != nil {
		t.Fatalf("provider recovery: %v", err)
	}
}

func TestLazyDiscoveryFailureIsThrottled(t *testing.T) {
	p := newProvider(t)
	p.set(func() { p.fail = true })
	now := time.Now().Truncate(time.Second)
	c := newClock(now)
	v := p.verifier(t, c, nil)
	token := p.token(p.claims(now), "initial")
	for range 10 {
		if _, err := v.Parse(context.Background(), token); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("unreachable provider must be unavailable: %v", err)
		}
	}
	if d, _ := p.counts(); d != 1 {
		t.Fatalf("discovery failure not throttled: %d", d)
	}
	p.set(func() { p.fail = false })
	c.set(now.Add(10 * time.Second))
	if _, err := v.Parse(context.Background(), p.token(p.claims(c.now()), "initial")); err != nil {
		t.Fatalf("recovery after cooldown: %v", err)
	}
}

func TestDiscoveredIssuerMismatchIsUnavailable(t *testing.T) {
	p := newProvider(t)
	p.set(func() { p.issuerOverride = "https://other.example.test" })
	var logs bytes.Buffer
	now := time.Now().Truncate(time.Second)
	v := p.verifier(t, newClock(now), &logs)
	token := p.token(p.claims(now), "initial")
	if _, err := v.Parse(context.Background(), token); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("issuer mismatch must be unavailable: %v", err)
	}
	if !strings.Contains(logs.String(), "level=ERROR") || !strings.Contains(logs.String(), p.server.URL) || strings.Contains(logs.String(), token) {
		t.Fatalf("issuer mismatch must log ERROR with issuer and without token: %s", logs.String())
	}
	if err := v.Load(context.Background()); err == nil {
		t.Fatal("Load accepted mismatched issuer")
	}
}

func TestRefreshFailureLogsWarning(t *testing.T) {
	p := newProvider(t)
	p.set(func() { p.fail = true })
	var logs bytes.Buffer
	now := time.Now().Truncate(time.Second)
	v := p.verifier(t, newClock(now), &logs)
	token := p.token(p.claims(now), "initial")
	_, _ = v.Parse(context.Background(), token)
	if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), p.server.URL) || strings.Contains(logs.String(), token) || strings.Contains(logs.String(), "synthetic upstream failure") {
		t.Fatalf("refresh failure must log WARN with issuer only: %s", logs.String())
	}
}

func TestConcurrentUnknownKidsCoalesce(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fail], func(t *testing.T) {
			p := newProvider(t)
			now := time.Now().Truncate(time.Second)
			v := p.verifier(t, newClock(now), nil)
			if err := v.Load(context.Background()); err != nil {
				t.Fatal(err)
			}
			entered, release := make(chan struct{}, 1), make(chan struct{})
			p.set(func() { p.fail, p.entered, p.release = fail, entered, release })
			var wg sync.WaitGroup
			errs := make(chan error, 32)
			for i := range 32 {
				token := p.token(p.claims(now), strings.Repeat("k", i+1))
				wg.Go(func() { _, err := v.Parse(context.Background(), token); errs <- err })
			}
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("refresh not started")
			}
			close(release)
			wg.Wait()
			close(errs)
			want := ErrInvalid
			if fail {
				want = ErrUnavailable
			}
			for err := range errs {
				if !errors.Is(err, want) {
					t.Fatalf("got %v, want %v", err, want)
				}
			}
			if _, j := p.counts(); j != 2 {
				t.Fatalf("concurrent unknown kids must share one refresh: %d", j)
			}
		})
	}
}

func TestParseRejectsUnsafeTokens(t *testing.T) {
	p := newProvider(t)
	now := time.Now().Truncate(time.Second)
	v := p.verifier(t, newClock(now), nil)
	if err := v.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	sign := func(method jwt.SigningMethod, key any, header map[string]any, claims jwt.MapClaims) string {
		token := jwt.NewWithClaims(method, claims)
		for k, val := range header {
			token.Header[k] = val
		}
		signed, err := token.SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		return signed
	}
	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub := p.key.PublicKey.N.Bytes()
	expired := p.claims(now.Add(-10 * time.Minute))
	skewed := p.claims(now)
	skewed["exp"] = now.Add(-20 * time.Second).Unix()
	noExp := p.claims(now)
	delete(noExp, "exp")
	wrongAud := p.claims(now)
	wrongAud["aud"] = "admin-api"
	wrongIss := p.claims(now)
	wrongIss["iss"] = "https://other.example.test"
	for name, raw := range map[string]string{
		"none":         sign(jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, map[string]any{"kid": "initial"}, p.claims(now)),
		"HS256 public": sign(jwt.SigningMethodHS256, pub, map[string]any{"kid": "initial"}, p.claims(now)),
		"ES256":        sign(jwt.SigningMethodES256, ec, map[string]any{"kid": "initial"}, p.claims(now)),
		"jku":          sign(jwt.SigningMethodRS256, p.key, map[string]any{"kid": "initial", "jku": "https://attacker.example/keys"}, p.claims(now)),
		"x5u":          sign(jwt.SigningMethodRS256, p.key, map[string]any{"kid": "initial", "x5u": "https://attacker.example/cert"}, p.claims(now)),
		"no kid":       sign(jwt.SigningMethodRS256, p.key, nil, p.claims(now)),
		"expired":      p.token(expired, "initial"),
		"no exp":       p.token(noExp, "initial"),
		"wrong aud":    p.token(wrongAud, "initial"),
		"wrong iss":    p.token(wrongIss, "initial"),
		"oversized":    strings.Repeat("a", 16*1024+1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := v.Parse(context.Background(), raw); !errors.Is(err, ErrInvalid) {
				t.Fatalf("got %v, want ErrInvalid", err)
			}
		})
	}
	if _, err := v.Parse(context.Background(), p.token(skewed, "initial")); err != nil {
		t.Fatalf("token within 30s leeway rejected: %v", err)
	}
}

func TestNewValidatesIssuer(t *testing.T) {
	for _, issuer := range []string{"http://idp.example.test", "https://user@idp.example.test", "https://idp.example.test?x=1", "https://idp.example.test#f", " https://idp.example.test", ""} {
		if _, err := New(issuer, "svc", Options{}); err == nil {
			t.Fatalf("accepted unsafe issuer %q", issuer)
		}
	}
	if _, err := New("https://idp.example.test", " ", Options{}); err == nil {
		t.Fatal("accepted blank audience")
	}
}

// A hanging provider must fail within RefreshTimeout, so requests inside the cooldown
// window answer unavailable immediately instead of each waiting for its own deadline.
func TestHangingProviderFailsFastInsideCooldown(t *testing.T) {
	p := newProvider(t)
	release := make(chan struct{})
	p.set(func() { p.entered, p.release = make(chan struct{}, 64), release })
	t.Cleanup(func() { close(release) })
	now := time.Now().Truncate(time.Second)
	v, err := New(p.server.URL, "svc", Options{HTTPClient: p.server.Client(), Now: newClock(now).now, RefreshInterval: 10 * time.Second, RefreshTimeout: 200 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	token := p.token(p.claims(now), "initial")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := v.Parse(ctx, token); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("hanging provider: %v", err)
	}
	start := time.Now()
	if _, err := v.Parse(ctx, token); !errors.Is(err, ErrUnavailable) || time.Since(start) > 100*time.Millisecond {
		t.Fatalf("request inside the failed window must be unavailable at once: %v after %v", err, time.Since(start))
	}
}
