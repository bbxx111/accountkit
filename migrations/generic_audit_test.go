package migrations

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/golang-migrate/migrate/v4"
	"github.com/jackc/pgx/v5"
)

func initialUp(t *testing.T, cfg *pgx.ConnConfig, schema string) {
	t.Helper()
	source := fstest.MapFS{}
	for _, name := range []string{"0001_init.up.sql", "0001_init.down.sql"} {
		b, err := fs.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		source[name] = &fstest.MapFile{Data: b}
	}
	if err := runWithSource(context.Background(), cfg, schema, source, func(m *migrate.Migrate) error { return m.Up() }); err != nil {
		t.Fatal(err)
	}
}

// Initial-only installation must already have the complete generic audit schema.
func TestGenericAuditInitialDefinition(t *testing.T) {
	cfg, schema := safetyDB(t)
	initialUp(t, cfg, schema)
	c := migrationTestConn(t, cfg, schema)
	var columns int
	var idType string
	if err := c.QueryRow(context.Background(), `SELECT count(*), max(udt_name) FILTER (WHERE column_name='id') FROM information_schema.columns WHERE table_schema=$1 AND table_name='audit_event'`, schema).Scan(&columns, &idType); err != nil || columns != 10 || idType != "uuid" {
		t.Fatalf("initial audit columns=%d id=%s err=%v, want 10/uuid", columns, idType, err)
	}
	assertVersion(t, cfg, schema, 1, false)
}

