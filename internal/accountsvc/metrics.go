package accountsvc

import (
	"errors"
	"net/http"

	"github.com/bbxx111/accountkit/observability"
	metricsprom "github.com/bbxx111/accountkit/observability/prometheus"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func newMetrics(cfg Config) (observability.Observer, http.Handler, error) {
	if !cfg.MetricsEnabled {
		return nil, nil, nil
	}
	registry := prometheus.NewRegistry()
	observer, err := metricsprom.New(registry, cfg.MetricsInstance)
	if err != nil {
		return nil, nil, errors.New("accountsvc: metrics construction failed")
	}
	return observer, newMetricsHandler(registry), nil
}

func newMetricsHandler(gatherer prometheus.Gatherer) http.Handler {
	handler := promhttp.HandlerFor(gatherer, promhttp.HandlerOpts{MaxRequestsInFlight: 5})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		handler.ServeHTTP(&metricsResponseWriter{ResponseWriter: w}, r)
	})
}

// promhttp writes collector errors after its error status. Intercept that status
// and discard only the error body, keeping successful metrics streaming directly
// to the client without retaining a second copy of the full exposition.
type metricsResponseWriter struct {
	http.ResponseWriter
	wroteHeader bool
	failed      bool
}

func (w *metricsResponseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	if status >= http.StatusInternalServerError {
		w.failed = true
		w.Header().Del("Content-Encoding")
		w.Header().Del("Content-Length")
		http.Error(w.ResponseWriter, "metrics unavailable", http.StatusServiceUnavailable)
		return
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *metricsResponseWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.failed {
		return len(p), nil
	}
	return w.ResponseWriter.Write(p)
}
