package adminauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestAdminRequestWaitIsBounded(t *testing.T) {
	f := newFixture(t)
	entered, release := make(chan struct{}, 1), make(chan struct{})
	f.entered, f.release = entered, release
	v, err := New(context.Background(), f.config(), Options{HTTPClient: f.server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	defer close(release)
	r := httptest.NewRequest(http.MethodGet, "/admin/v1/users", nil)
	r.Header.Set("Authorization", "Bearer "+f.token(f.claims(time.Now()), "initial"))
	w := httptest.NewRecorder()
	start := time.Now()
	v.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unavailable keys authenticated") })).ServeHTTP(w, r)
	if w.Code != 503 || time.Since(start) > 4*time.Second {
		t.Fatalf("unbounded administrator wait: status %d duration %s", w.Code, time.Since(start))
	}
	select {
	case <-entered:
	default:
		t.Fatal("provider was not contacted")
	}
}

func TestNewDoesNotContactProvider(t *testing.T) {
	f := newFixture(t)
	f.fail = true
	v, err := New(context.Background(), f.config(), Options{HTTPClient: f.server.Client()})
	if err != nil {
		t.Fatalf("static construction contacted provider: %v", err)
	}
	if f.count() != 0 {
		t.Fatal("New fetched keys")
	}
	defer v.Close()
}

func TestAdminProviderRecoversAfterCooldown(t *testing.T) {
	f := newFixture(t)
	now := time.Now().Truncate(time.Second)
	var clock atomic.Int64
	clock.Store(now.Unix())
	f.fail = true
	v, err := New(context.Background(), f.config(), Options{HTTPClient: f.server.Client(), Now: func() time.Time { return time.Unix(clock.Load(), 0) }})
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	if err := v.Load(context.Background()); err == nil {
		t.Fatal("failed provider loaded")
	}
	token := f.token(f.claims(now), "initial")
	if w := request(v, token, ""); w.Code != 503 {
		t.Fatalf("outage status %d", w.Code)
	}
	f.mu.Lock()
	f.fail = false
	f.mu.Unlock()
	if w := request(v, token, ""); w.Code != 503 {
		t.Fatalf("cooldown bypassed: %d", w.Code)
	}
	clock.Store(now.Add(time.Minute).Unix())
	if w := request(v, token, ""); w.Code != 204 {
		t.Fatalf("recovery status %d", w.Code)
	}
	if f.count() != 2 {
		t.Fatalf("want one failed and one recovered fetch: %d", f.count())
	}
}
