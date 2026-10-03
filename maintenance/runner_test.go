package maintenance_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bbxx111/accountkit/maintenance"
)

type fakeLocker struct {
	mu       sync.Mutex
	held     bool
	acquired atomic.Int32
	denied   atomic.Int32
}

func (f *fakeLocker) TryLock(context.Context) (bool, func(), error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.held {
		f.denied.Add(1)
		return false, nil, nil
	}
	f.held = true
	f.acquired.Add(1)
	return true, func() { f.mu.Lock(); f.held = false; f.mu.Unlock() }, nil
}

func TestRunOnceRunsAllTasksAndContinuesPastFailures(t *testing.T) {
	var order []string
	tasks := []maintenance.Task{
		{Name: "a", Run: func(context.Context) error { order = append(order, "a"); return nil }},
		{Name: "b", Run: func(context.Context) error { order = append(order, "b"); return errors.New("boom") }},
		{Name: "c", Run: func(context.Context) error { order = append(order, "c"); return nil }},
	}
	lk := &fakeLocker{}
	r := maintenance.NewRunner(time.Hour, lk, slog.New(slog.NewTextHandler(os.Stderr, nil)), tasks...)
	if !r.RunOnce(context.Background()) {
		t.Fatal("lock should be acquired")
	}
	if len(order) != 3 || order[0] != "a" || order[1] != "b" || order[2] != "c" {
		t.Fatalf("order = %v; a failure must not stop later tasks", order)
	}
	if lk.acquired.Load() != 1 || lk.held {
		t.Fatalf("lock must be acquired once and released: acquired=%d held=%v", lk.acquired.Load(), lk.held)
	}
}

func TestRunOnceSkipsWhenLockHeld(t *testing.T) {
	var runs atomic.Int32
	lk := &fakeLocker{held: true}
	r := maintenance.NewRunner(time.Hour, lk, slog.Default(), maintenance.Task{Name: "x", Run: func(context.Context) error { runs.Add(1); return nil }})
	if r.RunOnce(context.Background()) {
		t.Fatal("must report not acquired")
	}
	if runs.Load() != 0 || lk.denied.Load() != 1 {
		t.Fatalf("task must not run without the lock: runs=%d denied=%d", runs.Load(), lk.denied.Load())
	}
}

func TestStartTicksAndCloseStops(t *testing.T) {
	var runs atomic.Int32
	lk := &fakeLocker{}
	r := maintenance.NewRunner(50*time.Millisecond, lk, slog.Default(), maintenance.Task{Name: "x", Run: func(context.Context) error { runs.Add(1); return nil }})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.Start(ctx)
	r.Start(ctx) // 幂等
	deadline := time.Now().Add(2 * time.Second)
	for runs.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if runs.Load() < 2 {
		t.Fatalf("expected at least 2 ticks, got %d", runs.Load())
	}
	r.Close()
	r.Close()
	n := runs.Load()
	time.Sleep(200 * time.Millisecond)
	if runs.Load() != n {
		t.Fatal("no ticks after Close")
	}
}

func TestPanicInTaskIsRecovered(t *testing.T) {
	var after atomic.Bool
	r := maintenance.NewRunner(time.Hour, &fakeLocker{}, slog.Default(),
		maintenance.Task{Name: "panics", Run: func(context.Context) error { panic("kaboom") }},
		maintenance.Task{Name: "after", Run: func(context.Context) error { after.Store(true); return nil }},
	)
	r.RunOnce(context.Background())
	if !after.Load() {
		t.Fatal("a panicking task must not kill the round")
	}
}

func TestCloseCancelsInFlightRound(t *testing.T) {
	started := make(chan struct{})
	var sawCancel atomic.Bool
	r := maintenance.NewRunner(20*time.Millisecond, &fakeLocker{}, slog.Default(), maintenance.Task{
		Name: "blocks-until-cancelled",
		Run: func(ctx context.Context) error {
			select {
			case <-started:
			default:
				close(started)
			}
			<-ctx.Done()
			sawCancel.Store(true)
			return ctx.Err()
		},
	})
	r.Start(context.Background())
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("task never started")
	}
	done := make(chan struct{})
	go func() { r.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not return promptly while a task was blocked on ctx")
	}
	if !sawCancel.Load() {
		t.Fatal("in-flight task must observe ctx cancellation on Close")
	}
}

// 集成：同 schema 两个 locker 互斥，不同 schema 互不影响。
func TestPGLockerMutualExclusion(t *testing.T) {
	dsn := os.Getenv("SERVER_TEST_DB_DSN")
	if dsn == "" {
		t.Skip("SERVER_TEST_DB_DSN not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	a1 := maintenance.NewPGLocker(pool, "auth_a")
	a2 := maintenance.NewPGLocker(pool, "auth_a")
	b := maintenance.NewPGLocker(pool, "auth_b")

	ok, rel, err := a1.TryLock(ctx)
	if err != nil || !ok {
		t.Fatalf("first lock: ok=%v err=%v", ok, err)
	}
	ok2, rel2, err := a2.TryLock(ctx)
	if err != nil || ok2 {
		if rel2 != nil {
			rel2()
		}
		t.Fatalf("second lock on same schema must be denied: ok=%v err=%v", ok2, err)
	}
	okB, relB, err := b.TryLock(ctx)
	if err != nil || !okB {
		t.Fatalf("other schema must lock independently: ok=%v err=%v", okB, err)
	}
	relB()
	rel()
	ok3, rel3, err := a2.TryLock(ctx)
	if err != nil || !ok3 {
		t.Fatalf("after release the lock must be acquirable: ok=%v err=%v", ok3, err)
	}
	rel3()
}
