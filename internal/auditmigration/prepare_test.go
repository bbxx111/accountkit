package auditmigration

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/internal/migrationlock"
	"github.com/golang-migrate/migrate/v4/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func legacyDB(t *testing.T) (*pgx.Conn, Options) {
	t.Helper()
	dsn := os.Getenv("SERVER_TEST_DB_DSN")
	if dsn == "" {
		t.Skip("SERVER_TEST_DB_DSN not set")
	}
	c, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("akprep_%08x", rand.Uint32())
	o := Options{Schema: schema, ConfirmSchema: schema, Source: "test/auth", Namespace: "urn:test:accounts"}
	t.Cleanup(func() {
		_, _ = c.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		_ = c.Close(context.Background())
	})
	if _, err := c.Exec(context.Background(), "CREATE SCHEMA "+schema+"; SET search_path TO "+schema); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile("../../migrations/0001_init.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Exec(context.Background(), string(b)); err != nil {
		t.Fatal(err)
	}
	exec(t, c, `CREATE TABLE schema_migrations(version bigint PRIMARY KEY,dirty boolean NOT NULL); INSERT INTO schema_migrations VALUES(1,false)`)
	exec(t, c, `INSERT INTO audit_event(id,event_type,actor_kind,result,user_id,flow_id,subject_hint,occur_time) VALUES('e_0000000000001',1,1,1,'u_0000000000001','f_0000000000001','01234567','2024-03-04T05:06:07.123456Z')`)
	return c, o
}

func TestAuditMigrationPrepareSharesLocksAndOwnsConnections(t *testing.T) {
	for _, kind := range []string{"initialization", "engine"} {
		t.Run(kind, func(t *testing.T) {
			c, o := legacyDB(t)
			var release func()
			if kind == "initialization" {
				var err error
				release, err = migrationlock.Initialization(context.Background(), c.Config(), o.Schema)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				holder, err := pgx.ConnectConfig(context.Background(), c.Config())
				if err != nil {
					t.Fatal(err)
				}
				var db string
				if err := holder.QueryRow(context.Background(), `SELECT current_database()`).Scan(&db); err != nil {
					t.Fatal(err)
				}
				key, err := database.GenerateAdvisoryLockId(db, o.Schema, "schema_migrations")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := holder.Exec(context.Background(), `SELECT pg_advisory_lock($1)`, key); err != nil {
					t.Fatal(err)
				}
				release = func() { _ = holder.Close(context.Background()) }
			}
			defer release()
			ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
			defer cancel()
			start := time.Now()
			if _, err := Prepare(ctx, c, o); err == nil {
				t.Fatal("preparation ignored existing migration lock")
			}
			if time.Since(start) > time.Second {
				t.Fatal("lock wait ignored cancellation")
			}
			if c.IsClosed() {
				t.Fatal("Prepare closed caller connection")
			}
			var absent bool
			if err := c.QueryRow(context.Background(), `SELECT to_regclass('audit_event_upgrade_meta') IS NULL`).Scan(&absent); err != nil || !absent {
				t.Fatalf("cancelled preparation changed catalog: %v %v", absent, err)
			}
			release()
			if _, err := Prepare(context.Background(), c, o); err != nil {
				t.Fatalf("lock leaked after cancellation: %v", err)
			}
			if c.IsClosed() {
				t.Fatal("successful Prepare closed caller connection")
			}
		})
	}
}

func TestAuditMigrationCheckConfiguration(t *testing.T) {
	c, o := legacyDB(t)
	if err := CheckConfiguration(context.Background(), c, o); err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare(context.Background(), c, o); err != nil {
		t.Fatal(err)
	}
	if err := CheckConfiguration(context.Background(), c, o); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"source", "namespace"} {
		bad := o
		if field == "source" {
			bad.Source = "other/source"
		} else {
			bad.Namespace = "urn:other"
		}
		if err := CheckConfiguration(context.Background(), c, bad); err == nil {
			t.Fatalf("runtime %s mismatch accepted", field)
		}
	}
}

