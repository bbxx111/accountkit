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
	const flowTable, otherFlowTable = "sign_in_completion_flow", "identity_flow"
	var complete bool
	if err := db.QueryRowContext(ctx, `SELECT
 EXISTS (SELECT 1 FROM pg_class r JOIN pg_namespace n ON n.oid=r.relnamespace WHERE n.nspname=$1 AND r.relname=$2 AND r.relkind='r')
 AND NOT EXISTS (SELECT 1 FROM pg_class r JOIN pg_namespace n ON n.oid=r.relnamespace WHERE n.nspname=$1 AND r.relname=$3)
 AND (SELECT count(*)=13 AND bool_and(udt_name=CASE column_name WHEN 'state' THEN 'int2' WHEN 'cipher_version' THEN 'int2' WHEN 'revision' THEN 'int8' WHEN 'payload' THEN 'bytea' WHEN 'proof_user_ids' THEN '_text' WHEN 'create_time' THEN 'timestamptz' WHEN 'update_time' THEN 'timestamptz' WHEN 'expire_time' THEN 'timestamptz' ELSE 'text' END
  AND is_nullable=CASE WHEN column_name IN ('user_id','session_id') THEN 'YES' ELSE 'NO' END)
 FROM information_schema.columns WHERE table_schema=$1 AND table_name=$2 AND column_name IN ('id','token_digest','state','policy_fingerprint','payload','cipher_version','revision','proof_user_ids','user_id','session_id','create_time','update_time','expire_time'))
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
`, schema, flowTable, otherFlowTable).Scan(&complete); err != nil {
		return fmt.Errorf("migrations: inspect baseline structure: %w", err)
	}
	if !complete {
		return fmt.Errorf("%w: schema %s version %d has an incomplete or retired structure", ErrIncompatibleBaseline, schema, version)
	}
	if err := checkSessionContextBaseline(ctx, db, schema, version); err != nil {
		return err
	}
	return checkProfileAttributesBaseline(ctx, db, schema, version)
}

// The consolidated version 1 requires the full session context. The previous
// published v1 and withdrawn v2 are never automatically converted or renumbered.
// Materialize target-schema catalogs before deparsing: planner reordering must
// never inspect foreign relations that another instance can concurrently drop.
func checkSessionContextBaseline(ctx context.Context, db *sql.DB, schema string, version int) error {
	var complete bool
	query := `WITH expected(table_name,column_name,udt_name,is_nullable,max_length,collation_name) AS (VALUES
 ('session_authentication_identity','session_id','text','NO',NULL::int,'C'),
 ('session_authentication_identity','identity_id','text','NO',NULL::int,'C'),
 ('session_client_info','session_id','text','NO',NULL::int,'C'),
 ('session_client_info','client_info','jsonb','NO',NULL::int,NULL),
 ('session_client_info','observe_time','timestamptz','NO',NULL::int,NULL),
 ('session_client_info','create_time','timestamptz','NO',NULL::int,NULL),
 ('session_client_info','update_time','timestamptz','NO',NULL::int,NULL)
 ), expected_constraints(table_name,conname,definition) AS (VALUES
 ('session_authentication_identity','session_authentication_identity_pkey','PRIMARY KEY (session_id, identity_id)'),
 ('session_authentication_identity','session_authentication_identity_session_id_format','CHECK ((session_id ~ ''^s_[0-9a-hjkmnp-tv-z]{13}$''::text))'),
 ('session_authentication_identity','session_authentication_identity_identity_id_format','CHECK ((identity_id ~ ''^i_[0-9a-hjkmnp-tv-z]{13}$''::text))'),
 ('session_client_info','session_client_info_pkey','PRIMARY KEY (session_id)'),
 ('session_client_info','session_client_info_session_id_format','CHECK ((session_id ~ ''^s_[0-9a-hjkmnp-tv-z]{13}$''::text))')
 ), scoped_constraints AS MATERIALIZED (
 SELECT c.oid,c.conname,c.convalidated,r.relname FROM pg_constraint c
 JOIN pg_class r ON r.oid=c.conrelid JOIN pg_namespace n ON n.oid=r.relnamespace
 WHERE n.nspname=$1 AND r.relname IN ('session_authentication_identity','session_client_info')
), scoped_indexes AS MATERIALIZED (
 SELECT i.oid,i.relname AS index_name,r.relname AS table_name,x.indisvalid,x.indisunique,x.indpred,x.indnatts
 FROM pg_index x JOIN pg_class i ON i.oid=x.indexrelid
 JOIN pg_class r ON r.oid=x.indrelid JOIN pg_namespace n ON n.oid=r.relnamespace
 WHERE n.nspname=$1 AND r.relname IN ('session_authentication_identity','session')
) SELECT
 (SELECT count(*)=2 FROM pg_class r JOIN pg_namespace n ON n.oid=r.relnamespace WHERE n.nspname=$1 AND r.relname IN ('session_authentication_identity','session_client_info') AND r.relkind='r')
 AND (SELECT count(*)=7 FROM information_schema.columns WHERE table_schema=$1 AND table_name IN ('session_authentication_identity','session_client_info'))
 AND NOT EXISTS(SELECT 1 FROM expected e LEFT JOIN information_schema.columns c ON c.table_schema=$1 AND c.table_name=e.table_name AND c.column_name=e.column_name
 WHERE c.column_name IS NULL OR c.udt_name<>e.udt_name OR c.is_nullable<>e.is_nullable OR c.character_maximum_length IS DISTINCT FROM e.max_length OR (e.collation_name IS NOT NULL AND c.collation_name IS DISTINCT FROM e.collation_name))
 AND NOT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=$1 AND table_name='session' AND column_name='authentication_identities_known')
 AND (SELECT count(*)=2 AND bool_and(COALESCE(column_default='now()',false)) FROM information_schema.columns WHERE table_schema=$1 AND table_name='session_client_info' AND column_name IN ('create_time','update_time'))
 AND (SELECT count(*)=5 FROM pg_constraint c JOIN pg_class r ON r.oid=c.conrelid JOIN pg_namespace n ON n.oid=r.relnamespace WHERE n.nspname=$1 AND r.relname IN ('session_authentication_identity','session_client_info'))
 AND NOT EXISTS(SELECT 1 FROM expected_constraints e WHERE NOT EXISTS(
 SELECT 1 FROM scoped_constraints c WHERE c.relname=e.table_name AND c.conname=e.conname AND pg_get_constraintdef(c.oid)=e.definition AND c.convalidated))
 AND EXISTS(SELECT 1 FROM scoped_indexes i WHERE i.table_name='session_authentication_identity' AND i.index_name='idx_session_authentication_identity_identity_id_session_id' AND i.indisvalid AND NOT i.indisunique AND i.indpred IS NULL AND i.indnatts=2 AND pg_get_indexdef(i.oid,1,true)='identity_id' AND pg_get_indexdef(i.oid,2,true)='session_id')
 AND EXISTS(SELECT 1 FROM scoped_indexes i WHERE i.table_name='session' AND i.index_name='idx_session_user_id_id' AND i.indisvalid AND NOT i.indisunique AND i.indpred IS NULL AND i.indnatts=2 AND pg_get_indexdef(i.oid,1,true)='user_id' AND pg_get_indexdef(i.oid,2,true)='id')`
	if err := db.QueryRowContext(ctx, query, schema).Scan(&complete); err != nil {
		return fmt.Errorf("migrations: inspect session context structure: %w", err)
	}
	if !complete {
		return fmt.Errorf("%w: schema %s version %d has an incomplete or retired session context", ErrIncompatibleBaseline, schema, version)
	}
	return nil
}
