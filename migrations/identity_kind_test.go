package migrations

import (
	"context"
	"errors"
	"os/exec"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// identityKindTextDDL applies only the separately authorized text identity kind
// to a historical test schema, so catalog comparisons still detect anything else.
const identityKindTextDDL = `ALTER TABLE identity ALTER COLUMN kind TYPE varchar(32) COLLATE "C" USING kind::text;
ALTER TABLE identity ADD CONSTRAINT identity_kind_format CHECK (kind ~ '^[A-Z][A-Z0-9_]{1,31}$')`

// Pin the last SMALLINT definition (Google sign-in archive on develop); do not derive it
// from today's SQL, which would silently change historical evidence.
const smallintIdentityKindCommit = "9812144"

func TestIdentityKindInitialDefinition(t *testing.T) {
	cfg, schema, c := consolidatedProfileAttributesInitial(t)
	ctx := context.Background()
	var udt, nullable string
	var length *int
	var collation *string
	if err := c.QueryRow(ctx, `SELECT udt_name,character_maximum_length,collation_name,is_nullable FROM information_schema.columns WHERE table_schema=$1 AND table_name='identity' AND column_name='kind'`, schema).Scan(&udt, &length, &collation, &nullable); err != nil {
		t.Fatal(err)
	}
	if udt != "varchar" || length == nil || *length != 32 || collation == nil || *collation != "C" || nullable != "NO" {
		t.Fatalf("identity.kind = %s(%v) collate %v nullable %s", udt, length, collation, nullable)
	}
	var def string
	if err := c.QueryRow(ctx, `SELECT pg_get_constraintdef(c.oid) FROM pg_constraint c JOIN pg_class r ON r.oid=c.conrelid JOIN pg_namespace n ON n.oid=r.relnamespace WHERE n.nspname=$1 AND r.relname='identity' AND c.conname='identity_kind_format' AND c.convalidated`, schema).Scan(&def); err != nil {
		t.Fatalf("identity_kind_format: %v", err)
	}
	if _, err := c.Exec(ctx, `INSERT INTO user_account(id,state) VALUES('u_0000000000001',1)`); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"wechat", "3", "A", "A-B", "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456"} {
		_, err := c.Exec(ctx, `INSERT INTO identity(id,user_id,kind,provider_subject) VALUES('i_0000000000001','u_0000000000001',$1,'synthetic')`, kind)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || (pgErr.Code != "23514" && pgErr.Code != "22001") {
			t.Fatalf("kind %q accepted or wrong error: %v", kind, err)
		}
	}
	for i, kind := range []string{"WECHAT", "CORP_SSO"} {
		id := []string{"i_0000000000001", "i_0000000000002"}[i]
		if _, err := c.Exec(ctx, `INSERT INTO identity(id,user_id,kind,provider_subject) VALUES($1,'u_0000000000001',$2,'synthetic')`, id, kind); err != nil {
			t.Fatalf("kind %q rejected: %v", kind, err)
		}
	}
	catalog := migrationCatalog(t, c, schema)
	if err := Up(ctx, cfg, schema); err != nil {
		t.Fatal(err)
	}
	if catalog != migrationCatalog(t, c, schema) {
		t.Fatal("repeat mutated catalog")
	}
	assertVersion(t, cfg, schema, 1, false)
}

func TestIdentityKindSmallintV1Rejected(t *testing.T) {
	initial, err := exec.Command("git", "show", smallintIdentityKindCommit+":migrations/0001_init.up.sql").Output()
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
			}
			assertIdentityKindRejected(t, cfg, schema, c, 3)
		})
	}
}

func TestIdentityKindDriftRejected(t *testing.T) {
	for _, ddl := range []string{
		`ALTER TABLE identity ALTER COLUMN kind TYPE varchar(64)`,
		`ALTER TABLE identity ALTER COLUMN kind TYPE text`,
		`ALTER TABLE identity ALTER COLUMN kind TYPE varchar(32) COLLATE "default"`,
		`ALTER TABLE identity DROP CONSTRAINT identity_kind_format`,
		`ALTER TABLE identity DROP CONSTRAINT identity_kind_format, ADD CONSTRAINT identity_kind_format CHECK (kind ~ '^[A-Za-z]+$')`,
	} {
		t.Run(ddl, func(t *testing.T) {
			cfg, schema, c := consolidatedProfileAttributesInitial(t)
			seedRenameFlow(t, c, "sign_in_completion_flow")
			if _, err := c.Exec(context.Background(), ddl); err != nil {
				t.Fatal(err)
			}
			assertIdentityKindRejected(t, cfg, schema, c, 2)
		})
	}
}

func assertIdentityKindRejected(t *testing.T, cfg *pgx.ConnConfig, schema string, c *pgx.Conn, attempts int) {
	t.Helper()
	ctx := context.Background()
	catalog, data := migrationCatalog(t, c, schema), renameDataSnapshot(t, c, "sign_in_completion_flow")
	for range attempts {
		if err := Up(ctx, cfg, schema); !errors.Is(err, ErrIncompatibleBaseline) {
			t.Fatal("incompatible identity kind accepted or lock leaked:", err)
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
}
