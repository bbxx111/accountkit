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
 AND (SELECT count(*)=13 AND bool_and(udt_name=CASE column_name WHEN 'state' THEN 'int2' WHEN 'cipher_version' THEN 'int2' WHEN 'revision' THEN 'int8' WHEN 'payload' THEN 'bytea' WHEN 'proof_user_ids' THEN '_text' WHEN 'create_time' THEN 'timestamptz' WHEN 'update_time' THEN 'timestamptz' WHEN 'expire_time' THEN 'timestamptz' ELSE 'text' END
  AND is_nullable=CASE WHEN column_name IN ('user_id','session_id') THEN 'YES' ELSE 'NO' END)
 FROM information_schema.columns WHERE table_schema=$1 AND table_name='identity_flow' AND column_name IN ('id','token_digest','state','policy_fingerprint','payload','cipher_version','revision','proof_user_ids','user_id','session_id','create_time','update_time','expire_time'))
 AND
  (SELECT count(*)=10 AND bool_and(CASE column_name
   WHEN 'id' THEN udt_name='uuid' AND is_nullable='NO'
   WHEN 'source' THEN udt_name='varchar' AND character_maximum_length=128 AND collation_name='C' AND is_nullable='NO'
   WHEN 'action' THEN udt_name='varchar' AND character_maximum_length=128 AND collation_name='C' AND is_nullable='NO'
   WHEN 'result' THEN udt_name='int2' AND is_nullable='YES'
   WHEN 'actor' THEN udt_name='jsonb' AND is_nullable='NO'
   WHEN 'targets' THEN udt_name='jsonb' AND is_nullable='NO'
   WHEN 'context' THEN udt_name='jsonb' AND is_nullable='NO'
   WHEN 'data' THEN udt_name='jsonb' AND is_nullable='NO'
   WHEN 'occur_time' THEN udt_name='timestamptz' AND datetime_precision=6 AND is_nullable='NO'
   WHEN 'create_time' THEN udt_name='timestamptz' AND datetime_precision=6 AND is_nullable='NO'
   ELSE false END)
  FROM information_schema.columns WHERE table_schema=$1 AND table_name='audit_event')
`, schema).Scan(&complete); err != nil {
		return fmt.Errorf("migrations: inspect baseline structure: %w", err)
	}
	if !complete {
		return fmt.Errorf("%w: schema %s version %d has an incomplete or retired structure", ErrIncompatibleBaseline, schema, version)
	}
	return nil
}
