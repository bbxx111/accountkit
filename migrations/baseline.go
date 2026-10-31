package migrations

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source"
)

// ErrIncompatibleBaseline reports a recorded version that is beyond the current migration source,
// for example a database migrated by a newer binary and taken over by an older one.
// Rejection happens before any write and keeps catalog, data and version/dirty.
var ErrIncompatibleBaseline = errors.New("migrations: incompatible migration baseline")

// Both advisory locks are held by the caller before this read-only preflight; it runs no DDL.
// Only two rules remain: dirty precedence and version-beyond-source rejection.
func checkBaseline(ctx context.Context, db *sql.DB, schema string, src source.Driver) error {
	var exists bool
	if err := db.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, schema+".schema_migrations").Scan(&exists); err != nil {
		return fmt.Errorf("migrations: inspect baseline: %w", err)
	}
	if !exists {
		return nil
	}
	var version int
	var dirty bool
	err := db.QueryRowContext(ctx, "SELECT version, dirty FROM "+schema+".schema_migrations").Scan(&version, &dirty)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("migrations: inspect baseline version: %w", err)
	}
	// Preserve the migration engine's existing dirty sentinel and precedence.
	if dirty {
		return fmt.Errorf("migrations: schema %s: %w", schema, migrate.ErrDirty{Version: version})
	}
	if version < 1 {
		return nil
	}
	migration, _, err := src.ReadUp(uint(version))
	if err != nil {
		return fmt.Errorf("%w: schema %s version %d is beyond the current migration source", ErrIncompatibleBaseline, schema, version)
	}
	if err := migration.Close(); err != nil {
		return fmt.Errorf("migrations: close baseline source: %w", err)
	}
	return nil
}
