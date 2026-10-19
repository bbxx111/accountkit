package audit_test

import (
	"bytes"
	"context"
	"errors"
	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/audit/event"
	"github.com/bbxx111/accountkit/enum"
	"github.com/google/uuid"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func genericEvent() event.Event {
	r := enum.ResultSuccess
	return event.Event{ID: uuid.Must(uuid.NewV7()), Source: "test/source", Action: "TEST_ACTION", Result: &r, Actor: event.Actor{Type: event.ActorAnonymous}, Targets: []event.Resource{{Type: "USER", Namespace: "test/ns", ID: "u_0000000000001", Name: "original"}}, Data: []byte(`{"value":"original"}`), OccurTime: time.Now()}
}
func TestMemoryFreezesInputAndOutput(t *testing.T) {
	e := genericEvent()
	m := &audit.Memory{}
	m.Record(context.Background(), e)
	e.Targets[0].Name = "changed"
	e.Data[10] = 'X'
	*e.Result = enum.ResultFailure
	got := m.Events()
	if len(got) != 1 || got[0].Targets[0].Name != "original" || *got[0].Result != enum.ResultSuccess || !bytes.Contains(got[0].Data, []byte("original")) {
		t.Fatalf("input was not frozen: %+v", got)
	}
	got[0].Targets[0].Name = "output"
	got[0].Data[10] = 'Y'
	*got[0].Result = enum.ResultFailure
	if fresh := m.Events()[0]; fresh.Targets[0].Name != "original" || *fresh.Result != enum.ResultSuccess || !bytes.Contains(fresh.Data, []byte("original")) {
		t.Fatal("output aliases retained state")
	}
}

type classifiedInserter struct{ events []event.Event }

func (s *classifiedInserter) InsertBatch(_ context.Context, es []event.Event) (event.BatchResult, error) {
	s.events = append(s.events, es...)
	return event.BatchResult{Duplicate: 1, Conflicts: 1, Invalid: 1, Failed: 1, Written: 1}, errors.New("credential-private-host-error")
}
func TestAsyncFreezesQueueAndAccountsClassifications(t *testing.T) {
	var logs bytes.Buffer
	s := &classifiedInserter{}
	a := audit.NewAsync(s, audit.AsyncOptions{BatchSize: 5, Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	for range 5 {
		e := genericEvent()
		a.Record(context.Background(), e)
		e.Targets[0].Name = "changed"
		*e.Result = enum.ResultFailure
		e.Data[10] = 'X'
	}
	a.Record(context.Background(), event.Event{})
	a.Close()
	if len(s.events) != 5 || s.events[0].Targets[0].Name != "original" || *s.events[0].Result != enum.ResultSuccess || !bytes.Contains(s.events[0].Data, []byte("original")) {
		t.Fatal("queue not frozen")
	}
	st := a.Stats()
	if st.Written != 1 || st.Duplicate != 1 || st.Conflicts != 1 || st.Invalid != 2 || st.Failed != 1 {
		t.Fatalf("stats=%+v", st)
	}
	if strings.Contains(logs.String(), "credential-private") {
		t.Fatal("host error leaked")
	}
}
