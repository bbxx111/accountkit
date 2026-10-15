package migrations

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source"
)

// ErrIncompatibleBaseline rejects a retired initialization without altering it.
var ErrIncompatibleBaseline = errors.New("migrations: incompatible migration baseline")

// Both advisory locks are held by the caller before this read-only preflight.
// Run it before schema/driver initialization so rejecting an old database has no DDL side effects.
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
		return fmt.Errorf("%w: schema %s version %d is absent from the current source", ErrIncompatibleBaseline, schema, version)
	}
	if err := migration.Close(); err != nil {
		return fmt.Errorf("migrations: close baseline source: %w", err)
	}
	var complete bool
	if err := db.QueryRowContext(ctx, `SELECT
 EXISTS (SELECT 1 FROM pg_class r JOIN pg_namespace n ON n.oid=r.relnamespace WHERE n.nspname=$1 AND r.relname='identity_flow' AND r.relkind='r')
 AND EXISTS (SELECT 1 FROM pg_attribute a JOIN pg_class r ON r.oid=a.attrelid JOIN pg_namespace n ON n.oid=r.relnamespace WHERE n.nspname=$1 AND r.relname='audit_event' AND a.attname='flow_id' AND NOT a.attisdropped)`, schema).Scan(&complete); err != nil {
		return fmt.Errorf("migrations: inspect baseline structure: %w", err)
	}
	if !complete {
		return fmt.Errorf("%w: schema %s version %d lacks identity flow structure", ErrIncompatibleBaseline, schema, version)
	}
	return nil
}
