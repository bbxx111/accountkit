package audit_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/enum"
)

// fakeInserter 记录收到的批次；fail 为真时整批失败。
type fakeInserter struct {
	mu      sync.Mutex
	batches [][]audit.Event
	fail    atomic.Bool
	calls   atomic.Int32
}

func (f *fakeInserter) InsertBatch(_ context.Context, events []audit.Event) (int, error) {
	f.calls.Add(1)
	if f.fail.Load() {
		return len(events), errors.New("db down")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.batches = append(f.batches, append([]audit.Event(nil), events...))
	return 0, nil
}

func (f *fakeInserter) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, b := range f.batches {
		n += len(b)
	}
	return n
}

func (f *fakeInserter) maxBatch() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := 0
	for _, b := range f.batches {
		if len(b) > m {
			m = len(b)
		}
	}
	return m
}

func ev(i int) audit.Event {
	return audit.Event{Type: enum.EventCodeSent, Actor: enum.ActorUser, Result: enum.ResultSuccess, IP: "203.0.113.77", RequestID: "req-e2e-1", OccurTime: time.Unix(int64(1_800_000_000+i), 0)}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within 3s")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestAsyncFlushesByBatchSizeAndInterval(t *testing.T) {
	f := &fakeInserter{}
	a := audit.NewAsync(f, audit.AsyncOptions{QueueSize: 1000, BatchSize: 10, FlushInterval: 20 * time.Millisecond, Logger: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.Start(ctx)
	a.Start(ctx) // 幂等
	for i := 0; i < 25; i++ {
		a.Record(ctx, ev(i))
	}
	waitFor(t, func() bool { return f.total() == 25 })
	if f.maxBatch() > 10 {
		t.Fatalf("batch size exceeded: %d", f.maxBatch())
	}
	if s := a.Stats(); s.Written != 25 || s.Dropped != 0 || s.Failed != 0 {
		t.Fatalf("stats: %+v", s)
	}
	a.Close()
	a.Close() // 幂等
}

func TestAsyncCloseFlushesRemainingAndDropsAfterClose(t *testing.T) {
	f := &fakeInserter{}
	a := audit.NewAsync(f, audit.AsyncOptions{FlushInterval: time.Hour, Logger: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))})
	a.Start(context.Background())
	for i := 0; i < 3; i++ {
		a.Record(context.Background(), ev(i))
	}
	a.Close()
	if f.total() != 3 {
		t.Fatalf("Close must flush the queue: %d", f.total())
	}
	a.Record(context.Background(), ev(99))
	if s := a.Stats(); s.Dropped != 1 || f.total() != 3 {
		t.Fatalf("record after close must be dropped: %+v total=%d", s, f.total())
	}
}

func TestAsyncCloseWithoutStartFlushesSynchronously(t *testing.T) {
	f := &fakeInserter{}
	a := audit.NewAsync(f, audit.AsyncOptions{Logger: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))})
	for i := 0; i < 5; i++ {
		a.Record(context.Background(), ev(i))
	}
	a.Close()
	if f.total() != 5 || a.Stats().Written != 5 {
		t.Fatalf("never-started recorder must flush on Close: total=%d stats=%+v", f.total(), a.Stats())
	}
}

func TestAsyncStartAfterCloseDoesNotLeakWorker(t *testing.T) {
	f := &fakeInserter{}
	a := audit.NewAsync(f, audit.AsyncOptions{Logger: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))})
	a.Close()
	before := runtime.NumGoroutine()
	a.Start(context.Background())
	a.Record(context.Background(), ev(1))
	if s := a.Stats(); s.Dropped != 1 || f.total() != 0 {
		t.Fatalf("record after close must be dropped even when Start is called after Close: %+v total=%d", s, f.total())
	}
	waitFor(t, func() bool { return runtime.NumGoroutine() <= before })
	a.Close() // 幂等
}

