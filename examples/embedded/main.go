// A local development host. Replace log senders and inject an admin verifier in production.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/user/sender"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func run() error {
	if os.Getenv("ACCOUNTKIT_DEMO") != "1" {
		return fmt.Errorf("set ACCOUNTKIT_DEMO=1 for this local-only example with log senders")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	cfg, err := accountkit.ConfigFromEnv("ACCOUNTKIT_")
	if err != nil {
		return err
	}
	dsn := os.Getenv("ACCOUNTKIT_DATABASE_URL")
	addr := os.Getenv("ACCOUNTKIT_REDIS_ADDR")
	if dsn == "" || addr == "" {
		return fmt.Errorf("ACCOUNTKIT_DATABASE_URL and ACCOUNTKIT_REDIS_ADDR are required")
	}
	pc, err := accountkit.PoolConfig(dsn, cfg.Schema)
	if err != nil {
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return err
	}
	defer pool.Close()
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	defer rdb.Close()
	a, err := accountkit.New(cfg, accountkit.Deps{Pool: pool, Redis: rdb, SMSSender: sender.NewLog(nil), EmailSender: sender.NewLog(nil)})
	if err != nil {
		return err
	}
	defer a.Close()
	if err := a.Migrate(ctx); err != nil {
		return err
	}
	a.Start(ctx)
	router := chi.NewRouter()
	router.Mount("/v1", a.ConsumerHandler())
	// Without AdminVerifier/AdminPrincipal these endpoints return ADMIN_NOT_CONFIGURED.
	router.Mount("/admin/v1", a.AdminHandler())
	server := &http.Server{Addr: "127.0.0.1:8080", Handler: router, ReadHeaderTimeout: 5 * time.Second}
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
}
func main() {
	if err := run(); err != nil {
		slog.Error("example host stopped", "error", err)
		os.Exit(1)
	}
}
