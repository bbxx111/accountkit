package accountsvc

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/internal/accountsvc/oidcjwt"
	"github.com/bbxx111/accountkit/internal/accountsvc/oidcjwt/oidcjwttest"
	"github.com/bbxx111/accountkit/internal/accountsvc/serviceauth"
)

func TestServiceAuthenticatorsWithUnavailableIssuer(t *testing.T) {
	p := oidcjwttest.New(t)
	p.SetFail(true)
	serviceTokenEnvironment(t)
	t.Setenv("ACCOUNTSVC_SERVICE_ISSUER", p.URL())
	t.Setenv("ACCOUNTSVC_SERVICE_AUDIENCE", oidcjwttest.Audience)
	cfg, err := LoadConfig("serve")
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	intro, purge, preload, err := serviceAuthenticators(cfg, slog.New(slog.NewTextHandler(&logs, nil)), p.Client())
	if err != nil || preload == nil {
		t.Fatalf("authenticators: %v", err)
	}
	if err := preload(context.Background()); err == nil {
		t.Fatal("preload succeeded against a failing issuer")
	}
	request := func(a *serviceauth.Authenticator, authorization string) serviceauth.Result {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		r.Header.Set("Authorization", authorization)
		return a.Authenticate(r)
	}
	if res := request(intro, "Bearer "+p.Token(p.Claims(time.Now(), "intro-client"), "initial")); res.Outcome != serviceauth.Unavailable {
		t.Fatalf("bearer outcome with unavailable issuer = %v", res.Outcome)
	}
	var basic string
	for id, secrets := range cfg.IntrospectionClients {
		basic = "Basic " + base64.StdEncoding.EncodeToString([]byte(id+":"+secrets[0]))
	}
	if res := request(intro, basic); res.Outcome != serviceauth.OK {
		t.Fatalf("basic outcome with unavailable issuer = %v", res.Outcome)
	}
	if purge == nil || !purge.Enabled() {
		t.Fatal("purge feed authenticator missing")
	}
}

func TestServiceAuthenticatorsWithoutTokens(t *testing.T) {
	serveEnvironment(t)
	cfg, err := LoadConfig("serve")
	if err != nil {
		t.Fatal(err)
	}
	intro, purge, preload, err := serviceAuthenticators(cfg, slog.New(slog.DiscardHandler), nil)
	if err != nil || preload != nil || !intro.Enabled() || purge.Enabled() {
		t.Fatalf("basic-only wiring: err=%v preload=%v intro=%v purge=%v", err, preload != nil, intro.Enabled(), purge.Enabled())
	}
}

// runWithPreload starts runConfig with fake dependencies and the given preload, waits for
// /readyz, stops the service and returns its log.
func runWithPreload(t *testing.T, startupTimeout time.Duration, preload func(context.Context) error) string {
	t.Helper()
	serveEnvironment(t)
	t.Setenv("ACCOUNTSVC_TLS_ENABLED", "false")
	t.Setenv("ACCOUNTSVC_TLS_CERT_FILE", "")
	t.Setenv("ACCOUNTSVC_TLS_KEY_FILE", "")
	cfg, err := LoadConfig("serve")
	if err != nil {
		t.Fatal(err)
	}
	cfg.HTTPAddr = "127.0.0.1:0"
	cfg.StartupTimeout = startupTimeout
	cfg.ServiceToken.Issuer = "https://idp.example.test"
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	var logs safeLog
	bound := make(chan net.Listener, 1)
	factory := runtimeFactory{
		open: func(context.Context, Config, *slog.Logger) (*dependencies, error) {
			return &dependencies{databasePing: func(context.Context) error { return nil }, redisPing: func(context.Context) error { return nil }}, nil
		},
		newApp: func(Config, *dependencies, *slog.Logger) (*application, error) {
			ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
			return &application{migrate: func(context.Context) error { return nil }, start: func(context.Context) {}, close: func() {}, endUser: ok, admin: ok, introspect: ok, preloadServiceTokens: preload}, nil
		},
		listen: func(ctx context.Context, addr string) (net.Listener, error) {
			ln, err := net.Listen("tcp", addr)
			if err == nil {
				bound <- ln
			}
			return ln, err
		},
	}
	result := make(chan error, 1)
	go func() { result <- runConfig(ctx, cfg, slog.New(slog.NewTextHandler(&logs, nil)), factory) }()
	var ln net.Listener
	select {
	case ln = <-bound:
	case err := <-result:
		t.Fatalf("startup failed: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("listener not bound")
	}
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(3 * time.Second)
	for {
		resp, err := client.Get("http://" + ln.Addr().String() + "/readyz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				break
			}
		}
		select {
		case err := <-result:
			t.Fatalf("service exited while issuer unavailable: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("service not ready while issuer unavailable: %v", err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	stop()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("shutdown did not finish")
	}
	return logs.String()
}

func TestRuntimeStartsWhenServiceIssuerUnavailable(t *testing.T) {
	var preloaded atomic.Bool
	out := runWithPreload(t, 10*time.Second, func(context.Context) error {
		preloaded.Store(true)
		return errors.New("synthetic issuer outage https://secret.example/body")
	})
	if !preloaded.Load() || !strings.Contains(out, "level=WARN") || !strings.Contains(out, "https://idp.example.test") || strings.Contains(out, "secret.example") {
		t.Fatalf("preload failure must warn with the issuer only: %s", out)
	}
}

// A hanging issuer (dropped packets, DNS black hole) must neither consume the startup
// budget nor delay readiness; shutdown cancels the pending preload.
func TestRuntimeStartsWhenServiceIssuerHangs(t *testing.T) {
	var cancelled atomic.Bool
	runWithPreload(t, 300*time.Millisecond, func(ctx context.Context) error {
		<-ctx.Done()
		cancelled.Store(true)
		return ctx.Err()
	})
	if !cancelled.Load() {
		t.Fatal("pending preload was not cancelled before shutdown completed")
	}
}

type safeLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *safeLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}
func (l *safeLog) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.buf.String() }

func TestPreloadIssuerMismatchLogsError(t *testing.T) {
	var logs safeLog
	stop := preloadServiceTokens(func(context.Context) error { return fmt.Errorf("wrapped: %w", oidcjwt.ErrIssuerMismatch) }, "https://idp.example.test", slog.New(slog.NewTextHandler(&logs, nil)))
	stop()
	if out := logs.String(); !strings.Contains(out, "level=ERROR") || !strings.Contains(out, "https://idp.example.test") {
		t.Fatalf("issuer mismatch at startup must log ERROR: %s", out)
	}
}
