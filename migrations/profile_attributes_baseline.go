package migrations

import (
	"context"
	"database/sql"
	"fmt"
)

// Check the original account structure before accepting any reachable version. Extension
// columns cannot make a damaged or retired base account table compatible.
func checkProfileAttributesBaseline(ctx context.Context, db *sql.DB, schema string, version int) error {
	const query = `WITH base_columns(column_name,udt_name,is_nullable,column_default,collation_name) AS (VALUES
 ('id','text','NO',NULL::text,'C'),('state','int2','NO',NULL,NULL),
 ('avatar_url','text','YES',NULL,NULL),('display_name','text','YES',NULL,NULL),('freeze_time','timestamptz','YES',NULL,NULL),
 ('freeze_reason','text','YES',NULL,NULL),('freeze_actor_subject','text','YES',NULL,NULL),
 ('freeze_actor_username','text','YES',NULL,NULL),('delete_time','timestamptz','YES',NULL,NULL),
 ('purge_time','timestamptz','YES',NULL,NULL),('create_time','timestamptz','NO','now()',NULL),
 ('update_time','timestamptz','NO','now()',NULL)
 ), extension_columns(column_name,udt_name,is_nullable,column_default) AS (VALUES
 ('profile_attributes','jsonb','NO','''{}''::jsonb')
 ), base_constraints(conname,definition) AS (VALUES
 ('user_account_pkey','PRIMARY KEY (id)'),
 ('user_account_id_format','CHECK ((id ~ ''^u_[0-9a-hjkmnp-tv-z]{13}$''::text))')
 ), scoped_constraints AS MATERIALIZED (
 SELECT c.oid,c.conname,c.convalidated FROM pg_constraint c
 JOIN pg_class r ON r.oid=c.conrelid JOIN pg_namespace n ON n.oid=r.relnamespace
 WHERE n.nspname=$1 AND r.relname='user_account'
 ) SELECT
 EXISTS(SELECT 1 FROM pg_class r JOIN pg_namespace n ON n.oid=r.relnamespace WHERE n.nspname=$1 AND r.relname='user_account' AND r.relkind='r')
 AND NOT EXISTS(SELECT 1 FROM base_columns e LEFT JOIN information_schema.columns c ON c.table_schema=$1 AND c.table_name='user_account' AND c.column_name=e.column_name
 WHERE c.column_name IS NULL OR c.udt_name<>e.udt_name OR c.is_nullable<>e.is_nullable OR c.column_default IS DISTINCT FROM e.column_default OR (e.collation_name IS NOT NULL AND c.collation_name IS DISTINCT FROM e.collation_name) OR (e.udt_name='timestamptz' AND c.datetime_precision IS DISTINCT FROM 6))
 AND NOT EXISTS(SELECT 1 FROM base_constraints e WHERE NOT EXISTS(SELECT 1 FROM scoped_constraints c WHERE c.conname=e.conname AND c.convalidated AND pg_get_constraintdef(c.oid)=e.definition))
 AND NOT EXISTS(SELECT 1 FROM information_schema.columns c WHERE c.table_schema=$1 AND c.table_name='user_account' AND c.column_name IN ('extensions','extensions_schema_version','extensions_schema_fingerprint','extensions_revision','extensions_update_time','profile_attributes_revision','profile_attributes_schema_version','profile_attributes_schema_fingerprint','profile_attributes_update_time'))
 AND NOT EXISTS(SELECT 1 FROM scoped_constraints c WHERE c.conname IN ('user_account_extensions_revision_nonnegative','user_account_extensions_schema_pair','user_account_extensions_schema_version_format','user_account_extensions_schema_fingerprint_format','user_account_profile_attributes_revision_nonnegative','user_account_profile_attributes_schema_pair','user_account_profile_attributes_schema_version_format','user_account_profile_attributes_schema_fingerprint_format'))
 AND
 NOT EXISTS(SELECT 1 FROM extension_columns e LEFT JOIN information_schema.columns c ON c.table_schema=$1 AND c.table_name='user_account' AND c.column_name=e.column_name
 WHERE c.column_name IS NULL OR c.udt_name<>e.udt_name OR c.is_nullable<>e.is_nullable OR c.column_default IS DISTINCT FROM e.column_default OR (e.udt_name='timestamptz' AND c.datetime_precision IS DISTINCT FROM 6))`
	var complete bool
	if err := db.QueryRowContext(ctx, query, schema).Scan(&complete); err != nil {
		return fmt.Errorf("migrations: inspect account profile_attributes structure: %w", err)
	}
	if !complete {
		return fmt.Errorf("%w: schema %s version %d has an incomplete or inconsistent account profile_attributes structure", ErrIncompatibleBaseline, schema, version)
	}
	return nil
}
