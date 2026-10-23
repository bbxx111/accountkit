package idp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/bbxx111/accountkit/observability"
	"github.com/bbxx111/accountkit/user/idp"
)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type fbObserver func(context.Context, observability.Observation)

func (f fbObserver) Observe(ctx context.Context, o observability.Observation) { f(ctx, o) }

func TestFacebookMalformedRegistersNoNonce(t *testing.T) {
	f := newFBFixture(t)
	for _, raw := range []string{`{"id_token":"x","nonce":"n","access_token":"t"}`, `{"nonce":"n"}`, `{"id_token":"x","nonce":""}`, `{}`} {
		_, _ = f.fb.Verify(context.Background(), json.RawMessage(raw))
	}
	if keys := f.mr.Keys(); len(keys) != 0 {
		t.Fatalf("malformed credentials registered redis keys: %v", keys)
	}
}

func TestFacebookLimitedJWKSUnavailableWithoutCache(t *testing.T) {
	f := newFBFixture(t)
	f.jwksFail.Store(true)
	if _, err := f.fb.VerifyLimited(context.Background(), f.idToken(t, nil), "n1"); !errors.Is(err, idp.ErrUnavailable) {
		t.Fatalf("jwks 500 without cache: %v", err)
	}
}

func TestFacebookLimitedCachedKeyFallbackScenario(t *testing.T) {
	f := newFBFixture(t)
	ctx := context.Background()
	if _, err := f.fb.VerifyLimited(ctx, f.idToken(t, nil), "n1"); err != nil {
		t.Fatalf("load cache: %v", err)
	}
	f.jwksFail.Store(true)
	f.now = f.now.Add(61 * time.Second)
	bad := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": "https://www.facebook.com", "aud": fbApp, "sub": "asid-1", "exp": f.now.Add(time.Hour).Unix(), "nonce": "nk"})
	bad.Header["kid"] = "missing"
	s, err := bad.SignedString(f.key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.fb.VerifyLimited(ctx, s, "nk"); !errors.Is(err, idp.ErrUnavailable) {
		t.Fatalf("refresh failure: %v", err)
	}
	over := map[string]any{"nonce": "n2", "iat": f.now.Unix(), "exp": f.now.Add(time.Hour).Unix()}
	if _, err := f.fb.VerifyLimited(ctx, f.idToken(t, over), "n2"); err != nil {
		t.Fatalf("cached key must verify: %v", err)
	}
	f.obsMu.Lock()
	defer f.obsMu.Unlock()
	got := 0
	for _, o := range f.obs {
		d, ok := o.(observability.DegradationObservation)
		if !ok {
			continue
		}
		if d.Scenario != observability.DegradationFacebookCachedKeyFallback {
			t.Fatalf("unexpected degradation scenario %v", d.Scenario)
		}
		got += int(d.Count)
	}
	if got != 1 {
		t.Fatalf("facebook fallback count = %d", got)
	}
}

func TestFacebookLimitedAzpMismatch(t *testing.T) {
	f := newFBFixture(t)
	if _, err := f.fb.VerifyLimited(context.Background(), f.idToken(t, map[string]any{"azp": "other-app"}), "n1"); !errors.Is(err, idp.ErrAppNotAllowed) {
		t.Fatalf("azp: %v", err)
	}
}

const fbUserTok = "user-token-XYZ"

func TestFacebookClassicFailureModes(t *testing.T) {
	okDebug := map[string]any{"data": map[string]any{"app_id": fbApp, "type": "USER", "is_valid": true, "user_id": "asid-1"}}
	gerr := func(code int) map[string]any {
		return map[string]any{"error": map[string]any{"message": "m", "type": "T", "code": code}}
	}
	write := func(status int, body any) func(http.ResponseWriter, *http.Request) {
		return func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			if body != nil {
				_ = json.NewEncoder(w).Encode(body)
			}
		}
	}
	garbage := func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>not json")) }
	cases := map[string]struct {
		setup func(f *fbFixture, redirectTo string)
		want  error
	}{
		"invalid": {func(f *fbFixture, _ string) {
			f.debug = write(200, map[string]any{"data": map[string]any{"app_id": fbApp, "type": "USER", "is_valid": false, "user_id": "asid-1"}})
		}, idp.ErrInvalidCredential},
		"id mismatch":                    {func(f *fbFixture, _ string) { f.me = write(200, map[string]any{"id": "asid-2"}) }, idp.ErrInvalidCredential},
		"app token rejected":             {func(f *fbFixture, _ string) { f.debug = write(400, gerr(190)) }, idp.ErrMisconfigured},
		"debug throttled":                {func(f *fbFixture, _ string) { f.debug = write(400, gerr(4)) }, idp.ErrUnavailable},
		"me throttled":                   {func(f *fbFixture, _ string) { f.me = write(400, gerr(4)) }, idp.ErrUnavailable},
		"me 5xx":                         {func(f *fbFixture, _ string) { f.me = write(503, nil) }, idp.ErrUnavailable},
		"me code 100":                    {func(f *fbFixture, _ string) { f.me = write(400, gerr(100)) }, idp.ErrMisconfigured},
		"garbage debug":                  {func(f *fbFixture, _ string) { f.debug = garbage }, idp.ErrUnavailable},
		"garbage me":                     {func(f *fbFixture, _ string) { f.me = garbage }, idp.ErrUnavailable},
		"4xx debug without error object": {func(f *fbFixture, _ string) { f.debug = write(400, okDebug) }, idp.ErrUnavailable},
		"4xx me without error object":    {func(f *fbFixture, _ string) { f.me = write(400, map[string]any{"id": "asid-1"}) }, idp.ErrUnavailable},
		"redirect": {func(f *fbFixture, to string) {
			f.debug = func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, to, http.StatusFound) }
		}, idp.ErrUnavailable},
	}
	appToken := fbApp + "|" + fbSecret
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFBFixture(t)
			var hits atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
			defer target.Close()
			c.setup(f, target.URL+"/leak")
			_, err := f.fb.VerifyClassic(context.Background(), fbUserTok)
			if !errors.Is(err, c.want) {
				t.Fatalf("got %v want %v", err, c.want)
			}
			if hits.Load() != 0 {
				t.Fatal("redirect target must never be requested")
			}
			all := err.Error() + "\n" + f.logs.String()
			for _, secret := range []string{fbUserTok, fbSecret, proofOf(fbUserTok), proofOf(appToken)} {
				if strings.Contains(all, secret) {
					t.Fatalf("error or log leaks %q:\n%s", secret, all)
				}
			}
		})
	}
}

func TestFacebookClassicTimeout(t *testing.T) {
	f := newFBFixture(t)
	hang := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-hang:
		case <-r.Context().Done():
		}
	}))
	defer slow.Close()
	defer close(hang)
	fb := idp.NewFacebook(idp.FacebookOptions{AppID: fbApp, AppSecret: fbSecret, GraphAPIVersion: "v26.0", GraphAPIBaseURL: slow.URL, JWKSURL: f.jwks.URL,
		HTTP: &http.Client{Timeout: 100 * time.Millisecond}, Logger: slog.New(slog.NewTextHandler(f.logs, nil))})
	_, err := fb.VerifyClassic(context.Background(), fbUserTok)
	if !errors.Is(err, idp.ErrUnavailable) {
		t.Fatalf("timeout: %v", err)
	}
	all := err.Error() + f.logs.String()
	if strings.Contains(all, fbUserTok) || strings.Contains(all, fbSecret) {
		t.Fatal("timeout path leaks secret")
	}
}
