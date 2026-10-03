package migrations

import (
	"context"

	"os"
	"testing"
	"testing/fstest"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/jackc/pgx/v5"
)

func safetySource(t *testing.T, failure bool) fstest.MapFS {
	t.Helper()
	out := fstest.MapFS{}
	for _, name := range []string{"0001_init.up.sql", "0001_init.down.sql"} {
		b, err := fs.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		out[name] = &fstest.MapFile{Data: b}
	}
	name := "testdata/0002_success.up.sql"
	if failure {
		name = "testdata/0002_failure.up.sql"
	}
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	out["0002_probe.up.sql"] = &fstest.MapFile{Data: b}
	out["0002_probe.down.sql"] = &fstest.MapFile{Data: []byte("DROP TABLE upgrade_probe")}
	if failure {
		out["0003_after.up.sql"] = &fstest.MapFile{Data: []byte("CREATE TABLE after_failure (id integer)")}
	}
	return out
}
func sourceUp(t *testing.T, cfg *pgx.ConnConfig, schema string, failure bool) error {
	t.Helper()
	return runWithSource(context.Background(), cfg, schema, safetySource(t, failure), func(m *migrate.Migrate) error { return m.Up() })
}
func TestUpgradePreservesDataAndIsRepeatable(t *testing.T) {
	cfg, schema := safetyDB(t)
	ctx := context.Background()
	if err := Up(ctx, cfg, schema); err != nil {
		t.Fatal(err)
	}
	c, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(ctx)
	if _, err := c.Exec(ctx, "SET search_path TO "+schema); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		"INSERT INTO user_account(id,state) VALUES('u_0000000000001',1)",
		"INSERT INTO identity(id,user_id,kind,provider_subject) VALUES('i_0000000000001','u_0000000000001',3,'source-subject')",
		"INSERT INTO session(id,user_id,device_id,auth_time,refresh_token_hash,refresh_expire_time) VALUES('s_0000000000001','u_0000000000001','device',now(),decode('1234','hex'),now()+interval '1 day')",
		"INSERT INTO audit_event(id,event_type,actor_kind,result) VALUES('e_0000000000001',1,1,1)",
	} {
		if _, err := c.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := func() string {
		t.Helper()
		var s string
		if err := c.QueryRow(ctx, `SELECT jsonb_build_array((SELECT jsonb_agg(t) FROM user_account t),(SELECT jsonb_agg(t) FROM identity t),(SELECT jsonb_agg(t) FROM session t),(SELECT jsonb_agg(t) FROM audit_event t))::text`).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	before := snapshot()
	for range 2 {
		if err := sourceUp(t, cfg, schema, false); err != nil {
			t.Fatal(err)
		}
	}
	assertVersion(t, cfg, schema, 2, false)
	if snapshot() != before {
		t.Fatal("upgrade changed existing data")
	}
	var count int
	if err := c.QueryRow(ctx, "SELECT count(*) FROM upgrade_probe").Scan(&count); err != nil || count != 1 {
		t.Fatalf("upgrade replay: %d %v", count, err)
	}
}
func TestMigrationLockReleasedOnFailure(t *testing.T) {
	cfg, schema := safetyDB(t)
	if err := sourceUp(t, cfg, schema, true); err == nil {
		t.Fatal("wanted SQL failure")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	release, err := lockMigration(ctx, cfg, schema)
	if err != nil {
		t.Fatalf("leaked migration lock: %v", err)
	}
	release()
}
func TestMigrationLockWaitCancellation(t *testing.T) {
	cfg, schema := safetyDB(t)
	ctx := context.Background()
	release, err := lockMigration(ctx, cfg, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	waiting, cancel := context.WithTimeout(ctx, 75*time.Millisecond)
	defer cancel()
	if err := Up(waiting, cfg, schema); err == nil {
		t.Fatal("cancelled migration succeeded")
	}
	c, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(ctx)
	var count int
	if err := c.QueryRow(ctx, "SELECT count(*) FROM pg_namespace WHERE nspname=$1", schema).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("cancelled waiter executed DDL")
	}
}
