package audit_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/audit"
)

type stalledInserter struct {
	entered chan struct{}
	release chan struct{}
}

type synchronizedLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *synchronizedLog) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}
func (s *synchronizedLog) String() string { s.mu.Lock(); defer s.mu.Unlock(); return s.buf.String() }
func (s *stalledInserter) InsertBatch(_ context.Context, es []audit.Event) (audit.BatchResult, error) {
	close(s.entered)
	<-s.release
	return audit.BatchResult{Failed: len(es)}, errors.New("access_token=private-backend-credential")
}

func TestAsyncCloseTimeoutIsBoundedAndSafe(t *testing.T) {
	var logs synchronizedLog
	s := &stalledInserter{entered: make(chan struct{}), release: make(chan struct{})}
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(s.release) }) })
	a := audit.NewAsync(s, audit.AsyncOptions{BatchSize: 1, CloseTimeout: 20 * time.Millisecond, Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	a.Start(context.Background())
	a.Record(context.Background(), ev(1))
	select {
	case <-s.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not reach backend")
	}
	start := time.Now()
	a.Close() // A host backend that ignores cancellation cannot stall Auth forever.
	if time.Since(start) > time.Second {
		t.Fatal("close was not bounded")
	}
	a.Record(context.Background(), ev(2))
	if !strings.Contains(logs.String(), "close timed out") || a.Stats().Dropped != 1 {
		t.Fatalf("missing timeout/drop observation: stats=%+v logs=%s", a.Stats(), logs.String())
	}
	release.Do(func() { close(s.release) })
	waitFor(t, func() bool { return a.Stats().Failed == 1 && strings.Contains(logs.String(), "failed_total") })
	if strings.Contains(logs.String(), "private-backend-credential") || strings.Contains(logs.String(), "203.0.113.77") {
		t.Fatalf("timeout/failure log leaked event or backend secret: %s", logs.String())
	}
}

type malformedResultInserter struct{ result audit.BatchResult }

func (s malformedResultInserter) InsertBatch(context.Context, []audit.Event) (audit.BatchResult, error) {
	return s.result, errors.New("refresh_token=private-backend-credential")
}

func TestAsyncRejectsMalformedBackendAccounting(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result audit.BatchResult
	}{
		{"missing", audit.BatchResult{}},
		{"too_many", audit.BatchResult{Written: 3}},
		{"negative_balanced", audit.BatchResult{Written: 3, Failed: -1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			a := audit.NewAsync(malformedResultInserter{tc.result}, audit.AsyncOptions{Logger: slog.New(slog.NewTextHandler(&logs, nil))})
			a.Record(context.Background(), ev(1))
			a.Record(context.Background(), ev(2))
			a.Close()
			if got := a.Stats(); got != (audit.Stats{Failed: 2}) {
				t.Fatalf("untrusted backend classifications counted as success: %+v", got)
			}
			if !strings.Contains(logs.String(), "failed_total=2") || strings.Contains(logs.String(), "private-backend-credential") {
				t.Fatalf("unsafe or missing failure observation: %s", logs.String())
			}
		})
	}
}
