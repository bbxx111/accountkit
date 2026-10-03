package accountsvc

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/user/sender"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func TestHTTPBoundaryAndHealth(t *testing.T) {
	cfg := Config{}
	var probes atomic.Int32
	state := &healthState{database: func(context.Context) error { probes.Add(1); return nil }, redis: func(context.Context) error { probes.Add(1); return nil }}
	mark := func(status int) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) })
	}
	public, internal := serviceHandlers(cfg, mark(201), mark(202), mark(203), state)
	for _, tc := range []struct {
		h            http.Handler
		path, method string
		want         int
	}{{public, "/v1/test", "GET", 201}, {public, "/admin/v1/test", "GET", 202}, {public, "/internal/v1/introspect", "POST", 404}, {public, "/healthz", "GET", 404}, {public, "/readyz", "GET", 404}, {internal, "/v1/test", "GET", 404}, {internal, "/admin/v1/test", "GET", 404}, {internal, "/internal/v1/introspect", "POST", 203}, {internal, "/healthz", "GET", 200}, {internal, "/readyz", "GET", 503}, {internal, "/healthz", "POST", 405}} {
		rec := httptest.NewRecorder()
		tc.h.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
		if rec.Code != tc.want || rec.Header().Get("X-Request-Id") == "" {
			t.Fatalf("%s: got %d want %d", tc.path, rec.Code, tc.want)
		}
	}
	if probes.Load() != 0 {
		t.Fatal("liveness or pre-start readiness accessed dependencies")
	}
	state.ready.Store(true)
	rec := httptest.NewRecorder()
	internal.ServeHTTP(rec, httptest.NewRequest("GET", "/readyz", nil))
	if rec.Code != 200 || probes.Load() != 2 {
		t.Fatal("ready dependencies not checked")
	}
	state.redis = func(context.Context) error { return errors.New("redis://secret@private-address") }
	rec = httptest.NewRecorder()
	internal.ServeHTTP(rec, httptest.NewRequest("GET", "/readyz", nil))
	if rec.Code != 503 || strings.Contains(rec.Body.String(), "secret") || strings.Contains(rec.Body.String(), "private-address") {
		t.Fatal("readiness leaked dependency error")
	}
	rec = httptest.NewRecorder()
	internal.ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != 200 {
		t.Fatal("dependency failure changed liveness")
	}
}

type countedDelivery struct{ calls atomic.Int32 }

func (d *countedDelivery) SendSMS(context.Context, string, sender.Message) error {
	d.calls.Add(1)
	return nil
}
func (d *countedDelivery) SendEmail(context.Context, string, sender.Message) error {
	d.calls.Add(1)
	return nil
}

func TestConsumerDecoderRejectsOversizeBeforeCodeIssuance(t *testing.T) {
	configEnvironment(t)
	cfg, err := LoadConfig("migrate")
	if err != nil {
		t.Fatal(err)
	}
	pc, err := accountkit.PoolConfig(cfg.DatabaseURL, cfg.Library.Schema)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), pc)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	delivery := &countedDelivery{}
	auth, err := accountkit.New(cfg.Library, accountkit.Deps{Pool: pool, Redis: rdb, SMSSender: delivery, EmailSender: delivery, Audit: audit.Noop{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), ClientIP: clientIP, RequestID: requestID})
	if err != nil {
		t.Fatal(err)
	}
	defer auth.Close()
	public, _ := serviceHandlers(cfg, auth.ConsumerHandler(), auth.AdminHandler(), http.NotFoundHandler(), &healthState{})
	server := httptest.NewServer(public)
	defer server.Close()
	valid := `{"channel":"EMAIL","target":"oversize@example.org"}`
	for _, body := range []string{valid + strings.Repeat(" ", 65536), `{"channel":"EMAIL","target":"` + strings.Repeat("x", 65536) + `@example.org"}`} {
		for _, chunked := range []bool{false, true} {
			req, err := http.NewRequest("POST", server.URL+"/v1/users:sendSignInCode", strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			if chunked {
				req.Body = io.NopCloser(strings.NewReader(body))
				req.ContentLength = -1
				req.TransferEncoding = []string{"chunked"}
			}
			req.Header.Set("Content-Type", "application/json")
			res, err := server.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			payload, err := io.ReadAll(res.Body)
			_ = res.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if res.StatusCode != 400 || !strings.Contains(string(payload), "MALFORMED_BODY") {
				t.Fatalf("oversize chunked=%t returned %d", chunked, res.StatusCode)
			}
			if delivery.calls.Load() != 0 || len(mr.Keys()) != 0 {
				t.Fatal("oversize consumer request executed code issuance or delivery")
			}
		}
	}
	// 有效控制请求使用同一真实 decoder/领域存储，证明测试不会因装配不可用而假通过。
	res, err := server.Client().Post(server.URL+"/v1/users:sendSignInCode", "application/json", strings.NewReader(valid))
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 200 || delivery.calls.Load() != 1 || len(mr.Keys()) != 4 {
		t.Fatal("valid control request did not reach real code issuance")
	}
}

func TestReadinessHasSharedTwoSecondBudget(t *testing.T) {
	state := &healthState{database: func(ctx context.Context) error {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 2*time.Second {
			t.Error("missing shared ready deadline")
		}
		<-ctx.Done()
		return ctx.Err()
	}, redis: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }}
	state.ready.Store(true)
	_, internal := serviceHandlers(Config{}, http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler(), state)
	start := time.Now()
	rec := httptest.NewRecorder()
	internal.ServeHTTP(rec, httptest.NewRequest("GET", "/readyz", nil))
	if rec.Code != 503 || time.Since(start) > 2500*time.Millisecond {
		t.Fatal("ready probe exceeded total budget")
	}
}

func TestHTTPServerBudgetsAndBodyLimit(t *testing.T) {
	server := newHTTPServer("127.0.0.1:0", http.NotFoundHandler(), context.Background())
	if server.ReadHeaderTimeout != 5*time.Second || server.ReadTimeout != 15*time.Second || server.WriteTimeout != 30*time.Second || server.IdleTimeout != 60*time.Second {
		t.Fatal("HTTP finite budgets missing")
	}
	// 消费者/管理面在读取超限消息体时必须得到 MaxBytesError；内省保留先认证的原协议。
	reader := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 65537)
		_, err := r.Body.Read(buf)
		var large *http.MaxBytesError
		if errors.As(err, &large) {
			w.WriteHeader(413)
		} else {
			w.WriteHeader(400)
		}
	})
	public, _ := serviceHandlers(Config{}, reader, reader, http.NotFoundHandler(), &healthState{})
	rec := httptest.NewRecorder()
	public.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/test", strings.NewReader(strings.Repeat("x", 65537))))
	if rec.Code != 413 {
		t.Fatal("service public body limit missing")
	}
}
