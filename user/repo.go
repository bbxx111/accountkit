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

	"github.com/bbxx111/accountkit/user/db"
)

// Repo 封装连接池与 sqlc Queries。
type Repo struct {
	pool *pgxpool.Pool
	q    *db.Queries
}

// NewRepo 构造 Repo。
func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool, q: db.New(pool)}
}

// Q 返回非事务 Queries。
func (r *Repo) Q() *db.Queries { return r.q }

// WithTxRaw 在一个事务内执行 fn，同时把 pgx.Tx 交给 fn（purge 需要把同一事务传给宿主 Anonymizer）；
// fn 返回错误或 panic 时回滚。
func (r *Repo) WithTxRaw(ctx context.Context, fn func(tx pgx.Tx, q *db.Queries) error) (err error) {
	tx, err := r.pool.Begin(ctx)
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

// WithTx 在一个事务内执行 fn；fn 返回错误或 panic 时回滚。
func (r *Repo) WithTx(ctx context.Context, fn func(q *db.Queries) error) error {
	return r.WithTxRaw(ctx, func(_ pgx.Tx, q *db.Queries) error { return fn(q) })
}

// IsUniqueViolation 报告 err 是否为 PostgreSQL 唯一约束冲突（SQLSTATE 23505）。
func IsUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
