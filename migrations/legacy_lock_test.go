package migrations

import (
	"context"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4/database"
	"github.com/jackc/pgx/v5"
)

func TestLegacyMigrationLockCancellation(t *testing.T) {
	for _, deadline := range []bool{true, false} {
		name := "cancel"
		if deadline {
			name = "deadline"
		}
		t.Run(name, func(t *testing.T) {
			cfg, schema := safetyDB(t)
			ctx := context.Background()
			holder, err := pgx.ConnectConfig(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer holder.Close(ctx)
			var dbName string
			if err := holder.QueryRow(ctx, "SELECT current_database()").Scan(&dbName); err != nil {
				t.Fatal(err)
			}
			key, err := database.GenerateAdvisoryLockId(dbName, schema, "schema_migrations")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := holder.Exec(ctx, "SELECT pg_advisory_lock($1)", key); err != nil {
				t.Fatal(err)
			}
			waiting, cancel := context.WithCancel(ctx)
			if deadline {
				waiting, cancel = context.WithTimeout(ctx, 100*time.Millisecond)
			}
			defer cancel()
			result := make(chan error, 1)
			go func() { result <- Up(waiting, cfg, schema) }()
			if !deadline {
				time.Sleep(100 * time.Millisecond)
				cancel()
			}
			select {
			case err := <-result:
				if err == nil {
					t.Error("cancelled legacy lock wait succeeded")
				}
			case <-time.After(time.Second):
				t.Error("legacy lock wait ignored cancellation")
				holder.Close(ctx)
				select {
				case <-result:
				case <-time.After(5 * time.Second):
					t.Fatal("migration did not exit after releasing legacy lock")
				}
			}
			probe, err := pgx.ConnectConfig(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer probe.Close(ctx)
			var count int
			if err := probe.QueryRow(ctx, "SELECT count(*) FROM pg_namespace WHERE nspname=$1", schema).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Error("cancelled legacy lock waiter executed DDL")
			}
			holder.Close(ctx)
			if err := Up(ctx, cfg, schema); err != nil {
				t.Fatalf("retry after cancellation: %v", err)
			}
			assertVersion(t, cfg, schema, 1, false)
		})
	}
}

func TestDriverInitializationFailureReleasesEngineLock(t *testing.T) {
	cfg, schema := safetyDB(t)
	ctx := context.Background()
	probe, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close(ctx)
	if _, err := probe.Exec(ctx, "CREATE SCHEMA "+schema+"; CREATE TYPE "+schema+".schema_migrations AS ENUM ('conflict')"); err != nil {
		t.Fatal(err)
	}
	if err := Up(ctx, cfg, schema); err == nil {
		t.Fatal("expected migration table initialization failure")
	}
	var dbName string
	if err := probe.QueryRow(ctx, "SELECT current_database()").Scan(&dbName); err != nil {
		t.Fatal(err)
	}
	key, err := database.GenerateAdvisoryLockId(dbName, schema, "schema_migrations")
	if err != nil {
		t.Fatal(err)
	}
	var acquired bool
	if err := probe.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&acquired); err != nil {
		t.Fatal(err)
	}
	if !acquired {
		t.Fatal("failed driver initialization leaked engine lock")
	}
	if _, err := probe.Exec(ctx, "SELECT pg_advisory_unlock($1)", key); err != nil {
		t.Fatal(err)
	}
	if _, err := probe.Exec(ctx, "DROP TYPE "+schema+".schema_migrations"); err != nil {
		t.Fatal(err)
	}
	retry, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := Up(retry, cfg, schema); err != nil {
		t.Fatalf("retry after removing conflict: %v", err)
	}
	assertVersion(t, cfg, schema, 1, false)
}
