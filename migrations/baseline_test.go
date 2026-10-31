package migrations

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database"
	"github.com/jackc/pgx/v5"
)

// A version beyond the current source (for example a database migrated by a newer binary
// and taken over by an older one) must be rejected without writes; dirty takes precedence.
func TestVersionBeyondSourceRejected(t *testing.T) {
	ctx := context.Background()
	cfg, schema := safetyDB(t)
	initialUp(t, cfg, schema)
	c := migrationTestConn(t, cfg, schema)
	cfg = cfg.Copy()
	cfg.RuntimeParams["application_name"] = schema + "_rejection"
	if _, err := c.Exec(ctx, "INSERT INTO user_account(id,state,display_name) VALUES('u_0000000000001',1,'kept')"); err != nil {
		t.Fatal(err)
	}
	before := migrationCatalog(t, c, schema)
	data := migrationDataSnapshot(t, c)
	t.Run("version_ahead", func(t *testing.T) {
		if _, err := c.Exec(ctx, "UPDATE schema_migrations SET version=99, dirty=false"); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			waiting, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := Up(waiting, cfg, schema)
			cancel()
			if !errors.Is(err, ErrIncompatibleBaseline) {
				t.Fatalf("version ahead accepted: %v", err)
			}
			assertRejectedMigrationReleased(t, cfg, schema, c)
		}
		assertVersion(t, cfg, schema, 99, false)
		if migrationCatalog(t, c, schema) != before {
			t.Fatal("rejected database changed")
		}
		if migrationDataSnapshot(t, c) != data {
			t.Fatal("rejection changed business records")
		}
	})
	t.Run("dirty_precedence", func(t *testing.T) {
		if _, err := c.Exec(ctx, "UPDATE schema_migrations SET version=99, dirty=true"); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			waiting, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := Up(waiting, cfg, schema)
			cancel()
			var dirty migrate.ErrDirty
			if !errors.As(err, &dirty) || dirty.Version != 99 {
				t.Fatalf("dirty must win over version check: %v", err)
			}
			if errors.Is(err, ErrIncompatibleBaseline) {
				t.Fatal("dirty error must not be wrapped as incompatible baseline")
			}
			assertRejectedMigrationReleased(t, cfg, schema, c)
		}
		assertVersion(t, cfg, schema, 99, true)
		if migrationCatalog(t, c, schema) != before || migrationDataSnapshot(t, c) != data {
			t.Fatal("dirty rejection changed catalog or business records")
		}
	})
}

func assertRejectedMigrationReleased(t *testing.T, cfg *pgx.ConnConfig, schema string, probe *pgx.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Reacquire the initialization lock with the same acquisition path as Up.
	probeCfg := cfg.Copy()
	delete(probeCfg.RuntimeParams, "application_name")
	release, err := lockMigration(ctx, probeCfg, schema)
	if err != nil {
		t.Fatalf("rejection leaked initialization lock: %v", err)
	}
	release()
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
		t.Fatal("rejection leaked engine lock")
	}
	if _, err := probe.Exec(ctx, "SELECT pg_advisory_unlock($1)", key); err != nil {
		t.Fatal(err)
	}
	// Backend exit can follow the client close briefly; wait for an observed zero,
	// never infer release merely from the duration of a repeated rejection.
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		var connections int
		if err := probe.QueryRow(ctx, "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND application_name=$1", cfg.RuntimeParams["application_name"]).Scan(&connections); err != nil {
			t.Fatal(err)
		}
		if connections == 0 {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("rejection leaked %d migration connections: %v", connections, ctx.Err())
		case <-tick.C:
		}
	}
}
