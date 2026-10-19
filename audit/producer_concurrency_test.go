package audit_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/google/uuid"
)

// A generator shared by authentication producers must remain unique without
// replacing the separately supplied occurrence time with its UUID timestamp.
func TestAuthenticationRecorderConcurrentUUIDsAndOccurrenceTime(t *testing.T) {
	mem := &audit.Memory{}
	r, err := audit.NewAuthenticationRecorder(mem, audit.AuthenticationOptions{Source: "test/source", Namespace: "test/ns"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	past := time.Date(2001, 2, 3, 4, 5, 6, 123456789, time.FixedZone("fixture", 8*60*60))
	future := time.Date(2099, 2, 3, 4, 5, 6, 987654321, time.UTC)
	var wg sync.WaitGroup
	const workers, perWorker = 16, 64
	for worker := range workers {
		wg.Go(func() {
			occurred := past
			if worker%2 == 1 {
				occurred = future
			}
			for range perWorker {
				r.Record(context.Background(), audit.AuthenticationEvent{Type: enum.EventCodeSent, Result: enum.ResultSuccess, OccurTime: occurred})
			}
		})
	}
	wg.Wait()
	events := mem.Events()
	if len(events) != workers*perWorker || r.Stats().Failed != 0 {
		t.Fatalf("concurrent producer lost events: count=%d stats=%+v", len(events), r.Stats())
	}
	seen := make(map[uuid.UUID]bool, len(events))
	counts := map[int]int{}
	for _, e := range events {
		if e.ID.Version() != 7 || e.ID.Variant() != uuid.RFC4122 || seen[e.ID] {
			t.Fatalf("invalid or repeated generated UUID: %s", e.ID)
		}
		seen[e.ID] = true
		var uuidMilliseconds int64
		for _, b := range e.ID[:6] {
			uuidMilliseconds = uuidMilliseconds<<8 | int64(b)
		}
		if e.OccurTime.UnixMilli() == uuidMilliseconds {
			t.Fatal("occurrence time must remain independent of UUID generation time")
		}
		if !e.OccurTime.Equal(past.UTC().Truncate(time.Microsecond)) && !e.OccurTime.Equal(future.Truncate(time.Microsecond)) {
			t.Fatalf("occurrence time replaced by generation/ingestion time: %s", e.OccurTime)
		}
		counts[e.OccurTime.Year()]++
	}
	if counts[2001] != workers*perWorker/2 || counts[2099] != workers*perWorker/2 {
		t.Fatalf("occurrence time boundary counts: %v", counts)
	}
	// Feed an already generated identity through the actual producer again.
	original := events[0]
	r.Record(context.Background(), audit.AuthenticationEvent{ID: original.ID, Type: enum.EventCodeSent, Result: enum.ResultSuccess, OccurTime: original.OccurTime})
	retry := mem.Events()[len(events)]
	if retry.ID != original.ID || !retry.OccurTime.Equal(original.OccurTime) {
		t.Fatal("redelivery regenerated event identity or occurrence time")
	}
}
