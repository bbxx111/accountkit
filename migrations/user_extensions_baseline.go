package migrations

import (
	"context"
	"database/sql"
	"fmt"
)

// Check the original account structure before accepting either version. Extension
// columns cannot make a damaged or retired base account table compatible.
func checkUserExtensionsBaseline(ctx context.Context, db *sql.DB, schema string, version int) error {
	const query = `WITH base_columns(column_name,udt_name,is_nullable,column_default,collation_name) AS (VALUES
 ('id','text','NO',NULL::text,'C'),('state','int2','NO',NULL,NULL),
 ('display_name','text','YES',NULL,NULL),('freeze_time','timestamptz','YES',NULL,NULL),
 ('freeze_reason','text','YES',NULL,NULL),('freeze_actor_subject','text','YES',NULL,NULL),
 ('freeze_actor_username','text','YES',NULL,NULL),('delete_time','timestamptz','YES',NULL,NULL),
 ('purge_time','timestamptz','YES',NULL,NULL),('create_time','timestamptz','NO','now()',NULL),
 ('update_time','timestamptz','NO','now()',NULL)
 ), extension_columns(column_name,udt_name,is_nullable,column_default) AS (VALUES
 ('extensions','jsonb','NO','''{}''::jsonb'),('extensions_schema_version','text','YES',NULL),
 ('extensions_schema_fingerprint','text','YES',NULL),('extensions_revision','int8','NO','0'),
 ('extensions_update_time','timestamptz','YES',NULL)
 ), base_constraints(conname,definition) AS (VALUES
 ('user_account_pkey','PRIMARY KEY (id)'),
 ('user_account_id_format','CHECK ((id ~ ''^u_[0-9a-hjkmnp-tv-z]{13}$''::text))')
 ), extension_constraints(conname,definition) AS (VALUES
 ('user_account_extensions_revision_nonnegative','CHECK ((extensions_revision >= 0))'),
 ('user_account_extensions_schema_pair','CHECK (((extensions_schema_version IS NULL) = (extensions_schema_fingerprint IS NULL)))'),
 ('user_account_extensions_schema_version_format','CHECK ((extensions_schema_version ~ ''^[A-Za-z0-9._-]{1,64}$''::text))'),
 ('user_account_extensions_schema_fingerprint_format','CHECK ((extensions_schema_fingerprint ~ ''^[0-9a-f]{64}$''::text))')
 ), scoped_constraints AS MATERIALIZED (
 SELECT c.oid,c.conname,c.convalidated FROM pg_constraint c
 JOIN pg_class r ON r.oid=c.conrelid JOIN pg_namespace n ON n.oid=r.relnamespace
 WHERE n.nspname=$1 AND r.relname='user_account'
 ) SELECT
 EXISTS(SELECT 1 FROM pg_class r JOIN pg_namespace n ON n.oid=r.relnamespace WHERE n.nspname=$1 AND r.relname='user_account' AND r.relkind='r')
 AND NOT EXISTS(SELECT 1 FROM base_columns e LEFT JOIN information_schema.columns c ON c.table_schema=$1 AND c.table_name='user_account' AND c.column_name=e.column_name
 WHERE c.column_name IS NULL OR c.udt_name<>e.udt_name OR c.is_nullable<>e.is_nullable OR c.column_default IS DISTINCT FROM e.column_default OR (e.collation_name IS NOT NULL AND c.collation_name IS DISTINCT FROM e.collation_name) OR (e.udt_name='timestamptz' AND c.datetime_precision IS DISTINCT FROM 6))
 AND NOT EXISTS(SELECT 1 FROM base_constraints e WHERE NOT EXISTS(SELECT 1 FROM scoped_constraints c WHERE c.conname=e.conname AND c.convalidated AND pg_get_constraintdef(c.oid)=e.definition))
 AND CASE WHEN $2=1 THEN
 NOT EXISTS(SELECT 1 FROM information_schema.columns c JOIN extension_columns e USING(column_name) WHERE c.table_schema=$1 AND c.table_name='user_account')
 AND NOT EXISTS(SELECT 1 FROM scoped_constraints c JOIN extension_constraints e USING(conname))
 ELSE
 NOT EXISTS(SELECT 1 FROM extension_columns e LEFT JOIN information_schema.columns c ON c.table_schema=$1 AND c.table_name='user_account' AND c.column_name=e.column_name
 WHERE c.column_name IS NULL OR c.udt_name<>e.udt_name OR c.is_nullable<>e.is_nullable OR c.column_default IS DISTINCT FROM e.column_default OR (e.udt_name='timestamptz' AND c.datetime_precision IS DISTINCT FROM 6))
 AND NOT EXISTS(SELECT 1 FROM extension_constraints e WHERE NOT EXISTS(SELECT 1 FROM scoped_constraints c WHERE c.conname=e.conname AND c.convalidated AND pg_get_constraintdef(c.oid)=e.definition)) END`
	var complete bool
	if err := db.QueryRowContext(ctx, query, schema, version).Scan(&complete); err != nil {
		return fmt.Errorf("migrations: inspect account extensions structure: %w", err)
	}
	if !complete {
		return fmt.Errorf("%w: schema %s version %d has an incomplete or inconsistent account extensions structure", ErrIncompatibleBaseline, schema, version)
	}
	return nil
}
