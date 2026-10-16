package accountsvc

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A failed Accept makes net/http untrack the listener before its deferred Close
// finishes. This gate exposes that window without sleeping or changing runtime hooks.
type gatedAcceptListener struct {
	stop                    context.CancelFunc
	firstClose, secondClose chan struct{}
	closeGate               chan struct{}
	releaseOnce             sync.Once
	closeCalls              atomic.Int32
	closed                  atomic.Bool
}

func (l *gatedAcceptListener) Accept() (net.Conn, error) {
	l.stop()
	return nil, errors.New("synthetic accept failure")
}
func (l *gatedAcceptListener) Close() error {
	switch l.closeCalls.Add(1) {
	case 1:
		close(l.firstClose)
	case 2:
		close(l.secondClose)
	}
	<-l.closeGate
	l.closed.Store(true)
	return nil
}
func (l *gatedAcceptListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)} }
func (l *gatedAcceptListener) release()       { l.releaseOnce.Do(func() { close(l.closeGate) }) }

type closeBarrierLog struct {
	started <-chan struct{}
	ctx     context.Context
}

func (l closeBarrierLog) Write(p []byte) (int, error) {
	select {
	case <-l.started:
	case <-l.ctx.Done():
	}
	return len(p), nil
}

func startGatedListenerRuntime(t *testing.T, budget time.Duration) (*gatedAcceptListener, <-chan error, <-chan bool, *events) {
	t.Helper()
	ctx, stop := context.WithCancel(context.Background())
	ln := &gatedAcceptListener{stop: stop, firstClose: make(chan struct{}), secondClose: make(chan struct{}), closeGate: make(chan struct{})}
	trace := &events{}
	librarySawClosed := make(chan bool, 1)
	d := &dependencies{
		databasePing: func(context.Context) error { return nil }, redisPing: func(context.Context) error { return nil },
		closeRedis: func() error { trace.add("redis"); return nil }, closePool: func() { trace.add("pool") },
	}
	app := &application{
		migrate: func(context.Context) error { return nil }, start: func(context.Context) {},
		close:   func() { librarySawClosed <- ln.closed.Load(); trace.add("library") },
		endUser: http.NotFoundHandler(), admin: http.NotFoundHandler(), introspect: http.NotFoundHandler(),
	}
	factory := runtimeFactory{
		open:   func(context.Context, Config, *slog.Logger) (*dependencies, error) { return d, nil },
		newApp: func(Config, *dependencies, *slog.Logger) (*application, error) { return app, nil },
		listen: func(context.Context, string) (net.Listener, error) { return ln, nil },
	}
	logger := slog.New(slog.NewTextHandler(closeBarrierLog{started: ln.firstClose, ctx: t.Context()}, nil))
	result := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		result <- runConfig(ctx, Config{command: "serve", StartupTimeout: time.Second, ShutdownTimeout: budget}, logger, factory)
	}()
	t.Cleanup(func() {
		stop()
		ln.release()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("runtime did not terminate after releasing listener")
		}
	})
	return ln, result, librarySawClosed, trace
}

func TestRuntimeClosesUntrackedListenerBeforeDependencies(t *testing.T) {
	ln, result, librarySawClosed, trace := startGatedListenerRuntime(t, 16*time.Second)
	var err error
	returned := false
	select {
	case <-ln.secondClose:
	case err = <-result:
		returned = true
	case <-time.After(2 * time.Second):
		t.Fatal("runtime neither closed its listener nor returned")
	}
	ln.release()
	if !returned {
		select {
		case err = <-result:
		case <-time.After(2 * time.Second):
			t.Fatal("runtime did not finish shutdown")
		}
	}
	if err != nil {
		t.Fatalf("signal shutdown failed: %v", err)
	}
	select {
	case closed := <-librarySawClosed:
		if !closed {
			t.Fatal("library closed before the owned listener finished closing")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("library close was not observed")
	}
	if got := trace.snapshot(); !reflect.DeepEqual(got, []string{"library", "redis", "pool"}) {
		t.Fatalf("dependency close order changed: %v", got)
	}
}

func TestRuntimeListenerCloseUsesShutdownBudget(t *testing.T) {
	ln, result, _, trace := startGatedListenerRuntime(t, 50*time.Millisecond)
	select {
	case <-ln.secondClose:
	case err := <-result:
		t.Fatalf("runtime returned before closing its owned listener: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("runtime did not attempt owned listener close")
	}
	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "library close timeout") {
			t.Fatalf("blocked listener did not respect existing shutdown budget: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("listener close exceeded shutdown budget")
	}
	if ln.closed.Load() || len(trace.snapshot()) != 0 {
		t.Fatal("dependencies closed while listener close was blocked")
	}
}
