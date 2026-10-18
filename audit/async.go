package audit

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bbxx111/accountkit/audit/event"
	"github.com/bbxx111/accountkit/observability"
)

// AsyncOptions 配置 Async；零值取默认。
type AsyncOptions struct {
	QueueSize     int                    // 默认 1024
	BatchSize     int                    // 默认 100
	FlushInterval time.Duration          // 默认 1s
	CloseTimeout  time.Duration          // 默认 5s：单次写入超时，也是 Close 等待 worker 退出的上限
	Logger        *slog.Logger           // 默认 slog.Default()
	Observer      observability.Observer // 可选；必须并发安全并快速返回
}

// Stats 是累计计数（进程内，随实例生命周期）。
type Stats struct {
	Duplicate uint64 // 已保存的相同事件，不重复计入 Written
	Invalid   uint64 // 通用校验或后端校验拒收
	Conflicts uint64 // 同一身份包含不同事实
	Written   uint64 // 已成功写入
	Dropped   uint64 // 队列满或 Close 后到达而丢弃
	Failed    uint64 // 写入失败而丢弃
}

// dropLogInterval 是"事件被丢弃"告警日志的节流间隔。
const dropLogInterval = 10 * time.Second

// Async 是默认的 Recorder：Record 只入有界队列，单 worker 按批 / 按间隔写入 Inserter。
// 设计文档 §5.1：写入在认证动作提交之后异步进行，失败只记日志并计数，绝不改变动作结果；
// 因此 Record 永不阻塞、永不返回错误，队列满时丢弃。关键计数通过 Stats 与日志暴露（§6.3）。
type Async struct {
	store    Inserter
	o        AsyncOptions
	observer *observability.Recorder

	ch   chan Event
	stop chan struct{}
	done chan struct{}

	startOnce sync.Once
	closeOnce sync.Once
	started   atomic.Bool
	closed    atomic.Bool
	admission sync.Mutex // serializes final enqueue with closing admission; never held during I/O

	duplicate   atomic.Uint64
	invalid     atomic.Uint64
	conflicts   atomic.Uint64
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
	return &Async{store: store, o: o, observer: observability.NewRecorder(o.Observer, o.Logger), ch: make(chan Event, o.QueueSize), stop: make(chan struct{}), done: make(chan struct{})}
}

// Record 实现 Recorder：非阻塞入队；队列满或已 Close → 丢弃并计数。
func (a *Async) Record(ctx context.Context, e Event) {
	if a.closed.Load() {
		a.drop(ctx)
		return
	}
	frozen, err := event.Freeze(e)
	if err != nil {
		a.invalid.Add(1)
		a.observe(ctx, observability.AuditAdmission, observability.ResultInvalid, 1)
		return
	}
	a.admission.Lock()
	accepted := false
	if !a.closed.Load() {
		select {
		case a.ch <- frozen:
			accepted = true
		default:
		}
	}
	a.admission.Unlock()
	if !accepted {
		a.drop(ctx) // Host callbacks must remain outside the admission boundary.
	}
}

func (a *Async) closeAdmission() {
	a.admission.Lock()
	a.closed.Store(true)
	a.admission.Unlock()
}

func (a *Async) drop(ctx context.Context) {
	n := a.dropped.Add(1)
	a.observe(ctx, observability.AuditAdmission, observability.ResultDropped, 1)
	now := time.Now().UnixNano()
	last := a.lastDropLog.Load()
	if now-last >= int64(dropLogInterval) && a.lastDropLog.CompareAndSwap(last, now) {
		a.o.Logger.Warn("audit: events dropped (queue full or recorder closed)", "dropped_total", n)
	}
}

func (a *Async) observe(ctx context.Context, stage observability.AuditStage, result observability.Result, count int) {
	if count > 0 && a.observer.Enabled() {
		a.observer.Observe(ctx, observability.AuditObservation{Kind: observability.AuditEvents, Stage: stage, Result: result, Count: int64(count)})
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
			a.closeAdmission() // Serialize with final sends before draining.
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
	result, err := a.store.InsertBatch(ctx, batch)
	// Invalid backend classifications count as a failed batch; never log host errors.
	total := result.Written + result.Duplicate + result.Failed + result.Invalid + result.Conflicts
	if total != len(batch) || result.Written < 0 || result.Duplicate < 0 || result.Failed < 0 || result.Invalid < 0 || result.Conflicts < 0 {
		result = BatchResult{Failed: len(batch)}
	}
	a.written.Add(uint64(result.Written))
	a.duplicate.Add(uint64(result.Duplicate))
	a.invalid.Add(uint64(result.Invalid))
	a.conflicts.Add(uint64(result.Conflicts))
	failedTotal := a.failed.Add(uint64(result.Failed))
	a.observe(ctx, observability.AuditWrite, observability.ResultWritten, result.Written)
	a.observe(ctx, observability.AuditWrite, observability.ResultDuplicate, result.Duplicate)
	a.observe(ctx, observability.AuditWrite, observability.ResultInvalid, result.Invalid)
	a.observe(ctx, observability.AuditWrite, observability.ResultConflict, result.Conflicts)
	a.observe(ctx, observability.AuditWrite, observability.ResultFailed, result.Failed)
	if err != nil || result.Failed+result.Invalid+result.Conflicts > 0 {
		now := time.Now().UnixNano()
		last := a.lastFailLog.Load()
		if now-last >= int64(dropLogInterval) && a.lastFailLog.CompareAndSwap(last, now) {
			a.o.Logger.Error("audit: write failed; events dropped", "failed", result.Failed, "invalid", result.Invalid, "conflicts", result.Conflicts, "failed_total", failedTotal)
		}
	}

}

// Close 幂等：拒绝新事件，刷出队列中残留的事件。无论是否已 Start 都无条件关闭 stop——这样即使
// Start 在 Close 之后才被调用，那个 worker 也会在第一次 select 就看到 stop 已关闭并立即退出，
// 而不会永远阻塞在 select 里（因为 closed 已置位，Record 直接丢弃，channel 上不会再有事件到达）。
// 已 Start 时等待 worker 退出（上限 CloseTimeout）；从未 Start 时在当前协程同步刷出
// （例如只用 Migrate 的一次性作业）。
func (a *Async) Close() {
	a.closeOnce.Do(func() {
		a.closeAdmission()
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
	return Stats{Duplicate: a.duplicate.Load(), Invalid: a.invalid.Load(), Conflicts: a.conflicts.Load(), Written: a.written.Load(), Dropped: a.dropped.Load(), Failed: a.failed.Load()}
}
