// Package maintenance 运行周期性维护任务（purge、清理、密钥回填……）。
//
// 多副本部署时每轮先抢 PostgreSQL advisory lock，抢不到就跳过本轮，
// 保证同一 schema 的任务在集群内串行；任务顺序执行，单个失败或 panic
// 只记日志，不阻塞其他任务。
package maintenance

import (
	"context"
	"fmt"
	"hash/fnv"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Task 是一个维护任务。
type Task struct {
	Name string
	Run  func(ctx context.Context) error
}

// Locker 决定本轮是否由当前进程执行。
type Locker interface {
	TryLock(ctx context.Context) (acquired bool, release func(), err error)
}

// PGLocker 用 pg_try_advisory_lock 实现 Locker。锁是 session 级的，因此在持锁期间
// 独占池中的一条连接，release 时先 unlock 再归还连接。
type PGLocker struct {
	pool *pgxpool.Pool
	key  int64
}

// NewPGLocker 由 schema 派生锁 key：同 schema 的副本互斥，不同 schema 互不影响。
func NewPGLocker(pool *pgxpool.Pool, schema string) *PGLocker {
	h := fnv.New64a()
	_, _ = h.Write([]byte("authserver:" + schema))
	return &PGLocker{pool: pool, key: int64(h.Sum64())}
}

// TryLock 尝试获取 advisory lock。
func (l *PGLocker) TryLock(ctx context.Context) (bool, func(), error) {
	conn, err := l.pool.Acquire(ctx)
	if err != nil {
		return false, nil, fmt.Errorf("maintenance: acquire conn: %w", err)
	}
	var got bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, l.key).Scan(&got); err != nil {
		conn.Release()
		return false, nil, fmt.Errorf("maintenance: try lock: %w", err)
	}
	if !got {
		conn.Release()
		return false, nil, nil
	}
	release := func() {
		// 用独立的短超时 ctx 解锁：调用方的 ctx 可能已取消
		uctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = conn.Exec(uctx, `SELECT pg_advisory_unlock($1)`, l.key)
		conn.Release()
	}
	return true, release, nil
}

// Runner 周期性执行任务。
type Runner struct {
	interval time.Duration
	locker   Locker
	logger   *slog.Logger
	tasks    []Task

	startOnce sync.Once
	closeOnce sync.Once
	stopCh    chan struct{}
	doneCh    chan struct{}
	roundMu   sync.Mutex // 同一时刻只跑一轮
	cancel    context.CancelFunc
}

// NewRunner 构造 Runner。interval 为 ticker 周期。
func NewRunner(interval time.Duration, locker Locker, logger *slog.Logger, tasks ...Task) *Runner {
	if logger == nil {
		logger = slog.Default()
	}
	return &Runner{interval: interval, locker: locker, logger: logger, tasks: tasks,
		stopCh: make(chan struct{}), doneCh: make(chan struct{})}
}

// Start 非阻塞启动；重复调用只启动一次。ctx 取消或 Close 后退出。
func (r *Runner) Start(ctx context.Context) {
	r.startOnce.Do(func() {
		rctx, cancel := context.WithCancel(ctx)
		r.cancel = cancel
		go func() {
			defer close(r.doneCh)
			t := time.NewTicker(r.interval)
			defer t.Stop()
			for {
				select {
				case <-t.C:
					r.RunOnce(rctx)
				case <-rctx.Done():
					return
				case <-r.stopCh:
					return
				}
			}
		}()
	})
}

// Close 停止 ticker，取消进行中的一轮（任务通过 ctx 感知），并等待循环退出（最长 10s）。
// 未 Start 过也安全。
func (r *Runner) Close() {
	r.closeOnce.Do(func() {
		close(r.stopCh)
		r.startOnce.Do(func() { close(r.doneCh) }) // 从未 Start：直接标记完成
		if r.cancel != nil {
			r.cancel()
		}
		select {
		case <-r.doneCh:
		case <-time.After(10 * time.Second):
		}
	})
}

// RunOnce 执行一轮：抢锁 → 顺序执行全部任务 → 释放。返回是否抢到锁。
func (r *Runner) RunOnce(ctx context.Context) bool {
	r.roundMu.Lock()
	defer r.roundMu.Unlock()
	ok, release, err := r.locker.TryLock(ctx)
	if err != nil {
		r.logger.Warn("maintenance: lock error, skipping round", "err", err)
		return false
	}
	if !ok {
		r.logger.Debug("maintenance: lock held elsewhere, skipping round")
		return false
	}
	defer release()
	for _, task := range r.tasks {
		r.runTask(ctx, task)
	}
	return true
}

func (r *Runner) runTask(ctx context.Context, task Task) {
	defer func() {
		if p := recover(); p != nil {
			r.logger.Error("maintenance: task panicked", "task", task.Name, "panic", fmt.Sprint(p))
		}
	}()
	start := time.Now()
	if err := task.Run(ctx); err != nil {
		r.logger.Warn("maintenance: task failed", "task", task.Name, "err", err, "elapsed", time.Since(start))
		return
	}
	r.logger.Debug("maintenance: task ok", "task", task.Name, "elapsed", time.Since(start))
}
