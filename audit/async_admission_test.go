package audit

import (
	"context"
	"io"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type admissionSink struct{}

func (admissionSink) InsertBatch(_ context.Context, es []Event) (BatchResult, error) {
	return BatchResult{Written: len(es)}, nil
}

func admissionEvent() Event {
	return Event{ID: uuid.Must(uuid.NewV7()), Source: "admission", Action: "PROBE", Actor: Actor{Type: ActorAnonymous}, OccurTime: time.Now(), Data: []byte(`{"items":[` + strings.TrimSuffix(strings.Repeat(`{"n":123,"s":"value"},`, 2500), ",") + `]}`)}
}

func accounted(s Stats) uint64 {
	return s.Written + s.Duplicate + s.Failed + s.Invalid + s.Conflicts + s.Dropped
}

// Sampling the real Freeze stack gives a preemption point without production hooks
// or blocking arbitrary user code inside serialization. An unobserved run is not proof.
func TestAsyncAdmissionClosesDuringFreeze(t *testing.T) {
	old := runtime.GOMAXPROCS(2)
	defer runtime.GOMAXPROCS(old)
	for _, mode := range []string{"unstarted_close", "started_close", "worker_cancel"} {
		t.Run(mode, func(t *testing.T) {
			observed := false
			for attempt := 0; attempt < 40 && !observed; attempt++ {
				a := NewAsync(admissionSink{}, AsyncOptions{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
				ctx, cancel := context.WithCancel(context.Background())
				if mode != "unstarted_close" {
					a.Start(ctx)
				}
				e := admissionEvent()
				done := make(chan struct{})
				go func() { a.Record(context.Background(), e); close(done) }()
				buf := make([]byte, 1<<20)
				deadline := time.Now().Add(2 * time.Second)
			poll:
				for time.Now().Before(deadline) {
					n := runtime.Stack(buf, true)
					if strings.Contains(string(buf[:n]), "accountkit/audit/event.Freeze") {
						observed = true
						break
					}
					select {
					case <-done:
						break poll
					default:
					}
					runtime.Gosched()
				}
				if mode == "worker_cancel" {
					cancel()
					select {
					case <-a.done:
					case <-time.After(2 * time.Second):
						t.Fatal("worker did not exit")
					}
				} else {
					a.Close()
					cancel()
				}
				select {
				case <-done:
				case <-time.After(2 * time.Second):
					t.Fatal("Record did not return")
				}
				a.Close()
				if got := a.Stats(); accounted(got) != 1 || len(a.ch) != 0 {
					t.Fatalf("freeze_observed=%v unaccounted record: stats=%+v queued=%d", observed, got, len(a.ch))
				}
				a.Start(context.Background())
				select {
				case <-a.done:
				case <-time.After(2 * time.Second):
					t.Fatal("Start after Close leaked worker")
				}
			}
			if !observed {
				t.Fatal("did not observe in-flight Freeze; interleaving not verified")
			}
		})
	}
}

func TestAsyncConcurrentAdmissionAccounting(t *testing.T) {
	for _, started := range []bool{false, true} {
		a := NewAsync(admissionSink{}, AsyncOptions{QueueSize: 2, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
		ctx, cancel := context.WithCancel(context.Background())
		if started {
			a.Start(ctx)
		}
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < 100; i++ {
			wg.Go(func() { e := admissionEvent(); <-start; a.Record(ctx, e) })
		}
		close(start)
		a.Close()
		cancel()
		wg.Wait()
		if got := a.Stats(); accounted(got) != 100 || len(a.ch) != 0 {
			t.Fatalf("started=%v stats=%+v queued=%d", started, got, len(a.ch))
		}
	}
}
