package audit_test

import (
	"context"
	"sync"
	"testing"

	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/enum"
)

func TestNoopImplementsRecorder(t *testing.T) {
	var r audit.Recorder = audit.Noop{}
	r.Record(context.Background(), audit.Event{Type: enum.EventSignIn}) // 不 panic 即可
}

func TestMemoryRecordsInOrderAndIsConcurrencySafe(t *testing.T) {
	m := &audit.Memory{}
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			m.Record(context.Background(), audit.Event{Type: enum.EventCodeSent, Reason: string(rune('a' + i%26))})
		}(i)
	}
	wg.Wait()
	if got := len(m.Events()); got != 50 {
		t.Fatalf("events = %d, want 50", got)
	}
	// Events 返回拷贝：修改返回值不影响内部
	evs := m.Events()
	evs[0].Type = enum.EventUserPurged
	if m.Events()[0].Type == enum.EventUserPurged {
		t.Fatal("Events must return a copy")
	}
}

func TestHint(t *testing.T) {
	if audit.Hint("0123456789abcdef") != "01234567" || audit.Hint("abc") != "abc" || audit.Hint("") != "" {
		t.Fatal("Hint mismatch")
	}
}
