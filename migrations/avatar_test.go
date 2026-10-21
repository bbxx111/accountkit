package migrations

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
)

// Add only this task's authorized column to historical equivalence fixtures,
// preserving physical column order and all other historical definitions.
func avatarInitialFixtureSQL(t *testing.T, initial string) string {
	t.Helper()
	const marker = "    display_name          TEXT,"
	if strings.Count(initial, marker) != 1 {
		t.Fatal("historical display_name fixture changed")
	}
	return strings.Replace(initial, marker, marker+"\n    avatar_url            TEXT,", 1)
}

func TestAvatarInitialMigration(t *testing.T) {
	cfg, schema, c := consolidatedProfileAttributesInitial(t)
	var nullable, typ string
	var def *string
	err := c.QueryRow(context.Background(), `SELECT is_nullable,udt_name,column_default FROM information_schema.columns WHERE table_schema=$1 AND table_name='user_account' AND column_name='avatar_url'`, schema).Scan(&nullable, &typ, &def)
	if err != nil || nullable != "YES" || typ != "text" || def != nil {
		t.Fatalf("avatar column %s %s %v: %v", nullable, typ, def, err)
	}
	seedRenameFlow(t, c, "sign_in_completion_flow")
	if _, err = c.Exec(context.Background(), `UPDATE user_account SET avatar_url='https://example.com/a'`); err != nil {
		t.Fatal(err)
	}
	catalog, data := migrationCatalog(t, c, schema), renameDataSnapshot(t, c, "sign_in_completion_flow")
	if err = Up(context.Background(), cfg, schema); err != nil {
		t.Fatal(err)
	}
	if catalog != migrationCatalog(t, c, schema) || data != renameDataSnapshot(t, c, "sign_in_completion_flow") {
		t.Fatal("repeat mutated data/catalog")
	}
	assertVersion(t, cfg, schema, 1, false)
}

func TestAvatarBaselineRejected(t *testing.T) {
	for _, ddl := range []string{`DROP COLUMN avatar_url`, `ALTER COLUMN avatar_url TYPE varchar(2048)`, `ALTER COLUMN avatar_url SET NOT NULL`, `ALTER COLUMN avatar_url SET DEFAULT ''`} {
		for _, populated := range []bool{false, true} {
			t.Run(ddl+map[bool]string{false: "/empty", true: "/populated"}[populated], func(t *testing.T) {
				cfg, schema, c := consolidatedProfileAttributesInitial(t)
				ctx := context.Background()
				if populated {
					seedRenameFlow(t, c, "sign_in_completion_flow")
					if _, err := c.Exec(ctx, `UPDATE user_account SET avatar_url='https://example.com/a'`); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := c.Exec(ctx, "ALTER TABLE user_account "+ddl); err != nil {
					t.Fatal(err)
				}
				catalog, data := migrationCatalog(t, c, schema), renameDataSnapshot(t, c, "sign_in_completion_flow")
				for range 2 {
					if err := Up(ctx, cfg, schema); !errors.Is(err, ErrIncompatibleBaseline) {
						t.Fatal("incompatible avatar accepted", err)
					}
				}
				assertVersion(t, cfg, schema, 1, false)
				if catalog != migrationCatalog(t, c, schema) || data != renameDataSnapshot(t, c, "sign_in_completion_flow") {
					t.Fatal("rejection mutated database")
				}
				if _, err := c.Exec(ctx, "UPDATE schema_migrations SET dirty=true"); err != nil {
					t.Fatal(err)
				}
				var dirty migrate.ErrDirty
				if err := Up(ctx, cfg, schema); !errors.As(err, &dirty) || dirty.Version != 1 {
					t.Fatal("dirty precedence", err)
				}
				assertVersion(t, cfg, schema, 1, true)
				if catalog != migrationCatalog(t, c, schema) || data != renameDataSnapshot(t, c, "sign_in_completion_flow") {
					t.Fatal("dirty rejection mutated database")
				}
			})
		}
	}
}

func TestAvatarPreviousProfileV1Rejected(t *testing.T) {
	// Pin the previous profile-only definition; do not derive it by removing a
	// column from today's SQL, which would silently change historical evidence.
	initial, err := exec.Command("git", "show", "c7f2abb5ace87ef034213738fe4ff8bd19e18663:migrations/0001_init.up.sql").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, populated := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "populated"}[populated], func(t *testing.T) {
			cfg, schema := safetyDB(t)
			c := migrationTestConn(t, cfg, schema)
			ctx := context.Background()
			for _, q := range []string{"CREATE SCHEMA " + schema, string(initial), "CREATE TABLE schema_migrations(version bigint PRIMARY KEY,dirty boolean NOT NULL); INSERT INTO schema_migrations VALUES(1,false)"} {
				if _, err := c.Exec(ctx, q); err != nil {
					t.Fatal(err)
				}
			}
			if populated {
				seedRenameFlow(t, c, "sign_in_completion_flow")
				if _, err := c.Exec(ctx, `UPDATE user_account SET profile_attributes='{"theme":"synthetic"}'`); err != nil {
					t.Fatal(err)
				}
			}
			catalog, data := migrationCatalog(t, c, schema), renameDataSnapshot(t, c, "sign_in_completion_flow")
			for range 3 {
				if err := Up(ctx, cfg, schema); !errors.Is(err, ErrIncompatibleBaseline) {
					t.Fatal("old profile-only v1 accepted", err)
				}
			}
			assertVersion(t, cfg, schema, 1, false)
			if catalog != migrationCatalog(t, c, schema) || data != renameDataSnapshot(t, c, "sign_in_completion_flow") {
				t.Fatal("historical rejection changed database")
			}
			if _, err := c.Exec(ctx, "UPDATE schema_migrations SET dirty=true"); err != nil {
				t.Fatal(err)
			}
			var dirty migrate.ErrDirty
			if err := Up(ctx, cfg, schema); !errors.As(err, &dirty) || dirty.Version != 1 {
				t.Fatal("dirty precedence", err)
			}
			assertVersion(t, cfg, schema, 1, true)
			if catalog != migrationCatalog(t, c, schema) || data != renameDataSnapshot(t, c, "sign_in_completion_flow") {
				t.Fatal("dirty rejection changed database")
			}
		})
	}
}
