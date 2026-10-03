// Package migrations 持有 accountkit 的数据库迁移并在可配置 schema 内应用它们。
//
// 迁移 SQL 使用非限定表名；Up/UnsafeReset 把连接的 search_path 设为 "<schema>"，并让
// golang-migrate 的迁移记录表也落在该 schema 内，从而与宿主自己的迁移链互不干扰，
// 同一数据库可并存多个 accountkit 实例（不同 schema）。
package migrations

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	iofsapi "io/fs"
	"regexp"

	"github.com/golang-migrate/migrate/v4"
	migratepgx "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

//go:embed *.sql
var fs embed.FS

var schemaRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// ValidSchema 报告 s 是否可安全用作 schema 名（我们把它拼进 SQL，故必须白名单校验）。
func ValidSchema(s string) bool { return schemaRe.MatchString(s) }

// Up 创建 schema（若不存在）并应用全部迁移。
func Up(ctx context.Context, connCfg *pgx.ConnConfig, schema string) error {
	return run(ctx, connCfg, schema, func(m *migrate.Migrate) error { return m.Up() })
}

// Down is disabled to prevent accidental data loss. Use UnsafeReset only for disposable development/test data.
func Down(ctx context.Context, connCfg *pgx.ConnConfig, schema string) error {
	return ErrDestructiveOperation
}

func run(ctx context.Context, connCfg *pgx.ConnConfig, schema string, step func(*migrate.Migrate) error) error {
	return runWithSource(ctx, connCfg, schema, fs, step)
}

// Source injection is internal and used only for migration tests.
func runWithSource(ctx context.Context, connCfg *pgx.ConnConfig, schema string, source iofsapi.FS, step func(*migrate.Migrate) error) error {
	if !ValidSchema(schema) {
		return fmt.Errorf("migrations: invalid schema name %q", schema)
	}
	release, err := lockMigration(ctx, connCfg, schema)
	if err != nil {
		return err
	}
	defer release()
	if err := ctx.Err(); err != nil {
		return err
	}
	cfg := connCfg.Copy()
	if cfg.RuntimeParams == nil {
		cfg.RuntimeParams = map[string]string{}
	}
	cfg.RuntimeParams["search_path"] = schema
	// Reserve the engine lock on its own connection before any DDL. PostgreSQL
	// advisory locks are reentrant on a session, so the engine's internal lock
	// calls cannot block behind legacy callers after this cancellable wait.
	var connections migrationConnections
	defer connections.close()
	db := stdlib.OpenDB(*cfg, stdlib.OptionAfterConnect(func(_ context.Context, conn *pgx.Conn) error {
		if err := lockLegacyEngine(ctx, conn, schema); err != nil {
			return err
		}
		return connections.add(conn)
	}))
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	defer db.Close()

	if _, err := db.ExecContext(ctx, `CREATE SCHEMA IF NOT EXISTS `+schema); err != nil {
		return fmt.Errorf("migrations: create schema %s: %w", schema, err)
	}
	driver, err := migratepgx.WithInstance(db, &migratepgx.Config{
		SchemaName:      schema,
		MigrationsTable: "schema_migrations",
	})
	if err != nil {
		return fmt.Errorf("migrations: driver: %w", err)
	}
	src, err := iofs.New(source, ".")
	if err != nil {
		return fmt.Errorf("migrations: source: %w", err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "pgx5", driver)
	if err != nil {
		return fmt.Errorf("migrations: migrator: %w", err)
	}
	defer func() { _, _ = m.Close() }()
	if err := step(m); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrations: schema %s: %w", schema, err)
	}
	return nil
}

// 编译期确认 *sql.DB 类型被使用（stdlib.OpenDB 返回 *sql.DB）。
var _ *sql.DB
