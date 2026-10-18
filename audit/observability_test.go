package audit_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/audit/authentication"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/internal/obstest"
	"github.com/bbxx111/accountkit/observability"
)

type observationInserter func(context.Context, []audit.Event) (audit.BatchResult, error)
type observationOutput func(context.Context, audit.Event)
type auditObserverFunc func(context.Context, observability.Observation)

func (f auditObserverFunc) Observe(ctx context.Context, o observability.Observation) { f(ctx, o) }

func (f observationOutput) Record(ctx context.Context, e audit.Event) { f(ctx, e) }

func (f observationInserter) InsertBatch(ctx context.Context, es []audit.Event) (audit.BatchResult, error) {
	return f(ctx, es)
}

func auditCounts(t *testing.T, r *obstest.Recorder) map[observability.AuditObservation]int64 {
	t.Helper()
	counts := map[observability.AuditObservation]int64{}
	for _, o := range r.Snapshot() {
		a, ok := o.(observability.AuditObservation)
		if !ok || a.Count <= 0 {
			t.Fatalf("unexpected observation: %#v", o)
		}
		n := a.Count
		a.Count = 0
		counts[a] += n
	}
	return counts
}

func TestAuditObservations(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	t.Run("mixed batch increments and admission", func(t *testing.T) {
		var observed obstest.Recorder
		a := audit.NewAsync(observationInserter(func(_ context.Context, es []audit.Event) (audit.BatchResult, error) {
			if len(es) != 7 {
				t.Fatalf("batch=%d, want 7", len(es))
			}
			return audit.BatchResult{Written: 2, Duplicate: 1, Invalid: 1, Conflicts: 1, Failed: 2}, errors.New("private-backend-error")
		}), audit.AsyncOptions{Observer: &observed, Logger: logger, QueueSize: 7, BatchSize: 7})
		a.Record(ctx, audit.Event{})
		for i := 0; i < 8; i++ {
			a.Record(ctx, ev(i))
		}
		a.Close() // synchronous drain owns the write observations too
		a.Close()
		a.Record(ctx, ev(9))
		want := map[observability.AuditObservation]int64{
			{Kind: observability.AuditEvents, Stage: observability.AuditAdmission, Result: observability.ResultInvalid}: 1,
			{Kind: observability.AuditEvents, Stage: observability.AuditAdmission, Result: observability.ResultDropped}: 2,
			{Kind: observability.AuditEvents, Stage: observability.AuditWrite, Result: observability.ResultWritten}:     2,
			{Kind: observability.AuditEvents, Stage: observability.AuditWrite, Result: observability.ResultDuplicate}:   1,
			{Kind: observability.AuditEvents, Stage: observability.AuditWrite, Result: observability.ResultInvalid}:     1,
			{Kind: observability.AuditEvents, Stage: observability.AuditWrite, Result: observability.ResultConflict}:    1,
			{Kind: observability.AuditEvents, Stage: observability.AuditWrite, Result: observability.ResultFailed}:      2,
		}
		if got := auditCounts(t, &observed); !reflect.DeepEqual(got, want) {
			t.Fatalf("counts=%#v, want %#v", got, want)
		}
		if got := a.Stats(); got != (audit.Stats{Written: 2, Duplicate: 1, Invalid: 2, Conflicts: 1, Failed: 2, Dropped: 2}) {
			t.Fatalf("stats=%+v", got)
		}
	})
	t.Run("malformed backend classifications", func(t *testing.T) {
		for _, result := range []audit.BatchResult{{Written: 2}, {Written: -1, Duplicate: 2}, {Failed: 2}} {
			var observed obstest.Recorder
			a := audit.NewAsync(malformedResultInserter{result: result}, audit.AsyncOptions{Observer: &observed, Logger: logger})
			a.Record(ctx, ev(1))
			a.Close()
			want := map[observability.AuditObservation]int64{{Kind: observability.AuditEvents, Stage: observability.AuditWrite, Result: observability.ResultFailed}: 1}
			if got := auditCounts(t, &observed); !reflect.DeepEqual(got, want) {
				t.Fatalf("%+v: got %#v", result, got)
			}
			if got := a.Stats(); got.Failed != 1 || got.Written != 0 || got.Duplicate != 0 {
				t.Fatalf("stats=%+v", got)
			}
		}
	})
	t.Run("external recorder mapping and optional fields", func(t *testing.T) {
		var observed obstest.Recorder
		written := 0
		out := observationOutput(func(context.Context, audit.Event) { written++ })
		r, err := audit.NewAuthenticationRecorder(out, audit.AuthenticationOptions{Source: "test", Namespace: "test", Observer: &observed}, logger)
		if err != nil {
			t.Fatal(err)
		}
		r.Record(ctx, audit.AuthenticationEvent{}) // mapping failure, no writer ownership
		r.Record(ctx, audit.AuthenticationEvent{Type: enum.EventCodeSent, RequestID: strings.Repeat("r", 129), DeviceID: strings.Repeat("d", 257), SubjectHint: strings.Repeat("s", 9), AdminUsername: strings.Repeat("n", 257), IP: "private-invalid-ip"})
		want := map[observability.AuditObservation]int64{
			{Kind: observability.AuditEvents, Stage: observability.AuditMapping, Result: observability.ResultFailed}: 1,
			{Kind: observability.AuditOmittedFields, Field: observability.FieldRequestID}:                            1,
			{Kind: observability.AuditOmittedFields, Field: observability.FieldDeviceID}:                             1,
			{Kind: observability.AuditOmittedFields, Field: observability.FieldSubjectHint}:                          1,
			{Kind: observability.AuditOmittedFields, Field: observability.FieldActorName}:                            1,
			{Kind: observability.AuditOmittedFields, Field: observability.FieldClientIP}:                             1,
		}
		if got := auditCounts(t, &observed); !reflect.DeepEqual(got, want) {
			t.Fatalf("counts=%#v", got)
		}
		if r.Stats() != (authentication.Stats{Failed: 1, Omitted: 5}) {
			t.Fatalf("stats=%+v", r.Stats())
		}
		if written != 1 {
			t.Fatalf("external recorder received %d events, want 1", written)
		}
	})
	t.Run("close timeout does not invent drops", func(t *testing.T) {
		var observed obstest.Recorder
		entered, release := make(chan struct{}), make(chan struct{})
		var enteredOnce, releaseOnce sync.Once
		t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
		a := audit.NewAsync(observationInserter(func(_ context.Context, es []audit.Event) (audit.BatchResult, error) {
			enteredOnce.Do(func() { close(entered) })
			<-release
			return audit.BatchResult{Failed: len(es)}, errors.New("private-error")
		}), audit.AsyncOptions{Observer: &observed, Logger: logger, BatchSize: 1, CloseTimeout: 20 * time.Millisecond})
		a.Start(ctx)
		a.Record(ctx, ev(1))
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatal("writer did not start")
		}
		a.Record(ctx, ev(2))
		a.Close()
		if len(observed.Snapshot()) != 0 || a.Stats() != (audit.Stats{}) {
			t.Fatalf("timeout invented accounting: %+v %#v", a.Stats(), observed.Snapshot())
		}
		releaseOnce.Do(func() { close(release) })
		waitFor(t, func() bool {
			return auditCounts(t, &observed)[observability.AuditObservation{Kind: observability.AuditEvents, Stage: observability.AuditWrite, Result: observability.ResultFailed}] == 2
		})
		if a.Stats() != (audit.Stats{Failed: 2}) {
			t.Fatalf("stats=%+v", a.Stats())
		}
	})
	t.Run("admission callback can close recorder", func(t *testing.T) {
		var a *audit.Async
		var observed obstest.Recorder
		a = audit.NewAsync(&fakeInserter{}, audit.AsyncOptions{Observer: auditObserverFunc(func(ctx context.Context, o observability.Observation) {
			observed.Observe(ctx, o)
			if o.(observability.AuditObservation).Result == observability.ResultDropped {
				a.Close()
			}
		}), Logger: logger, QueueSize: 1})
		a.Record(ctx, ev(1))
		done := make(chan struct{})
		go func() { a.Record(ctx, ev(2)); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("admission callback deadlocked Close")
		}
		if a.Stats() != (audit.Stats{Written: 1, Dropped: 1}) {
			t.Fatalf("stats=%+v", a.Stats())
		}
		if len(observed.Snapshot()) != 2 {
			t.Fatalf("observations=%#v", observed.Snapshot())
		}
	})
	t.Run("concurrent increments match stats", func(t *testing.T) {
		var observed obstest.Recorder
		a := audit.NewAsync(&fakeInserter{}, audit.AsyncOptions{Observer: &observed, Logger: logger, QueueSize: 2, BatchSize: 2})
		a.Start(ctx)
		var wg sync.WaitGroup
		for i := range 100 {
			wg.Go(func() { a.Record(ctx, ev(i)) })
		}
		wg.Wait()
		a.Close()
		counts := auditCounts(t, &observed)
		written := counts[observability.AuditObservation{Kind: observability.AuditEvents, Stage: observability.AuditWrite, Result: observability.ResultWritten}]
		dropped := counts[observability.AuditObservation{Kind: observability.AuditEvents, Stage: observability.AuditAdmission, Result: observability.ResultDropped}]
		if written+dropped != 100 || uint64(written) != a.Stats().Written || uint64(dropped) != a.Stats().Dropped {
			t.Fatalf("written=%d dropped=%d stats=%+v", written, dropped, a.Stats())
		}
	})
	t.Run("observer and diagnostic logger panic stay isolated", func(t *testing.T) {
		var diagnostics atomic.Int64
		a := audit.NewAsync(&fakeInserter{}, audit.AsyncOptions{Observer: auditObserverFunc(func(context.Context, observability.Observation) { panic("private-observer-panic") }), Logger: slog.New(panicDiagnosticHandler{calls: &diagnostics})})
		for range 10 {
			a.Record(ctx, audit.Event{})
		}
		a.Close()
		if a.Stats() != (audit.Stats{Invalid: 10}) || diagnostics.Load() != 1 {
			t.Fatalf("stats=%+v diagnostic attempts=%d", a.Stats(), diagnostics.Load())
		}
	})
}

type panicDiagnosticHandler struct{ calls *atomic.Int64 }

func (h panicDiagnosticHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h panicDiagnosticHandler) Handle(context.Context, slog.Record) error {
	h.calls.Add(1)
	panic("private-logger-panic")
}
func (h panicDiagnosticHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h panicDiagnosticHandler) WithGroup(string) slog.Handler      { return h }
