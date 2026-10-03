package accountsvc

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"
)

// requestTracker 先阻止新请求进入，再等待已进入 handler 的请求结束。
// Server.Close 只关闭连接，不能替代这一等待（handler 仍可能持有数据库连接）。
type requestTracker struct {
	mu       sync.Mutex
	active   int
	stopping bool
	done     chan struct{}
}

func newRequestTracker() *requestTracker {
	done := make(chan struct{})
	close(done)
	return &requestTracker{done: done}
}
func (t *requestTracker) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.mu.Lock()
		if t.stopping {
			t.mu.Unlock()
			healthResponse(w, 503, "stopping")
			return
		}
		if t.active == 0 {
			t.done = make(chan struct{})
		}
		t.active++
		t.mu.Unlock()
		defer func() {
			t.mu.Lock()
			t.active--
			if t.active == 0 {
				close(t.done)
			}
			t.mu.Unlock()
		}()
		next.ServeHTTP(w, r)
	})
}
func (t *requestTracker) stop() <-chan struct{} {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stopping = true
	return t.done
}

func boundedClose(ctx context.Context, fn func()) error {
	done := make(chan struct{})
	go func() { defer close(done); fn() }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func shutdownRuntime(ctx context.Context, httpBudget time.Duration, servers []*http.Server, state *healthState, tracker *requestTracker, cancelRequests func(), closeLibrary func(), closeRedis func() error, closePool func()) error {
	state.ready.Store(false)
	requestsDone := tracker.stop()
	defer cancelRequests()
	httpCtx, stop := context.WithTimeout(ctx, httpBudget)
	defer stop()
	results := make(chan error, len(servers))
	for _, server := range servers {
		go func(server *http.Server) { results <- server.Shutdown(httpCtx) }(server)
	}
	var result error
	for i := 0; i < len(servers); i++ {
		select {
		case err := <-results:
			if err != nil {
				result = errors.New("accountsvc: HTTP drain timeout")
			}
		case <-httpCtx.Done():
			result = errors.New("accountsvc: HTTP drain timeout")
		}
	}
	if result != nil {
		cancelRequests()
		for _, server := range servers {
			_ = server.Close()
		}
	}
	select {
	case <-requestsDone:
	case <-ctx.Done():
		return errors.New("accountsvc: request termination timeout")
	}
	// 在途请求必须已经退出，才能停止异步审计与其数据库依赖。
	if closeLibrary != nil {
		if err := boundedClose(ctx, closeLibrary); err != nil {
			return errors.New("accountsvc: library close timeout")
		}
	}
	if closeRedis != nil {
		done := make(chan error, 1)
		go func() { done <- closeRedis() }()
		select {
		case err := <-done:
			if err != nil {
				result = errors.New("accountsvc: Redis close failed")
			}
		case <-ctx.Done():
			result = errors.New("accountsvc: Redis close timeout")
		}
	}
	// pgxpool.Close 无 context 且会等待借出的连接；外层预算防止进程无限等待。
	if closePool != nil {
		if err := boundedClose(ctx, closePool); err != nil {
			return errors.New("accountsvc: database pool close timeout")
		}
	}
	return result
}
