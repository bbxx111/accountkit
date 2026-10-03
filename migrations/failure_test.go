package migrations

import (
	"context"
	"errors"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/jackc/pgx/v5"
)

func TestFailedMigrationBlocksRetry(t *testing.T) {
	cfg, schema := safetyDB(t)
	if err := sourceUp(t, cfg, schema, true); err == nil {
		t.Fatal("failed SQL reported success")
	}
	assertVersion(t, cfg, schema, 2, true)
	var dirty migrate.ErrDirty
	if err := sourceUp(t, cfg, schema, false); !errors.As(err, &dirty) {
		t.Fatalf("dirty did not block: %v", err)
	}
	c, err := pgx.ConnectConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(context.Background())
	var absent bool
	if err := c.QueryRow(context.Background(), "SELECT to_regclass($1) IS NULL", schema+".after_failure").Scan(&absent); err != nil {
		t.Fatal(err)
	}
	if !absent {
		t.Fatal("migration after failed version unexpectedly ran")
	}
}
