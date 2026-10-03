package adminauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestJWKSRotationAndFailureCategories(t *testing.T) {
	f := newFixture(t)
	now := time.Now().Truncate(time.Second)
	var clock atomic.Int64
	clock.Store(now.Unix())
	v := f.verifier(Options{Now: func() time.Time { return time.Unix(clock.Load(), 0) }})
	known := f.token(f.claims(now), "initial")
	unknown := f.token(f.claims(now), "rotated")
	if w := request(v, unknown, ""); w.Code != 401 {
		t.Fatalf("successful fetch, unknown kid: %d", w.Code)
	}
	if f.count() != 2 {
		t.Fatalf("unknown must refresh once: %d", f.count())
	}
	for range 10 {
		if w := request(v, unknown, ""); w.Code != 401 {
			t.Fatalf("successful throttled unknown: %d", w.Code)
		}
	}
	if f.count() != 2 {
		t.Fatalf("unknown cache unbounded: %d", f.count())
	}
	f.mu.Lock()
	f.keys = append(f.keys, jwk("rotated", &f.key.PublicKey))
	f.mu.Unlock()
	clock.Store(now.Add(59 * time.Second).Unix())
	if w := request(v, unknown, ""); w.Code != 401 {
		t.Fatalf("59s should throttle: %d", w.Code)
	}
	clock.Store(now.Add(60 * time.Second).Unix())
	if w := request(v, unknown, ""); w.Code != 204 {
		t.Fatalf("rotation not accepted: %d %s", w.Code, w.Body.String())
	}
	f.mu.Lock()
	f.fail = true
	f.mu.Unlock()
	clock.Store(now.Add(120 * time.Second).Unix())
	missing := f.token(f.claims(now), "missing")
	if w := request(v, missing, ""); w.Code != 503 {
		t.Fatalf("failed unknown fetch: %d", w.Code)
	}
	before := f.count()
	for range 10 {
		if w := request(v, missing, ""); w.Code != 503 {
			t.Fatalf("failed throttled fetch: %d", w.Code)
		}
	}
	if f.count() != before {
		t.Fatal("failure throttle re-fetched")
	}
	if w := request(v, known, ""); w.Code != 204 {
		t.Fatalf("known valid cache must survive provider failure: %d", w.Code)
	}
	clock.Store(now.Add(60*time.Second + time.Hour).Unix())
	if w := request(v, known, ""); w.Code != 503 {
		t.Fatalf("hard-expired key must not authenticate: %d", w.Code)
	}
	before = f.count()
	for range 10 {
		if w := request(v, known, ""); w.Code != 503 {
			t.Fatalf("hard expiry failure throttle: %d", w.Code)
		}
	}
	if f.count() != before {
		t.Fatal("hard expiry failure storm")
	}
	f.mu.Lock()
	f.fail = false
	f.mu.Unlock()
	clock.Store(now.Add(120*time.Second + time.Hour).Unix())
	if w := request(v, known, ""); w.Code != 204 {
		t.Fatalf("provider recovery: %d", w.Code)
	}
}

func TestConcurrentUnknownKidsCoalesce(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fail], func(t *testing.T) {
			f := newFixture(t)
			now := time.Now().Truncate(time.Second)
			v := f.verifier(Options{Now: func() time.Time { return now }})
			entered, release := make(chan struct{}, 1), make(chan struct{})
			f.mu.Lock()
			f.fail = fail
			f.entered = entered
			f.release = release
			f.mu.Unlock()
			var wg sync.WaitGroup
			results := make(chan int, 32)
			for i := range 32 {
				token := f.token(f.claims(now), strings.Repeat("k", i+1))
				wg.Go(func() { results <- request(v, token, "").Code })
			}
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("refresh not started")
			}
			if w := request(v, f.token(f.claims(now), "initial"), ""); w.Code != 204 {
				t.Errorf("known cache blocked by refresh: %d", w.Code)
			}
			close(release)
			wg.Wait()
			close(results)
			want := 401
			if fail {
				want = 503
			}
			for code := range results {
				if code != want {
					t.Errorf("mixed unknown concurrent status %d, want %d", code, want)
				}
			}
			if f.count() != 2 {
				t.Fatalf("concurrent refresh count %d, want 2 including startup", f.count())
			}
		})
	}
}

func TestCanceledRefreshWaiterDoesNotStartExtraFetch(t *testing.T) {
	f := newFixture(t)
	v := f.verifier(Options{})
	entered, release := make(chan struct{}, 1), make(chan struct{})
	f.mu.Lock()
	f.entered = entered
	f.release = release
	f.mu.Unlock()
	done := make(chan struct{})
	go func() { request(v, f.token(f.claims(time.Now()), "missing"), ""); close(done) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("refresh not started")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest("GET", "/", nil).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer "+f.token(f.claims(time.Now()), "other"))
	w := httptest.NewRecorder()
	v.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("canceled request authenticated") })).ServeHTTP(w, r)
	if w.Code != 503 {
		t.Fatalf("cancel status %d", w.Code)
	}
	close(release)
	<-done
	if f.count() != 2 {
		t.Fatal("canceled waiter started fetch")
	}
}

