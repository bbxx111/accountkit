package audit

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// Inserter 是 Async 的落库后端；*Store 实现它。
type Inserter interface {
	InsertBatch(ctx context.Context, events []Event) (failed int, err error)
}

// AsyncOptions 配置 Async；零值取默认。
type AsyncOptions struct {
	QueueSize     int           // 默认 1024
	BatchSize     int           // 默认 100
	FlushInterval time.Duration // 默认 1s
	CloseTimeout  time.Duration // 默认 5s：单次写入超时，也是 Close 等待 worker 退出的上限
	Logger        *slog.Logger  // 默认 slog.Default()
}

// Stats 是累计计数（进程内，随实例生命周期）。
type Stats struct {
	Written uint64 // 已成功写入
	Dropped uint64 // 队列满或 Close 后到达而丢弃
	Failed  uint64 // 写入失败而丢弃
}

// dropLogInterval 是"事件被丢弃"告警日志的节流间隔。
const dropLogInterval = 10 * time.Second

// Async 是默认的 Recorder：Record 只入有界队列，单 worker 按批 / 按间隔写入 Inserter。
// 设计文档 §5.1：写入在认证动作提交之后异步进行，失败只记日志并计数，绝不改变动作结果；
// 因此 Record 永不阻塞、永不返回错误，队列满时丢弃。关键计数通过 Stats 与日志暴露（§6.3）。
type Async struct {
	store Inserter
	o     AsyncOptions

	ch   chan Event
	stop chan struct{}
	done chan struct{}

	startOnce sync.Once
	closeOnce sync.Once
	started   atomic.Bool
	closed    atomic.Bool

	written     atomic.Uint64
	dropped     atomic.Uint64
	failed      atomic.Uint64
	lastDropLog atomic.Int64 // UnixNano
	lastFailLog atomic.Int64 // UnixNano
}

// NewAsync 构造（不做 I/O、不启协程）。
func NewAsync(store Inserter, o AsyncOptions) *Async {
	if o.QueueSize <= 0 {
		o.QueueSize = 1024
	}
	if o.BatchSize <= 0 {
		o.BatchSize = 100
	}
	if o.FlushInterval <= 0 {
		o.FlushInterval = time.Second
	}
	if o.CloseTimeout <= 0 {
		o.CloseTimeout = 5 * time.Second
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	return &Async{store: store, o: o, ch: make(chan Event, o.QueueSize), stop: make(chan struct{}), done: make(chan struct{})}
}

// Record 实现 Recorder：非阻塞入队；队列满或已 Close → 丢弃并计数。
func (a *Async) Record(_ context.Context, e Event) {
	if a.closed.Load() {
		a.drop()
		return
	}
	select {
	case a.ch <- e:
	default:
		a.drop()
	}
}

func (a *Async) drop() {
	n := a.dropped.Add(1)
	now := time.Now().UnixNano()
	last := a.lastDropLog.Load()
	if now-last >= int64(dropLogInterval) && a.lastDropLog.CompareAndSwap(last, now) {
		a.o.Logger.Warn("audit: events dropped (queue full or recorder closed)", "dropped_total", n)
	}
}

// Start 启动 worker；重复调用只启动一次。ctx 取消时 worker 刷出队列并退出（之后的事件被丢弃计数）。
// 若在 Close 之后才调用 Start：Close 已无条件关闭 stop，worker 在第一次 select 就会命中该分支，
// 刷出（此时队列必为空，因为 Close 之后的 Record 已被丢弃并计数）并立即退出，不会泄漏 goroutine。
func (a *Async) Start(ctx context.Context) {
	a.startOnce.Do(func() {
		a.started.Store(true)
		go a.run(ctx)
	})
}

func (a *Async) run(ctx context.Context) {
	defer close(a.done)
	ticker := time.NewTicker(a.o.FlushInterval)
	defer ticker.Stop()
	batch := make([]Event, 0, a.o.BatchSize)
	for {
		select {
		case e := <-a.ch:
			batch = append(batch, e)
			if len(batch) >= a.o.BatchSize {
				a.flush(batch)
				batch = batch[:0]
			}
		case <-ticker.C:
			if len(batch) > 0 {
				a.flush(batch)
				batch = batch[:0]
			}
		case <-ctx.Done():
			a.closed.Store(true) // 之后的 Record 直接丢弃，drain 才能收敛
			a.flush(batch)
			a.drain()
			return
		case <-a.stop:
			a.flush(batch)
			a.drain()
			return
		}
	}
}

// drain 把队列中剩余事件按批刷出。调用前 closed 已置位，因此没有新的生产者，循环必然收敛。
func (a *Async) drain() {
	batch := make([]Event, 0, a.o.BatchSize)
	for {
		select {
		case e := <-a.ch:
			batch = append(batch, e)
			if len(batch) >= a.o.BatchSize {
				a.flush(batch)
				batch = batch[:0]
			}
		default:
			a.flush(batch)
			return
		}
	}
}

// flush 同步写一批；失败只计数、记日志（不含事件内容）。
func (a *Async) flush(batch []Event) {
	if len(batch) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), a.o.CloseTimeout)
	defer cancel()
	failed, err := a.store.InsertBatch(ctx, batch)
	if err != nil {
		if failed <= 0 || failed > len(batch) {
			failed = len(batch)
		}
		total := a.failed.Add(uint64(failed))
		a.written.Add(uint64(len(batch) - failed))
		now := time.Now().UnixNano()
		last := a.lastFailLog.Load()
		if now-last >= int64(dropLogInterval) && a.lastFailLog.CompareAndSwap(last, now) {
			// err 来自 pgx：*pgconn.PgError.Error() 只包含 Severity + Message + SQLSTATE，
			// 不含 Detail（CHECK/NOT NULL 违例时 Detail 才会带上失败行的内容），因此这里的
			// err 不会携带事件内容；若未来 pgx 改变这一行为需重新审视。
			a.o.Logger.Error("audit: write failed; events dropped", "failed", failed, "failed_total", total, "err", err)
		}
		return
	}
	a.written.Add(uint64(len(batch)))
}

// Close 幂等：拒绝新事件，刷出队列中残留的事件。无论是否已 Start 都无条件关闭 stop——这样即使
// Start 在 Close 之后才被调用，那个 worker 也会在第一次 select 就看到 stop 已关闭并立即退出，
// 而不会永远阻塞在 select 里（因为 closed 已置位，Record 直接丢弃，channel 上不会再有事件到达）。
// 已 Start 时等待 worker 退出（上限 CloseTimeout）；从未 Start 时在当前协程同步刷出
// （例如只用 Migrate 的一次性作业）。
func (a *Async) Close() {
	a.closeOnce.Do(func() {
		a.closed.Store(true)
		close(a.stop)
		if !a.started.Load() {
			a.drain()
			return
		}
		select {
		case <-a.done:
		case <-time.After(a.o.CloseTimeout):
			a.o.Logger.Error("audit: close timed out; remaining queued events dropped", "queued", len(a.ch))
		}
	})
}

// Stats 返回累计计数的快照。
func (a *Async) Stats() Stats {
	return Stats{Written: a.written.Load(), Dropped: a.dropped.Load(), Failed: a.failed.Load()}
}
