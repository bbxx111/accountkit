package accountsvc

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
)

type healthState struct {
	ready    atomic.Bool
	database func(context.Context) error
	redis    func(context.Context) error
}

func healthResponse(w http.ResponseWriter, status int, label string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Status string `json:"status"`
	}{label})
}

func (s *healthState) serveReady(w http.ResponseWriter, r *http.Request) {
	if !s.ready.Load() {
		healthResponse(w, 503, "not_ready")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	results := make(chan error, 2)
	for _, probe := range []func(context.Context) error{s.database, s.redis} {
		go func(probe func(context.Context) error) { results <- probe(ctx) }(probe)
	}
	for i := 0; i < 2; i++ {
		select {
		case err := <-results:
			if err != nil {
				healthResponse(w, 503, "not_ready")
				return
			}
		case <-ctx.Done():
			healthResponse(w, 503, "not_ready")
			return
		}
	}
	if !s.ready.Load() {
		healthResponse(w, 503, "not_ready")
		return
	}
	healthResponse(w, 200, "ready")
}

func serviceHandler(cfg Config, endUser, admin, introspect http.Handler, state *healthState) http.Handler {
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
			next.ServeHTTP(w, r)
		})
	})
	router.Handle("/v1/introspect", introspect)
	router.Mount("/v1", endUser)
	router.Mount("/admin/v1", admin)
	router.Get("/healthz", func(w http.ResponseWriter, r *http.Request) { healthResponse(w, 200, "ok") })
	router.Get("/readyz", state.serveReady)
	return requestMetadata(cfg.TrustedProxyCIDRs, router)
}

func newHTTPServer(addr string, handler http.Handler, requestCtx context.Context) *http.Server {
	return &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, BaseContext: func(net.Listener) context.Context { return requestCtx }}
}
