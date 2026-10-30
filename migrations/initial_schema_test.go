package migrations

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// The published initial schema and its production increments install seven business tables.
func TestConsolidatedInitialSchema(t *testing.T) {
	entries, err := fs.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, entry.Name())
		}
	}
	if !slices.Equal(names, []string{"0001_init.down.sql", "0001_init.up.sql"}) {
		t.Errorf("unexpected production migrations: %v", names)
	}
	cfg, schema := safetyDB(t)
	if err := Up(context.Background(), cfg, schema); err != nil {
		t.Fatal(err)
	}
	assertVersion(t, cfg, schema, latestVersion(t), false)
	c := migrationTestConn(t, cfg, schema)
	var tables int
	if err := c.QueryRow(context.Background(), "SELECT count(*) FROM pg_tables WHERE schemaname=$1 AND tablename <> 'schema_migrations'", schema).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 8 {
		t.Fatalf("business tables = %d, want 8", tables)
	}
	if _, err := c.Exec(context.Background(), "INSERT INTO user_account(id,state) VALUES('u_0000000000001',1)"); err != nil {
		t.Fatal(err)
	}
	before := migrationCatalog(t, c, schema)
	if err := Up(context.Background(), cfg, schema); err != nil {
		t.Fatal(err)
	}
	if got := migrationCatalog(t, c, schema); got != before {
		t.Fatal("repeat Up changed schema")
	}
	var users int
	if err := c.QueryRow(context.Background(), "SELECT count(*) FROM user_account").Scan(&users); err != nil || users != 1 {
		t.Fatalf("repeat Up changed data: %d %v", users, err)
	}
}

func migrationTestConn(t *testing.T, cfg *pgx.ConnConfig, schema string) *pgx.Conn {
	t.Helper()
	c, err := pgx.ConnectConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	if _, err := c.Exec(context.Background(), "SET search_path TO "+schema); err != nil {
		t.Fatal(err)
	}
	return c
}
func migrationCatalog(t *testing.T, c *pgx.Conn, schema string) string {
	t.Helper()
	var result string
	err := c.QueryRow(context.Background(), `
 SELECT jsonb_build_object(
 'columns',(SELECT jsonb_agg(to_jsonb(x) ORDER BY x.table_name,x.ordinal_position) FROM (
 SELECT table_name,ordinal_position,column_name,data_type,udt_name,is_nullable,column_default,collation_name,character_maximum_length,datetime_precision,numeric_precision,numeric_scale
 FROM information_schema.columns WHERE table_schema=$1 AND table_name <> 'schema_migrations') x),
 'constraints',(SELECT jsonb_agg(to_jsonb(x) ORDER BY x.table_name,x.name) FROM (
 SELECT r.relname AS table_name,c.conname AS name,c.contype,pg_get_constraintdef(c.oid) AS definition
 FROM pg_constraint c JOIN pg_class r ON r.oid=c.conrelid JOIN pg_namespace n ON n.oid=r.relnamespace
 WHERE n.nspname=$1 AND r.relname <> 'schema_migrations') x),
 'indexes',(SELECT jsonb_agg(to_jsonb(x) ORDER BY x.tablename,x.indexname) FROM (
 SELECT tablename,indexname,replace(indexdef,quote_ident($1)||'.','') AS definition FROM pg_indexes
 WHERE schemaname=$1 AND tablename <> 'schema_migrations') x))::text`, schema).Scan(&result)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
