package migrations

import (
	"context"
	"database/sql"
	"fmt"
)

// The text identity kind replaced SMALLINT inside version 1. Earlier v1 databases keep
// their integer column and are rejected without writes rather than converted in place.
func checkIdentityKindBaseline(ctx context.Context, db *sql.DB, schema string, version int) error {
	const query = `SELECT
 EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=$1 AND table_name='identity' AND column_name='kind'
  AND udt_name='varchar' AND character_maximum_length=32 AND collation_name='C' AND is_nullable='NO')
 AND EXISTS(SELECT 1 FROM pg_constraint c JOIN pg_class r ON r.oid=c.conrelid JOIN pg_namespace n ON n.oid=r.relnamespace
  WHERE n.nspname=$1 AND r.relname='identity' AND c.conname='identity_kind_format' AND c.convalidated
  AND pg_get_constraintdef(c.oid)='CHECK (((kind)::text ~ ''^[A-Z][A-Z0-9_]{1,31}$''::text))')`
	var complete bool
	if err := db.QueryRowContext(ctx, query, schema).Scan(&complete); err != nil {
		return fmt.Errorf("migrations: inspect identity kind structure: %w", err)
	}
	if !complete {
		return fmt.Errorf("%w: schema %s version %d has a retired or inconsistent identity kind column", ErrIncompatibleBaseline, schema, version)
	}
	return nil
}
