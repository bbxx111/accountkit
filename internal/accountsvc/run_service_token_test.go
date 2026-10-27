package accountsvc

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/internal/accountsvc/oidcjwt/oidcjwttest"
	"github.com/bbxx111/accountkit/internal/accountsvc/serviceauth"
)

func TestServiceAuthenticatorsWithUnavailableIssuer(t *testing.T) {
	p := oidcjwttest.New(t)
	p.SetFail(true)
	serviceTokenEnvironment(t)
	t.Setenv("ACCOUNTSVC_SERVICE_TOKEN_ISSUER", p.URL())
	t.Setenv("ACCOUNTSVC_SERVICE_TOKEN_AUDIENCE", oidcjwttest.Audience)
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

func TestRuntimeStartsWhenServiceIssuerUnavailable(t *testing.T) {
	serveEnvironment(t)
	t.Setenv("ACCOUNTSVC_TLS_ENABLED", "false")
	t.Setenv("ACCOUNTSVC_TLS_CERT_FILE", "")
	t.Setenv("ACCOUNTSVC_TLS_KEY_FILE", "")
	cfg, err := LoadConfig("serve")
	if err != nil {
		t.Fatal(err)
	}
	cfg.HTTPAddr = "127.0.0.1:0"
	cfg.ServiceToken.Issuer = "https://idp.example.test"
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	var logs safeLog
	bound := make(chan net.Listener, 1)
	preloaded := false
	factory := runtimeFactory{
		open: func(context.Context, Config, *slog.Logger) (*dependencies, error) {
			return &dependencies{databasePing: func(context.Context) error { return nil }, redisPing: func(context.Context) error { return nil }}, nil
		},
		newApp: func(Config, *dependencies, *slog.Logger) (*application, error) {
			ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
			return &application{migrate: func(context.Context) error { return nil }, start: func(context.Context) {}, close: func() {}, endUser: ok, admin: ok, introspect: ok,
				preloadServiceTokens: func(context.Context) error {
					preloaded = true
					return errors.New("synthetic issuer outage https://secret.example/body")
				}}, nil
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
	deadline := time.Now().Add(2 * time.Second)
	for {
		resp, err := client.Get("http://" + ln.Addr().String() + "/readyz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("service not ready while issuer unavailable: %v", err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	stop()
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	out := logs.String()
	if !preloaded || !strings.Contains(out, "level=WARN") || !strings.Contains(out, "https://idp.example.test") || strings.Contains(out, "secret.example") {
		t.Fatalf("preload failure must warn with the issuer only: %s", out)
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
