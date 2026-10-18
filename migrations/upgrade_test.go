package migrations

import (
	"context"
	"fmt"
	"strconv"
	"strings"

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
	entries, err := fs.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
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
	out[fmt.Sprintf("%04d_probe.up.sql", latestVersion(t)+1)] = &fstest.MapFile{Data: b}
	out[fmt.Sprintf("%04d_probe.down.sql", latestVersion(t)+1)] = &fstest.MapFile{Data: []byte("DROP TABLE upgrade_probe")}
	if failure {
		out[fmt.Sprintf("%04d_after.up.sql", latestVersion(t)+2)] = &fstest.MapFile{Data: []byte("CREATE TABLE after_failure (id integer)")}
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
		`INSERT INTO audit_event(id,source,action,result,actor,targets,occur_time) VALUES('019a0000-0000-7000-8000-000000000001','test/auth','CODE_SENT',1,'{"type":"ANONYMOUS"}','[{"type":"IDENTITY_FLOW","namespace":"urn:test:accounts","id":"f_0000000000001"}]',now())`,
		"INSERT INTO sign_in_completion_flow(id,token_digest,state,policy_fingerprint,payload,cipher_version,proof_user_ids,user_id,session_id,create_time,update_time,expire_time) VALUES('f_0000000000001','upgrade-token-digest',1,'policy',decode('1234','hex'),1,ARRAY['u_0000000000001'],'u_0000000000001','s_0000000000001',now(),now(),now()+interval '5 minutes')",
	} {
		if _, err := c.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := func() string {
		t.Helper()
		var s string
		if err := c.QueryRow(ctx, `SELECT jsonb_build_array((SELECT jsonb_agg(t) FROM user_account t),(SELECT jsonb_agg(t) FROM identity t),(SELECT jsonb_agg(t) FROM session t),(SELECT jsonb_agg(t) FROM audit_event t),(SELECT jsonb_agg(t) FROM sign_in_completion_flow t))::text`).Scan(&s); err != nil {
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
	assertVersion(t, cfg, schema, latestVersion(t)+1, false)
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

// latestVersion enumerates embedded production sources; test probes always follow them.
func latestVersion(t *testing.T) int {
	t.Helper()
	entries, err := fs.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	latest := 0
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".up.sql") {
			v, err := strconv.Atoi(strings.SplitN(entry.Name(), "_", 2)[0])
			if err != nil {
				t.Fatal(err)
			}
			if v > latest {
				latest = v
			}
		}
	}
	return latest
}
