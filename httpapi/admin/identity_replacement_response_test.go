package admin_test

import (
	"context"
	"testing"

	"github.com/bbxx111/accountkit/audit"
	auditdb "github.com/bbxx111/accountkit/audit/db"
	"github.com/bbxx111/accountkit/enum"
)

// The existing audit resource must preserve the appended rejection event and
// replacement reason without adding fields to the active-session resource.
func TestIdentityReplacementAuditResponse(t *testing.T) {
	eventType, err := enum.ParseEventType("IDENTITY_REPLACE_REJECTED")
	if err != nil {
		t.Fatal(err)
	}
	replaced, unchanged, request := "IDENTITY_REPLACED", "IDENTITY_UNCHANGED", "req-replacement"
	kind := enum.IdentityEmail
	rows := []auditdb.AuditEvent{
		{ID: "e_0k3f9c2m1xq71", EventType: enum.EventIdentityUnbound, ActorKind: enum.ActorUser, UserID: &uid, SessionID: &sid, IdentityKind: &kind, Result: enum.ResultSuccess, Reason: &replaced, RequestID: &request, OccurTime: testNow},
		{ID: "e_0k3f9c2m1xq72", EventType: enum.EventIdentityBound, ActorKind: enum.ActorUser, UserID: &uid, SessionID: &sid, IdentityKind: &kind, Result: enum.ResultSuccess, Reason: &replaced, RequestID: &request, OccurTime: testNow},
		{ID: "e_0k3f9c2m1xq73", EventType: enum.EventSessionRevoked, ActorKind: enum.ActorUser, UserID: &uid, SessionID: &sid, Result: enum.ResultSuccess, Reason: &replaced, RequestID: &request, OccurTime: testNow},
		{ID: "e_0k3f9c2m1xq74", EventType: eventType, ActorKind: enum.ActorUser, UserID: &uid, SessionID: &sid, IdentityKind: &kind, Result: enum.ResultFailure, Reason: &unchanged, RequestID: &request, OccurTime: testNow},
	}
	f := &fakeService{userExists: func(context.Context, string) error { return nil }}
	fa := &fakeAudit{listByUser: func(context.Context, string, *audit.EventCursor, int) ([]auditdb.AuditEvent, error) { return rows, nil }}
	rec := do(t, newHandler(t, f, fa), call{method: "GET", path: "/users/" + uid + "/auditEvents", headers: operator})
	var out struct {
		AuditEvents []map[string]any `json:"audit_events"`
	}
	decode(t, rec, &out)
	if rec.Code != 200 || len(out.AuditEvents) != 4 {
		t.Fatalf("replacement audit response: %d %s", rec.Code, rec.Body.String())
	}
	for index, want := range []struct{ event, reason, result string }{
		{"IDENTITY_UNBOUND", "IDENTITY_REPLACED", "SUCCESS"},
		{"IDENTITY_BOUND", "IDENTITY_REPLACED", "SUCCESS"},
		{"SESSION_REVOKED", "IDENTITY_REPLACED", "SUCCESS"},
		{"IDENTITY_REPLACE_REJECTED", "IDENTITY_UNCHANGED", "FAILURE"},
	} {
		event := out.AuditEvents[index]
		assertKeys(t, event, "name", "event_type", "actor_kind", "result", "reason", "session_id", "identity_kind", "subject_hint", "ip", "device_id", "request_id", "admin_issuer", "admin_subject", "admin_username", "occur_time")
		if event["event_type"] != want.event || event["reason"] != want.reason || event["result"] != want.result || event["request_id"] != "req-replacement" || event["session_id"] != "s_0k3f9c2m1xq7z" {
			t.Fatalf("event %d: %v", index, event)
		}
	}
}
