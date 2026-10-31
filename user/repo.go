// Package user 是账号、身份与会话的领域层：service.go 持有业务规则与事务边界，
// repo.go 是 sqlc 生成代码的薄封装。HTTP handler（阶段 3b）与维护任务只调用 Service。
package user

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bbxx111/accountkit/internal/userdb"
)

// Repo 是领域服务的 PostgreSQL 存储依赖；查询和事务入口仅供包内使用。
type Repo struct {
	pool *pgxpool.Pool
	q    *db.Queries
}

// NewRepo 构造 Repo。
func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool, q: db.New(pool)}
}

// queries 返回非事务 Queries。
func (r *Repo) queries() *db.Queries { return r.q }

// withTxRaw 在一个事务内执行 fn，同时把 pgx.Tx 交给 fn（purge 需要把同一事务传给宿主 Anonymizer）；
// fn 返回错误或 panic 时回滚。
func (r *Repo) withTxRaw(ctx context.Context, fn func(tx pgx.Tx, q *db.Queries) error) (err error) {
	return r.withTxRawOptions(ctx, pgx.TxOptions{}, fn)
}

// withDomainTxRaw 显式使用 READ COMMITTED，使领域操作在等待既有锁后，
// 下一条复核语句能看到最新提交状态，不受宿主默认隔离级别影响。
// 原始事务回调与领域查询使用同一个事务。
func (r *Repo) withDomainTxRaw(ctx context.Context, fn func(tx pgx.Tx, q *db.Queries) error) error {
	return r.withTxRawOptions(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, fn)
}

func (r *Repo) withDomainTx(ctx context.Context, fn func(q *db.Queries) error) error {
	return r.withDomainTxRaw(ctx, func(_ pgx.Tx, q *db.Queries) error { return fn(q) })
}

func (r *Repo) withTxRawOptions(ctx context.Context, options pgx.TxOptions, fn func(tx pgx.Tx, q *db.Queries) error) (err error) {
	tx, err := r.pool.BeginTx(ctx, options)
	if err != nil {
		return fmt.Errorf("user: begin tx: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()
	if err = fn(tx, r.q.WithTx(tx)); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("user: commit tx: %w", err)
	}
	return nil
}

// withTx 在一个事务内执行 fn；fn 返回错误或 panic 时回滚。
func (r *Repo) withTx(ctx context.Context, fn func(q *db.Queries) error) error {
	return r.withTxRaw(ctx, func(_ pgx.Tx, q *db.Queries) error { return fn(q) })
}

// isUniqueViolation 报告 err 是否为 PostgreSQL 唯一约束冲突（SQLSTATE 23505）。
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
