package accountsvc

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRuntimeSingleListener(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("TLS_%v", enabled), func(t *testing.T) {
			serveEnvironment(t)
			if !enabled {
				t.Setenv("ACCOUNTSVC_TLS_ENABLED", "false")
				t.Setenv("ACCOUNTSVC_TLS_CERT_FILE", "")
				t.Setenv("ACCOUNTSVC_TLS_KEY_FILE", "")
			}
			cfg, err := LoadConfig("serve")
			if err != nil {
				t.Fatal(err)
			}
			cfg.HTTPAddr = "127.0.0.1:0"
			ctx, stop := context.WithCancel(context.Background())
			defer stop()
			var log bytes.Buffer
			binds := 0
			bound := make(chan net.Listener, 2)
			mark := func(status int) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) })
			}
			factory := runtimeFactory{
				open: func(context.Context, Config, *slog.Logger) (*dependencies, error) {
					return &dependencies{databasePing: func(context.Context) error { return nil }, redisPing: func(context.Context) error { return nil }}, nil
				},
				newApp: func(Config, *dependencies, *slog.Logger) (*application, error) {
					return &application{migrate: func(context.Context) error { return nil }, start: func(context.Context) {}, close: func() {}, consumer: mark(201), admin: mark(202), introspect: mark(203)}, nil
				},
				listen: func(ctx context.Context, addr string) (net.Listener, error) {
					binds++
					ln, err := net.Listen("tcp", addr)
					if err == nil {
						bound <- ln
					}
					return ln, err
				},
			}
			result := make(chan error, 1)
			go func() { result <- runConfig(ctx, cfg, slog.New(slog.NewTextHandler(&log, nil)), factory) }()
			var ln net.Listener
			select {
			case ln = <-bound:
			case err := <-result:
				t.Fatalf("startup failed: %v", err)
			case <-time.After(2 * time.Second):
				t.Fatal("listener not bound")
			}
			_, port, _ := net.SplitHostPort(ln.Addr().String())
			baseURL := "http://localhost:" + port
			transport := &http.Transport{}
			defer transport.CloseIdleConnections()
			if enabled {
				baseURL = "https://localhost:" + port
				roots := x509.NewCertPool()
				cert, err := x509.ParseCertificate(cfg.tlsConfig.Certificates[0].Certificate[0])
				if err != nil {
					t.Fatal(err)
				}
				roots.AddCert(cert)
				transport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
			}
			client := &http.Client{Transport: transport, Timeout: time.Second}
			deadline := time.Now().Add(2 * time.Second)
			for {
				resp, err := client.Get(baseURL + "/readyz")
				if err == nil {
					resp.Body.Close()
					if resp.StatusCode == 200 {
						break
					}
				}
				if time.Now().After(deadline) {
					t.Fatalf("same-address readiness failed: %v", err)
				}
				time.Sleep(time.Millisecond)
			}
			for _, tc := range []struct {
				path   string
				status int
			}{{"/v1/test", 201}, {"/admin/v1/test", 202}, {"/v1/introspect", 203}, {"/healthz", 200}, {"/readyz", 200}, {"/internal/v1/introspect", 404}} {
				resp, err := client.Get(baseURL + tc.path)
				if err != nil {
					t.Fatal(err)
				}
				resp.Body.Close()
				if resp.StatusCode != tc.status {
					t.Fatalf("%s status=%d want=%d", tc.path, resp.StatusCode, tc.status)
				}
			}
			stop()
			if err := <-result; err != nil {
				t.Fatal(err)
			}
			if binds != 1 {
				t.Fatalf("listener bound %d times, want 1", binds)
			}
			if !enabled && !strings.Contains(log.String(), "HTTP TLS is disabled in this process") {
				t.Fatal("effective HTTP transport log missing")
			}
			if enabled && !strings.Contains(log.String(), "HTTPS listener started") {
				t.Fatal("effective HTTPS transport log missing")
			}
		})
	}
}

func TestCommandParsing(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
		bad  bool
	}{{nil, "serve", false}, {[]string{"serve"}, "serve", false}, {[]string{"migrate"}, "migrate", false}, {[]string{"synthetic-secret"}, "", true}, {[]string{"serve", "synthetic-secret"}, "", true}} {
		command, err := parseCommand(tc.args)
		if (err != nil) != tc.bad || command != tc.want {
			t.Fatal("incorrect command dispatch")
		}
		if err != nil && strings.Contains(err.Error(), "synthetic-secret") {
			t.Fatal("unknown arguments leaked")
		}
	}
}

