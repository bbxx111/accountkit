package accountsvc

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"
)

type events struct {
	mu    sync.Mutex
	names []string
}

func (e *events) add(name string) { e.mu.Lock(); defer e.mu.Unlock(); e.names = append(e.names, name) }
func (e *events) snapshot() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string{}, e.names...)
}

func TestShutdownDrainsBeforeLibraryAndPool(t *testing.T) {
	tracker := newRequestTracker()
	state := &healthState{}
	state.ready.Store(true)
	trace := &events{}
	requestCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewUnstartedServer(tracker.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		trace.add("last request audit")
		w.WriteHeader(200)
	})))
	srv.Config.BaseContext = newHTTPServer("", nil, requestCtx).BaseContext
	srv.Start()
	defer srv.Close()
	doneRequest := make(chan struct{})
	go func() {
		defer close(doneRequest)
		resp, err := http.Get(srv.URL)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
	}()
	<-entered
	ctx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	result := make(chan error, 1)
	go func() {
		result <- shutdownRuntime(ctx, 300*time.Millisecond, []*http.Server{srv.Config}, state, tracker, cancel, func() {
			if requestCtx.Err() != nil {
				t.Error("signal cancelled request context before graceful drain")
			}
			trace.add("library")
		}, func() error { trace.add("redis"); return nil }, func() { trace.add("pool") })
	}()
	time.Sleep(30 * time.Millisecond)
	if state.ready.Load() || len(trace.snapshot()) != 0 {
		t.Fatal("shutdown did not withdraw ready before drain")
	}
	close(release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	<-doneRequest
	if got := trace.snapshot(); !reflect.DeepEqual(got, []string{"last request audit", "library", "redis", "pool"}) {
		t.Fatalf("wrong close order %v", got)
	}
}

func TestShutdownTimeoutCancelsRequestsAndBoundsPoolClose(t *testing.T) {
	tracker := newRequestTracker()
	state := &healthState{}
	state.ready.Store(true)
	trace := &events{}
	requestCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	srv := httptest.NewUnstartedServer(tracker.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		trace.add("request stopped")
	})))
	srv.Config.BaseContext = newHTTPServer("", nil, requestCtx).BaseContext
	srv.Start()
	defer srv.Close()
	go func() {
		resp, err := http.Get(srv.URL)
		if err == nil {
			_ = resp.Body.Close()
		}
	}()
	<-entered
	blockPool := make(chan struct{})
	defer close(blockPool)
	ctx, stop := context.WithTimeout(context.Background(), 180*time.Millisecond)
	defer stop()
	start := time.Now()
	result := make(chan error, 1)
	go func() {
		result <- shutdownRuntime(ctx, 40*time.Millisecond, []*http.Server{srv.Config}, state, tracker, cancel, func() { trace.add("library") }, func() error { trace.add("redis"); return nil }, func() { trace.add("pool"); <-blockPool })
	}()
	select {
	case err := <-result:
		if err == nil || time.Since(start) > 350*time.Millisecond {
			t.Fatal("shutdown timeout was hidden or exceeded")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("pool close blocked total shutdown budget")
	}
	if got := trace.snapshot(); !reflect.DeepEqual(got, []string{"request stopped", "library", "redis", "pool"}) {
		t.Fatalf("wrong forced close order %v", got)
	}
}

func TestShutdownDoesNotCloseResourcesUnderUncooperativeHandler(t *testing.T) {
	tracker := newRequestTracker()
	state := &healthState{}
	requestCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewUnstartedServer(tracker.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-release })))
	srv.Config.BaseContext = newHTTPServer("", nil, requestCtx).BaseContext
	srv.Start()
	defer srv.Close()
	defer close(release)
	go func() {
		resp, err := http.Get(srv.URL)
		if err == nil {
			_ = resp.Body.Close()
		}
	}()
	<-entered
	ctx, stop := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer stop()
	closed := false
	err := shutdownRuntime(ctx, 20*time.Millisecond, []*http.Server{srv.Config}, state, tracker, cancel, func() { closed = true }, func() error { closed = true; return nil }, func() { closed = true })
	if err == nil || closed {
		t.Fatal("uncooperative request caused early dependency close")
	}
}
