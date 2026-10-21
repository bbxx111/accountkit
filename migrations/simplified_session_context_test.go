package migrations

import (
	"context"
	"errors"
	"os/exec"
	"slices"
	"testing"

	"github.com/golang-migrate/migrate/v4"
)

func TestSimplifiedSessionClientStorage(t *testing.T) {
	cfg, schema := safetyDB(t)
	ctx := context.Background()
	if err := Up(ctx, cfg, schema); err != nil {
		t.Fatal(err)
	}
	c := migrationTestConn(t, cfg, schema)
	var knownExists bool
	if err := c.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=$1 AND table_name='session' AND column_name='authentication_identities_known')`, schema).Scan(&knownExists); err != nil || knownExists {
		t.Fatalf("removed known flag still present=%v: %v", knownExists, err)
	}
	var columns []string
	if err := c.QueryRow(ctx, `SELECT array_agg(column_name::text ORDER BY ordinal_position) FROM information_schema.columns WHERE table_schema=$1 AND table_name='session_client_info'`, schema).Scan(&columns); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(columns, []string{"session_id", "client_info", "observe_time", "create_time", "update_time"}) {
		t.Fatalf("unexpected single-record columns: %v", columns)
	}
	// Content validation belongs to the business layer; PostgreSQL accepts JSON values.
	for _, content := range []string{`{}`, `[]`, `{"unknown":{"access_token":"synthetic"}}`} {
		if _, err := c.Exec(ctx, `INSERT INTO session_client_info(session_id,client_info,observe_time) VALUES('s_0000000000001',$1,now()) ON CONFLICT(session_id) DO UPDATE SET client_info=EXCLUDED.client_info`, content); err != nil {
			t.Fatal("unexpected database business validation", err)
		}
	}
	for _, statement := range []string{
		`INSERT INTO session_client_info(session_id,client_info,observe_time) VALUES('s_0000000000001','{}',now())`,
		`INSERT INTO session_client_info(session_id,client_info,observe_time) VALUES('invalid','{}',now())`,
		`INSERT INTO session_client_info(session_id,client_info,observe_time) VALUES('s_0000000000002',NULL,now())`,
	} {
		if _, err := c.Exec(ctx, statement); err == nil {
			t.Fatal("missing basic constraint", statement)
		}
	}
	assertVersion(t, cfg, schema, latestVersion(t), false)
}

func TestSimplifiedSessionRejectsPreviousSnapshotBaseline(t *testing.T) {
	cfg, schema := safetyDB(t)
	ctx := context.Background()
	c := migrationTestConn(t, cfg, schema)
	previous, err := exec.Command("git", "show", "b414fef:migrations/0001_init.up.sql").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{"CREATE SCHEMA " + schema, string(previous), "CREATE TABLE schema_migrations(version bigint PRIMARY KEY,dirty boolean NOT NULL); INSERT INTO schema_migrations VALUES(1,false)"} {
		if _, err := c.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	seedRenameFlow(t, c, "sign_in_completion_flow")
	if _, err := c.Exec(ctx, `INSERT INTO session_authentication_identity VALUES('s_0000000000001','i_0000000000001'); INSERT INTO session_client_info(session_id,snapshot_kind,app_name,observe_time) VALUES('s_0000000000001',1,'preserved',now()),('s_0000000000001',2,'newer',now())`); err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		var value string
		if err := c.QueryRow(ctx, `SELECT jsonb_agg(t ORDER BY snapshot_kind)::text FROM session_client_info t`).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return renameDataSnapshot(t, c, "sign_in_completion_flow") + value
	}
	before, catalog := snapshot(), migrationCatalog(t, c, schema)
	for range 2 {
		if err := Up(ctx, cfg, schema); !errors.Is(err, ErrIncompatibleBaseline) {
			t.Fatalf("old two-snapshot v1 accepted: %v", err)
		}
		assertVersion(t, cfg, schema, 1, false)
		if snapshot() != before || migrationCatalog(t, c, schema) != catalog {
			t.Fatal("rejection changed old data/catalog")
		}
	}
	if _, err := c.Exec(ctx, "UPDATE schema_migrations SET dirty=true"); err != nil {
		t.Fatal(err)
	}
	var dirty migrate.ErrDirty
	if err := Up(ctx, cfg, schema); !errors.As(err, &dirty) {
		t.Fatalf("dirty precedence lost: %v", err)
	}
	assertVersion(t, cfg, schema, 1, true)
}
