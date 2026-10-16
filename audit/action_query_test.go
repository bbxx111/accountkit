package audit_test

import (
	"context"
	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/enum"
	"testing"
)

func TestStoreActionFilterBeforeLimit(t *testing.T) {
	s := testStore(t, testPool(t))
	var events []audit.Event
	for i := 0; i < 5; i++ {
		e := mapped(t, audit.AuthenticationEvent{Type: enum.EventSignIn, UserID: "u_0000000000001", OccurTime: ev(i).OccurTime})
		if i%2 == 0 {
			e.Action = "ORDER_APPROVED"
		}
		events = append(events, e)
	}
	if r, err := s.InsertBatch(context.Background(), events); err != nil || r.Written != 5 {
		t.Fatalf("insert: %+v %v", r, err)
	}
	q := audit.Query{Source: "test/source", Resource: audit.Resource{Type: "USER", Namespace: "test/ns", ID: "u_0000000000001"}, Actions: []string{"SIGN_IN", "SIGN_IN"}, Limit: 1}
	for _, index := range []int{1, 3} {
		p, err := s.ListByResource(context.Background(), q)
		if err != nil || len(p.Records) != 1 || p.Records[0].ID != events[index].ID {
			t.Fatalf("filter before limit: %+v %v", p, err)
		}
		q.After = &audit.EventCursor{Time: p.Records[0].OccurTime, ID: p.Records[0].ID}
	}
	p, err := s.ListByResource(context.Background(), q)
	if err != nil || len(p.Records) != 0 {
		t.Fatalf("filtered end: %+v %v", p, err)
	}
	q.After = nil
	q.Actions = []string{"ORDER_APPROVED"}
	p, err = s.ListByResource(context.Background(), q)
	if err != nil || len(p.Records) != 1 || p.Records[0].ID != events[0].ID {
		t.Fatalf("explicit business filter: %+v %v", p, err)
	}
	q.Actions = []string{"INVALID lower"}
	if _, err = s.ListByResource(context.Background(), q); err == nil {
		t.Fatal("invalid action filter accepted")
	}
}

func TestStoreActionFilterPreservesFlowEvidence(t *testing.T) {
	s := testStore(t, testPool(t))
	const flow = "f_0000000000001"
	const uid = "u_0000000000001"
	early := mapped(t, audit.AuthenticationEvent{Type: enum.EventSignInCompletionStarted, FlowID: flow, OccurTime: ev(0).OccurTime})
	proof := mapped(t, audit.AuthenticationEvent{Type: enum.EventSignInCompletionCompleted, FlowID: flow, UserID: uid, Result: enum.ResultSuccess, VerifiedSubject: true, OccurTime: ev(1).OccurTime})
	if r, err := s.InsertBatch(context.Background(), []audit.Event{early, proof}); err != nil || r.Written != 2 {
		t.Fatalf("insert: %+v %v", r, err)
	}
	p, err := s.ListByResource(context.Background(), audit.Query{Source: "test/source", Resource: audit.Resource{Type: "USER", Namespace: "test/ns", ID: uid}, Actions: []string{"SIGN_IN_COMPLETION_STARTED"}, Limit: 1})
	if err != nil || len(p.Records) != 1 || p.Records[0].ID != early.ID {
		t.Fatalf("early event lost: %+v %v", p, err)
	}
	if len(p.FlowLinks) != 1 || p.FlowLinks[0].Evidence.ID != proof.ID || p.FlowLinks[0].Live {
		t.Fatalf("evidence filtered out: %+v", p.FlowLinks)
	}
}
