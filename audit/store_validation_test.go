package audit_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/audit/event"
	"github.com/bbxx111/accountkit/enum"
	"github.com/google/uuid"
)

// Direct Store consumers must receive the same validation as queued events,
// before a valid neighbor can be committed.
func TestStoreRejectsInvalidBatch(t *testing.T) {
	pool := testPool(t)
	store := testStore(t, pool)
	ctx := context.Background()
	for _, tt := range []struct {
		name string
		edit func(*audit.Event)
	}{
		{"invalid UUID", func(e *audit.Event) { e.ID = uuid.New() }},
		{"source format", func(e *audit.Event) { e.Source = "/invalid" }},
		{"foreign source", func(e *audit.Event) { e.Source = "foreign/source" }},
		{"action format", func(e *audit.Event) { e.Action = "invalid" }},
		{"result unspecified", func(e *audit.Event) { v := enum.ResultUnspecified; e.Result = &v }},
		{"actor missing identity", func(e *audit.Event) { e.Actor = audit.Actor{Type: audit.ActorUser} }},
		{"actor unknown type", func(e *audit.Event) { e.Actor.Type = "ADMIN" }},
		{"actor anonymous identity", func(e *audit.Event) { e.Actor.ID = "unexpected" }},
		{"invalid client IP", func(e *audit.Event) { e.Context.ClientIP = "forged" }},
		{"span without trace", func(e *audit.Event) { e.Context.SpanID = "0123456789abcdef" }},
		{"request ID too long", func(e *audit.Event) { e.Context.RequestID = strings.Repeat("x", 129) }},
		{"resource missing identity", func(e *audit.Event) { e.Targets = []audit.Resource{{Type: "ORDER"}} }},
		{"too many targets", func(e *audit.Event) {
			e.Targets = make([]audit.Resource, 33)
			for i := range e.Targets {
				e.Targets[i] = audit.Resource{Type: "ORDER", Namespace: "orders", ID: "same"}
			}
		}},
		{"too many related resources", func(e *audit.Event) {
			e.Context.RelatedResources = make([]audit.Resource, 33)
			for i := range e.Context.RelatedResources {
				e.Context.RelatedResources[i] = audit.Resource{Type: "ORDER", Namespace: "orders", ID: "same"}
			}
		}},
		{"zero time", func(e *audit.Event) { e.OccurTime = time.Time{} }},
		{"time outside API range", func(e *audit.Event) { e.OccurTime = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) }},
		{"duplicate data keys", func(e *audit.Event) { e.Data = json.RawMessage(`{"nested":{"key":1,"key":2}}`) }},
		{"oversized event", func(e *audit.Event) { e.Data = json.RawMessage(`{"padding":"` + strings.Repeat("x", 65536) + `"}`) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			bad := genericEvent()
			tt.edit(&bad)
			for _, batch := range [][]audit.Event{{bad}, {genericEvent(), bad, genericEvent()}} {
				result, err := store.InsertBatch(ctx, batch)
				want := audit.BatchResult{Invalid: 1, Failed: len(batch) - 1}
				if !errors.Is(err, audit.ErrInvalid) || result != want {
					t.Fatalf("invalid batch: result=%+v want=%+v err=%v", result, want, err)
				}
				var count int
				if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_event`).Scan(&count); err != nil || count != 0 {
					t.Fatalf("invalid batch partially written: count=%d err=%v", count, err)
				}
			}
		})
	}
}

func TestStoreEventSizeBoundary(t *testing.T) {
	pool := testPool(t)
	privacyUsers(t, pool)
	store := testStore(t, pool)
	ctx := context.Background()
	e := genericEvent()
	e.Data = json.RawMessage(`{"padding":""}`)
	frozen, err := event.Freeze(e)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := json.Marshal(frozen)
	if err != nil {
		t.Fatal(err)
	}
	e.Data = json.RawMessage(`{"padding":"` + strings.Repeat("x", 65536-len(wire)) + `"}`)
	if result, err := store.InsertBatch(ctx, []audit.Event{e}); err != nil || result.Written != 1 {
		t.Fatalf("65536-byte event rejected: result=%+v err=%v", result, err)
	}
	stored := records(t, store)[0].Event
	wire, err = json.Marshal(stored)
	if err != nil || len(wire) != 65536 {
		t.Fatalf("stored event size=%d err=%v", len(wire), err)
	}
	e.ID = uuid.Must(uuid.NewV7())
	e.Data = append(e.Data[:len(e.Data)-2], 'x', '"', '}')
	result, err := store.InsertBatch(ctx, []audit.Event{genericEvent(), e})
	if !errors.Is(err, audit.ErrInvalid) || result != (audit.BatchResult{Invalid: 1, Failed: 1}) {
		t.Fatalf("65537-byte event accepted: result=%+v err=%v", result, err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_event`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("oversized batch partially written: count=%d err=%v", count, err)
	}
}
