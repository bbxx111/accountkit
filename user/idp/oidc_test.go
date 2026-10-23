package idp_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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

const corpIssuer = "https://sso.corp.example"

type oidcFixture struct {
	key   *rsa.PrivateKey
	srv   *httptest.Server
	hits  atomic.Int32
	serve atomic.Bool
	now   time.Time
	mr    *miniredis.Miniredis
	rdb   *redis.Client
	obs   *observationLog
}

type observationLog struct{ items []observability.Observation }

func (l *observationLog) Observe(_ context.Context, o observability.Observation) {
	l.items = append(l.items, o)
}

func newOIDCFixture(t *testing.T) *oidcFixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &oidcFixture{key: key, now: time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC), obs: &observationLog{}}
	f.serve.Store(true)
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		if !f.serve.Load() {
			w.WriteHeader(500)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{jwkOf("ck1", &f.key.PublicKey)}})
	}))
	t.Cleanup(f.srv.Close)
	f.mr = miniredis.RunT(t)
	f.rdb = redis.NewClient(&redis.Options{Addr: f.mr.Addr()})
	t.Cleanup(func() { _ = f.rdb.Close() })
	return f
}

func (f *oidcFixture) provider(kind enum.IdentityKind, mutate func(*idp.OIDCOptions)) *idp.OIDC {
	o := idp.OIDCOptions{
		Kind: kind, Issuer: corpIssuer, JWKSURL: f.srv.URL, ClientIDs: []string{"corp-web", "corp-ios"},
		HTTP: f.srv.Client(), Nonces: idp.NewNonceRegistry(f.rdb, "t:"), Observer: f.obs,
		Now: func() time.Time { return f.now }, NonceTTL: 10 * time.Minute,
	}
	if mutate != nil {
		mutate(&o)
	}
	return idp.NewOIDC(o)
}