func TestRuntimeStartupOrderAndFailureCleanup(t *testing.T) {
	for _, failure := range []string{"", "open", "new", "migrate", "admin", "listen", "after listen", "listener stopped"} {
		t.Run(failure, func(t *testing.T) {
			ctx, stop := context.WithCancel(context.Background())
			defer stop()
			trace := &events{}
			var serviceCtx context.Context
			fail := func(stage string) error {
				trace.add(stage)
				if failure == stage {
					return errors.New("synthetic-sensitive-upstream")
				}
				return nil
			}
			deps := &dependencies{databasePing: func(context.Context) error { return nil }, redisPing: func(context.Context) error { return nil }, closeRedis: func() error { trace.add("redis close"); return nil }, closePool: func() { trace.add("pool close") }}
			var first net.Listener
			app := &application{migrate: func(context.Context) error { return fail("migrate") }, initAdmin: func(context.Context) error { return fail("admin") }, start: func(c context.Context) {
				serviceCtx = c
				trace.add("start")
				if failure == "listener stopped" {
					_ = first.Close()
				} else {
					stop()
				}
			}, close: func() {
				if serviceCtx != nil && serviceCtx.Err() != nil {
					t.Error("signal prematurely cancelled library background")
				}
				trace.add("library close")
			}, consumer: http.NotFoundHandler(), admin: http.NotFoundHandler(), introspect: http.NotFoundHandler()}
			listens := 0
			factory := runtimeFactory{
				open: func(context.Context, Config, *slog.Logger) (*dependencies, error) {
					if err := fail("open"); err != nil {
						return nil, err
					}
					return deps, nil
				},
				newApp: func(Config, *dependencies, *slog.Logger) (*application, error) {
					if err := fail("new"); err != nil {
						return nil, err
					}
					return app, nil
				},
				listen: func(ctx context.Context, addr string) (net.Listener, error) {
					listens++
					stage := "listen"
					if err := fail(stage); err != nil {
						return nil, err
					}
					ln, err := net.Listen("tcp", "127.0.0.1:0")
					if listens == 1 {
						first = ln
					}
					if failure == "after listen" {
						stop()
					}
					return ln, err
				},
			}
			cfg := Config{command: "serve", AdminEnabled: true, StartupTimeout: time.Second, ShutdownTimeout: 16 * time.Second, HTTPAddr: "127.0.0.1:0"}
			err := runConfig(ctx, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), factory)
			if (err != nil) != (failure != "") {
				t.Fatalf("failure %s returned wrong status", failure)
			}
			if err != nil && strings.Contains(err.Error(), "synthetic-sensitive-upstream") {
				t.Fatal("startup error leaked original dependency error")
			}
			want := []string{"open"}
			if failure != "open" {
				want = append(want, "new")
				if failure != "new" {
					want = append(want, "migrate")
					if failure != "migrate" {
						want = append(want, "admin")
						if failure != "admin" {
							want = append(want, "listen")
							if failure != "listen" && failure != "after listen" {
								want = append(want, "start")
							}
						}
					}
					want = append(want, "library close")
				}
				want = append(want, "redis close", "pool close")
			}
			if got := trace.snapshot(); !reflect.DeepEqual(got, want) {
				t.Fatalf("startup/close order got %v want %v", got, want)
			}
			if first != nil {
				if conn, err := net.DialTimeout("tcp", first.Addr().String(), 50*time.Millisecond); err == nil {
					_ = conn.Close()
					t.Fatal("startup/shutdown leaked first listener")
				}
			}
		})
	}
}

func TestMigrateDoesNotInitializeServiceOrStartWorkers(t *testing.T) {
	trace := &events{}
	factory := runtimeFactory{
		open: func(context.Context, Config, *slog.Logger) (*dependencies, error) {
			trace.add("open")
			return &dependencies{closeRedis: func() error { trace.add("redis"); return nil }, closePool: func() { trace.add("pool") }}, nil
		},
		newApp: func(cfg Config, d *dependencies, l *slog.Logger) (*application, error) {
			trace.add("new")
			return &application{migrate: func(context.Context) error { trace.add("migrate"); return nil }, close: func() { trace.add("library") }, initAdmin: func(context.Context) error { t.Error("migrate initialized admin"); return nil }, start: func(context.Context) { t.Error("migrate started workers") }}, nil
		},
		listen: func(context.Context, string) (net.Listener, error) {
			t.Error("migrate listened")
			return nil, errors.New("unexpected listen")
		},
	}
	if err := runConfig(context.Background(), Config{command: "migrate", StartupTimeout: time.Second, ShutdownTimeout: time.Second}, slog.New(slog.NewTextHandler(io.Discard, nil)), factory); err != nil {
		t.Fatal(err)
	}
	if got := trace.snapshot(); !reflect.DeepEqual(got, []string{"open", "new", "migrate", "library", "redis", "pool"}) {
		t.Fatal("wrong migrate lifecycle")
	}
}
