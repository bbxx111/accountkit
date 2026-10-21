package migrations

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/jackc/pgx/v5"
)

// The published initial schema and its production increments install seven business tables.
func TestConsolidatedInitialSchema(t *testing.T) {
	entries, err := fs.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, entry.Name())
		}
	}
	if !slices.Equal(names, []string{"0001_init.down.sql", "0001_init.up.sql"}) {
		t.Errorf("unexpected production migrations: %v", names)
	}
	cfg, schema := safetyDB(t)
	if err := Up(context.Background(), cfg, schema); err != nil {
		t.Fatal(err)
	}
	assertVersion(t, cfg, schema, latestVersion(t), false)
	c := migrationTestConn(t, cfg, schema)
	var tables int
	if err := c.QueryRow(context.Background(), "SELECT count(*) FROM pg_tables WHERE schemaname=$1 AND tablename <> 'schema_migrations'", schema).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 7 {
		t.Fatalf("business tables = %d, want 7", tables)
	}
	if _, err := c.Exec(context.Background(), "INSERT INTO user_account(id,state) VALUES('u_0000000000001',1)"); err != nil {
		t.Fatal(err)
	}
	before := migrationCatalog(t, c, schema)
	if err := Up(context.Background(), cfg, schema); err != nil {
		t.Fatal(err)
	}
	if got := migrationCatalog(t, c, schema); got != before {
		t.Fatal("repeat Up changed schema")
	}
	var users int
	if err := c.QueryRow(context.Background(), "SELECT count(*) FROM user_account").Scan(&users); err != nil || users != 1 {
		t.Fatalf("repeat Up changed data: %d %v", users, err)
	}
}

// Retired version numbers must not cause silent no-op or automatic conversion.
func TestOldMigrationBaselineRejected(t *testing.T) {
	for _, tc := range []struct {
		name    string
		version int
		partial string
	}{
		{"complete_v1_empty", 1, "complete_empty"}, {"complete_v1_nonempty", 1, "complete_nonempty"}, {"complete_v2", 2, "complete_empty"}, {"old_initial", 1, ""}, {"old_incremental", 2, ""}, {"flow_table_only", 1, "table"}, {"audit_column_only", 1, "column"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			version := tc.version
			cfg, schema := safetyDB(t)
			c := migrationTestConn(t, cfg, schema)
			ctx := context.Background()
			if _, err := c.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
				t.Fatal(err)
			}
			initialPath := "../tests/testdata/source-baseline/0001_init.up.sql"
			if strings.HasPrefix(tc.partial, "complete_") {
				initialPath = "testdata/legacy_integrated_v1.sql"
			}
			initial, err := os.ReadFile(initialPath)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := c.Exec(ctx, string(initial)); err != nil {
				t.Fatal(err)
			}
			if tc.partial == "table" {
				if _, err := c.Exec(ctx, strings.Split(historicalFlowSQL(t), "-- Correlate")[0]); err != nil {
					t.Fatal(err)
				}
			}
			if tc.partial == "column" {
				if _, err := c.Exec(ctx, "ALTER TABLE audit_event ADD COLUMN flow_id text"); err != nil {
					t.Fatal(err)
				}
			}
			if version == 2 && !strings.HasPrefix(tc.partial, "complete_") {
				if _, err := c.Exec(ctx, historicalFlowSQL(t)); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := c.Exec(ctx, "CREATE TABLE schema_migrations(version bigint PRIMARY KEY, dirty boolean NOT NULL); INSERT INTO schema_migrations VALUES($1,false)", pgx.QueryExecModeSimpleProtocol, version); err != nil {
				t.Fatal(err)
			}
			if _, err := c.Exec(ctx, "INSERT INTO user_account(id,state,display_name) VALUES('u_0000000000001',1,'keep')"); err != nil {
				t.Fatal(err)
			}
			if tc.partial == "complete_nonempty" {
				if _, err := c.Exec(ctx, "INSERT INTO audit_event(id,event_type,actor_kind,user_id,result) VALUES('e_0000000000001',1,1,'u_0000000000001',1)"); err != nil {
					t.Fatal(err)
				}
			}
			before := migrationCatalog(t, c, schema)
			var dataBefore string
			if err := c.QueryRow(ctx, "SELECT jsonb_build_array((SELECT jsonb_agg(t) FROM user_account t),(SELECT jsonb_agg(t) FROM audit_event t))::text").Scan(&dataBefore); err != nil {
				t.Fatal(err)
			}
			err = Up(ctx, cfg, schema)
			if !errors.Is(err, ErrIncompatibleBaseline) {
				t.Errorf("old baseline version %d error = %v, want explicit incompatibility", version, err)
			}
			if got := migrationCatalog(t, c, schema); got != before {
				t.Error("rejected baseline changed catalog")
			}
			assertVersion(t, cfg, schema, version, false)
			var dataAfter string
			if err := c.QueryRow(ctx, "SELECT jsonb_build_array((SELECT jsonb_agg(t) FROM user_account t),(SELECT jsonb_agg(t) FROM audit_event t))::text").Scan(&dataAfter); err != nil {
				t.Fatal(err)
			}
			if dataAfter != dataBefore {
				t.Error("rejected baseline changed data")
			}
			// Even an incompatible old schema must retain the engine's dirty error.
			if _, err := c.Exec(ctx, "UPDATE schema_migrations SET dirty=true"); err != nil {
				t.Fatal(err)
			}
			var dirty migrate.ErrDirty
			if err := Up(ctx, cfg, schema); !errors.As(err, &dirty) {
				t.Errorf("dirty error lost: %v", err)
			}
			assertVersion(t, cfg, schema, version, true)
			// Rejection must release both advisory locks and physical connections.
			if _, err := c.Exec(ctx, "UPDATE schema_migrations SET dirty=false"); err != nil {
				t.Fatal(err)
			}
			retry, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()
			if err := Up(retry, cfg, schema); !errors.Is(err, ErrIncompatibleBaseline) {
				t.Errorf("retry after baseline rejection lost incompatibility or leaked lock: %v", err)
			}
		})
	}
}

