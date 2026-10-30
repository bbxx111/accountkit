package migrations

import (
	"context"
	"slices"
	"testing"
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
