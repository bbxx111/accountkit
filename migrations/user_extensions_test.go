package migrations

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/jackc/pgx/v5"
)

func publishedProfileAttributesInitial(t *testing.T) (*pgx.ConnConfig, string, *pgx.Conn) {
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

func profileAttributesOldData(t *testing.T, c *pgx.Conn) string {
	t.Helper()
	var result string
	if err := c.QueryRow(context.Background(), `SELECT jsonb_build_array((SELECT jsonb_agg(to_jsonb(t)-ARRAY['profile_attributes','profile_attributes_schema_version','profile_attributes_schema_fingerprint','profile_attributes_update_time'] ORDER BY id) FROM user_account t),(SELECT jsonb_agg(t ORDER BY id) FROM identity t),(SELECT jsonb_agg(t ORDER BY id) FROM session t),(SELECT jsonb_agg(t ORDER BY id) FROM sign_in_completion_flow t),(SELECT jsonb_agg(t ORDER BY id) FROM audit_event t),(SELECT jsonb_agg(t ORDER BY session_id,identity_id) FROM session_authentication_identity t),(SELECT jsonb_agg(t ORDER BY session_id) FROM session_client_info t))::text`).Scan(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestProfileAttributesMigration(t *testing.T) {
	cfg, schema, c := publishedProfileAttributesInitial(t)
	ctx := context.Background()
	seedRenameFlow(t, c, "sign_in_completion_flow")
	if _, err := c.Exec(ctx, `INSERT INTO session_authentication_identity VALUES('s_0000000000001','i_0000000000001'); INSERT INTO session_client_info(session_id,client_info,observe_time) VALUES('s_0000000000001','{"os":{"name":"synthetic"}}','2024-01-02T03:04:05Z')`); err != nil {
		t.Fatal(err)
	}
	before := profileAttributesOldData(t, c)
	// Keep a second independently configured instance at the published baseline.
	_, otherSchema, other := publishedProfileAttributesInitial(t)
	seedRenameFlow(t, other, "sign_in_completion_flow")
	otherCatalog, otherData := migrationCatalog(t, other, otherSchema), profileAttributesOldData(t, other)
	assertVersion(t, cfg, schema, 1, false)
	if err := Up(ctx, cfg, schema); err != nil {
		t.Fatal(err)
	}
	assertVersion(t, cfg, schema, 2, false)
	if profileAttributesOldData(t, c) != before {
		t.Fatal("upgrade changed old fields, times, credentials or context")
	}
	var defaults bool
	if err := c.QueryRow(ctx, `SELECT profile_attributes='{}'::jsonb AND profile_attributes_schema_version IS NULL AND profile_attributes_schema_fingerprint IS NULL AND profile_attributes_update_time IS NULL FROM user_account`).Scan(&defaults); err != nil || !defaults {
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
		`profile_attributes_schema_version='v1'`,
		`profile_attributes_schema_version='',profile_attributes_schema_fingerprint=repeat('a',64)`,
		`profile_attributes_schema_version=repeat('a',65),profile_attributes_schema_fingerprint=repeat('a',64)`,
		`profile_attributes_schema_version='bad version',profile_attributes_schema_fingerprint=repeat('a',64)`,
		`profile_attributes_schema_version='v1',profile_attributes_schema_fingerprint=repeat('A',64)`,
		`profile_attributes_schema_version='v1',profile_attributes_schema_fingerprint='abc'`,
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
	t.Run("concurrent_v1_upgrade", func(t *testing.T) {
		cfg, schema, c := publishedProfileAttributesInitial(t)
		seedRenameFlow(t, c, "sign_in_completion_flow")
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
		assertVersion(t, cfg, schema, 2, false)
		if profileAttributesOldData(t, c) != before {
			t.Fatal("concurrent upgrade altered old data")
		}
	})
}

func TestProfileAttributesBaseline(t *testing.T) {
	t.Run("retired_and_mixed_structures", func(t *testing.T) {
		const old = "ALTER TABLE user_account ADD COLUMN extensions JSONB NOT NULL DEFAULT '{}'::jsonb, ADD COLUMN extensions_schema_version TEXT, ADD COLUMN extensions_schema_fingerprint TEXT, ADD COLUMN extensions_revision BIGINT NOT NULL DEFAULT 0, ADD COLUMN extensions_update_time TIMESTAMPTZ, ADD CONSTRAINT user_account_extensions_revision_nonnegative CHECK (extensions_revision >= 0), ADD CONSTRAINT user_account_extensions_schema_pair CHECK ((extensions_schema_version IS NULL) = (extensions_schema_fingerprint IS NULL)), ADD CONSTRAINT user_account_extensions_schema_version_format CHECK (extensions_schema_version ~ '^[A-Za-z0-9._-]{1,64}$'), ADD CONSTRAINT user_account_extensions_schema_fingerprint_format CHECK (extensions_schema_fingerprint ~ '^[0-9a-f]{64}$');"
		for _, version := range []int{1, 2} {
			for _, residue := range []string{old, "ALTER TABLE user_account ADD COLUMN extensions jsonb", "ALTER TABLE user_account ADD COLUMN extensions_schema_version text", "ALTER TABLE user_account ADD COLUMN extensions_schema_fingerprint text", "ALTER TABLE user_account ADD COLUMN extensions_revision bigint", "ALTER TABLE user_account ADD COLUMN extensions_update_time timestamptz", "ALTER TABLE user_account ADD COLUMN profile_attributes_revision bigint", "ALTER TABLE user_account ADD CONSTRAINT user_account_extensions_schema_pair CHECK(true)"} {
				t.Run(fmt.Sprintf("v%d/%s", version, residue), func(t *testing.T) {
					cfg, schema, c := publishedProfileAttributesInitial(t)
					ctx := context.Background()
					seedRenameFlow(t, c, "sign_in_completion_flow")
					if version == 2 && residue != old {
						if err := Up(ctx, cfg, schema); err != nil {
							t.Fatal(err)
						}
					}
					if _, err := c.Exec(ctx, residue); err != nil {
						t.Fatal(err)
					}
					if version == 2 && residue == old {
						if _, err := c.Exec(ctx, "UPDATE schema_migrations SET version=2"); err != nil {
							t.Fatal(err)
						}
					}
					catalog, data := migrationCatalog(t, c, schema), renameDataSnapshot(t, c, "sign_in_completion_flow")
					for range 2 {
						if err := Up(ctx, cfg, schema); !errors.Is(err, ErrIncompatibleBaseline) {
							t.Fatal("retired structure accepted", err)
						}
					}
					assertVersion(t, cfg, schema, version, false)
					if catalog != migrationCatalog(t, c, schema) || data != renameDataSnapshot(t, c, "sign_in_completion_flow") {
						t.Fatal("rejection changed catalog/data")
					}
					if _, err := c.Exec(ctx, "UPDATE schema_migrations SET dirty=true"); err != nil {
						t.Fatal(err)
					}
					var dirty migrate.ErrDirty
					if err := Up(ctx, cfg, schema); !errors.As(err, &dirty) || dirty.Version != version {
						t.Fatal("dirty precedence", err)
					}
				})
			}
		}
	})

	t.Run("timestamp_precision", func(t *testing.T) {
		for _, baseline := range []struct {
			name    string
			version int
		}{{"v1", 1}, {"v2", 2}} {
			columns := []string{"freeze_time", "delete_time", "purge_time", "create_time", "update_time"}
			if baseline.version == 2 {
				columns = append(columns, "profile_attributes_update_time")
			}
			for _, column := range columns {
				for _, precision := range []string{"0", "3", "6"} {
					t.Run(baseline.name+"/"+column+"/precision_"+precision, func(t *testing.T) {
						cfg, schema, c := publishedProfileAttributesInitial(t)
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
						oldData := profileAttributesOldData(t, c)
						if precision == "6" {
							if err := Up(ctx, cfg, schema); err != nil {
								t.Fatalf("explicit microsecond precision rejected: %v", err)
							}
							assertVersion(t, cfg, schema, 2, false)
							if oldData != profileAttributesOldData(t, c) {
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
		`ALTER TABLE user_account DROP COLUMN profile_attributes_update_time`,
		`ALTER TABLE user_account ALTER COLUMN profile_attributes TYPE json USING profile_attributes::json`,
		`ALTER TABLE user_account ALTER COLUMN profile_attributes DROP NOT NULL`,
		`ALTER TABLE user_account ALTER COLUMN profile_attributes DROP DEFAULT`,
		`ALTER TABLE user_account ALTER COLUMN profile_attributes SET DEFAULT '[]'::jsonb`,
		`ALTER TABLE user_account ALTER COLUMN profile_attributes_schema_version SET NOT NULL`,
		`ALTER TABLE user_account ALTER COLUMN profile_attributes_schema_version SET DEFAULT 'v1'`,
		`ALTER TABLE user_account ALTER COLUMN profile_attributes_schema_fingerprint TYPE varchar(64)`,
		`ALTER TABLE user_account ALTER COLUMN profile_attributes_schema_fingerprint SET NOT NULL`,
		`ALTER TABLE user_account ALTER COLUMN profile_attributes_update_time SET NOT NULL`,
		`ALTER TABLE user_account ALTER COLUMN profile_attributes_update_time TYPE timestamp`,
		`ALTER TABLE user_account ALTER COLUMN profile_attributes_update_time SET DEFAULT now()`,
		`ALTER TABLE user_account DROP CONSTRAINT user_account_profile_attributes_schema_pair`,
		`ALTER TABLE user_account DROP CONSTRAINT user_account_profile_attributes_schema_version_format`,
		`ALTER TABLE user_account DROP CONSTRAINT user_account_profile_attributes_schema_fingerprint_format`,
		`ALTER TABLE user_account DROP CONSTRAINT user_account_profile_attributes_schema_pair, ADD CONSTRAINT user_account_profile_attributes_schema_pair CHECK(true)`,
		`ALTER TABLE user_account DROP CONSTRAINT user_account_profile_attributes_schema_pair, ADD CONSTRAINT user_account_profile_attributes_schema_pair CHECK ((profile_attributes_schema_version IS NULL) = (profile_attributes_schema_fingerprint IS NULL)) NOT VALID`,
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
			if _, err := c.Exec(ctx, `UPDATE user_account SET profile_attributes='{"synthetic":9007199254740993}',profile_attributes_schema_version='v1',profile_attributes_schema_fingerprint=repeat('a',64),profile_attributes_update_time='2024-01-02T03:04:05Z'`); err != nil {
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
	for _, column := range []string{"profile_attributes jsonb", "profile_attributes_schema_version text", "profile_attributes_schema_fingerprint text", "profile_attributes_update_time timestamptz"} {
		t.Run("v1_residual_"+strings.Fields(column)[0], func(t *testing.T) {
			cfg, schema, c := publishedProfileAttributesInitial(t)
			ctx := context.Background()
			seedRenameFlow(t, c, "sign_in_completion_flow")
			if _, err := c.Exec(ctx, "ALTER TABLE user_account ADD COLUMN "+column); err != nil {
				t.Fatal(err)
			}
			before, data := migrationCatalog(t, c, schema), profileAttributesOldData(t, c)
			if err := Up(ctx, cfg, schema); !errors.Is(err, ErrIncompatibleBaseline) {
				t.Fatalf("v1 residue accepted: %v", err)
			}
			assertVersion(t, cfg, schema, 1, false)
			if before != migrationCatalog(t, c, schema) || data != profileAttributesOldData(t, c) {
				t.Fatal("rejection changed catalog/data")
			}
		})
	}
	t.Run("v1_all_profile_attributes_residual", func(t *testing.T) {
		cfg, schema, c := publishedProfileAttributesInitial(t)
		sql, err := fs.ReadFile("0002_add_user_extensions.up.sql")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.Exec(context.Background(), string(sql)); err != nil {
			t.Fatal(err)
		}
		catalog := migrationCatalog(t, c, schema)
		if err := Up(context.Background(), cfg, schema); !errors.Is(err, ErrIncompatibleBaseline) {
			t.Fatalf("v1 all residual profile_attributes accepted: %v", err)
		}
		assertVersion(t, cfg, schema, 1, false)
		if catalog != migrationCatalog(t, c, schema) {
			t.Fatal("residual rejection changed catalog")
		}
	})
	t.Run("cancel_v1_upgrade_releases_locks", func(t *testing.T) {
		cfg, schema, _ := publishedProfileAttributesInitial(t)
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