func TestGenericAuditNonemptyRepeatInstall(t *testing.T) {
	cfg, schema := safetyDB(t)
	if err := Up(context.Background(), cfg, schema); err != nil {
		t.Fatal(err)
	}
	c := migrationTestConn(t, cfg, schema)
	for _, q := range []string{
		`INSERT INTO user_account(id,state,display_name) VALUES('u_0000000000001',1,'preserved')`,
		`INSERT INTO identity(id,user_id,kind,provider_subject) VALUES('i_0000000000001','u_0000000000001',3,'synthetic-provider')`,
		`INSERT INTO session(id,user_id,device_id,auth_time,refresh_token_hash,refresh_expire_time) VALUES('s_0000000000001','u_0000000000001','device',now(),decode('1234','hex'),now()+interval '1 day')`,
		`INSERT INTO identity_flow(id,token_digest,state,policy_fingerprint,payload,cipher_version,proof_user_ids,user_id,session_id,create_time,update_time,expire_time) VALUES('f_0000000000001','test-token-digest',1,'policy',decode('1234','hex'),1,ARRAY['u_0000000000001'],'u_0000000000001','s_0000000000001',now(),now(),now()+interval '5 minutes')`,
		`INSERT INTO audit_event(id,source,action,result,actor,targets,context,data,occur_time) VALUES('019a0000-0000-7000-8000-000000000001','test/auth','SIGN_IN',1,'{"type":"USER","namespace":"urn:test:accounts","id":"u_0000000000001"}','[]','{}','{"accountkit":{"flow_id":"f_0000000000001"}}','2024-03-04T05:06:07.123456Z')`,
	} {
		if _, err := c.Exec(context.Background(), q); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := func() string {
		t.Helper()
		var s string
		if err := c.QueryRow(context.Background(), `SELECT jsonb_build_array((SELECT jsonb_agg(t) FROM user_account t),(SELECT jsonb_agg(t) FROM identity t),(SELECT jsonb_agg(t) FROM session t),(SELECT jsonb_agg(t) FROM identity_flow t),(SELECT jsonb_agg(t) FROM audit_event t),(SELECT jsonb_agg(t) FROM schema_migrations t))::text`).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	before := snapshot()
	catalog := migrationCatalog(t, c, schema)
	for range 2 {
		if err := Up(context.Background(), cfg, schema); err != nil {
			t.Fatal(err)
		}
	}
	if snapshot() != before || migrationCatalog(t, c, schema) != catalog {
		t.Fatal("repeat Up changed nonempty current schema or data")
	}
	assertVersion(t, cfg, schema, 1, false)
}

func TestGenericAuditConcurrentInitialInstall(t *testing.T) {
	cfg, schema := safetyDB(t)
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- Up(context.Background(), cfg, schema) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	assertVersion(t, cfg, schema, 1, false)
}

func TestGenericAuditBaselineRejectsPartialLatest(t *testing.T) {
	for _, tc := range []struct{ name, sql string }{
		{"missing_column", `ALTER TABLE audit_event DROP COLUMN create_time`},
		{"wrong_type", `ALTER TABLE audit_event ALTER COLUMN source TYPE text`},
		{"extra_column", `ALTER TABLE audit_event ADD COLUMN old_column text`},
		{"wrong_nullability", `ALTER TABLE audit_event ALTER COLUMN result SET NOT NULL`},
		{"missing_flow_structure", `ALTER TABLE identity_flow DROP COLUMN proof_user_ids`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, schema := safetyDB(t)
			if err := Up(context.Background(), cfg, schema); err != nil {
				t.Fatal(err)
			}
			c := migrationTestConn(t, cfg, schema)
			if _, err := c.Exec(context.Background(), tc.sql); err != nil {
				t.Fatal(err)
			}
			before := migrationCatalog(t, c, schema)
			if err := Up(context.Background(), cfg, schema); !errors.Is(err, ErrIncompatibleBaseline) {
				t.Fatalf("partial latest accepted: %v", err)
			}
			if after := migrationCatalog(t, c, schema); before != after {
				t.Fatal("preflight modified partial schema")
			}
			assertVersion(t, cfg, schema, 1, false)
		})
	}
}

func TestGenericAuditDatabaseConstraints(t *testing.T) {
	cfg, schema := safetyDB(t)
	if err := Up(context.Background(), cfg, schema); err != nil {
		t.Fatal(err)
	}
	c := migrationTestConn(t, cfg, schema)
	valid := `INSERT INTO audit_event(id,source,action,actor,occur_time) VALUES('019a0000-0000-7000-8000-000000000001','test/auth','ORDER_APPROVED','{"type":"ANONYMOUS"}','2024-01-01')`
	if _, err := c.Exec(context.Background(), valid); err != nil {
		t.Fatal(err)
	}
	for _, assignment := range []string{`id='019a0000-0000-4000-8000-000000000001'`, `id='019a0000-0000-7000-c000-000000000001'`, `source=''`, `action='bad-action'`, `actor='[]'`, `actor='{"type":"USER","namespace":"n"}'`, `actor='{"type":"ANONYMOUS","id":"x"}'`, `targets='{}'`, `targets='[{"type":"USER","namespace":"n","id":""}]'`, `context='[]'`, `context='{"span_id":"0123456789abcdef"}'`, `context='{"client_ip":"not-ip"}'`, `data='[]'`, `data=jsonb_build_object('oversize',repeat('x',65536))`} {
		t.Run(assignment, func(t *testing.T) {
			if _, err := c.Exec(context.Background(), fmt.Sprintf("UPDATE audit_event SET %s", assignment)); err == nil {
				t.Fatal("invalid SQL event accepted")
			}
		})
	}
}

// pg_restore and hosts can address a qualified table with an unrelated search_path.
// Its validators must still resolve functions in the table's own schema.
func TestGenericAuditQualifiedInsertWithoutSearchPath(t *testing.T) {
	cfg, schema := safetyDB(t)
	if err := Up(context.Background(), cfg, schema); err != nil {
		t.Fatal(err)
	}
	c := migrationTestConn(t, cfg, schema)
	if _, err := c.Exec(context.Background(), `SET search_path TO public`); err != nil {
		t.Fatal(err)
	}
	query := fmt.Sprintf(`INSERT INTO %s.audit_event(id,source,action,actor,targets,data,occur_time) VALUES('019a0000-0000-7000-8000-000000000001','test/auth','ORDER_APPROVED','{"type":"USER","namespace":"urn:test","id":"owner"}','[{"type":"ORDER","namespace":"urn:test","id":"order"}]','{"nested":{"number":12}}',now())`, schema)
	if _, err := c.Exec(context.Background(), query); err != nil {
		t.Fatalf("schema-local validators require caller search_path: %v", err)
	}
}

func TestAuditMigrationRejectsIncompleteInitialBaseline(t *testing.T) {
	for _, q := range []string{`ALTER TABLE audit_event RENAME COLUMN source TO unexpected_column`, `ALTER TABLE identity_flow DROP COLUMN token_digest`} {
		t.Run(q, func(t *testing.T) {
			cfg, schema := safetyDB(t)
			initialUp(t, cfg, schema)
			c := migrationTestConn(t, cfg, schema)
			if _, err := c.Exec(context.Background(), q); err != nil {
				t.Fatal(err)
			}
			before := migrationCatalog(t, c, schema)
			if err := Up(context.Background(), cfg, schema); !errors.Is(err, ErrIncompatibleBaseline) {
				t.Fatalf("incomplete initial accepted: %v", err)
			}
			if before != migrationCatalog(t, c, schema) {
				t.Fatal("incompatible preflight changed initial schema")
			}
			assertVersion(t, cfg, schema, 1, false)
		})
	}
}

func TestGenericAuditEmptyInstall(t *testing.T) {
	cfg, schema := safetyDB(t)
	if err := Up(context.Background(), cfg, schema); err != nil {
		t.Fatal(err)
	}
	c := migrationTestConn(t, cfg, schema)
	var columns int
	var idType string
	if err := c.QueryRow(context.Background(), `SELECT count(*), max(udt_name) FILTER (WHERE column_name='id') FROM information_schema.columns WHERE table_schema=$1 AND table_name='audit_event'`, schema).Scan(&columns, &idType); err != nil {
		t.Fatal(err)
	}
	if columns != 10 || idType != "uuid" {
		t.Fatalf("generic audit schema: columns=%d id=%s, want 10/uuid", columns, idType)
	}
	assertVersion(t, cfg, schema, 1, false)
}