func TestAsyncDropsWhenQueueFullAndNeverBlocks(t *testing.T) {
	var logs bytes.Buffer
	f := &fakeInserter{}
	a := audit.NewAsync(f, audit.AsyncOptions{QueueSize: 2, Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	start := time.Now()
	for i := 0; i < 1000; i++ {
		a.Record(context.Background(), ev(i)) // 未 Start：队列容量 2，其余必须立即丢弃
	}
	if time.Since(start) > time.Second {
		t.Fatalf("Record must never block: took %s", time.Since(start))
	}
	if s := a.Stats(); s.Dropped != 998 {
		t.Fatalf("dropped = %d, want 998", s.Dropped)
	}
	a.Close()
	if f.total() != 2 {
		t.Fatalf("queued events must be flushed on Close: %d", f.total())
	}
	out := logs.String()
	if !strings.Contains(out, "dropped_total") || strings.Contains(out, "203.0.113.77") || strings.Contains(out, "req-e2e-1") {
		t.Fatalf("drop log must carry counts only, never event content: %s", out)
	}
	// 10 秒节流：998 次丢弃只产生 1 行 Warn
	if n := strings.Count(out, "dropped_total"); n != 1 {
		t.Fatalf("drop log must be throttled to one line per 10s, got %d", n)
	}
}

func TestAsyncWriteFailureIsCountedNotPropagated(t *testing.T) {
	var logs bytes.Buffer
	f := &fakeInserter{}
	f.fail.Store(true)
	a := audit.NewAsync(f, audit.AsyncOptions{BatchSize: 2, FlushInterval: time.Hour, Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	a.Start(context.Background())
	for i := 0; i < 4; i++ {
		a.Record(context.Background(), ev(i))
	}
	a.Close()
	if s := a.Stats(); s.Failed != 4 || s.Written != 0 || s.Dropped != 0 {
		t.Fatalf("stats after failures: %+v", s)
	}
	out := logs.String()
	if !strings.Contains(out, "db down") || strings.Contains(out, "203.0.113.77") {
		t.Fatalf("failure log must carry the error, never event content: %s", out)
	}
	// 后端恢复后同一实例已 Close，新实例正常写入（失败不会让 worker 退出，这里用第二个实例验证独立性）
	f.fail.Store(false)
	b := audit.NewAsync(f, audit.AsyncOptions{Logger: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))})
	b.Start(context.Background())
	b.Record(context.Background(), ev(1))
	b.Close()
	if f.total() != 1 {
		t.Fatalf("recovered backend must receive events: %d", f.total())
	}
}

func TestAsyncWriteFailureLogIsThrottled(t *testing.T) {
	var logs bytes.Buffer
	f := &fakeInserter{}
	f.fail.Store(true)
	a := audit.NewAsync(f, audit.AsyncOptions{BatchSize: 1, FlushInterval: 5 * time.Millisecond, Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	a.Start(context.Background())
	for i := 0; i < 20; i++ {
		a.Record(context.Background(), ev(i))
	}
	waitFor(t, func() bool { return a.Stats().Failed == 20 })
	a.Close()
	out := logs.String()
	// 20 次写失败都被计数，但 10 秒节流下只应产生 1 行 Error
	if n := strings.Count(out, "failed_total"); n != 1 {
		t.Fatalf("write-failure log must be throttled to one line per 10s, got %d: %s", n, out)
	}
}

func TestAsyncWorkerKeepsRunningAfterAFailedBatch(t *testing.T) {
	f := &fakeInserter{}
	f.fail.Store(true)
	a := audit.NewAsync(f, audit.AsyncOptions{BatchSize: 1, FlushInterval: 10 * time.Millisecond, Logger: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))})
	a.Start(context.Background())
	a.Record(context.Background(), ev(1))
	waitFor(t, func() bool { return a.Stats().Failed == 1 })
	f.fail.Store(false)
	a.Record(context.Background(), ev(2))
	waitFor(t, func() bool { return f.total() == 1 })
	a.Close()
	if s := a.Stats(); s.Failed != 1 || s.Written != 1 {
		t.Fatalf("stats: %+v", s)
	}
}

func TestAsyncContextCancelFlushesAndStops(t *testing.T) {
	f := &fakeInserter{}
	a := audit.NewAsync(f, audit.AsyncOptions{FlushInterval: time.Hour, Logger: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))})
	ctx, cancel := context.WithCancel(context.Background())
	a.Start(ctx)
	a.Record(ctx, ev(1))
	a.Record(ctx, ev(2))
	cancel()
	waitFor(t, func() bool { return f.total() == 2 })
	a.Record(context.Background(), ev(3)) // worker 已退出：丢弃计数
	if s := a.Stats(); s.Dropped != 1 {
		t.Fatalf("after ctx cancel new events must be dropped: %+v", s)
	}
	done := make(chan struct{})
	go func() { a.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close after ctx cancel must return promptly")
	}
}

func TestAsyncWritesToPostgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	a := audit.NewAsync(audit.NewStore(pool), audit.AsyncOptions{Logger: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))})
	a.Start(ctx)
	for i := 0; i < 3; i++ {
		a.Record(ctx, ev(i))
	}
	a.Close()
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_event WHERE event_type = $1`, int16(enum.EventCodeSent)).Scan(&n); err != nil || n != 3 {
		t.Fatalf("rows: %d %v", n, err)
	}
	if s := a.Stats(); s.Written != 3 {
		t.Fatalf("stats: %+v", s)
	}
}
