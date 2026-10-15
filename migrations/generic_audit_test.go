package migrations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/bbxx111/accountkit/audit/event"
	"github.com/bbxx111/accountkit/internal/auditmigration"
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

func prepareAudit(t *testing.T, c *pgx.Conn, schema string) {
	t.Helper()
	if _, err := auditmigration.Prepare(context.Background(), c, auditmigration.Options{Schema: schema, ConfirmSchema: schema, Source: "test/auth", Namespace: "urn:test:accounts"}); err != nil {
		t.Fatal(err)
	}
}

func seedAudit(t *testing.T, c *pgx.Conn) {
	t.Helper()
	for _, q := range []string{
		`INSERT INTO user_account(id,state,display_name) VALUES('u_0000000000001',1,'preserved')`,
		`INSERT INTO identity(id,user_id,kind,provider_subject) VALUES('i_0000000000001','u_0000000000001',3,'synthetic-provider')`,
		`INSERT INTO session(id,user_id,device_id,auth_time,refresh_token_hash,refresh_expire_time) VALUES('s_0000000000001','u_0000000000001','device',now(),decode('1234','hex'),now()+interval '1 day')`,
		`INSERT INTO identity_flow(id,token_digest,state,policy_fingerprint,payload,cipher_version,proof_user_ids,user_id,session_id,create_time,update_time,expire_time) VALUES('f_0000000000001','test-token-digest',1,'policy',decode('1234','hex'),1,ARRAY['u_0000000000001'],'u_0000000000001','s_0000000000001',now(),now(),now()+interval '5 minutes')`,
		`INSERT INTO audit_event(id,event_type,actor_kind,result,user_id,session_id,flow_id,subject_hint,occur_time) VALUES('e_0000000000001',4,1,1,'u_0000000000001','s_0000000000001','f_0000000000001','01234567','2024-03-04T05:06:07.123456Z')`,
		`INSERT INTO audit_event(id,event_type,actor_kind,result,user_id,admin_subject,admin_username,occur_time) VALUES('e_0000000000002',4,2,2,'u_0000000000001','historical-subject','display snapshot','2024-03-04T05:06:08.123456Z')`,
	} {
		if _, err := c.Exec(context.Background(), q); err != nil {
			t.Fatal(err)
		}
	}
}
func nonAuditSnapshot(t *testing.T, c *pgx.Conn) string {
	t.Helper()
	var s string
	if err := c.QueryRow(context.Background(), `SELECT jsonb_build_array((SELECT jsonb_agg(t) FROM user_account t),(SELECT jsonb_agg(t) FROM identity t),(SELECT jsonb_agg(t) FROM session t),(SELECT jsonb_agg(t) FROM identity_flow t))::text`).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}
func TestGenericAuditNonemptyUpgrade(t *testing.T) {
	cfg, schema := safetyDB(t)
	initialUp(t, cfg, schema)
	c := migrationTestConn(t, cfg, schema)
	seedAudit(t, c)
	before := nonAuditSnapshot(t, c)
	prepareAudit(t, c, schema)
	var staged string
	if err := c.QueryRow(context.Background(), `SELECT jsonb_agg(event ORDER BY legacy_id)::text FROM audit_event_upgrade_stage`).Scan(&staged); err != nil {
		t.Fatal(err)
	}
	if err := Up(context.Background(), cfg, schema); err != nil {
		t.Fatal(err)
	}
	if after := nonAuditSnapshot(t, c); after != before {
		t.Fatal("conversion changed non-audit data")
	}
	var converted string
	if err := c.QueryRow(context.Background(), `SELECT jsonb_agg((to_jsonb(t)-'create_time') || jsonb_build_object('result',CASE result WHEN 1 THEN 'SUCCESS' WHEN 2 THEN 'FAILURE' END) ORDER BY data#>>'{accountkit,legacy_event_id}')::text FROM audit_event t`).Scan(&converted); err != nil {
		t.Fatal(err)
	}
	var expected, actual []event.Event
	if err := json.Unmarshal([]byte(staged), &expected); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(converted), &actual); err != nil {
		t.Fatal(err)
	}
	if len(actual) != 2 {
		t.Fatalf("got %d historical events", len(actual))
	}
	for i := range expected {
		if !event.Equivalent(expected[i], actual[i]) {
			t.Fatalf("event %d lost data", i)
		}
	}
	if actual[0].ID.Version() != 7 || actual[0].OccurTime.Format(time.RFC3339Nano) != "2024-03-04T05:06:07.123456Z" || actual[0].Action != "SIGN_IN" || actual[0].Actor.Type != event.ActorUser {
		t.Fatalf("history changed: %+v", actual[0])
	}
	if actual[1].Actor.Type != event.ActorUnknown || actual[1].Actor.ID != "" || actual[1].Actor.Namespace != "" {
		t.Fatal("fabricated incomplete administrator identity")
	}
	var snap string
	if err := c.QueryRow(context.Background(), `SELECT jsonb_agg(t ORDER BY id)::text FROM audit_event t`).Scan(&snap); err != nil {
		t.Fatal(err)
	}
	if err := Up(context.Background(), cfg, schema); err != nil {
		t.Fatal(err)
	}
	var again string
	if err := c.QueryRow(context.Background(), `SELECT jsonb_agg(t ORDER BY id)::text FROM audit_event t`).Scan(&again); err != nil {
		t.Fatal(err)
	}
	if snap != again {
		t.Fatal("repeat migration changed UUID or first storage time")
	}
	var leaked bool
	if err := c.QueryRow(context.Background(), `SELECT to_regclass('audit_event_upgrade_stage') IS NOT NULL OR to_regclass('audit_event_upgrade_meta') IS NOT NULL`).Scan(&leaked); err != nil || leaked {
		t.Fatalf("staging not removed: %v %v", leaked, err)
	}
}

