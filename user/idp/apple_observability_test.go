package idp_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/internal/obstest"
	"github.com/bbxx111/accountkit/observability"
	"github.com/bbxx111/accountkit/user/idp"
)

type appleObserverFunc func(context.Context, observability.Observation)

func (f appleObserverFunc) Observe(ctx context.Context, o observability.Observation) { f(ctx, o) }

func TestAppleCachedKeyObservation(t *testing.T) {
	f := newAppleFixture(t)
	// Use the existing key/nonce fixture through a trusted local TLS endpoint.
	tls := httptest.NewTLSServer(f.srv.Config.Handler)
	t.Cleanup(tls.Close)
	var observed obstest.Recorder
	var reentered atomic.Bool
	var nestedErr error
	nestedToken := f.token(t, map[string]any{"nonce": nonceHash("nested")}, nil, "", nil)
	f.apple = idp.NewApple(idp.AppleOptions{
		BundleIDs: []string{"co.shifang.zavelo", "co.shifang.diet"}, JWKSURL: tls.URL, HTTP: tls.Client(), Nonces: f.nonces,
		Now: func() time.Time { return f.now }, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Observer: appleObserverFunc(func(ctx context.Context, o observability.Observation) {
			observed.Observe(ctx, o)
			if reentered.CompareAndSwap(false, true) {
				_, nestedErr = f.apple.VerifyApple(ctx, nestedToken, "nested")
			}
		}),
	})
	ctx := context.Background()
	if _, err := f.apple.VerifyApple(ctx, f.token(t, nil, nil, "", nil), rawNonce); err != nil {
		t.Fatal(err)
	}
	if len(observed.Snapshot()) != 0 {
		t.Fatal("healthy cache must not report fallback")
	}
	f.serve.Store(false)
	f.now = f.now.Add(61 * time.Second)
	if _, err := f.apple.VerifyApple(ctx, f.token(t, nil, nil, "unknown", nil), rawNonce); !errors.Is(err, idp.ErrUnavailable) {
		t.Fatalf("refresh error=%v", err)
	}
	if len(observed.Snapshot()) != 0 {
		t.Fatal("unknown key failure did not actually serve a cached key")
	}
	// Cached key use is observable even if later audience validation fails. The
	// observer reenters key lookup to prove its callback runs after unlocking mu.
	badAudience := f.token(t, map[string]any{"aud": "untrusted-app"}, nil, "", nil)
	done := make(chan error, 1)
	go func() { _, err := f.apple.VerifyApple(ctx, badAudience, rawNonce); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, idp.ErrAppNotAllowed) || nestedErr != nil {
			t.Fatalf("outer=%v nested=%v", err, nestedErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("observer reentrancy deadlocked cached key lookup")
	}
	want := []observability.Observation{
		observability.DegradationObservation{Scenario: observability.DegradationAppleCachedKeyFallback, Count: 1},
		observability.DegradationObservation{Scenario: observability.DegradationAppleCachedKeyFallback, Count: 1},
	}
	if got := observed.Snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("observations=%#v", got)
	}
	if _, err := f.apple.VerifyApple(ctx, f.token(t, nil, nil, "unknown", nil), rawNonce); !errors.Is(err, idp.ErrUnavailable) {
		t.Fatalf("throttled error=%v", err)
	}
	if f.hits.Load() != 2 {
		t.Fatalf("hits=%d, want 2; observations must not fetch JWKS", f.hits.Load())
	}
	f.serve.Store(true)
	f.now = f.now.Add(61 * time.Second)
	if _, err := f.apple.VerifyApple(ctx, f.token(t, nil, nil, "unknown", nil), rawNonce); !errors.Is(err, idp.ErrInvalidCredential) {
		t.Fatalf("recovered unknown key=%v", err)
	}
	if _, err := f.apple.VerifyApple(ctx, f.token(t, map[string]any{"nonce": nonceHash("recovered")}, nil, "", nil), "recovered"); err != nil {
		t.Fatal(err)
	}
	if f.hits.Load() != 3 || !reflect.DeepEqual(observed.Snapshot(), want) {
		t.Fatalf("recovery hits=%d observations=%#v", f.hits.Load(), observed.Snapshot())
	}
	t.Run("observer panic preserves verification", func(t *testing.T) {
		f := newAppleFixture(t)
		var logs bytes.Buffer
		f.apple = idp.NewApple(idp.AppleOptions{
			BundleIDs: []string{"co.shifang.zavelo"}, JWKSURL: f.srv.URL, HTTP: f.srv.Client(), Nonces: f.nonces,
			Now: func() time.Time { return f.now }, Logger: slog.New(slog.NewTextHandler(&logs, nil)),
			Observer: appleObserverFunc(func(context.Context, observability.Observation) { panic("private-apple-observer-secret") }),
		})
		if _, err := f.apple.VerifyApple(ctx, f.token(t, nil, nil, "", nil), rawNonce); err != nil {
			t.Fatal(err)
		}
		f.serve.Store(false)
		if _, err := f.apple.VerifyApple(ctx, f.token(t, nil, nil, "unknown", nil), rawNonce); !errors.Is(err, idp.ErrUnavailable) {
			t.Fatalf("refresh error=%v", err)
		}
		for _, nonce := range []string{"panic-1", "panic-2"} {
			if _, err := f.apple.VerifyApple(ctx, f.token(t, map[string]any{"nonce": nonceHash(nonce)}, nil, "", nil), nonce); err != nil {
				t.Fatalf("cached verification with failed observer: %v", err)
			}
		}
		if f.hits.Load() != 2 || strings.Count(logs.String(), "accountkit observer panic; observation lost") != 1 || strings.Contains(logs.String(), "private-apple-observer-secret") {
			t.Fatalf("hits=%d logs=%s", f.hits.Load(), logs.String())
		}
	})
}