// PostgreSQL, not SQL text, defines equivalence, including column order.
func TestInitialSchemaPreservesNonAuditCatalog(t *testing.T) {
	cfg, oldSchema := safetyDB(t)
	_, newSchema := safetyDB(t)
	old := migrationTestConn(t, cfg, oldSchema)
	ctx := context.Background()
	if _, err := old.Exec(ctx, "CREATE SCHEMA "+oldSchema); err != nil {
		t.Fatal(err)
	}
	initial, err := exec.Command("git", "show", "511ac6e:migrations/0001_init.up.sql").Output()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(ctx, string(initial)); err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(ctx, historicalFlowSQL(t)); err != nil {
		t.Fatal(err)
	}
	// Compare the historical base plus the explicitly consolidated session DDL.
	if _, err := old.Exec(ctx, sessionContextHistoricalSQL(t, "0002_session_authentication_context.up.sql")); err != nil {
		t.Fatal(err)
	}
	// Apply only the authorized index naming change to the historical test
	// database. Keep the frozen SQL unchanged; only the separately authorized
	// session flag and client information table are excluded from comparison.
	if _, err := old.Exec(ctx, `DO $$
DECLARE old_index text;
BEGIN
 FOR old_index IN SELECT indexname FROM pg_indexes
  WHERE schemaname=current_schema() AND tablename<>'audit_event' AND indexname ~ '_(uidx|idx)$'
 LOOP
  EXECUTE format('ALTER INDEX %I RENAME TO %I', old_index,
   CASE WHEN old_index ~ '_uidx$' THEN 'uk_' ELSE 'idx_' END
   || regexp_replace(old_index, '_(uidx|idx)$', ''));
 END LOOP;
END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(ctx, databaseReviewIndexSQL); err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(ctx, profileAttributesInitialDDL); err != nil {
		t.Fatal(err)
	}
	initialUp(t, cfg, newSchema)
	current := migrationTestConn(t, cfg, newSchema)
	if before, after := strings.ReplaceAll(sessionContextUnchangedCatalog(t, migrationCatalogForTables(t, old, oldSchema, true)), "identity_flow", "sign_in_completion_flow"), sessionContextUnchangedCatalog(t, migrationCatalogForTables(t, current, newSchema, true)); sortedRenameCatalog(t, before) != sortedRenameCatalog(t, after) {
		t.Fatalf("catalog mismatch\nprevious: %s\ninitial: %s", before, after)
	}
}

// databaseReviewIndexSQL applies only the separately authorized database review
// index adjustments to a historical test schema, so catalog comparisons still
// prove that nothing else in the initial definition changed. Frozen SQL is untouched.
const databaseReviewIndexSQL = `
DROP INDEX IF EXISTS idx_identity_digest_key_version, idx_identity_cipher_key_version, idx_session_user_id,
 idx_audit_event_occur_time_id, idx_audit_event_request_id, idx_audit_event_flow_id,
 idx_sign_in_completion_flow_session_id, idx_identity_flow_session_id;
CREATE INDEX idx_identity_digest_key_version_create_time_id ON identity (digest_key_version, create_time, id)
 WHERE delete_time IS NULL AND digest_key_version IS NOT NULL;
CREATE INDEX idx_identity_cipher_key_version_create_time_id ON identity (cipher_key_version, create_time, id)
 WHERE delete_time IS NULL AND cipher_key_version IS NOT NULL;
CREATE UNIQUE INDEX uk_session_user_id_device_id ON session (user_id, device_id) WHERE revoke_time IS NULL;`

func historicalFlowSQL(t *testing.T) string {
	t.Helper()
	b, err := exec.Command("git", "show", "511ac6e:migrations/0002_identity_flow.up.sql").Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
func migrationTestConn(t *testing.T, cfg *pgx.ConnConfig, schema string) *pgx.Conn {
	t.Helper()
	c, err := pgx.ConnectConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	if _, err := c.Exec(context.Background(), "SET search_path TO "+schema); err != nil {
		t.Fatal(err)
	}
	return c
}
func migrationCatalog(t *testing.T, c *pgx.Conn, schema string) string {
	return migrationCatalogForTables(t, c, schema, false)
}
func migrationCatalogForTables(t *testing.T, c *pgx.Conn, schema string, nonAudit bool) string {
	t.Helper()
	var result string
	err := c.QueryRow(context.Background(), `
 SELECT jsonb_build_object(
 'columns',(SELECT jsonb_agg(to_jsonb(x) ORDER BY x.table_name,x.ordinal_position) FROM (
 SELECT table_name,ordinal_position,column_name,data_type,udt_name,is_nullable,column_default,collation_name,character_maximum_length,datetime_precision,numeric_precision,numeric_scale
 FROM information_schema.columns WHERE table_schema=$1 AND table_name <> 'schema_migrations' AND (NOT $2 OR table_name <> 'audit_event')) x),
 'constraints',(SELECT jsonb_agg(to_jsonb(x) ORDER BY x.table_name,x.name) FROM (
 SELECT r.relname AS table_name,c.conname AS name,c.contype,pg_get_constraintdef(c.oid) AS definition
 FROM pg_constraint c JOIN pg_class r ON r.oid=c.conrelid JOIN pg_namespace n ON n.oid=r.relnamespace
 WHERE n.nspname=$1 AND r.relname <> 'schema_migrations' AND (NOT $2 OR r.relname <> 'audit_event')) x),
 'indexes',(SELECT jsonb_agg(to_jsonb(x) ORDER BY x.tablename,x.indexname) FROM (
 SELECT tablename,indexname,replace(indexdef,quote_ident($1)||'.','') AS definition FROM pg_indexes
 WHERE schemaname=$1 AND tablename <> 'schema_migrations' AND (NOT $2 OR tablename <> 'audit_event')) x))::text`, schema, nonAudit).Scan(&result)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
