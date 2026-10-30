package migrations

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func profileAttributesOldData(t *testing.T, c *pgx.Conn) string {
	t.Helper()
	var result string
	if err := c.QueryRow(context.Background(), `SELECT jsonb_build_array((SELECT jsonb_agg(to_jsonb(t)-ARRAY['profile_attributes','profile_attributes_schema_version','profile_attributes_schema_fingerprint','profile_attributes_update_time'] ORDER BY id) FROM user_account t),(SELECT jsonb_agg(t ORDER BY id) FROM identity t),(SELECT jsonb_agg(t ORDER BY id) FROM session t),(SELECT jsonb_agg(t ORDER BY id) FROM sign_in_completion_flow t),(SELECT jsonb_agg(t ORDER BY id) FROM audit_event t),(SELECT jsonb_agg(t ORDER BY session_id,identity_id) FROM session_authentication_identity t),(SELECT jsonb_agg(t ORDER BY session_id) FROM session_client_info t))::text`).Scan(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestProfileAttributesMigration(t *testing.T) {
	cfg, schema, c := consolidatedProfileAttributesInitial(t)
	ctx := context.Background()
	seedRenameFlow(t, c)
	if _, err := c.Exec(ctx, `INSERT INTO session_authentication_identity VALUES('s_0000000000001','i_0000000000001'); INSERT INTO session_client_info(session_id,client_info,observe_time) VALUES('s_0000000000001','{"os":{"name":"synthetic"}}','2024-01-02T03:04:05Z')`); err != nil {
		t.Fatal(err)
	}
	before := profileAttributesOldData(t, c)
	// Keep a second independently configured instance at the current baseline.
	_, otherSchema, other := consolidatedProfileAttributesInitial(t)
	seedRenameFlow(t, other)
	otherCatalog, otherData := migrationCatalog(t, other, otherSchema), profileAttributesOldData(t, other)
	assertVersion(t, cfg, schema, 1, false)
	if err := Up(ctx, cfg, schema); err != nil {
		t.Fatal(err)
	}
	assertVersion(t, cfg, schema, 1, false)
	if profileAttributesOldData(t, c) != before {
		t.Fatal("upgrade changed old fields, times, credentials or context")
	}
	var defaults bool
	if err := c.QueryRow(ctx, `SELECT profile_attributes='{}'::jsonb FROM user_account`).Scan(&defaults); err != nil || !defaults {
		t.Fatalf("defaults=%v: %v", defaults, err)
	}
	catalog, data := migrationCatalog(t, c, schema), renameDataSnapshot(t, c)
	if err := Up(ctx, cfg, schema); err != nil {
		t.Fatal(err)
	}
	if catalog != migrationCatalog(t, c, schema) || data != renameDataSnapshot(t, c) {
		t.Fatal("repeat changed data/catalog")
	}
	for _, invalid := range []string{
		`profile_attributes=NULL`,
	} {
		if _, err := c.Exec(ctx, "UPDATE user_account SET "+invalid); err == nil {
			t.Fatalf("invalid storage metadata accepted: %s", invalid)
		}
	}
	if _, err := c.Exec(ctx, `UPDATE user_account SET profile_attributes='[]'::jsonb`); err != nil {
		t.Fatal("database unexpectedly validates JSON business shape", err)
	}
	if err := Up(ctx, cfg, schema); err != nil {
		t.Fatal(err)
	}
	assertVersion(t, cfg, otherSchema, 1, false)
	if otherCatalog != migrationCatalog(t, other, otherSchema) || otherData != profileAttributesOldData(t, other) {
		t.Fatal("upgrade altered another instance")
	}
	t.Run("concurrent_v1_repeat", func(t *testing.T) {
		cfg, schema, c := consolidatedProfileAttributesInitial(t)
		seedRenameFlow(t, c)
		before := profileAttributesOldData(t, c)
		var wg sync.WaitGroup
		errs := make(chan error, 4)
		for range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				wait, cancel := context.WithTimeout(ctx, 10*time.Second)
				defer cancel()
				errs <- Up(wait, cfg.Copy(), schema)
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Error(err)
			}
		}
		assertVersion(t, cfg, schema, 1, false)
		if profileAttributesOldData(t, c) != before {
			t.Fatal("concurrent upgrade altered old data")
		}
	})
}

func TestProfileAttributesBaseline(t *testing.T) {
	t.Run("cancel_v1_repeat_releases_locks", func(t *testing.T) {
		cfg, schema, _ := consolidatedProfileAttributesInitial(t)
		ctx := context.Background()
		release, err := lockMigration(ctx, cfg, schema)
		if err != nil {
			t.Fatal(err)
		}
		wait, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
		err = Up(wait, cfg, schema)
		cancel()
		release()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("canceled upgrade: %v", err)
		}
		assertVersion(t, cfg, schema, 1, false)
		if err := Up(ctx, cfg, schema); err != nil {
			t.Fatal("cancellation leaked lock", err)
		}
		assertVersion(t, cfg, schema, 1, false)
	})
}

func consolidatedProfileAttributesInitial(t *testing.T) (*pgx.ConnConfig, string, *pgx.Conn) {
	t.Helper()
	cfg, schema := safetyDB(t)
	if err := Up(context.Background(), cfg, schema); err != nil {
		t.Fatal(err)
	}
	return cfg, schema, migrationTestConn(t, cfg, schema)
}
