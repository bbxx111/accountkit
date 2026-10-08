package accountkit_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/user/sender"
)

// 此用例使用独占的一次性测试数据库，不覆盖已有的 account schema。
func TestDefaultSchemaMigrateAgainstRealDB(t *testing.T) {
	dsn := dbDSN(t)
	ctx := context.Background()
	poolCfg, err := accountkit.PoolConfig(dsn, "account")
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	schemaExists := func(schema string) bool {
		t.Helper()
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)`, schema).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		return exists
	}
	if schemaExists("account") {
		t.Fatal("account schema already exists; use a fresh disposable test database")
	}
	authExisted := schemaExists("auth")
	a, err := accountkit.New(minimal(), accountkit.Deps{
		Pool: pool, Redis: testRedis(t), SMSSender: sender.NewLog(nil), EmailSender: sender.NewLog(nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	if got := a.Config().Schema; got != "account" {
		t.Fatalf("default schema = %q, want account", got)
	}
	migrateErr := a.Migrate(ctx)
	// 预检时不存在，且本例独占测试库；仅在迁移创建后登记清理，兼顾半完成的迁移。
	if schemaExists("account") {
		t.Cleanup(func() {
			if _, err := pool.Exec(ctx, `DROP SCHEMA account CASCADE`); err != nil {
				t.Errorf("clean up test-created account schema: %v", err)
			}
		})
	}
	if migrateErr != nil {
		t.Fatalf("migrate default schema: %v", migrateErr)
	}
	var currentSchema string
	if err := pool.QueryRow(ctx, `SELECT current_schema()`).Scan(&currentSchema); err != nil || currentSchema != "account" {
		t.Fatalf("search_path resolves to %q, want account: %v", currentSchema, err)
	}
	rows, err := pool.Query(ctx, `SELECT tablename FROM pg_tables WHERE schemaname = 'account' ORDER BY tablename`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		tables = append(tables, table)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	wantTables := []string{"audit_event", "identity", "schema_migrations", "session", "user_account"}
	if !reflect.DeepEqual(tables, wantTables) {
		t.Fatalf("account tables = %v, want %v", tables, wantTables)
	}
	if err := a.Migrate(ctx); err != nil {
		t.Fatalf("repeat migrate default schema: %v", err)
	}
	var version int64
	var dirty bool
	if err := pool.QueryRow(ctx, `SELECT version, dirty FROM account.schema_migrations`).Scan(&version, &dirty); err != nil || version != 1 || dirty {
		t.Fatalf("migration version=%d dirty=%t, want version=1 dirty=false: %v", version, dirty, err)
	}
	if authExists := schemaExists("auth"); authExists != authExisted {
		t.Fatalf("auth existence changed: before=%t after=%t", authExisted, authExists)
	}
}