func exec(t *testing.T, c *pgx.Conn, q string) {
	t.Helper()
	if _, err := c.Exec(context.Background(), q); err != nil {
		t.Fatal(err)
	}
}
func snapshot(t *testing.T, c *pgx.Conn) string {
	t.Helper()
	var s string
	if err := c.QueryRow(context.Background(), `SELECT jsonb_build_array((SELECT jsonb_agg(t ORDER BY id) FROM audit_event t),(SELECT to_jsonb(t) FROM schema_migrations t))::text`).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestAuditMigrationPrepareStableMapping(t *testing.T) {
	c, o := legacyDB(t)
	before := snapshot(t, c)
	r, err := Prepare(context.Background(), c, o)
	if err != nil {
		t.Fatal(err)
	}
	if r.Prepared != 1 || r.Reused != 0 {
		t.Fatalf("first report: %+v", r)
	}
	var id uuid.UUID
	var eventText string
	if err := c.QueryRow(context.Background(), `SELECT id,event::text FROM audit_event_upgrade_stage WHERE legacy_id='e_0000000000001'`).Scan(&id, &eventText); err != nil {
		t.Fatal(err)
	}
	if id.Version() != 7 || id.Variant() != uuid.RFC4122 {
		t.Fatal("mapping must be UUIDv7")
	}
	exec(t, c, `SET TIME ZONE 'Asia/Shanghai'`)
	r, err = Prepare(context.Background(), c, o)
	if err != nil {
		t.Fatal(err)
	}
	if r.Prepared != 0 || r.Reused != 1 {
		t.Fatalf("repeat report: %+v", r)
	}
	var id2 uuid.UUID
	var event2 string
	if err := c.QueryRow(context.Background(), `SELECT id,event::text FROM audit_event_upgrade_stage`).Scan(&id2, &event2); err != nil {
		t.Fatal(err)
	}
	if id != id2 || eventText != event2 {
		t.Fatal("rerun changed stable mapping")
	}
	exec(t, c, `SET TIME ZONE 'UTC'`)
	if snapshot(t, c) != before {
		t.Fatal("Prepare changed original rows or migration version")
	}
}

func TestAuditMigrationPrepareRejectsChanges(t *testing.T) {
	for _, tc := range []struct{ name, sql string }{
		{"source_row", `UPDATE audit_event SET reason='changed'`},
		{"new_source_row", `INSERT INTO audit_event(id,event_type,actor_kind,result) VALUES('e_0000000000002',1,1,1)`},
		{"deleted_source_row", `DELETE FROM audit_event`},
		{"staged_event", `UPDATE audit_event_upgrade_stage SET event=jsonb_set(event,'{action}','"OTHER_ACTION"')`},
		{"staged_original", `UPDATE audit_event_upgrade_stage SET original=jsonb_set(original,'{reason}','"changed"')`},
		{"staged_id", `UPDATE audit_event_upgrade_stage SET id='019a0000-0000-7000-8000-000000000001'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, o := legacyDB(t)
			if _, err := Prepare(context.Background(), c, o); err != nil {
				t.Fatal(err)
			}
			exec(t, c, tc.sql)
			before := snapshot(t, c)
			if _, err := Prepare(context.Background(), c, o); err == nil {
				t.Fatal("accepted changed preparation")
			}
			if snapshot(t, c) != before {
				t.Fatal("rejected Prepare changed original data")
			}
		})
	}
}

func TestAuditMigrationPrepareRejectsConfiguration(t *testing.T) {
	c, o := legacyDB(t)
	before := snapshot(t, c)
	bad := o
	bad.ConfirmSchema = "other"
	if _, err := Prepare(context.Background(), c, bad); err == nil {
		t.Fatal("accepted unconfirmed schema")
	}
	if snapshot(t, c) != before {
		t.Fatal("confirmation failure changed source")
	}
	if _, err := Prepare(context.Background(), c, o); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"source", "namespace"} {
		bad = o
		if field == "source" {
			bad.Source = "other/source"
		} else {
			bad.Namespace = "urn:other"
		}
		if _, err := Prepare(context.Background(), c, bad); err == nil {
			t.Fatalf("accepted changed %s", field)
		}
	}
}

func TestAuditMigrationPrepareRejectsIncompatibleHistory(t *testing.T) {
	for _, q := range []string{`UPDATE audit_event SET event_type=32767`, `UPDATE audit_event SET actor_kind=32767`, `UPDATE audit_event SET result=32767`, `UPDATE audit_event SET identity_kind=32767`, `UPDATE audit_event SET subject_hint='overlong-historical-hint'`, `UPDATE audit_event SET device_id=repeat('x',257)`, `UPDATE schema_migrations SET version=2`, `UPDATE schema_migrations SET dirty=true`} {
		t.Run(q[:min(len(q), 50)], func(t *testing.T) {
			c, o := legacyDB(t)
			exec(t, c, q)
			before := snapshot(t, c)
			if _, err := Prepare(context.Background(), c, o); err == nil {
				t.Fatal("accepted incompatible history")
			}
			if snapshot(t, c) != before {
				t.Fatal("rejected history changed source")
			}
			var stage *string
			if err := c.QueryRow(context.Background(), `SELECT to_regclass('audit_event_upgrade_stage')::text`).Scan(&stage); err != nil {
				t.Fatal(err)
			}
			if stage != nil {
				t.Fatal("failed preparation left staging DDL")
			}
		})
	}
}