func (f *oidcFixture) token(t *testing.T, over map[string]any, method jwt.SigningMethod, signKey any) string {
	t.Helper()
	claims := jwt.MapClaims{
		"iss": corpIssuer, "aud": "corp-web", "sub": "corp-sub-1",
		"iat": f.now.Unix(), "exp": f.now.Add(10 * time.Minute).Unix(), "nonce": "raw-1",
		"email": "person@corp.example", "email_verified": true,
		"name": "Corp Person", "picture": "https://corp.example/p.png",
	}
	for k, v := range over {
		if v == nil {
			delete(claims, k)
		} else {
			claims[k] = v
		}
	}
	if method == nil {
		method = jwt.SigningMethodRS256
	}
	if signKey == nil {
		signKey = f.key
	}
	tok := jwt.NewWithClaims(method, claims)
	tok.Header["kid"] = "ck1"
	if kid, ok := over["kid"].(string); ok {
		delete(claims, "kid")
		tok.Header["kid"] = kid
	}
	s, err := tok.SignedString(signKey)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestOIDCVerifyHappyPath(t *testing.T) {
	f := newOIDCFixture(t)
	p := f.provider("CORP_SSO", nil)
	if p.Kind() != "CORP_SSO" {
		t.Fatal(p.Kind())
	}
	raw, _ := json.Marshal(map[string]string{"id_token": f.token(t, nil, nil, nil), "nonce": "raw-1"})
	id, err := p.Verify(context.Background(), raw)
	if err != nil || id.Kind != "CORP_SSO" || id.Subject != "corp-sub-1" || id.HintEmail != "person@corp.example" || id.Name != "Corp Person" || id.Picture != "https://corp.example/p.png" || !id.ExpireTime.Equal(f.now.Add(10*time.Minute)) {
		t.Fatalf("%+v %v", id, err)
	}
	if keys := f.mr.Keys(); len(keys) != 1 || !strings.HasPrefix(keys[0], "t:nonce:oidc:CORP_SSO:") {
		t.Fatalf("nonce key must be instance-prefixed and kind-scoped: %v", keys)
	}
	if ttl := f.mr.TTL(f.mr.Keys()[0]); ttl < 10*time.Minute {
		t.Fatalf("nonce ttl %v", ttl)
	}
	// 同一 sub 经另一个允许的 client ID 仍得到同一主体。
	id, err = p.VerifyIDToken(context.Background(), f.token(t, map[string]any{"aud": []string{"other", "corp-ios"}, "nonce": "raw-2"}, nil, nil), "raw-2")
	if err != nil || id.Subject != "corp-sub-1" {
		t.Fatalf("second client: %+v %v", id, err)
	}
}

func TestOIDCVerifyRejections(t *testing.T) {
	f := newOIDCFixture(t)
	p := f.provider("CORP_SSO", nil)
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	ctx := context.Background()
	cases := []struct {
		name  string
		tok   string
		nonce string
		want  error
	}{
		{"issuer", f.token(t, map[string]any{"iss": corpIssuer + "/"}, nil, nil), "raw-1", idp.ErrInvalidCredential},
		{"issuer missing", f.token(t, map[string]any{"iss": nil}, nil, nil), "raw-1", idp.ErrInvalidCredential},
		{"audience", f.token(t, map[string]any{"aud": "other"}, nil, nil), "raw-1", idp.ErrAppNotAllowed},
		{"azp", f.token(t, map[string]any{"azp": "other"}, nil, nil), "raw-1", idp.ErrAppNotAllowed},
		{"hs256", f.token(t, nil, jwt.SigningMethodHS256, []byte("secret")), "raw-1", idp.ErrInvalidCredential},
		{"wrong key", f.token(t, nil, nil, other), "raw-1", idp.ErrInvalidCredential},
		{"expired", f.token(t, map[string]any{"exp": f.now.Add(-31 * time.Second).Unix()}, nil, nil), "raw-1", idp.ErrInvalidCredential},
		{"no exp", f.token(t, map[string]any{"exp": nil}, nil, nil), "raw-1", idp.ErrInvalidCredential},
		{"nonce mismatch", f.token(t, nil, nil, nil), "raw-x", idp.ErrInvalidCredential},
		{"no sub", f.token(t, map[string]any{"sub": nil}, nil, nil), "raw-1", idp.ErrInvalidCredential},
	}
	for _, c := range cases {
		if _, err := p.VerifyIDToken(ctx, c.tok, c.nonce); !errors.Is(err, c.want) {
			t.Fatalf("%s: %v, want %v", c.name, err, c.want)
		}
	}
	none := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{"iss": corpIssuer, "aud": "corp-web", "sub": "s", "exp": f.now.Add(time.Minute).Unix(), "nonce": "raw-1"})
	unsigned, _ := none.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if _, err := p.VerifyIDToken(ctx, unsigned, "raw-1"); !errors.Is(err, idp.ErrInvalidCredential) {
		t.Fatalf("none: %v", err)
	}
	if len(f.mr.Keys()) != 0 {
		t.Fatalf("rejections must not register nonces: %v", f.mr.Keys())
	}
	if _, err := p.VerifyIDToken(ctx, f.token(t, map[string]any{"exp": f.now.Add(-20 * time.Second).Unix()}, nil, nil), "raw-1"); err != nil {
		t.Fatalf("within leeway: %v", err)
	}
	if _, err := p.VerifyIDToken(ctx, f.token(t, nil, nil, nil), "raw-1"); !errors.Is(err, idp.ErrNonceReplayed) {
		t.Fatalf("replay: %v", err)
	}
	// 其他类型的同一 nonce 互不影响。
	if _, err := f.provider("PARTNER_SSO", nil).VerifyIDToken(ctx, f.token(t, nil, nil, nil), "raw-1"); err != nil {
		t.Fatalf("nonce scope must be per kind: %v", err)
	}
}

