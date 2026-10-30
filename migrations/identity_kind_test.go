package migrations

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

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
