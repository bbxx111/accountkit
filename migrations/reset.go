package migrations

import (
	"context"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	"github.com/jackc/pgx/v5"
)

var ErrDestructiveOperation = errors.New("migrations: destructive operation disabled")

// UnsafeResetOptions explicitly confirms the schema whose auth tables will be destroyed.
type UnsafeResetOptions struct{ ConfirmSchema string }

// UnsafeReset removes all migrated auth tables. DEVELOPMENT/TEST ONLY.
// This is not a production rollback or a substitute for restoring a backup.
func UnsafeReset(ctx context.Context, connCfg *pgx.ConnConfig, schema string, opts UnsafeResetOptions) error {
	if !ValidSchema(schema) {
		return fmt.Errorf("migrations: invalid schema name %q", schema)
	}
	if opts.ConfirmSchema != schema {
		return fmt.Errorf("%w: ConfirmSchema must match the target", ErrDestructiveOperation)
	}
	return run(ctx, connCfg, schema, func(m *migrate.Migrate) error { return m.Down() })
}