func TestCanceledRefreshLeaderDoesNotPoisonSharedRefresh(t *testing.T) {
	for _, scenario := range []string{"hard-expiry", "key-rotation"} {
		t.Run(scenario, func(t *testing.T) {
			f := newFixture(t)
			now := time.Now().Truncate(time.Second)
			var clock atomic.Int64
			clock.Store(now.Unix())
			v := f.verifier(Options{Now: func() time.Time { return time.Unix(clock.Load(), 0) }})
			kid := "initial"
			if scenario == "hard-expiry" {
				clock.Store(now.Add(time.Hour).Unix())
			} else {
				kid = "rotated"
				f.mu.Lock()
				f.keys = append(f.keys, jwk(kid, &f.key.PublicKey))
				f.mu.Unlock()
			}
			token := f.token(f.claims(now), kid)
			entered, release := make(chan struct{}, 1), make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			defer unblock()
			f.mu.Lock()
			f.entered, f.release = entered, release
			f.mu.Unlock()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r := httptest.NewRequest("GET", "/", nil).WithContext(ctx)
			r.Header.Set("Authorization", "Bearer "+token)
			leader := make(chan int, 1)
			go func() {
				w := httptest.NewRecorder()
				v.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })).ServeHTTP(w, r)
				leader <- w.Code
			}()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("leader did not start refresh")
			}
			waiter := make(chan int, 1)
			waiterStarted := make(chan struct{})
			go func() { close(waiterStarted); waiter <- request(v, token, "").Code }()
			<-waiterStarted
			cancel()
			select {
			case code := <-leader:
				if code != 503 {
					t.Fatalf("canceled leader status %d", code)
				}
			case <-time.After(time.Second):
				t.Fatal("caller cancellation did not stop its own wait")
			}
			unblock()
			select {
			case code := <-waiter:
				if code != 204 {
					t.Fatalf("healthy provider poisoned by leader cancellation: waiter status %d", code)
				}
			case <-time.After(time.Second):
				t.Fatal("shared refresh did not release waiter")
			}
			if w := request(v, token, ""); w.Code != 204 {
				t.Fatalf("healthy provider throttled after leader cancellation: status %d", w.Code)
			}
			if f.count() != 2 {
				t.Fatalf("shared refresh downloaded %d times, want 2 including startup", f.count())
			}
		})
	}
}

func TestRedirectDowngradeAndRequestLimits(t *testing.T) {
	var insecureRequests atomic.Int32
	insecure := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { insecureRequests.Add(1); _, _ = w.Write([]byte(`{}`)) }))
	defer insecure.Close()
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, insecure.URL, http.StatusFound) }))
	defer secure.Close()
	if _, err := New(context.Background(), Config{Issuer: secure.URL, Audience: "admin"}, Options{HTTPClient: secure.Client()}); err == nil {
		t.Fatal("redirect downgrade accepted")
	}
	if insecureRequests.Load() != 0 {
		t.Fatal("insecure request followed redirect")
	}
	t.Run("discovery-limit", func(t *testing.T) {
		s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(strings.Repeat(" ", 256*1024+1))) }))
		defer s.Close()
		if _, err := New(context.Background(), Config{Issuer: s.URL, Audience: "admin"}, Options{HTTPClient: s.Client()}); err == nil {
			t.Fatal("oversized discovery accepted")
		}
	})
	t.Run("caller-timeout", func(t *testing.T) {
		s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
		defer s.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		start := time.Now()
		if _, err := New(ctx, Config{Issuer: s.URL, Audience: "admin"}, Options{HTTPClient: s.Client()}); err == nil {
			t.Fatal("timeout accepted")
		}
		if time.Since(start) > time.Second {
			t.Fatal("ignored caller timeout")
		}
	})
	t.Run("untrusted-tls", func(t *testing.T) {
		f := newFixture(t)
		if _, err := New(context.Background(), f.config(), Options{}); err == nil {
			t.Fatal("untrusted issuer accepted")
		}
	})
	t.Run("large-key-set", func(t *testing.T) {
		f := newFixture(t)
		for i := range 129 {
			f.keys = append(f.keys, jwk(strings.Repeat("x", i+1), &f.key.PublicKey))
		}
		if _, err := New(context.Background(), f.config(), Options{HTTPClient: f.server.Client()}); err == nil {
			t.Fatal("unbounded key set accepted")
		}
	})
	t.Run("non-signing-key", func(t *testing.T) {
		f := newFixture(t)
		f.keys[0]["use"] = "enc"
		if _, err := New(context.Background(), f.config(), Options{HTTPClient: f.server.Client()}); err == nil {
			t.Fatal("encryption key trusted for signatures")
		}
	})
	t.Run("malformed-key", func(t *testing.T) {
		f := newFixture(t)
		f.keys[0]["e"] = "AAAAAA"
		body, _ := json.Marshal(map[string]any{"keys": f.keys})
		f.body = string(body)
		if _, err := New(context.Background(), f.config(), Options{HTTPClient: f.server.Client()}); err == nil {
			t.Fatal("invalid exponent accepted")
		}
	})
}
