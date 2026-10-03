package migrations_test

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bbxx111/accountkit/ids"
	"github.com/bbxx111/accountkit/migrations"
)

func TestValidSchema(t *testing.T) {
	for _, ok := range []string{"auth", "auth_test", "a1_b2"} {
		if !migrations.ValidSchema(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "Auth", "1auth", "auth-test", "auth.x", "public; drop", string(make([]byte, 64))} {
		if migrations.ValidSchema(bad) {
			t.Errorf("%q should be invalid", bad)
		}
	}
}

// testDSN 返回集成测试库 DSN；未设置则跳过。
func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("SERVER_TEST_DB_DSN")
	if dsn == "" {
		t.Skip("SERVER_TEST_DB_DSN not set")
	}
	return dsn
}

func freshSchema(t *testing.T) string {
	return fmt.Sprintf("authtest_%08x", rand.Uint32())
}

func connCfg(t *testing.T, dsn string) *pgx.ConnConfig {
	t.Helper()
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func dropSchema(t *testing.T, dsn, schema string) {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	_, _ = pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
}

func TestUpCreatesTablesInsideSchemaOnly(t *testing.T) {
	dsn := testDSN(t)
	schema := freshSchema(t)
	t.Cleanup(func() { dropSchema(t, dsn, schema) })
	ctx := context.Background()
	if err := migrations.Up(ctx, connCfg(t, dsn), schema); err != nil {
		t.Fatal(err)
	}
	pool, _ := pgxpool.New(ctx, dsn)
	defer pool.Close()
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_tables WHERE schemaname = $1
		AND tablename IN ('user_account','identity','session','audit_event','schema_migrations')`, schema).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Fatalf("tables in %s = %d, want 5 (4 tables + schema_migrations)", schema, n)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_tables WHERE schemaname = 'public'
		AND tablename IN ('user_account','identity','session','audit_event')`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("auth tables leaked into public: %d", n)
	}
	// 幂等：再次 Up 无变化
	if err := migrations.Up(ctx, connCfg(t, dsn), schema); err != nil {
		t.Fatalf("second Up must be a no-op: %v", err)
	}
}

func TestTwoSchemasCoexist(t *testing.T) {
	dsn := testDSN(t)
	a, b := freshSchema(t), freshSchema(t)
	t.Cleanup(func() { dropSchema(t, dsn, a); dropSchema(t, dsn, b) })
	ctx := context.Background()
	if err := migrations.Up(ctx, connCfg(t, dsn), a); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Up(ctx, connCfg(t, dsn), b); err != nil {
		t.Fatal(err)
	}
	pool, _ := pgxpool.New(ctx, dsn)
	defer pool.Close()
	id, _ := ids.New(ids.User)
	if _, err := pool.Exec(ctx, fmt.Sprintf(`INSERT INTO %s.user_account (id, state) VALUES ($1, 1)`, a), id); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = pool.QueryRow(ctx, fmt.Sprintf(`SELECT count(*) FROM %s.user_account`, b)).Scan(&n)
	if n != 0 {
		t.Fatal("row written to schema a must not be visible in schema b")
	}
	if err := migrations.UnsafeReset(ctx, connCfg(t, dsn), a, migrations.UnsafeResetOptions{ConfirmSchema: a}); err != nil {
		t.Fatal(err)
	}
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM pg_tables WHERE schemaname = $1 AND tablename = 'user_account'`, a).Scan(&n)
	if n != 0 {
		t.Fatal("Down must drop tables in schema a")
	}
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM pg_tables WHERE schemaname = $1 AND tablename = 'user_account'`, b).Scan(&n)
	if n != 1 {
		t.Fatal("Down on schema a must not touch schema b")
	}
}

func TestSchemaPinsInvariants(t *testing.T) {
	dsn := testDSN(t)
	schema := freshSchema(t)
	t.Cleanup(func() { dropSchema(t, dsn, schema) })
	ctx := context.Background()
	if err := migrations.Up(ctx, connCfg(t, dsn), schema); err != nil {
		t.Fatal(err)
	}
	pool, _ := pgxpool.New(ctx, dsn)
	defer pool.Close()
	q := func(sql string, args ...any) error {
		_, err := pool.Exec(ctx, fmt.Sprintf(sql, schema), args...)
		return err
	}

	// id 格式 CHECK 与 ids.Pattern 一致
	if err := q(`INSERT INTO %s.user_account (id, state) VALUES ('u_UPPERCASE0000', 1)`); err == nil {
		t.Fatal("uppercase id must violate CHECK")
	}
	goodUser, _ := ids.New(ids.User)
	if err := q(`INSERT INTO %s.user_account (id, state) VALUES ($1, 1)`, goodUser); err != nil {
		t.Fatalf("valid id rejected: %v", err)
	}
	var pattern string
	_ = pool.QueryRow(ctx, `SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = 'user_account_id_format'`).Scan(&pattern)
	if want := ids.Pattern(ids.User); !contains(pattern, want) {
		t.Fatalf("CHECK %q must embed ids.Pattern %q", pattern, want)
	}

	// identity：digest 与 provider_subject 恰好一个
	iid, _ := ids.New(ids.Identity)
	if err := q(`INSERT INTO %s.identity (id, user_id, kind) VALUES ($1, $2, 1)`, iid, goodUser); err == nil {
		t.Fatal("both NULL must violate identity_subject_exactly_one")
	}
	if err := q(`INSERT INTO %s.identity (id, user_id, kind, subject_digest, provider_subject) VALUES ($1, $2, 1, 'd', 'p')`, iid, goodUser); err == nil {
		t.Fatal("both set must violate identity_subject_exactly_one")
	}
	// 唯一部分索引：同 (kind, digest) 活跃行只能一个；软删后可重建
	i1, _ := ids.New(ids.Identity)
	i2, _ := ids.New(ids.Identity)
	if err := q(`INSERT INTO %s.identity (id, user_id, kind, subject_digest, digest_key_version) VALUES ($1, $2, 1, 'dup', 1)`, i1, goodUser); err != nil {
		t.Fatal(err)
	}
	if err := q(`INSERT INTO %s.identity (id, user_id, kind, subject_digest, digest_key_version) VALUES ($1, $2, 1, 'dup', 1)`, i2, goodUser); err == nil {
		t.Fatal("duplicate active (kind, digest) must be rejected")
	}
	if err := q(`UPDATE %s.identity SET delete_time = now() WHERE id = $1`, i1); err != nil {
		t.Fatal(err)
	}
	if err := q(`INSERT INTO %s.identity (id, user_id, kind, subject_digest, digest_key_version) VALUES ($1, $2, 1, 'dup', 1)`, i2, goodUser); err != nil {
		t.Fatalf("after soft delete the same subject must be insertable: %v", err)
	}

	// 无外键
	var fks int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM pg_constraint c JOIN pg_namespace n ON n.oid = c.connamespace
		WHERE n.nspname = $1 AND c.contype = 'f'`, schema).Scan(&fks)
	if fks != 0 {
		t.Fatalf("foreign keys present: %d, spec says none", fks)
	}

	// purge 索引谓词钉住 PENDING_DELETION = 3
	var idxdef string
	_ = pool.QueryRow(ctx, `SELECT indexdef FROM pg_indexes WHERE schemaname = $1 AND indexname = 'user_account_purge_time_idx'`, schema).Scan(&idxdef)
	if !contains(idxdef, "state = 3") {
		t.Fatalf("purge index predicate must be state = 3 (PENDING_DELETION): %s", idxdef)
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
