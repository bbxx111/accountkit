package migrations

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
)

// A version beyond the current source (for example a database migrated by a newer binary
// and taken over by an older one) must be rejected without writes; dirty takes precedence.
func TestVersionBeyondSourceRejected(t *testing.T) {
	ctx := context.Background()
	cfg, schema := safetyDB(t)
	initialUp(t, cfg, schema)
	c := migrationTestConn(t, cfg, schema)
	if _, err := c.Exec(ctx, "INSERT INTO user_account(id,state,display_name) VALUES('u_0000000000001',1,'kept')"); err != nil {
		t.Fatal(err)
	}
	before := migrationCatalog(t, c, schema)
	t.Run("version_ahead", func(t *testing.T) {
		if _, err := c.Exec(ctx, "UPDATE schema_migrations SET version=99, dirty=false"); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			start := time.Now()
			if err := Up(ctx, cfg, schema); !errors.Is(err, ErrIncompatibleBaseline) {
				t.Fatalf("version ahead accepted: %v", err)
			}
			if time.Since(start) > time.Second {
				t.Fatal("rejection waited on a lock: connection or lock leaked")
			}
		}
		assertVersion(t, cfg, schema, 99, false)
		if migrationCatalog(t, c, schema) != before {
			t.Fatal("rejected database changed")
		}
		var rows int
		if err := c.QueryRow(ctx, "SELECT count(*) FROM user_account").Scan(&rows); err != nil || rows != 1 {
			t.Fatalf("data changed: rows=%d err=%v", rows, err)
		}
	})
	t.Run("dirty_precedence", func(t *testing.T) {
		if _, err := c.Exec(ctx, "UPDATE schema_migrations SET version=99, dirty=true"); err != nil {
			t.Fatal(err)
		}
		err := Up(ctx, cfg, schema)
		var dirty migrate.ErrDirty
		if !errors.As(err, &dirty) || dirty.Version != 99 {
			t.Fatalf("dirty must win over version check: %v", err)
		}
		if errors.Is(err, ErrIncompatibleBaseline) {
			t.Fatal("dirty error must not be wrapped as incompatible baseline")
		}
		assertVersion(t, cfg, schema, 99, true)
	})
}
