package remoteauth_test

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/bbxx111/accountkit/examples/remoteauth"
	"github.com/bbxx111/accountkit/internal/testkeys"
	"github.com/bbxx111/accountkit/tokens"
)

type jwksServer struct {
	*httptest.Server
	mu     sync.Mutex
	body   []byte
	status int
	hits   atomic.Int32
}

func newJWKSServer(t *testing.T, signer *tokens.Signer) *jwksServer {
	t.Helper()
	s := &jwksServer{body: signer.JWKS(), status: 200}
	s.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.hits.Add(1)
		s.mu.Lock()
		defer s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(s.status)
		_, _ = w.Write(s.body)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *jwksServer) set(body []byte, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.body, s.status = body, status
}

func signer(t *testing.T, versions ...int) *tokens.Signer {
	t.Helper()
	keys := map[uint16]*ecdsa.PrivateKey{}
	for _, v := range versions {
		keys[uint16(v)] = testkeys.JWT(100 + v)
	}
	s, err := tokens.NewSigner(tokens.Options{Keys: keys, Active: uint16(versions[len(versions)-1]), Issuer: "consumer.example", Audience: "business.example"})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func access(t *testing.T, s *tokens.Signer, issued time.Time) string {
	t.Helper()
	raw, _, err := s.Sign(tokens.Claims{UserID: "u_0123456789abc", SessionID: "s_0123456789abc", Scope: "user", AuthTime: issued}, issued, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func verifier(t *testing.T, srv *jwksServer, now *time.Time) *remoteauth.Verifier {
	t.Helper()
	v, err := remoteauth.NewVerifier(remoteauth.VerifierConfig{JWKSURL: srv.URL + "/.well-known/jwks.json", Issuer: "consumer.example", Audience: "business.example", HTTPClient: srv.Client(), Now: func() time.Time { return *now }})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestVerifierAcceptsValidAndRejectsInvalid(t *testing.T) {
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	s := signer(t, 1)
	srv := newJWKSServer(t, s)
	v := verifier(t, srv, &now)

	r, err := v.Verify(ctx, access(t, s, now))
	if err != nil || !r.Active || r.Subject != "u_0123456789abc" || r.SessionID != "s_0123456789abc" || r.Scope != "user" || !r.AuthTime.Equal(now) {
		t.Fatalf("valid: %+v %v", r, err)
	}

	k := testkeys.JWT(101)
	claims := jwt.MapClaims{"iss": "consumer.example", "aud": "business.example", "sub": "u_0123456789abc", "sid": "s_0123456789abc", "scope": "user", "auth_time": now.Unix(), "exp": now.Add(time.Minute).Unix()}
	mk := func(method jwt.SigningMethod, header map[string]any, c jwt.MapClaims, key any) string {
		tok := jwt.NewWithClaims(method, c)
		delete(tok.Header, "typ")
		for name, value := range header {
			tok.Header[name] = value
		}
		raw, err := tok.SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	with := func(name string, value any) jwt.MapClaims {
		c := jwt.MapClaims{}
		for k, v := range claims {
			c[k] = v
		}
		c[name] = value
		return c
	}
	at := map[string]any{"kid": "1", "typ": "at+jwt"}
	for name, raw := range map[string]string{
		"hs256":       mk(jwt.SigningMethodHS256, at, claims, []byte("0123456789abcdef0123456789abcdef")),
		"none":        mk(jwt.SigningMethodNone, at, claims, jwt.UnsafeAllowNoneSignatureType),
		"wrong typ":   mk(jwt.SigningMethodES256, map[string]any{"kid": "1", "typ": "JWT"}, claims, k),
		"no typ":      mk(jwt.SigningMethodES256, map[string]any{"kid": "1"}, claims, k),
		"wrong aud":   mk(jwt.SigningMethodES256, at, with("aud", "other.example"), k),
		"wrong iss":   mk(jwt.SigningMethodES256, at, with("iss", "evil.example"), k),
		"expired":     mk(jwt.SigningMethodES256, at, with("exp", now.Add(-time.Hour).Unix()), k),
		"bad sub":     mk(jwt.SigningMethodES256, at, with("sub", "users/1"), k),
		"no exp":      mk(jwt.SigningMethodES256, at, jwt.MapClaims{"iss": "consumer.example", "aud": "business.example", "sub": "u_0123456789abc", "sid": "s_0123456789abc", "scope": "user", "auth_time": now.Unix()}, k),
		"other key":   mk(jwt.SigningMethodES256, at, claims, testkeys.JWT(999)),
		"garbage":     "a.b.c",
		"empty token": "",
	} {
		if r, err := v.Verify(ctx, raw); err != nil || r.Active {
			t.Errorf("%s: %+v %v", name, r, err)
		}
	}
}

func TestVerifierRefreshesUnknownKeyWithThrottle(t *testing.T) {
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	old := signer(t, 1)
	srv := newJWKSServer(t, old)
	v := verifier(t, srv, &now)
	if r, err := v.Verify(ctx, access(t, old, now)); err != nil || !r.Active {
		t.Fatalf("initial: %+v %v", r, err)
	}
	// 轮换：服务端发布 kid 2 后，未知 kid 触发一次刷新
	rotated := signer(t, 1, 2)
	srv.set(rotated.JWKS(), 200)
	now = now.Add(2 * time.Minute)
	if r, err := v.Verify(ctx, access(t, rotated, now)); err != nil || !r.Active {
		t.Fatalf("after rotation: %+v %v", r, err)
	}
	hits := srv.hits.Load()
	// 节流期内的另一个未知 kid 不再请求 JWKS
	third := signer(t, 3)
	if r, err := v.Verify(ctx, access(t, third, now)); err != nil || r.Active || srv.hits.Load() != hits {
		t.Fatalf("throttled unknown kid: %+v %v hits=%d/%d", r, err, srv.hits.Load(), hits)
	}
	// JWKS 故障：未知 kid 按依赖不可用处理，已缓存的 kid 不受影响
	now = now.Add(2 * time.Minute)
	srv.set([]byte("oops"), 500)
	if _, err := v.Verify(ctx, access(t, third, now)); !errors.Is(err, remoteauth.ErrUnavailable) {
		t.Fatalf("JWKS outage must be unavailable: %v", err)
	}
	if r, err := v.Verify(ctx, access(t, rotated, now)); err != nil || !r.Active {
		t.Fatalf("cached key must keep working: %+v %v", r, err)
	}
}

func TestVerifierConfigurationAndRedirects(t *testing.T) {
	for name, cfg := range map[string]remoteauth.VerifierConfig{
		"http without opt-in": {JWKSURL: "http://accounts.example/.well-known/jwks.json", Issuer: "i", Audience: "a"},
		"no issuer":           {JWKSURL: "https://accounts.example/.well-known/jwks.json", Audience: "a"},
		"no audience":         {JWKSURL: "https://accounts.example/.well-known/jwks.json", Issuer: "i"},
		"userinfo":            {JWKSURL: "https://u:p@accounts.example/.well-known/jwks.json", Issuer: "i", Audience: "a"},
	} {
		if _, err := remoteauth.NewVerifier(cfg); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	if _, err := remoteauth.NewVerifier(remoteauth.VerifierConfig{JWKSURL: "http://127.0.0.1:1/jwks", Issuer: "i", Audience: "a", AllowHTTP: true}); err != nil {
		t.Fatalf("explicit HTTP: %v", err)
	}
	now := time.Now().Truncate(time.Second)
	s := signer(t, 1)
	target := newJWKSServer(t, s)
	redirect := httptest.NewTLSServer(http.RedirectHandler(target.URL, http.StatusFound))
	t.Cleanup(redirect.Close)
	v, err := remoteauth.NewVerifier(remoteauth.VerifierConfig{JWKSURL: redirect.URL, Issuer: "consumer.example", Audience: "business.example", HTTPClient: redirect.Client(), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.Verify(context.Background(), access(t, s, now)); !errors.Is(err, remoteauth.ErrUnavailable) || target.hits.Load() != 0 {
		t.Fatalf("redirect must not be followed: %v hits=%d", err, target.hits.Load())
	}
}

// TestTieredProtectedHandler: after a session is revoked, an ordinary route still
// accepts the unexpired access while a critical route rejects it via introspection.
func TestTieredProtectedHandler(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	s := signer(t, 1)
	srv := newJWKSServer(t, s)
	v := verifier(t, srv, &now)
	var introspections atomic.Int32
	var mu sync.Mutex
	upstream := `{"active":false}` // the session has been revoked
	status := 200
	c := clientFor(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		introspections.Add(1)
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(upstream))
	}))
	owns := func(context.Context, string, string) (bool, error) { return true, nil }
	serve := func(h http.Handler, token string) int {
		r := httptest.NewRequest("GET", "/documents/one", nil)
		r.SetPathValue("resource", "one")
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	tok := access(t, s, now)
	ordinary := remoteauth.ProtectedHandler{Verifier: v, MaxAuthAge: time.Hour, Now: func() time.Time { return now }, OwnsResource: owns}
	critical := remoteauth.ProtectedHandler{Verifier: v, Client: c, Critical: true, MaxAuthAge: time.Hour, Now: func() time.Time { return now }, OwnsResource: owns}
	if code := serve(ordinary, tok); code != 204 || introspections.Load() != 0 {
		t.Fatalf("ordinary route: %d introspections=%d", code, introspections.Load())
	}
	if code := serve(critical, tok); code != 401 || introspections.Load() != 1 {
		t.Fatalf("critical route must reject revoked session: %d", code)
	}
	// Locally invalid tokens never reach introspection.
	if code := serve(critical, "a.b.c"); code != 401 || introspections.Load() != 1 {
		t.Fatalf("invalid token reached introspection: %d", code)
	}
	// Introspection outage fails closed on critical routes only.
	mu.Lock()
	status, upstream = 503, `{"error":"temporarily_unavailable"}`
	mu.Unlock()
	if code := serve(critical, tok); code != 503 {
		t.Fatalf("critical route during outage: %d", code)
	}
	if code := serve(ordinary, tok); code != 204 {
		t.Fatalf("ordinary route during introspection outage: %d", code)
	}
}

// JWKS outages must not trigger a fetch per request (random kid amplification).
func TestVerifierThrottlesFailedFetches(t *testing.T) {
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	s := signer(t, 1)
	srv := newJWKSServer(t, s)
	srv.set([]byte("down"), 503)
	v := verifier(t, srv, &now)
	tok := access(t, s, now)
	for range 20 {
		if _, err := v.Verify(ctx, tok); !errors.Is(err, remoteauth.ErrUnavailable) {
			t.Fatalf("startup outage must be unavailable: %v", err)
		}
	}
	if srv.hits.Load() != 1 {
		t.Fatalf("failed fetches not throttled: %d requests", srv.hits.Load())
	}
	srv.set(s.JWKS(), 200)
	now = now.Add(61 * time.Second)
	if r, err := v.Verify(ctx, tok); err != nil || !r.Active || srv.hits.Load() != 2 {
		t.Fatalf("recovery after throttle window: %+v %v hits=%d", r, err, srv.hits.Load())
	}
}

// A slow JWKS fetch for an unknown kid must not block requests with cached keys,
// and concurrent unknown-kid requests share one fetch.
func TestVerifierDoesNotHoldLockDuringFetch(t *testing.T) {
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	s := signer(t, 1)
	release := make(chan struct{})
	var hits atomic.Int32
	var slow atomic.Bool
	var offset atomic.Int64 // advances the verifier clock past the default throttle window
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if slow.Load() {
			<-release
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(s.JWKS())
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	v, err := remoteauth.NewVerifier(remoteauth.VerifierConfig{JWKSURL: srv.URL, Issuer: "consumer.example", Audience: "business.example", HTTPClient: srv.Client(), Now: func() time.Time { return time.Now().Add(time.Duration(offset.Load())) }})
	if err != nil {
		t.Fatal(err)
	}
	good := access(t, s, now)
	if r, err := v.Verify(ctx, good); err != nil || !r.Active {
		t.Fatalf("warm cache: %+v %v", r, err)
	}
	slow.Store(true)
	offset.Store(int64(2 * time.Minute))
	unknown := access(t, signer(t, 7), now)
	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() { _, _ = v.Verify(ctx, unknown) })
	}
	time.Sleep(100 * time.Millisecond) // let the unknown-kid fetch start and block
	done := make(chan struct{})
	go func() {
		defer close(done)
		if r, err := v.Verify(ctx, good); err != nil || !r.Active {
			t.Errorf("cached key during slow fetch: %+v %v", r, err)
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("cached-key verification blocked behind JWKS fetch")
	}
	close(release)
	wg.Wait()
	if hits.Load() != 2 {
		t.Fatalf("concurrent unknown-kid requests must share one fetch: %d", hits.Load())
	}
}

// Keys removed from the JWKS stop being accepted after MaxCacheAge, and when the
// JWKS stays unreachable cached keys are dropped after MaxCacheAge+StaleGrace.
func TestVerifierBoundsCacheAge(t *testing.T) {
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	old := signer(t, 1)
	srv := newJWKSServer(t, old)
	v, err := remoteauth.NewVerifier(remoteauth.VerifierConfig{JWKSURL: srv.URL, Issuer: "consumer.example", Audience: "business.example", HTTPClient: srv.Client(), Now: func() time.Time { return now }, MaxCacheAge: 5 * time.Minute, StaleGrace: 10 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	tok := func() string { return access(t, old, now) }
	if r, err := v.Verify(ctx, tok()); err != nil || !r.Active {
		t.Fatalf("initial: %+v %v", r, err)
	}
	// Operator removed version 1 (rotation step 5 or compromise).
	srv.set(signer(t, 2).JWKS(), 200)
	now = now.Add(4 * time.Minute)
	if r, _ := v.Verify(ctx, tok()); !r.Active {
		t.Fatal("within MaxCacheAge the cached key is still used")
	}
	now = now.Add(2 * time.Minute)
	if r, err := v.Verify(ctx, tok()); err != nil || r.Active {
		t.Fatalf("removed key must be rejected after refresh: %+v %v", r, err)
	}

	// Outage case: stale keys survive only within the grace period.
	v2, _ := remoteauth.NewVerifier(remoteauth.VerifierConfig{JWKSURL: srv.URL, Issuer: "consumer.example", Audience: "business.example", HTTPClient: srv.Client(), Now: func() time.Time { return now }, MaxCacheAge: 5 * time.Minute, StaleGrace: 10 * time.Minute})
	s2 := signer(t, 2)
	if r, err := v2.Verify(ctx, access(t, s2, now)); err != nil || !r.Active {
		t.Fatalf("v2 initial: %+v %v", r, err)
	}
	srv.set([]byte("down"), 503)
	now = now.Add(10 * time.Minute)
	if r, err := v2.Verify(ctx, access(t, s2, now)); err != nil || !r.Active {
		t.Fatalf("stale key within grace: %+v %v", r, err)
	}
	now = now.Add(6 * time.Minute)
	if _, err := v2.Verify(ctx, access(t, s2, now)); !errors.Is(err, remoteauth.ErrUnavailable) {
		t.Fatalf("keys beyond grace must fail closed: %v", err)
	}
}

// A 200 response without usable keys must not wipe the cached set.
func TestVerifierKeepsKeysOnEmptySet(t *testing.T) {
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	s := signer(t, 1)
	srv := newJWKSServer(t, s)
	v := verifier(t, srv, &now)
	if r, err := v.Verify(ctx, access(t, s, now)); err != nil || !r.Active {
		t.Fatalf("initial: %+v %v", r, err)
	}
	srv.set([]byte(`{"keys":[]}`), 200)
	now = now.Add(6 * time.Minute) // past MaxCacheAge → refresh returns an empty set
	if r, err := v.Verify(ctx, access(t, s, now)); err != nil || !r.Active {
		t.Fatalf("empty set must be treated as a failed refresh: %+v %v", r, err)
	}
}
