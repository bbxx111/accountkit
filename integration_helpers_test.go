package authserver_test

import (
	"context"
	"fmt"
	"math/rand/v2"
	"testing"

	authserver "github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/audit"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func integrationInstance(t *testing.T, cfg authserver.Config, rdb redis.UniversalClient) (*authserver.Auth, *pgxpool.Pool, *captureSender) {
	t.Helper()
	cfg.Schema = fmt.Sprintf("aktest_%08x", rand.Uint32())
	pc, err := authserver.PoolConfig(dbDSN(t), cfg.Schema)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), pc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		defer pool.Close()
		if _, err := pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+cfg.Schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	sent := &captureSender{}
	a, err := authserver.New(cfg, authserver.Deps{Pool: pool, Redis: rdb, SMSSender: sent, EmailSender: sent, Audit: audit.Noop{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	return a, pool, sent
}