func TestAuditMigrationRejectsChangedSourceRows(t *testing.T) {
	for _, tc := range []struct{ name, sql string }{
		{"added", `INSERT INTO audit_event(id,event_type,actor_kind,result) VALUES('e_0000000000003',1,1,1)`},
		{"changed", `UPDATE audit_event SET reason='changed'`},
		{"deleted", `DELETE FROM audit_event`},
		{"stage_deleted", `DELETE FROM audit_event_upgrade_stage WHERE legacy_id='e_0000000000001'`},
		{"stage_action", `UPDATE audit_event_upgrade_stage SET event=jsonb_set(event,'{action}','"OTHER_ACTION"')`},
		{"stage_uuid", `UPDATE audit_event_upgrade_stage SET id='019a0000-0000-7000-8000-000000000001' WHERE legacy_id='e_0000000000001'`},
		{"stage_config", `UPDATE audit_event_upgrade_meta SET source='other/source'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, schema := safetyDB(t)
			initialUp(t, cfg, schema)
			c := migrationTestConn(t, cfg, schema)
			seedAudit(t, c)
			prepareAudit(t, c, schema)
			if _, err := c.Exec(context.Background(), tc.sql); err != nil {
				t.Fatal(err)
			}
			var before string
			if err := c.QueryRow(context.Background(), `SELECT coalesce(jsonb_agg(t ORDER BY id),'[]')::text FROM audit_event t`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			if err := Up(context.Background(), cfg, schema); err == nil {
				t.Fatal("changed preparation upgraded")
			}
			var after string
			if err := c.QueryRow(context.Background(), `SELECT coalesce(jsonb_agg(t ORDER BY id),'[]')::text FROM audit_event t`).Scan(&after); err != nil {
				t.Fatal(err)
			}
			if before != after {
				t.Fatal("failed conversion changed original table")
			}
			assertVersion(t, cfg, schema, 2, true)
			var dirty migrate.ErrDirty
			if err := Up(context.Background(), cfg, schema); !errors.As(err, &dirty) {
				t.Fatalf("dirty lost: %v", err)
			}
		})
	}
}

func TestGenericAuditConcurrentPreparedUpgrade(t *testing.T) {
	cfg, schema := safetyDB(t)
	initialUp(t, cfg, schema)
	c := migrationTestConn(t, cfg, schema)
	seedAudit(t, c)
	prepareAudit(t, c, schema)
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
	var count int
	if err := c.QueryRow(context.Background(), `SELECT count(DISTINCT id) FROM audit_event`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("mapped events=%d err=%v", count, err)
	}
	assertVersion(t, cfg, schema, 2, false)
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
			assertVersion(t, cfg, schema, 2, false)
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
	for _, assignment := range []string{`id='019a0000-0000-4000-8000-000000000001'`, `id='019a0000-0000-7000-c000-000000000001'`, `source=''`, `action='bad-action'`, `result=0`, `result=3`, `actor='[]'`, `actor='{"type":"USER","namespace":"n"}'`, `actor='{"type":"ANONYMOUS","id":"x"}'`, `targets='{}'`, `targets='[{"type":"USER","namespace":"n","id":""}]'`, `context='[]'`, `context='{"span_id":"0123456789abcdef"}'`, `context='{"client_ip":"not-ip"}'`, `data='[]'`, `data=jsonb_build_object('oversize',repeat('x',65536))`} {
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

func TestAuditMigrationRejectsChangedNamespaceWithoutResources(t *testing.T) {
	cfg, schema := safetyDB(t)
	initialUp(t, cfg, schema)
	c := migrationTestConn(t, cfg, schema)
	if _, err := c.Exec(context.Background(), `INSERT INTO audit_event(id,event_type,actor_kind,result) VALUES('e_0000000000001',1,1,1)`); err != nil {
		t.Fatal(err)
	}
	prepareAudit(t, c, schema)
	if _, err := c.Exec(context.Background(), `UPDATE audit_event_upgrade_meta SET namespace='urn:changed'`); err != nil {
		t.Fatal(err)
	}
	if err := Up(context.Background(), cfg, schema); err == nil {
		t.Fatal("accepted changed metadata when events have no resource namespace")
	}
	assertVersion(t, cfg, schema, 2, true)
}

func TestAuditMigrationRejectsIncompleteInitialBaseline(t *testing.T) {
	for _, q := range []string{`ALTER TABLE audit_event RENAME COLUMN session_id TO unexpected_column`, `ALTER TABLE identity_flow DROP COLUMN token_digest`} {
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
	assertVersion(t, cfg, schema, 2, false)
}

func TestGenericAuditNonemptyRequiresPreparation(t *testing.T) {
	cfg, schema := safetyDB(t)
	initialUp(t, cfg, schema)
	c := migrationTestConn(t, cfg, schema)
	if _, err := c.Exec(context.Background(), `INSERT INTO audit_event(id,event_type,actor_kind,result) VALUES('e_0000000000001',1,1,1)`); err != nil {
		t.Fatal(err)
	}
	if err := Up(context.Background(), cfg, schema); err == nil {
		t.Fatal("nonempty audit upgraded without preparation")
	}
	var count int
	if err := c.QueryRow(context.Background(), `SELECT count(*) FROM audit_event WHERE id='e_0000000000001'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("original row lost: %d %v", count, err)
	}
	assertVersion(t, cfg, schema, 2, true)
}
