package accountsvc

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/observability"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	"github.com/redis/go-redis/v9"
)

func metricsApplication(t *testing.T, instance string, enabled bool) (*application, *atomic.Int32) {
	t.Helper()
	serveEnvironment(t)
	cfg, err := LoadConfig("serve")
	if err != nil {
		t.Fatal(err)
	}
	cfg.MetricsEnabled, cfg.MetricsInstance = enabled, instance
	var calls atomic.Int32
	pc, err := pgxpool.ParseConfig("postgres://synthetic:synthetic@127.0.0.1:1/unreachable?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	pc.BeforeConnect = func(context.Context, *pgx.ConnConfig) error { calls.Add(1); return nil }
	pool, err := pgxpool.NewWithConfig(t.Context(), pc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1})
	rdb.AddHook(metricsRedisHook{calls: &calls})
	t.Cleanup(func() { _ = rdb.Close() })
	app, err := newApplication(cfg, &dependencies{pool: pool, redis: rdb}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.close)
	return app, &calls
}

func TestApplicationMetrics(t *testing.T) {
	app, calls := metricsApplication(t, "application-a", true)
	r := httptest.NewRequest(http.MethodPost, "/v1/introspect", strings.NewReader("token=synthetic-invalid-token"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetBasicAuth("test-client", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{8}, 32)))
	w := httptest.NewRecorder()
	app.introspect.ServeHTTP(w, r)
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"active":false}` {
		t.Fatalf("authentication status=%d body=%s", w.Code, w.Body.String())
	}
	h := serviceHandler(Config{MetricsEnabled: true}, app.endUser, app.admin, app.introspect, app.jwks, &healthState{}, app.metrics)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if w.Code != 200 {
		t.Fatalf("metrics status=%d, want 200", w.Code)
	}
	assertAuthenticationMetric(t, w.Body.String(), "application-a", 1)
	if calls.Load() != 0 {
		t.Fatalf("metrics touched unavailable database: %d calls", calls.Load())
	}
}

type metricsRedisHook struct{ calls *atomic.Int32 }

func (h metricsRedisHook) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		h.calls.Add(1)
		return next(ctx, network, addr)
	}
}
func (h metricsRedisHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error { h.calls.Add(1); return next(ctx, cmd) }
}
func (h metricsRedisHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error { h.calls.Add(1); return next(ctx, cmds) }
}

func assertAuthenticationMetric(t *testing.T, body, instance string, count float64) {
	t.Helper()
	parser := expfmt.NewTextParser(model.UTF8Validation)
	families, err := parser.TextToMetricFamilies(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"accountkit_auth_operations_total", "accountkit_auth_operation_duration_seconds"} {
		family := families[name]
		if family == nil || len(family.Metric) != 1 {
			t.Fatalf("missing or mixed family %s: %s", name, body)
		}
		metric := family.Metric[0]
		labels := make(map[string]string)
		for _, label := range metric.Label {
			labels[label.GetName()] = label.GetValue()
		}
		if labels["accountkit_instance"] != instance || labels["operation"] != "authenticate" || labels["result"] != "rejected" {
			t.Fatalf("unexpected authentication labels: %v", labels)
		}
		if metric.Counter != nil && metric.Counter.GetValue() != count {
			t.Fatalf("counter=%v want=%v", metric.Counter.GetValue(), count)
		}
		if metric.Histogram != nil && metric.Histogram.GetSampleCount() != uint64(count) {
			t.Fatalf("histogram count=%v", metric.Histogram.GetSampleCount())
		}
	}
}

func TestMetricsHTTP(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(strconv.FormatBool(enabled), func(t *testing.T) {
			cfg := Config{MetricsEnabled: enabled, MetricsInstance: "http-instance"}
			observer, metrics, err := newMetrics(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if enabled {
				observer.Observe(t.Context(), observability.AuthObservation{Operation: observability.AuthAuthenticate, Result: observability.ResultRejected})
			} else if observer != nil || metrics != nil {
				t.Fatal("disabled metrics allocated observer or handler")
			}
			h := serviceHandler(cfg, http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler(), nil, &healthState{}, metrics)
			for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodHead, http.MethodOptions} {
				w := httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest(method, "/metrics", nil))
				want := 404
				if enabled {
					want = 405
					if method == http.MethodGet {
						want = 200
					}
				}
				if w.Code != want {
					t.Fatalf("%s status=%d want=%d", method, w.Code, want)
				}
				if enabled && method == http.MethodGet {
					if w.Header().Get("Cache-Control") != "no-store" {
						t.Fatal("metrics response may be cached")
					}
					assertAuthenticationMetric(t, w.Body.String(), "http-instance", 1)
					for _, forbidden := range []string{"go_goroutines", "process_cpu", "promhttp_", "synthetic-invalid-token"} {
						if strings.Contains(w.Body.String(), forbidden) {
							t.Fatalf("unexpected exported data %s", forbidden)
						}
					}
				}
			}
		})
	}
}

func TestMetricsIsolation(t *testing.T) {
	first, calls1 := metricsApplication(t, "isolation-a", true)
	second, calls2 := metricsApplication(t, "isolation-b", true)
	disabled, calls3 := metricsApplication(t, "ignored/invalid", false)
	if disabled.metrics != nil {
		t.Fatal("disabled application exports metrics")
	}
	for i := 0; i < 2; i++ {
		r := httptest.NewRequest(http.MethodPost, "/v1/introspect", strings.NewReader("token=invalid"))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.SetBasicAuth("test-client", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{8}, 32)))
		w := httptest.NewRecorder()
		first.introspect.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("introspection status=%d", w.Code)
		}
	}
	w := httptest.NewRecorder()
	first.metrics.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	assertAuthenticationMetric(t, w.Body.String(), "isolation-a", 2)
	w = httptest.NewRecorder()
	second.metrics.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != "" {
		t.Fatalf("second application contains first observations: %s", w.Body.String())
	}
	if calls1.Load()+calls2.Load()+calls3.Load() != 0 {
		t.Fatal("construction, authentication or scraping used an unavailable dependency")
	}
}

func TestMetricsGatherFailure(t *testing.T) {
	h := newMetricsHandler(prometheus.GathererFunc(func() ([]*dto.MetricFamily, error) {
		return nil, errors.New("synthetic-secret-internal-collector-error")
	}))
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	h.ServeHTTP(w, r)
	if w.Code != 503 || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Content-Encoding") != "" || w.Body.String() != "metrics unavailable\n" {
		t.Fatalf("unsafe gather failure: status=%d headers=%v body=%q", w.Code, w.Header(), w.Body.String())
	}
}

func TestMetricsConcurrencyLimit(t *testing.T) {
	started, release, finished := make(chan struct{}, 10), make(chan struct{}), make(chan int, 10)
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	h := newMetricsHandler(prometheus.GathererFunc(func() ([]*dto.MetricFamily, error) {
		started <- struct{}{}
		<-release
		return nil, nil
	}))
	for i := 0; i < 5; i++ {
		go func() {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
			finished <- w.Code
		}()
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("scrape did not begin")
		}
	}
	go func() {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		finished <- w.Code
	}()
	select {
	case status := <-finished:
		if status != 503 {
			t.Fatalf("overload status=%d", status)
		}
	case <-started:
		t.Fatal("sixth concurrent scrape reached gatherer")
	case <-time.After(2 * time.Second):
		t.Fatal("overload request blocked")
	}
	unblock()
	for i := 0; i < 5; i++ {
		select {
		case status := <-finished:
			if status != 200 {
				t.Fatalf("admitted scrape status=%d", status)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("admitted scrape did not finish")
		}
	}
}
