package idp_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/observability"
	"github.com/bbxx111/accountkit/user/idp"
)

type googleFixture struct {
	key    *rsa.PrivateKey
	kid    string
	srv    *httptest.Server
	hits   atomic.Int32
	serve  atomic.Bool
	google *idp.Google
	now    time.Time
	mr     *miniredis.Miniredis
}

func newGoogleFixture(t *testing.T) *googleFixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &googleFixture{key: key, kid: "gk1", now: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)}
	f.serve.Store(true)
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		if !f.serve.Load() {
			w.WriteHeader(500)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{jwkOf(f.kid, &f.key.PublicKey)}})
	}))
	t.Cleanup(f.srv.Close)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	f.mr = mr
	f.google = idp.NewGoogle(idp.GoogleOptions{
		ClientIDs: []string{"client-a.apps.googleusercontent.com", "client-b.apps.googleusercontent.com"},
		JWKSURL:   f.srv.URL,
		HTTP:      f.srv.Client(),
		Nonces:    idp.NewNonceRegistry(rdb, "t:"),
		NonceTTL:  10 * time.Minute,
		Now:       func() time.Time { return f.now },
	})
	return f
}

func (f *googleFixture) token(t *testing.T, over map[string]any, kid string, signKey any) string {
	t.Helper()
	claims := jwt.MapClaims{
		"iss": "https://accounts.google.com", "aud": "client-a.apps.googleusercontent.com", "sub": "google-sub-1",
		"iat": f.now.Unix(), "exp": f.now.Add(10 * time.Minute).Unix(),
		"nonce": "raw-nonce-1", "email": "person@example.test", "email_verified": true,
		"name": "Ada Lovelace", "picture": "https://example.test/a.png",
	}
	for k, v := range over {
		if v == nil {
			delete(claims, k)
		} else {
			claims[k] = v
		}
	}
	if kid == "" {
		kid = f.kid
	}
	if signKey == nil {
		signKey = f.key
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = kid
	s, err := tok.SignedString(signKey)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestGoogleVerifyAcceptsRawNonceAndBothIssuers(t *testing.T) {
	f := newGoogleFixture(t)
	ctx := context.Background()
	id, err := f.google.VerifyGoogle(ctx, f.token(t, nil, "", nil), "raw-nonce-1")
	if err != nil || id.Kind != enum.IdentityGoogle || id.Subject != "google-sub-1" || id.HintEmail != "person@example.test" || id.Name != "Ada Lovelace" || id.Picture != "https://example.test/a.png" {
		t.Fatalf("%+v %v", id, err)
	}
	hashed := sha256.Sum256([]byte("raw-nonce-2"))
	if _, err := f.google.VerifyGoogle(ctx, f.token(t, map[string]any{"nonce": hex.EncodeToString(hashed[:])}, "", nil), "raw-nonce-2"); !errors.Is(err, idp.ErrInvalidCredential) {
		t.Fatalf("hashed nonce must be rejected: %v", err)
	}
	if _, err := f.google.VerifyGoogle(ctx, f.token(t, map[string]any{"iss": "accounts.google.com", "nonce": "raw-nonce-3"}, "", nil), "raw-nonce-3"); err != nil {
		t.Fatalf("bare issuer: %v", err)
	}
	if _, err := f.google.VerifyGoogle(ctx, f.token(t, map[string]any{"aud": "other", "nonce": "raw-nonce-4"}, "", nil), "raw-nonce-4"); !errors.Is(err, idp.ErrAppNotAllowed) {
		t.Fatalf("bad aud: %v", err)
	}
	if _, err := f.google.VerifyGoogle(ctx, f.token(t, map[string]any{"azp": "other", "nonce": "raw-nonce-5"}, "", nil), "raw-nonce-5"); !errors.Is(err, idp.ErrAppNotAllowed) {
		t.Fatalf("bad azp: %v", err)
	}
	if _, err := f.google.VerifyGoogle(ctx, f.token(t, nil, "", nil), "raw-nonce-1"); !errors.Is(err, idp.ErrNonceReplayed) {
		t.Fatalf("replay: %v", err)
	}
	id, err = f.google.VerifyGoogle(ctx, f.token(t, map[string]any{"nonce": "raw-nonce-6", "email_verified": false}, "", nil), "raw-nonce-6")
	if err != nil || id.HintEmail != "" {
		t.Fatalf("unverified email must not hint: %+v %v", id, err)
	}
}

func TestGoogleDisabled(t *testing.T) {
	f := newGoogleFixture(t)
	g := idp.NewGoogle(idp.GoogleOptions{Nonces: idp.NewNonceRegistry(redis.NewClient(&redis.Options{Addr: f.mr.Addr()}), "t:"), Now: func() time.Time { return f.now }})
	if _, err := g.VerifyGoogle(context.Background(), f.token(t, nil, "", nil), "raw-nonce-1"); !errors.Is(err, idp.ErrAppNotAllowed) {
		t.Fatalf("disabled: %v", err)
	}
}

type googleObserver func(context.Context, observability.Observation)

func (f googleObserver) Observe(ctx context.Context, o observability.Observation) { f(ctx, o) }

func TestGoogleJWKSFailClosedAndCachedFallback(t *testing.T) {
	f := newGoogleFixture(t)
	ctx := context.Background()
	f.serve.Store(false)
	if _, err := f.google.VerifyGoogle(ctx, f.token(t, nil, "", nil), "raw-nonce-1"); !errors.Is(err, idp.ErrUnavailable) {
		t.Fatalf("no cache must be unavailable, not an invalid credential: %v", err)
	}
	f.serve.Store(true)
	var seen []observability.Observation
	f.google = idp.NewGoogle(idp.GoogleOptions{
		ClientIDs: []string{"client-a.apps.googleusercontent.com"},
		JWKSURL:   f.srv.URL,
		HTTP:      f.srv.Client(),
		Nonces:    idp.NewNonceRegistry(redis.NewClient(&redis.Options{Addr: f.mr.Addr()}), "t:"),
		Now:       func() time.Time { return f.now },
		Observer: googleObserver(func(_ context.Context, obs observability.Observation) {
			seen = append(seen, obs)
		}),
	})
	if _, err := f.google.VerifyGoogle(ctx, f.token(t, nil, "", nil), "raw-nonce-1"); err != nil {
		t.Fatalf("load cache: %v", err)
	}
	f.serve.Store(false)
	f.now = f.now.Add(61 * time.Second)
	if _, err := f.google.VerifyGoogle(ctx, f.token(t, map[string]any{"nonce": "unknown-kid"}, "missing", nil), "unknown-kid"); !errors.Is(err, idp.ErrUnavailable) {
		t.Fatalf("refresh failure must not be an invalid credential: %v", err)
	}
	if _, err := f.google.VerifyGoogle(ctx, f.token(t, map[string]any{"nonce": "cached-2"}, "", nil), "cached-2"); err != nil {
		t.Fatalf("cached key must still verify: %v", err)
	}
	got := 0
	for _, obs := range seen {
		d, ok := obs.(observability.DegradationObservation)
		if !ok {
			continue
		}
		if d.Scenario == observability.DegradationAppleCachedKeyFallback {
			t.Fatal("google fallback must not use the apple scenario")
		}
		if d.Scenario == observability.DegradationGoogleCachedKeyFallback {
			got += int(d.Count)
		}
	}
	if got != 1 {
		t.Fatalf("google cached-key fallback count = %d", got)
	}
}
