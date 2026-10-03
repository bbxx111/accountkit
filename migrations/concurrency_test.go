package migrations

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func safetyDB(t *testing.T) (*pgx.ConnConfig, string) {
	t.Helper()
	dsn := os.Getenv("SERVER_TEST_DB_DSN")
	if dsn == "" {
		t.Skip("SERVER_TEST_DB_DSN not set")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("aksafety_%08x", rand.Uint32())
	t.Cleanup(func() {
		c, err := pgx.ConnectConfig(context.Background(), cfg)
		if err != nil {
			t.Error(err)
			return
		}
		defer c.Close(context.Background())
		if _, err := c.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	return cfg, schema
}
func assertVersion(t *testing.T, cfg *pgx.ConnConfig, schema string, want int, dirty bool) {
	t.Helper()
	c, err := pgx.ConnectConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(context.Background())
	var v int
	var d bool
	if err := c.QueryRow(context.Background(), "SELECT version,dirty FROM "+schema+".schema_migrations").Scan(&v, &d); err != nil {
		t.Fatal(err)
	}
	if v != want || d != dirty {
		t.Fatalf("version=%d dirty=%v, want %d/%v", v, d, want, dirty)
	}
}
func concurrentUp(t *testing.T, existing bool) {
	t.Helper()
	for range 3 {
		cfg, schema := safetyDB(t)
		if existing {
			if err := Up(context.Background(), cfg, schema); err != nil {
				t.Fatal(err)
			}
		}
		start := make(chan struct{})
		results := make(chan error, 8)
		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				results <- Up(ctx, cfg.Copy(), schema)
			}()
		}
		close(start)
		wg.Wait()
		close(results)
		for err := range results {
			if err != nil {
				t.Errorf("concurrent Up: %v", err)
			}
		}
		assertVersion(t, cfg, schema, 1, false)
	}
}
func TestConcurrentFreshSchema(t *testing.T)    { concurrentUp(t, false) }
func TestConcurrentExistingSchema(t *testing.T) { concurrentUp(t, true) }
func TestDownRefusesWithoutIO(t *testing.T) {
	cfg, _ := pgx.ParseConfig("postgres://u:p@127.0.0.1:1/db?sslmode=disable")
	err := Down(context.Background(), cfg, "auth")
	if !errors.Is(err, ErrDestructiveOperation) {
		t.Fatalf("Down must refuse before I/O, got %v", err)
	}

}
