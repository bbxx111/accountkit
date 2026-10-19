package maintenance_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/internal/maintenancectx"
	"github.com/bbxx111/accountkit/internal/obstest"
	"github.com/bbxx111/accountkit/maintenance"
	"github.com/bbxx111/accountkit/observability"
)

type observationLocker struct {
	ok       bool
	err      error
	released bool
}

func (l *observationLocker) TryLock(context.Context) (bool, func(), error) {
	return l.ok, func() { l.released = true }, l.err
}

type observerFunc func(context.Context, observability.Observation)

func (f observerFunc) Observe(ctx context.Context, observation observability.Observation) {
	f(ctx, observation)
}

func TestRunnerObservations(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	t.Run("lock outcomes do not invent task runs", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			ok   bool
			err  error
			want observability.Result
		}{
			{"acquired", true, nil, observability.ResultAcquired},
			{"skipped", false, nil, observability.ResultSkipped},
			{"error", false, errors.New("storage unavailable"), observability.ResultError},
			{"canceled", false, context.Canceled, observability.ResultCanceled},
			{"deadline", false, context.DeadlineExceeded, observability.ResultDeadlineExceeded},
		} {
			t.Run(tc.name, func(t *testing.T) {
				capture := &obstest.Recorder{}
				locker := &observationLocker{ok: tc.ok, err: tc.err}
				runner := maintenance.NewRunnerWithObserver(time.Hour, locker, logger, capture)
				if got := runner.RunOnce(context.Background()); got != tc.ok {
					t.Fatalf("RunOnce=%v, want %v", got, tc.ok)
				}
				got := capture.Snapshot()
				if len(got) != 1 || got[0] != (observability.MaintenanceObservation{Kind: observability.MaintenanceRound, Result: tc.want}) {
					t.Fatalf("observations=%+v", got)
				}
				if locker.released != tc.ok {
					t.Fatalf("released=%v, want %v", locker.released, tc.ok)
				}
			})
		}
	})
	t.Run("task terminal and ownership", func(t *testing.T) {
		capture := &obstest.Recorder{}
		owner := observability.NewRecorder(capture, logger)
		tasks := []maintenance.Task{
			{Name: "cleanup_sessions", Run: func(ctx context.Context) error {
				if !maintenancectx.OwnsTask(ctx, observability.TaskCleanupSessions, owner) {
					t.Error("Runner must own matching task terminal")
				}
				return nil
			}},
			{Name: "purge_users", Run: func(context.Context) error { return errors.New("failed") }},
			{Name: "rekey_digests", Run: func(context.Context) error { return context.Canceled }},
			{Name: "reencrypt_subjects", Run: func(context.Context) error { return context.DeadlineExceeded }},
			{Name: "cleanup_sign_in_completion_flows", Run: func(context.Context) error { panic("task panic") }},
			{Name: "audit_retention", Run: func(context.Context) error { return nil }},
			{Name: "host-private-user-id", Run: func(ctx context.Context) error {
				if maintenancectx.OwnsTask(ctx, observability.TaskCleanupSessions, owner) {
					t.Error("custom task cannot suppress nested maintenance")
				}
				return nil
			}},
		}
		locker := &observationLocker{ok: true}
		if !maintenance.NewRunnerWithObserver(time.Hour, locker, logger, owner, tasks...).RunOnce(context.Background()) {
			t.Fatal("lock not acquired")
		}
		got := capture.Snapshot()
		if len(got) != 8 {
			t.Fatalf("observations=%+v", got)
		}
		wants := []struct {
			task   observability.Task
			result observability.Result
		}{
			{observability.TaskCleanupSessions, observability.ResultSuccess},
			{observability.TaskPurgeUsers, observability.ResultError},
			{observability.TaskRekeyDigests, observability.ResultCanceled},
			{observability.TaskReencryptSubjects, observability.ResultDeadlineExceeded},
			{observability.TaskCleanupSignInCompletionFlows, observability.ResultPanic},
			{observability.TaskAuditRetention, observability.ResultSuccess},
			{observability.TaskOther, observability.ResultSuccess},
		}
		for i, want := range wants {
			actual, ok := got[i+1].(observability.MaintenanceObservation)
			if !ok || actual.Kind != observability.MaintenanceRun || actual.Task != want.task || actual.Result != want.result || actual.Duration < 0 {
				t.Fatalf("run %d=%+v, want %+v", i, actual, want)
			}
		}
		if !locker.released {
			t.Fatal("lock not released")
		}
	})
	t.Run("observer panic preserves execution", func(t *testing.T) {
		runs := 0
		locker := &observationLocker{ok: true}
		runner := maintenance.NewRunnerWithObserver(time.Hour, locker, logger, observerFunc(func(context.Context, observability.Observation) { panic("observer secret") }),
			maintenance.Task{Name: "cleanup_sessions", Run: func(context.Context) error { runs++; return nil }})
		if !runner.RunOnce(context.Background()) || runs != 1 || !locker.released {
			t.Fatalf("runs=%d released=%v", runs, locker.released)
		}
	})
	t.Run("disabled runner cannot own observed service", func(t *testing.T) {
		owner := observability.NewRecorder(&obstest.Recorder{}, nil)
		maintenance.NewRunner(time.Hour, &observationLocker{ok: true}, logger, maintenance.Task{Name: "cleanup_sessions", Run: func(ctx context.Context) error {
			if maintenancectx.OwnsTask(ctx, observability.TaskCleanupSessions, owner) {
				t.Error("disabled Runner owns observed service")
			}
			return nil
		}}).RunOnce(context.Background())
	})
	t.Run("distinct owner remains independent", func(t *testing.T) {
		owner := observability.NewRecorder(&obstest.Recorder{}, nil)
		other := observability.NewRecorder(&obstest.Recorder{}, nil)
		maintenance.NewRunnerWithObserver(time.Hour, &observationLocker{ok: true}, logger, owner, maintenance.Task{Name: "cleanup_sessions", Run: func(ctx context.Context) error {
			if maintenancectx.OwnsTask(ctx, observability.TaskCleanupSessions, other) {
				t.Error("Runner suppressed a different observer")
			}
			return nil
		}}).RunOnce(context.Background())
	})
	t.Run("manual rounds are serialized and observed once", func(t *testing.T) {
		capture := &obstest.Recorder{}
		var runs atomic.Int32
		runner := maintenance.NewRunnerWithObserver(time.Hour, &fakeLocker{}, logger, capture, maintenance.Task{Name: "cleanup_sessions", Run: func(context.Context) error { runs.Add(1); return nil }})
		var wg sync.WaitGroup
		for range 20 {
			wg.Go(func() { runner.RunOnce(context.Background()) })
		}
		wg.Wait()
		if runs.Load() != 20 || len(capture.Snapshot()) != 40 {
			t.Fatalf("runs=%d observations=%d", runs.Load(), len(capture.Snapshot()))
		}
	})
	t.Run("timed cancellation records final run", func(t *testing.T) {
		capture := &obstest.Recorder{}
		started := make(chan struct{})
		var first sync.Once
		var runs atomic.Int32
		runner := maintenance.NewRunnerWithObserver(time.Millisecond, &fakeLocker{}, logger, capture, maintenance.Task{Name: "cleanup_sessions", Run: func(ctx context.Context) error {
			runs.Add(1)
			first.Do(func() { close(started) })
			<-ctx.Done()
			return ctx.Err()
		}})
		runner.Start(context.Background())
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			runner.Close()
			t.Fatal("timed task did not start")
		}
		runner.Close()
		got := capture.Snapshot()
		if len(got) != 2*int(runs.Load()) {
			t.Fatalf("observations=%+v", got)
		}
		for i := 1; i < len(got); i += 2 {
			terminal := got[i].(observability.MaintenanceObservation)
			if terminal.Kind != observability.MaintenanceRun || terminal.Result != observability.ResultCanceled {
				t.Fatalf("terminal=%+v", terminal)
			}
		}
	})
}
