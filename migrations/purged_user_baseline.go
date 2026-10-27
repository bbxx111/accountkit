package migrations

import (
	"context"
	"database/sql"
	"fmt"
)

// The purged_user feed table was added inside version 1 by explicit user authorization.
// Earlier v1 databases (v0.6.0 and before) lack it and are rejected without writes rather
// than patched in place. The identity sequence must keep increment 1, cache 1 and no
// cycle: any drift would break "seq order equals commit order" that cursor readers rely
// on. Catalogs are materialized for the target schema before deparsing, so another
// instance dropping its own schema concurrently cannot affect this check.
func checkPurgedUserBaseline(ctx context.Context, db *sql.DB, schema string, version int) error {
	const query = `WITH rel AS MATERIALIZED (
 SELECT r.oid FROM pg_class r JOIN pg_namespace n ON n.oid=r.relnamespace
 WHERE n.nspname=$1 AND r.relname='purged_user' AND r.relkind='r'
), cols AS MATERIALIZED (
 SELECT a.attname, format_type(a.atttypid,a.atttypmod) AS typ, a.attnotnull, a.attidentity,
  (SELECT co.collname FROM pg_collation co WHERE co.oid=a.attcollation) AS coll,
  pg_get_expr(d.adbin,d.adrelid) AS def
 FROM pg_attribute a JOIN rel ON a.attrelid=rel.oid
 LEFT JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum
 WHERE a.attnum>0 AND NOT a.attisdropped
), cons AS MATERIALIZED (
 SELECT c.conname, c.contype, c.convalidated, pg_get_constraintdef(c.oid) AS def
 FROM pg_constraint c JOIN rel ON c.conrelid=rel.oid
), seqs AS MATERIALIZED (
 SELECT s.seqincrement, s.seqcache, s.seqcycle
 FROM pg_depend d JOIN rel ON d.refobjid=rel.oid
 JOIN pg_attribute a ON a.attrelid=rel.oid AND a.attnum=d.refobjsubid AND a.attname='seq'
 JOIN pg_sequence s ON s.seqrelid=d.objid
 WHERE d.classid='pg_class'::regclass AND d.deptype='i'
), idx AS MATERIALIZED (
 SELECT i.relname, pg_get_indexdef(i.oid) AS def
 FROM pg_index x JOIN rel ON x.indrelid=rel.oid JOIN pg_class i ON i.oid=x.indexrelid
) SELECT
 EXISTS(SELECT 1 FROM rel)
 AND (SELECT count(*)=3 FROM cols)
 AND EXISTS(SELECT 1 FROM cols WHERE attname='user_id' AND typ='text' AND coll='C' AND attnotnull AND attidentity='' AND def IS NULL)
 AND EXISTS(SELECT 1 FROM cols WHERE attname='seq' AND typ='bigint' AND attnotnull AND attidentity='a' AND def IS NULL)
 AND EXISTS(SELECT 1 FROM cols WHERE attname='purge_time' AND typ='timestamp with time zone' AND attnotnull AND attidentity='' AND def='clock_timestamp()')
 AND (SELECT count(*)=3 FROM cons)
 AND EXISTS(SELECT 1 FROM cons WHERE conname='purged_user_pkey' AND contype='p' AND def='PRIMARY KEY (user_id)')
 AND EXISTS(SELECT 1 FROM cons WHERE conname='uk_purged_user_seq' AND contype='u' AND def='UNIQUE (seq)')
 AND EXISTS(SELECT 1 FROM cons WHERE conname='purged_user_user_id_format' AND contype='c' AND convalidated
  AND def='CHECK ((user_id ~ ''^u_[0-9a-hjkmnp-tv-z]{13}$''::text))')
 AND (SELECT count(*)=1 AND bool_and(seqincrement=1 AND seqcache=1 AND NOT seqcycle) FROM seqs)
 AND (SELECT count(*)=3 FROM idx)
 AND EXISTS(SELECT 1 FROM idx WHERE relname='idx_purged_user_purge_time' AND def LIKE 'CREATE INDEX idx_purged_user_purge_time ON % USING btree (purge_time)')`
	var complete bool
	if err := db.QueryRowContext(ctx, query, schema).Scan(&complete); err != nil {
		return fmt.Errorf("migrations: inspect purged_user structure: %w", err)
	}
	if !complete {
		return fmt.Errorf("%w: schema %s version %d has a missing or inconsistent purged_user table", ErrIncompatibleBaseline, schema, version)
	}
	return nil
}
