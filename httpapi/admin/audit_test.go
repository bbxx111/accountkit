package admin_test

import (
	"context"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
)

func TestListAuditEvents(t *testing.T) {
	var got struct {
		uid   string
		after *audit.EventCursor
		limit int
	}
	ip := "203.0.113.5"
	reason, session, hint, dev, req := "NEW_USER", sid, "abcdefgh", "dev-1", "req-1"
	flow := "f_0000000000001"
	kind := enum.IdentityPhone
	rows := []audit.RecordView{
		testAuditRecord(t, audit.AuthenticationEvent{Type: enum.EventSignIn, Actor: enum.ActorUser, UserID: uid, SessionID: session, FlowID: flow, IdentityKind: kind, SubjectHint: hint, Result: enum.ResultSuccess, Reason: reason, IP: ip, DeviceID: dev, RequestID: req, OccurTime: testNow}),
		testAuditRecord(t, audit.AuthenticationEvent{Type: enum.EventUserFrozen, Actor: enum.ActorAdmin, AdminIssuer: wantAdmin.Issuer, AdminSubject: wantAdmin.Subject, UserID: uid, Result: enum.ResultSuccess, OccurTime: testNow.Add(time.Second)}),
		testAuditRecord(t, audit.AuthenticationEvent{Type: enum.EventUserUnfrozen, Actor: enum.ActorAdmin, AdminIssuer: wantAdmin.Issuer, AdminSubject: wantAdmin.Subject, UserID: uid, Result: enum.ResultSuccess, OccurTime: testNow.Add(2 * time.Second)}),
	}
	fa := &fakeAudit{listByUser: func(_ context.Context, id string, after *audit.EventCursor, limit int) ([]audit.RecordView, error) {
		got.uid, got.after, got.limit = id, after, limit
		remaining := rows
		if after != nil {
			for i, e := range rows {
				if e.ID == after.ID {
					remaining = rows[i+1:]
					break
				}
			}
		}
		if limit-1 < len(remaining) {
			return remaining[:limit], nil // limit = page_size + 1
		}
		return remaining, nil
	}}
	f := &fakeService{userExists: func(_ context.Context, id string) error {
		if id != uid {
			return user.ErrNotFound
		}
		return nil
	}}
	h := newHandler(t, f, fa)

	rec := do(t, h, call{method: "GET", path: "/users/" + uid + "/auditEvents?page_size=2", headers: operator})
	var out map[string]any
	decode(t, rec, &out)
	if rec.Code != 200 || got.uid != uid || got.limit != 3 || got.after != nil {
		t.Fatalf("list: %d got=%+v", rec.Code, got)
	}
	evs := out["audit_events"].([]any)
	if len(evs) != 2 || out["next_page_token"] == "" {
		t.Fatalf("page: %v", out)
	}
	first := evs[0].(map[string]any)
	assertKeys(t, first, "name", "event_type", "actor_kind", "result", "reason", "session_id", "flow_id", "identity_kind", "subject_hint", "ip", "device_id", "request_id", "admin_issuer", "admin_subject", "admin_username", "occur_time")
	if first["flow_id"] != flow {
		t.Fatal("missing flow correlation")
	}
	if first["name"] != "users/"+uid+"/auditEvents/"+rows[0].ID.String() || first["event_type"] != "SIGN_IN" || first["actor_kind"] != "USER" || first["result"] != "SUCCESS" || first["identity_kind"] != "PHONE" || first["ip"] != "203.0.113.5" || first["reason"] != "NEW_USER" {
		t.Fatalf("event 1: %v", first)
	}
	second := evs[1].(map[string]any)
	if _, ok := second["flow_id"]; ok {
		t.Fatal("absent flow_id must remain optional")
	}
	if second["actor_kind"] != "ADMIN" || second["reason"] != nil || second["identity_kind"] != nil || second["ip"] != nil || second["session_id"] != nil {
		t.Fatalf("nullable fields must be null: %v", second)
	}
	// 第二页：token → after 游标（上一页最后一条的 occur_time, id）
	rec = do(t, h, call{method: "GET", path: "/users/" + uid + "/auditEvents?page_size=2&page_token=" + out["next_page_token"].(string), headers: operator})
	if rec.Code != 200 || got.after == nil || got.after.ID != rows[1].ID || !got.after.Time.Equal(testNow.Add(time.Second)) {
		t.Fatalf("page 2 cursor: %d %+v", rec.Code, got.after)
	}
	decode(t, rec, &out)
	if out["next_page_token"] != "" {
		t.Fatalf("last page must have empty token: %v", out)
	}
	// 未知用户 404；非法 id 400；坏 token 400
	if rec = do(t, h, call{method: "GET", path: "/users/u_0000000000000/auditEvents", headers: operator}); rec.Code != 404 {
		t.Fatalf("unknown user: %d", rec.Code)
	}
	if rec = do(t, h, call{method: "GET", path: "/users/nope/auditEvents", headers: operator}); rec.Code != 400 {
		t.Fatalf("bad id: %d", rec.Code)
	}
	rec = do(t, h, call{method: "GET", path: "/users/" + uid + "/auditEvents?page_token=zzz", headers: operator})
	if status, _ := aipError(t, rec); rec.Code != 400 || status != "INVALID_ARGUMENT/INVALID_PAGE_TOKEN" {
		t.Fatalf("bad token: %d %s", rec.Code, status)
	}
}
