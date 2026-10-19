package observability_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/bbxx111/accountkit/internal/obstest"
	"github.com/bbxx111/accountkit/observability"
)

type observerFunc func(context.Context, observability.Observation)

func (f observerFunc) Observe(ctx context.Context, o observability.Observation) { f(ctx, o) }

type nilObserver struct{}

func (*nilObserver) Observe(context.Context, observability.Observation) { panic("typed nil called") }

type nilFunc func()

func (f nilFunc) Observe(context.Context, observability.Observation) { f() }

type nilMap map[string]int

func (nilMap) Observe(context.Context, observability.Observation) { panic("nil map called") }

type nilSlice []int

func (nilSlice) Observe(context.Context, observability.Observation) { panic("nil slice called") }

type nilChannel chan int

func (nilChannel) Observe(context.Context, observability.Observation) { panic("nil channel called") }

func TestObserverContract(t *testing.T) {
	var ptr *nilObserver
	var fn nilFunc
	for _, observer := range []observability.Observer{nil, ptr, fn, nilMap(nil), nilSlice(nil), nilChannel(nil), (*observability.Recorder)(nil)} {
		r := observability.NewRecorder(observer, nil)
		if r.Enabled() {
			t.Fatal("nil observer enabled")
		}
		r.Observe(context.Background(), observability.AuthObservation{})
	}
	var unset *observability.Recorder
	if unset.Enabled() {
		t.Fatal("nil recorder enabled")
	}
	unset.Observe(context.Background(), nil)
	var sink obstest.Recorder
	mutable := observability.AuthObservation{Operation: observability.AuthSignIn}
	sink.Observe(context.Background(), &mutable)
	if len(sink.Snapshot()) != 0 {
		t.Fatal("test recorder retained mutable pointer")
	}
	r := observability.NewRecorder(&sink, nil)
	if !r.Enabled() || observability.NewRecorder(r, nil) != r {
		t.Fatal("recorder not reused")
	}
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			for range 100 {
				r.Observe(context.Background(), observability.AuthObservation{Operation: observability.AuthSignIn})
			}
		})
	}
	wg.Wait()
	first := sink.Snapshot()
	if len(first) != 1600 {
		t.Fatalf("lost concurrent observations: %d", len(first))
	}
	first[0] = nil
	if sink.Snapshot()[0] == nil {
		t.Fatal("snapshot aliases storage")
	}
	sink.Reset()
	if len(sink.Snapshot()) != 0 {
		t.Fatal("reset did not clear observations")
	}
	if allocs := testing.AllocsPerRun(1000, func() { unset.Observe(context.Background(), observability.AuthObservation{}) }); allocs != 0 {
		t.Fatalf("disabled allocations: %v", allocs)
	}
}

type panicHandler struct{}

func (panicHandler) Enabled(context.Context, slog.Level) bool  { return true }
func (panicHandler) Handle(context.Context, slog.Record) error { panic("logger-secret") }
func (h panicHandler) WithAttrs([]slog.Attr) slog.Handler      { return h }
func (h panicHandler) WithGroup(string) slog.Handler           { return h }

func TestRecorderFaultIsolation(t *testing.T) {
	var logs bytes.Buffer
	var calls atomic.Int64
	fault := observerFunc(func(context.Context, observability.Observation) { calls.Add(1); panic("synthetic-secret") })
	r := observability.NewRecorder(fault, slog.New(slog.NewTextHandler(&logs, nil)))
	for range 100 {
		r.Observe(context.Background(), observability.DegradationObservation{})
	}
	if calls.Load() != 100 {
		t.Fatal("diagnostic called observer recursively or disabled it")
	}
	if strings.Contains(logs.String(), "secret") || strings.Count(logs.String(), "\n") != 1 {
		t.Fatalf("unsafe or unbounded diagnostic: %s", logs.String())
	}
	observability.NewRecorder(fault, slog.New(panicHandler{})).Observe(context.Background(), observability.AuthObservation{})
	func() {
		defer func() {
			if recover() != "business-panic" {
				t.Fatal("business panic swallowed or changed")
			}
		}()
		defer r.Observe(context.Background(), observability.AuthObservation{})
		panic("business-panic")
	}()
}

func BenchmarkRecorderDisabled(b *testing.B) {
	r := observability.NewRecorder(nil, nil)
	b.ReportAllocs()
	for b.Loop() {
		r.Observe(context.Background(), observability.AuthObservation{})
	}
}
