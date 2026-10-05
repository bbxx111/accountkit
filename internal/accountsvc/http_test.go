package accountsvc

import (
	"bytes"
	"context"
	"encoding/base64"
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
	"github.com/bbxx111/accountkit/internal/accountsvc/introspection"
	"github.com/bbxx111/accountkit/user"
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
	public := serviceHandler(cfg, mark(201), mark(202), mark(203), state)
	for _, tc := range []struct {
		h            http.Handler
		path, method string
		want         int
	}{{public, "/v1/test", "GET", 201}, {public, "/admin/v1/test", "GET", 202}, {public, "/internal/v1/introspect", "POST", 404}, {public, "/v1/introspect", "POST", 203}, {public, "/v1/introspect", "GET", 203}, {public, "/healthz", "GET", 200}, {public, "/readyz", "GET", 503}, {public, "/healthz", "POST", 405}} {
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
	public.ServeHTTP(rec, httptest.NewRequest("GET", "/readyz", nil))
	if rec.Code != 200 || probes.Load() != 2 {
		t.Fatal("ready dependencies not checked")
	}
	state.redis = func(context.Context) error { return errors.New("redis://secret@private-address") }
	rec = httptest.NewRecorder()
	public.ServeHTTP(rec, httptest.NewRequest("GET", "/readyz", nil))
	if rec.Code != 503 || strings.Contains(rec.Body.String(), "secret") || strings.Contains(rec.Body.String(), "private-address") {
		t.Fatal("readiness leaked dependency error")
	}
	rec = httptest.NewRecorder()
	public.ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != 200 {
		t.Fatal("dependency failure changed liveness")
	}
}

func TestHTTPIntrospectionBoundary(t *testing.T) {
	secret := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{8}, 32))
	calls := 0
	introspect, err := introspection.New(map[string][]string{"business": {secret}}, func(context.Context, string) (user.Principal, error) {
		calls++
		return user.Principal{}, user.ErrInvalidToken
	})
	if err != nil {
		t.Fatal(err)
	}
	consumer := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("consumer mount swallowed introspection")
		w.WriteHeader(418)
	})
	public := serviceHandler(Config{}, consumer, http.NotFoundHandler(), introspect, &healthState{})
	for _, tc := range []struct {
		method, auth, contentType, body string
		want                            int
	}{
		{"POST", "", "application/x-www-form-urlencoded", "token=access", 401},
		{"GET", "", "application/json", "", 401},
		{"POST", "Bearer consumer-token", "application/x-www-form-urlencoded", "token=access", 401},
		{"POST", "Bearer administrator-token", "application/x-www-form-urlencoded", "token=access", 401},
		{"POST", "wrong basic", "application/x-www-form-urlencoded", "token=access", 401},
		{"GET", "basic", "application/x-www-form-urlencoded", "token=access", 405},
		{"DELETE", "basic", "application/x-www-form-urlencoded", "token=access", 405},
		{"POST", "basic", "application/json", "{}", 415},
		{"POST", "basic", "application/x-www-form-urlencoded", "", 400},
		{"POST", "basic", "application/x-www-form-urlencoded", "token=" + strings.Repeat("x", 65536), 413},
		{"POST", "", "application/x-www-form-urlencoded", strings.Repeat("x", 65537), 401},
		{"POST", "basic", "application/x-www-form-urlencoded", "token=access", 200},
	} {
		t.Run(tc.method+"/"+tc.auth+"/"+http.StatusText(tc.want), func(t *testing.T) {
			before := calls
			r := httptest.NewRequest(tc.method, "/v1/introspect", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", tc.contentType)
			if tc.auth == "basic" {
				r.SetBasicAuth("business", secret)
			} else if tc.auth == "wrong basic" {
				r.SetBasicAuth("business", "wrong")
			} else if tc.auth != "" {
				r.Header.Set("Authorization", tc.auth)
			}
			w := httptest.NewRecorder()
			public.ServeHTTP(w, r)
			if w.Code != tc.want || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Request-Id") == "" {
				t.Fatalf("status=%d headers=%v body=%s", w.Code, w.Header(), w.Body)
			}
			if tc.want == 401 && (w.Header().Get("WWW-Authenticate") != `Basic realm="accountsvc-introspection"` || !strings.Contains(w.Body.String(), `"error":"invalid_client"`)) {
				t.Fatal("Basic challenge/error lost")
			}
			if tc.want != 200 && calls != before {
				t.Fatal("token authentication ran before caller/format checks")
			}
			if tc.want == 405 && w.Header().Get("Allow") != "POST" {
				t.Fatal("method error lost")
			}
			if tc.want == 200 && (calls != before+1 || strings.TrimSpace(w.Body.String()) != `{"active":false}`) {
				t.Fatal("real introspection handler not reached")
			}
		})
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
	public := serviceHandler(cfg, auth.ConsumerHandler(), auth.AdminHandler(), http.NotFoundHandler(), &healthState{})
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
	internal := serviceHandler(Config{}, http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler(), state)
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
	public := serviceHandler(Config{}, reader, reader, http.NotFoundHandler(), &healthState{})
	rec := httptest.NewRecorder()
	public.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/test", strings.NewReader(strings.Repeat("x", 65537))))
	if rec.Code != 413 {
		t.Fatal("service public body limit missing")
	}
}
