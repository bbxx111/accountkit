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

// serviceHandler 组装单监听器路由；purgeFeed / jwks 为 nil 时不挂载对应路由（仅供不涉及它们的测试）。
func serviceHandler(cfg Config, endUser, admin, introspect, purgeFeed, jwks, identityResolve http.Handler, state *healthState, metrics ...http.Handler) http.Handler {
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
			next.ServeHTTP(w, r)
		})
	})
	for _, route := range routeDeclarations() {
		var handler http.Handler
		switch route.id {
		case routeIntrospect:
			handler = introspect
		case routePurgeFeed:
			handler = purgeFeed
		case routeIdentityResolve:
			handler = identityResolve
		case routeJWKS:
			handler = jwks
		case routeEndUser:
			handler = endUser
		case routeAdministrator:
			handler = admin
		case routeHealth:
			handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { healthResponse(w, 200, "ok") })
		case routeReady:
			handler = http.HandlerFunc(state.serveReady)
		case routeMetrics:
			if cfg.MetricsEnabled && len(metrics) > 0 {
				handler = metrics[0]
			}
		}
		if handler == nil {
			continue
		}
		switch route.category {
		case routeConsumer, routeAdmin:
			router.Mount(route.path, handler)
		case routeOperations:
			router.Method(route.methods, route.path, handler)
		default:
			// 服务先认证再判方法；JWKS 自行限制 GET/HEAD。
			router.Handle(route.path, handler)
		}
	}
	return requestMetadata(cfg.TrustedProxyCIDRs, router)
}

func newHTTPServer(addr string, handler http.Handler, requestCtx context.Context) *http.Server {
	return &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, BaseContext: func(net.Listener) context.Context { return requestCtx }}
}