func TestOIDCDependencyFailures(t *testing.T) {
	f := newOIDCFixture(t)
	f.serve.Store(false)
	p := f.provider("CORP_SSO", nil)
	ctx := context.Background()
	if _, err := p.VerifyIDToken(ctx, f.token(t, nil, nil, nil), "raw-1"); !errors.Is(err, idp.ErrUnavailable) {
		t.Fatalf("jwks down: %v", err)
	}
	f.serve.Store(true)
	f.now = f.now.Add(61 * time.Second)
	if _, err := p.VerifyIDToken(ctx, f.token(t, map[string]any{"nonce": "raw-2"}, nil, nil), "raw-2"); err != nil {
		t.Fatal(err)
	}
	f.serve.Store(false)
	f.now = f.now.Add(61 * time.Second)
	if _, err := p.VerifyIDToken(ctx, f.token(t, map[string]any{"nonce": "raw-x", "kid": "unknown"}, nil, nil), "raw-x"); !errors.Is(err, idp.ErrUnavailable) {
		t.Fatalf("unknown kid after failed refresh: %v", err)
	}
	if _, err := p.VerifyIDToken(ctx, f.token(t, map[string]any{"nonce": "raw-3"}, nil, nil), "raw-3"); err != nil {
		t.Fatalf("cached key must keep verifying: %v", err)
	}
	var fallback, google bool
	for _, o := range f.obs.items {
		if d, ok := o.(observability.DegradationObservation); ok {
			fallback = fallback || d.Scenario == observability.DegradationOIDCCachedKeyFallback
			google = google || d.Scenario == observability.DegradationGoogleCachedKeyFallback || d.Scenario == observability.DegradationAppleCachedKeyFallback
		}
	}
	if !fallback || google {
		t.Fatalf("expected only the OIDC cached-key degradation: %+v", f.obs.items)
	}
	f.mr.SetError("redis down")
	f.serve.Store(true)
	if _, err := p.VerifyIDToken(ctx, f.token(t, map[string]any{"nonce": "raw-4"}, nil, nil), "raw-4"); !errors.Is(err, idp.ErrUnavailable) {
		t.Fatalf("nonce store down: %v", err)
	}
}

func TestOIDCClaimMapping(t *testing.T) {
	f := newOIDCFixture(t)
	ctx := context.Background()
	fallback := f.provider("CORP_SSO", func(o *idp.OIDCOptions) {
		o.NameClaims = []string{"name", "preferred_username"}
		o.PictureClaims = []string{"avatar_url", "picture"}
	})
	id, err := fallback.VerifyIDToken(ctx, f.token(t, map[string]any{"name": nil, "preferred_username": "corp.user", "avatar_url": map[string]any{"x": 1}}, nil, nil), "raw-1")
	if err != nil || id.Name != "corp.user" || id.Picture != "https://corp.example/p.png" {
		t.Fatalf("fallback mapping: %+v %v", id, err)
	}
	off := f.provider("PARTNER_SSO", func(o *idp.OIDCOptions) { o.NameClaims, o.PictureClaims = []string{}, []string{} })
	id, err = off.VerifyIDToken(ctx, f.token(t, nil, nil, nil), "raw-1")
	if err != nil || id.Name != "" || id.Picture != "" {
		t.Fatalf("disabled mapping: %+v %v", id, err)
	}
	unverified, err := f.provider("THIRD_SSO", nil).VerifyIDToken(ctx, f.token(t, map[string]any{"email_verified": false}, nil, nil), "raw-1")
	if err != nil || unverified.HintEmail != "" {
		t.Fatalf("unverified email must not be hinted: %+v %v", unverified, err)
	}
}

func TestOIDCPolicyFingerprint(t *testing.T) {
	f := newOIDCFixture(t)
	a := f.provider("CORP_SSO", nil)
	b := f.provider("CORP_SSO", func(o *idp.OIDCOptions) { o.ClientIDs = []string{"corp-ios", "corp-web"} })
	c := f.provider("CORP_SSO", func(o *idp.OIDCOptions) { o.Issuer = corpIssuer + "/v2" })
	d := f.provider("CORP_SSO", func(o *idp.OIDCOptions) { o.ClientIDs = []string{"corp-web"} })
	if a.PolicyFingerprint() != b.PolicyFingerprint() || a.PolicyFingerprint() == c.PolicyFingerprint() || a.PolicyFingerprint() == d.PolicyFingerprint() {
		t.Fatal("fingerprint must ignore client order and track issuer and client IDs")
	}
}
