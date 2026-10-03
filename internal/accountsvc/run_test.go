package accountsvc

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

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
	for _, failure := range []string{"", "open", "new", "migrate", "admin", "listen2"} {
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
			app := &application{migrate: func(context.Context) error { return fail("migrate") }, initAdmin: func(context.Context) error { return fail("admin") }, start: func(c context.Context) { serviceCtx = c; trace.add("start"); stop() }, close: func() {
				if serviceCtx != nil && serviceCtx.Err() != nil {
					t.Error("signal prematurely cancelled library background")
				}
				trace.add("library close")
			}, consumer: http.NotFoundHandler(), admin: http.NotFoundHandler(), introspect: http.NotFoundHandler()}
			listens := 0
			var first net.Listener
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
					stage := "listen1"
					if listens == 2 {
						stage = "listen2"
					}
					if err := fail(stage); err != nil {
						return nil, err
					}
					ln, err := net.Listen("tcp", "127.0.0.1:0")
					if listens == 1 {
						first = ln
					}
					return ln, err
				},
			}
			cfg := Config{command: "serve", AdminEnabled: true, StartupTimeout: time.Second, ShutdownTimeout: 16 * time.Second, HTTPAddr: "127.0.0.1:0", InternalAddr: "127.0.0.1:0"}
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
							want = append(want, "listen1", "listen2")
							if failure != "listen2" {
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
