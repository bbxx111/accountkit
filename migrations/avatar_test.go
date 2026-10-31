package migrations

import (
	"context"
	"testing"
)

func TestAvatarInitialMigration(t *testing.T) {
	cfg, schema, c := consolidatedProfileAttributesInitial(t)
	var nullable, typ string
	var def *string
	err := c.QueryRow(context.Background(), `SELECT is_nullable,udt_name,column_default FROM information_schema.columns WHERE table_schema=$1 AND table_name='user_account' AND column_name='avatar_url'`, schema).Scan(&nullable, &typ, &def)
	if err != nil || nullable != "YES" || typ != "text" || def != nil {
		t.Fatalf("avatar column %s %s %v: %v", nullable, typ, def, err)
	}
	seedInitialFlow(t, c)
	if _, err = c.Exec(context.Background(), `UPDATE user_account SET avatar_url='https://example.com/a'`); err != nil {
		t.Fatal(err)
	}
	catalog, data := migrationCatalog(t, c, schema), migrationDataSnapshot(t, c)
	if err = Up(context.Background(), cfg, schema); err != nil {
		t.Fatal(err)
	}
	if catalog != migrationCatalog(t, c, schema) || data != migrationDataSnapshot(t, c) {
		t.Fatal("repeat mutated data/catalog")
	}
	assertVersion(t, cfg, schema, 1, false)
}
