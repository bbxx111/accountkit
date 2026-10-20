package migrations

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/jackc/pgx/v5"
)

func publishedExtensionsInitial(t *testing.T) (*pgx.ConnConfig, string, *pgx.Conn) {
	t.Helper()
	cfg, schema := safetyDB(t)
	c := migrationTestConn(t, cfg, schema)
	initial, err := exec.Command("git", "show", "62f0799b6c6ef1603b33f81834d39033dd0d0c4d:migrations/0001_init.up.sql").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"CREATE SCHEMA " + schema, string(initial), "CREATE TABLE schema_migrations(version bigint PRIMARY KEY,dirty boolean NOT NULL); INSERT INTO schema_migrations VALUES(1,false)"} {
		if _, err := c.Exec(context.Background(), q); err != nil {
			t.Fatal(err)
		}
	}
	return cfg, schema, c
}

func extensionsOldData(t *testing.T, c *pgx.Conn) string {
	t.Helper()
	var result string
	if err := c.QueryRow(context.Background(), `SELECT jsonb_build_array((SELECT jsonb_agg(to_jsonb(t)-ARRAY['extensions','extensions_schema_version','extensions_schema_fingerprint','extensions_revision','extensions_update_time'] ORDER BY id) FROM user_account t),(SELECT jsonb_agg(t ORDER BY id) FROM identity t),(SELECT jsonb_agg(t ORDER BY id) FROM session t),(SELECT jsonb_agg(t ORDER BY id) FROM sign_in_completion_flow t),(SELECT jsonb_agg(t ORDER BY id) FROM audit_event t),(SELECT jsonb_agg(t ORDER BY session_id,identity_id) FROM session_authentication_identity t),(SELECT jsonb_agg(t ORDER BY session_id) FROM session_client_info t))::text`).Scan(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestUserExtensionsMigration(t *testing.T) {
	cfg, schema, c := publishedExtensionsInitial(t)
	ctx := context.Background()
	seedRenameFlow(t, c, "sign_in_completion_flow")
	if _, err := c.Exec(ctx, `INSERT INTO session_authentication_identity VALUES('s_0000000000001','i_0000000000001'); INSERT INTO session_client_info(session_id,client_info,observe_time) VALUES('s_0000000000001','{"os":{"name":"synthetic"}}','2024-01-02T03:04:05Z')`); err != nil {
		t.Fatal(err)
	}
	before := extensionsOldData(t, c)
	// Keep a second independently configured instance at the published baseline.
	_, otherSchema, other := publishedExtensionsInitial(t)
	seedRenameFlow(t, other, "sign_in_completion_flow")
	otherCatalog, otherData := migrationCatalog(t, other, otherSchema), extensionsOldData(t, other)
	assertVersion(t, cfg, schema, 1, false)
	if err := Up(ctx, cfg, schema); err != nil {
		t.Fatal(err)
	}
	assertVersion(t, cfg, schema, 2, false)
	if extensionsOldData(t, c) != before {
		t.Fatal("upgrade changed old fields, times, credentials or context")
	}
	var defaults bool
	if err := c.QueryRow(ctx, `SELECT extensions='{}'::jsonb AND extensions_revision=0 AND extensions_schema_version IS NULL AND extensions_schema_fingerprint IS NULL AND extensions_update_time IS NULL FROM user_account`).Scan(&defaults); err != nil || !defaults {
		t.Fatalf("defaults=%v: %v", defaults, err)
	}
	catalog, data := migrationCatalog(t, c, schema), renameDataSnapshot(t, c, "sign_in_completion_flow")
	if err := Up(ctx, cfg, schema); err != nil {
		t.Fatal(err)
	}
	if catalog != migrationCatalog(t, c, schema) || data != renameDataSnapshot(t, c, "sign_in_completion_flow") {
		t.Fatal("repeat changed data/catalog")
	}
	for _, invalid := range []string{
		`extensions_revision=-1`,
		`extensions_schema_version='v1'`,
		`extensions_schema_version='',extensions_schema_fingerprint=repeat('a',64)`,
		`extensions_schema_version=repeat('a',65),extensions_schema_fingerprint=repeat('a',64)`,
		`extensions_schema_version='bad version',extensions_schema_fingerprint=repeat('a',64)`,
		`extensions_schema_version='v1',extensions_schema_fingerprint=repeat('A',64)`,
		`extensions_schema_version='v1',extensions_schema_fingerprint='abc'`,
		`extensions=NULL`,
		`extensions_revision=NULL`,
	} {
		if _, err := c.Exec(ctx, "UPDATE user_account SET "+invalid); err == nil {
			t.Fatalf("invalid storage metadata accepted: %s", invalid)
		}
	}
	if _, err := c.Exec(ctx, `UPDATE user_account SET extensions='[]'::jsonb`); err != nil {
		t.Fatal("database unexpectedly validates JSON business shape", err)
	}
	if err := Up(ctx, cfg, schema); err != nil {
		t.Fatal(err)
	}
	assertVersion(t, cfg, otherSchema, 1, false)
	if otherCatalog != migrationCatalog(t, other, otherSchema) || otherData != extensionsOldData(t, other) {
		t.Fatal("upgrade altered another instance")
	}
	t.Run("concurrent_v1_upgrade", func(t *testing.T) {
		cfg, schema, c := publishedExtensionsInitial(t)
		seedRenameFlow(t, c, "sign_in_completion_flow")
		before := extensionsOldData(t, c)
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
		assertVersion(t, cfg, schema, 2, false)
		if extensionsOldData(t, c) != before {
			t.Fatal("concurrent upgrade altered old data")
		}
	})
}

func TestUserExtensionsBaseline(t *testing.T) {
	t.Run("timestamp_precision", func(t *testing.T) {
		for _, baseline := range []struct {
			name    string
			version int
		}{{"v1", 1}, {"v2", 2}} {
			columns := []string{"freeze_time", "delete_time", "purge_time", "create_time", "update_time"}
			if baseline.version == 2 {
				columns = append(columns, "extensions_update_time")
			}
			for _, column := range columns {
				for _, precision := range []string{"0", "3", "6"} {
					t.Run(baseline.name+"/"+column+"/precision_"+precision, func(t *testing.T) {
						cfg, schema, c := publishedExtensionsInitial(t)
						ctx := context.Background()
						if baseline.version == 2 {
							if err := Up(ctx, cfg, schema); err != nil {
								t.Fatal(err)
							}
						}
						seedRenameFlow(t, c, "sign_in_completion_flow")
						if _, err := c.Exec(ctx, "UPDATE user_account SET "+column+"='2026-10-21T01:02:03.456789Z'"); err != nil {
							t.Fatal(err)
						}
						if _, err := c.Exec(ctx, "ALTER TABLE user_account ALTER COLUMN "+column+" TYPE timestamptz("+precision+")"); err != nil {
							t.Fatal(err)
						}
						catalog, data := migrationCatalog(t, c, schema), renameDataSnapshot(t, c, "sign_in_completion_flow")
						oldData := extensionsOldData(t, c)
						if precision == "6" {
							if err := Up(ctx, cfg, schema); err != nil {
								t.Fatalf("explicit microsecond precision rejected: %v", err)
							}
							assertVersion(t, cfg, schema, 2, false)
							if oldData != extensionsOldData(t, c) {
								t.Fatal("accepted baseline changed old data")
							}
							if baseline.version == 2 && (catalog != migrationCatalog(t, c, schema) || data != renameDataSnapshot(t, c, "sign_in_completion_flow")) {
								t.Fatal("repeat changed catalog/data")
							}
							return
						}
						for range 2 {
							if err := Up(ctx, cfg, schema); !errors.Is(err, ErrIncompatibleBaseline) {
								t.Fatalf("reduced timestamp precision accepted: %v", err)
							}
						}
						assertVersion(t, cfg, schema, baseline.version, false)
						if catalog != migrationCatalog(t, c, schema) || data != renameDataSnapshot(t, c, "sign_in_completion_flow") {
							t.Fatal("precision rejection changed catalog/data")
						}
					})
				}
			}
		}
	})
	mutations := []string{
		`ALTER TABLE user_account DROP COLUMN extensions_update_time`,
		`ALTER TABLE user_account ALTER COLUMN extensions TYPE json USING extensions::json`,
		`ALTER TABLE user_account ALTER COLUMN extensions DROP NOT NULL`,
		`ALTER TABLE user_account ALTER COLUMN extensions DROP DEFAULT`,
		`ALTER TABLE user_account ALTER COLUMN extensions SET DEFAULT '[]'::jsonb`,
		`ALTER TABLE user_account ALTER COLUMN extensions_revision DROP DEFAULT`,
		`ALTER TABLE user_account ALTER COLUMN extensions_revision SET DEFAULT 1`,
		`ALTER TABLE user_account ALTER COLUMN extensions_revision DROP NOT NULL`,
		`ALTER TABLE user_account ALTER COLUMN extensions_schema_version SET NOT NULL`,
		`ALTER TABLE user_account ALTER COLUMN extensions_schema_version SET DEFAULT 'v1'`,
		`ALTER TABLE user_account ALTER COLUMN extensions_schema_fingerprint TYPE varchar(64)`,
		`ALTER TABLE user_account ALTER COLUMN extensions_schema_fingerprint SET NOT NULL`,
		`ALTER TABLE user_account ALTER COLUMN extensions_update_time SET NOT NULL`,
		`ALTER TABLE user_account ALTER COLUMN extensions_update_time TYPE timestamp`,
		`ALTER TABLE user_account ALTER COLUMN extensions_update_time SET DEFAULT now()`,
		`ALTER TABLE user_account DROP CONSTRAINT user_account_extensions_revision_nonnegative`,
		`ALTER TABLE user_account DROP CONSTRAINT user_account_extensions_schema_pair`,
		`ALTER TABLE user_account DROP CONSTRAINT user_account_extensions_schema_version_format`,
		`ALTER TABLE user_account DROP CONSTRAINT user_account_extensions_schema_fingerprint_format`,
		`ALTER TABLE user_account DROP CONSTRAINT user_account_extensions_schema_pair, ADD CONSTRAINT user_account_extensions_schema_pair CHECK(true)`,
		`ALTER TABLE user_account DROP CONSTRAINT user_account_extensions_schema_pair, ADD CONSTRAINT user_account_extensions_schema_pair CHECK ((extensions_schema_version IS NULL) = (extensions_schema_fingerprint IS NULL)) NOT VALID`,
		`ALTER TABLE user_account DROP COLUMN display_name`,
		`ALTER TABLE user_account DROP CONSTRAINT user_account_id_format`,
		`ALTER TABLE user_account DROP CONSTRAINT user_account_pkey`,
		`ALTER TABLE user_account ALTER COLUMN create_time DROP DEFAULT`,
	}
	for _, mutation := range mutations {
		t.Run(mutation, func(t *testing.T) {
			cfg, schema := safetyDB(t)
			ctx := context.Background()
			if err := Up(ctx, cfg, schema); err != nil {
				t.Fatal(err)
			}
			c := migrationTestConn(t, cfg, schema)
			seedRenameFlow(t, c, "sign_in_completion_flow")
			if _, err := c.Exec(ctx, `UPDATE user_account SET extensions='{"synthetic":9007199254740993}',extensions_schema_version='v1',extensions_schema_fingerprint=repeat('a',64),extensions_update_time='2024-01-02T03:04:05Z'`); err != nil {
				t.Fatal(err)
			}
			if _, err := c.Exec(ctx, mutation); err != nil {
				t.Fatal(err)
			}
			before, data := migrationCatalog(t, c, schema), renameDataSnapshot(t, c, "sign_in_completion_flow")
			for range 2 {
				if err := Up(ctx, cfg, schema); !errors.Is(err, ErrIncompatibleBaseline) {
					t.Fatalf("incomplete version 2 accepted: %v", err)
				}
			}
			release, err := lockMigration(ctx, cfg, schema)
			if err != nil {
				t.Fatalf("rejected baseline leaked lock: %v", err)
			}
			release()
			assertVersion(t, cfg, schema, 2, false)
			if migrationCatalog(t, c, schema) != before || renameDataSnapshot(t, c, "sign_in_completion_flow") != data {
				t.Fatal("rejection changed catalog/data")
			}
			if _, err := c.Exec(ctx, "UPDATE schema_migrations SET dirty=true"); err != nil {
				t.Fatal(err)
			}
			var dirty migrate.ErrDirty
			if err := Up(ctx, cfg, schema); !errors.As(err, &dirty) || dirty.Version != 2 {
				t.Fatalf("dirty precedence: %v", err)
			}
			assertVersion(t, cfg, schema, 2, true)
			if migrationCatalog(t, c, schema) != before || renameDataSnapshot(t, c, "sign_in_completion_flow") != data {
				t.Fatal("dirty rejection changed data/catalog")
			}
		})
	}
	for _, column := range []string{"extensions jsonb", "extensions_schema_version text", "extensions_schema_fingerprint text", "extensions_revision bigint", "extensions_update_time timestamptz"} {
		t.Run("v1_residual_"+strings.Fields(column)[0], func(t *testing.T) {
			cfg, schema, c := publishedExtensionsInitial(t)
			ctx := context.Background()
			seedRenameFlow(t, c, "sign_in_completion_flow")
			if _, err := c.Exec(ctx, "ALTER TABLE user_account ADD COLUMN "+column); err != nil {
				t.Fatal(err)
			}
			before, data := migrationCatalog(t, c, schema), extensionsOldData(t, c)
			if err := Up(ctx, cfg, schema); !errors.Is(err, ErrIncompatibleBaseline) {
				t.Fatalf("v1 residue accepted: %v", err)
			}
			assertVersion(t, cfg, schema, 1, false)
			if before != migrationCatalog(t, c, schema) || data != extensionsOldData(t, c) {
				t.Fatal("rejection changed catalog/data")
			}
		})
	}
	t.Run("v1_all_extensions_residual", func(t *testing.T) {
		cfg, schema, c := publishedExtensionsInitial(t)
		sql, err := fs.ReadFile("0002_add_user_extensions.up.sql")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.Exec(context.Background(), string(sql)); err != nil {
			t.Fatal(err)
		}
		catalog := migrationCatalog(t, c, schema)
		if err := Up(context.Background(), cfg, schema); !errors.Is(err, ErrIncompatibleBaseline) {
			t.Fatalf("v1 all residual extensions accepted: %v", err)
		}
		assertVersion(t, cfg, schema, 1, false)
		if catalog != migrationCatalog(t, c, schema) {
			t.Fatal("residual rejection changed catalog")
		}
	})
	t.Run("cancel_v1_upgrade_releases_locks", func(t *testing.T) {
		cfg, schema, _ := publishedExtensionsInitial(t)
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
		assertVersion(t, cfg, schema, 2, false)
	})
}
