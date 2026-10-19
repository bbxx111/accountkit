package admin_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/audit/authentication"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/httpapi/admin"
	"github.com/bbxx111/accountkit/user"
	"github.com/google/uuid"
)

func auditRecordAt(t *testing.T, n int, at time.Time) audit.RecordView {
	t.Helper()
	return testAuditRecord(t, audit.AuthenticationEvent{ID: uuid.MustParse(fmt.Sprintf("01900000-0000-7000-8000-%012d", n)), Type: enum.EventSignIn, UserID: uid, Result: enum.ResultSuccess, OccurTime: at})
}

func auditHandler(t *testing.T, reader audit.Reader, change func(*admin.Deps)) http.Handler {
	t.Helper()
	d := admin.Deps{Users: &fakeService{userExists: func(context.Context, string) error { return nil }}, Audit: reader, AuditSource: "test/source", AuditNamespace: "test/ns", Verifier: fakeVerifier{}, Principal: principalFromHeader, ClientIP: func(*http.Request) string { return "198.51.100.7" }, RequestID: func(*http.Request) string { return "req-audit-validation" }}
	d.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	if change != nil {
		change(&d)
	}
	h, err := admin.New(d)
	if err != nil {
		t.Fatal(err)
	}
	return withAdminHeader(h.Router())
}

func auditPageReader(page audit.Page) *fakeAudit {
	return &fakeAudit{listByResource: func(context.Context, audit.Query) (audit.Page, error) { return page, nil }}
}

func auditToken(t *testing.T, source, namespace, userID string, row audit.RecordView) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"v": 2, "source": source, "resource": audit.Resource{Type: "USER", Namespace: namespace, ID: userID}, "actions": authentication.Actions(), "t": row.OccurTime, "id": row.ID})
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func TestAuditExternalReaderBounds(t *testing.T) {
	for _, name := range []string{"descending_time", "descending_uuid", "duplicate", "duplicate_other_time", "at_after", "before_after", "lookahead_order", "lookahead_scope", "too_many", "zero_create_time", "invalid_create_time", "invalid_event", "unknown_action", "source", "user", "namespace", "resource_type", "session_namespace", "flow_namespace"} {
		t.Run(name, func(t *testing.T) {
			rows := []audit.RecordView{auditRecordAt(t, 1, testNow), auditRecordAt(t, 2, testNow), auditRecordAt(t, 3, testNow)}
			path := "/users/" + uid + "/auditEvents?page_size=2"
			switch name {
			case "descending_time":
				rows[0].OccurTime = testNow.Add(time.Second)
			case "descending_uuid":
				rows[0], rows[1] = rows[1], rows[0]
			case "duplicate":
				rows[1] = rows[0]
			case "duplicate_other_time":
				rows[1] = rows[0]
				rows[1].OccurTime = testNow.Add(time.Second)
				rows[2].OccurTime = testNow.Add(2 * time.Second)
			case "at_after":
				path += "&page_token=" + auditToken(t, "test/source", "test/ns", uid, rows[0])
			case "before_after":
				path += "&page_token=" + auditToken(t, "test/source", "test/ns", uid, rows[1])
			case "lookahead_order":
				rows[2].OccurTime = testNow.Add(-time.Second)
			case "lookahead_scope":
				rows[2].Source = "other/source"
			case "too_many":
				rows = append(rows, auditRecordAt(t, 4, testNow))
			case "zero_create_time":
				rows[0].CreateTime = time.Time{}
			case "invalid_create_time":
				rows[0].CreateTime = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
			case "invalid_event":
				rows[0].ID = uuid.Nil
			case "unknown_action":
				rows[0].Action = "ORDER_APPROVED"
			case "source":
				rows[0].Source = "other/source"
			case "user":
				rows[0] = testAuditRecord(t, audit.AuthenticationEvent{Type: enum.EventSignIn, UserID: "u_0000000000002", OccurTime: testNow})
			case "namespace", "resource_type":
				rows[0].Actor = audit.Actor{Type: audit.ActorUnknown}
				for i := range rows[0].Context.RelatedResources {
					if name == "namespace" {
						rows[0].Context.RelatedResources[i].Namespace = "other/ns"
					} else {
						rows[0].Context.RelatedResources[i].Type = "ORDER"
					}
				}
			case "session_namespace", "flow_namespace":
				rows[0] = testAuditRecord(t, audit.AuthenticationEvent{ID: rows[0].ID, Type: enum.EventSignIn, UserID: uid, SessionID: sid, FlowID: "f_0000000000001", OccurTime: testNow})
				for i := range rows[0].Targets {
					if rows[0].Targets[i].Type == "SESSION" && name == "session_namespace" {
						rows[0].Targets[i].Namespace = "other/ns"
					}
				}
				for i := range rows[0].Context.RelatedResources {
					if rows[0].Context.RelatedResources[i].Type == "IDENTITY_FLOW" && name == "flow_namespace" {
						rows[0].Context.RelatedResources[i].Namespace = "other/ns"
					}
				}
			}
			rec := do(t, auditHandler(t, auditPageReader(audit.Page{Records: rows}), nil), call{method: "GET", path: path, headers: operator})
			if rec.Code != 503 {
				t.Fatalf("invalid page accepted: %d %s", rec.Code, rec.Body.String())
			}
			status, _ := aipError(t, rec)
			if status != "UNAVAILABLE/AUDIT_QUERY_UNAVAILABLE" || strings.Contains(rec.Body.String(), "audit_events") || strings.Contains(rec.Body.String(), "next_page_token") {
				t.Fatalf("unsafe page response: %s", rec.Body.String())
			}
		})
	}
}

func TestAuditLegacyCursorRejected(t *testing.T) {
	b, _ := json.Marshal(map[string]any{"t": testNow, "id": "e_0000000000001"})
	rec := do(t, auditHandler(t, nil, nil), call{method: "GET", path: "/users/" + uid + "/auditEvents?page_token=" + base64.RawURLEncoding.EncodeToString(b), headers: operator})
	status, _ := aipError(t, rec)
	if rec.Code != 400 || status != "INVALID_ARGUMENT/INVALID_PAGE_TOKEN" || !strings.Contains(rec.Body.String(), "restart") {
		t.Fatalf("legacy cursor: %d %s", rec.Code, rec.Body.String())
	}
}

func TestAuditCursorScope(t *testing.T) {
	for _, scope := range []struct{ name, source, namespace, id string }{
		{"source", "other/source", "test/ns", uid}, {"namespace", "test/source", "other/ns", uid}, {"user", "test/source", "test/ns", "u_0000000000002"},
	} {
		t.Run(scope.name, func(t *testing.T) {
			token := auditToken(t, scope.source, scope.namespace, scope.id, auditRecordAt(t, 1, testNow))
			rec := do(t, auditHandler(t, nil, nil), call{method: "GET", path: "/users/" + uid + "/auditEvents?page_token=" + token, headers: operator})
			status, _ := aipError(t, rec)
			if rec.Code != 400 || status != "INVALID_ARGUMENT/INVALID_PAGE_TOKEN" {
				t.Fatalf("scope: %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestAuditSameTimePagination(t *testing.T) {
	rows := []audit.RecordView{auditRecordAt(t, 1, testNow), auditRecordAt(t, 2, testNow), auditRecordAt(t, 3, testNow)}
	reader := &fakeAudit{listByResource: func(_ context.Context, q audit.Query) (audit.Page, error) {
		if q.Source != "test/source" || q.Resource != (audit.Resource{Type: "USER", Namespace: "test/ns", ID: uid}) || q.Limit != 3 {
			t.Fatalf("query scope: %+v", q)
		}
		if q.After == nil {
			return audit.Page{Records: rows}, nil
		}
		if q.After.ID != rows[1].ID || !q.After.Time.Equal(testNow) {
			t.Fatalf("cursor: %+v", q.After)
		}
		return audit.Page{Records: rows[2:]}, nil
	}}
	h := auditHandler(t, reader, nil)
	var out struct {
		Events []map[string]any `json:"audit_events"`
		Next   string           `json:"next_page_token"`
	}
	rec := do(t, h, call{method: "GET", path: "/users/" + uid + "/auditEvents?page_size=2", headers: operator})
	decode(t, rec, &out)
	if rec.Code != 200 || len(out.Events) != 2 || out.Next == "" {
		t.Fatalf("first page: %s", rec.Body.String())
	}
	for i := range out.Events {
		if out.Events[i]["name"] != "users/"+uid+"/auditEvents/"+rows[i].ID.String() {
			t.Fatal(out.Events)
		}
	}
	rec = do(t, h, call{method: "GET", path: "/users/" + uid + "/auditEvents?page_size=2&page_token=" + out.Next, headers: operator})
	decode(t, rec, &out)
	if rec.Code != 200 || len(out.Events) != 1 || out.Next != "" || out.Events[0]["name"] != "users/"+uid+"/auditEvents/"+rows[2].ID.String() {
		t.Fatalf("last page: %s", rec.Body.String())
	}
}

func TestAuditNullableResult(t *testing.T) {
	for _, result := range []enum.Result{enum.ResultUnspecified, enum.ResultSuccess, enum.ResultFailure} {
		t.Run(result.String(), func(t *testing.T) {
			row := testAuditRecord(t, audit.AuthenticationEvent{Type: enum.EventUserPurged, Actor: enum.ActorSystem, UserID: uid, Result: result, OccurTime: testNow})
			rec := do(t, auditHandler(t, auditPageReader(audit.Page{Records: []audit.RecordView{row}}), nil), call{method: "GET", path: "/users/" + uid + "/auditEvents", headers: operator})
			var out struct {
				Events []map[string]any `json:"audit_events"`
			}
			decode(t, rec, &out)
			if rec.Code != 200 || len(out.Events) != 1 {
				t.Fatalf("result page: %s", rec.Body.String())
			}
			var want any
			if result.Valid() {
				want = result.String()
			}
			_, resultPresent := out.Events[0]["result"]
			if !resultPresent || out.Events[0]["result"] != want || out.Events[0]["actor_kind"] != "SYSTEM" {
				t.Fatalf("nullable result: %v", out.Events[0])
			}
		})
	}
}

func TestAuditFlowLinkValidation(t *testing.T) {
	for _, name := range []string{"evidence", "evidence_after_event", "live", "name_is_not_identity", "missing", "other_user_event", "wrong_flow_namespace", "wrong_user_namespace", "wrong_user_type", "evidence_source", "evidence_failure", "evidence_action", "evidence_user", "evidence_flow", "evidence_projection_namespace", "evidence_zero_create", "evidence_invalid_create", "live_no_callback", "live_false", "live_error", "live_evidence_id", "live_evidence_source", "live_evidence_time", "live_evidence_empty_data"} {
		t.Run(name, func(t *testing.T) {
			flow := "f_0000000000001"
			early := testAuditRecord(t, audit.AuthenticationEvent{Type: enum.EventSignInCompletionStarted, FlowID: flow, OccurTime: testNow})
			proof := testAuditRecord(t, audit.AuthenticationEvent{Type: enum.EventSignInCompletionProofVerified, UserID: uid, FlowID: flow, VerifiedSubject: true, Result: enum.ResultSuccess, OccurTime: testNow})
			link := audit.FlowLink{Flow: audit.Resource{Type: "IDENTITY_FLOW", Namespace: "test/ns", ID: flow}, User: audit.Resource{Type: "USER", Namespace: "test/ns", ID: uid}, Evidence: proof}
			live := strings.HasPrefix(name, "live")
			if live {
				link.Live = true
				link.Evidence = audit.RecordView{}
			}
			ownerCalls := 0
			owner := func(_ context.Context, u, f string) (bool, error) {
				ownerCalls++
				if u != uid || f != flow {
					t.Fatalf("owner scope: %s %s", u, f)
				}
				if name == "live_error" {
					return false, errors.New("synthetic-private-error")
				}
				return name != "live_false", nil
			}
			switch name {
			case "evidence_after_event":
				link.Evidence.OccurTime = testNow.Add(time.Hour)
				link.Evidence.CreateTime = testNow.Add(2 * time.Hour)
			case "name_is_not_identity":
				link.User.Name = "Display user"
				link.Flow.Name = "Display flow"
			case "other_user_event":
				early = testAuditRecord(t, audit.AuthenticationEvent{Type: enum.EventSignInCompletionProofVerified, UserID: "u_0000000000002", FlowID: flow, VerifiedSubject: true, Result: enum.ResultSuccess, OccurTime: testNow})
			case "wrong_flow_namespace":
				link.Flow.Namespace = "other/ns"
			case "wrong_user_namespace":
				link.User.Namespace = "other/ns"
			case "wrong_user_type":
				link.User.Type = "ORDER"
			case "evidence_source":
				link.Evidence.Source = "other/source"
			case "evidence_failure":
				v := enum.ResultFailure
				link.Evidence.Result = &v
			case "evidence_action":
				link.Evidence.Action = "SIGN_IN"
			case "evidence_user":
				link.Evidence.Actor.ID = "u_0000000000002"
				link.Evidence.Context.RelatedResources = nil
			case "evidence_flow":
				link.Evidence.Targets[0].ID = "f_0000000000002"
			case "evidence_projection_namespace":
				link.Evidence = testAuditRecord(t, audit.AuthenticationEvent{Type: enum.EventSignInCompletionProofVerified, UserID: uid, SessionID: sid, FlowID: flow, VerifiedSubject: true, Result: enum.ResultSuccess, OccurTime: testNow})
				for i := range link.Evidence.Context.RelatedResources {
					if link.Evidence.Context.RelatedResources[i].Type == "SESSION" {
						link.Evidence.Context.RelatedResources[i].Namespace = "other/ns"
					}
				}
			case "evidence_zero_create":
				link.Evidence.CreateTime = time.Time{}
			case "evidence_invalid_create":
				link.Evidence.CreateTime = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
			case "live_evidence_id":
				link.Evidence.ID = proof.ID
			case "live_evidence_source":
				link.Evidence.Source = "synthetic-private-payload"
			case "live_evidence_time":
				link.Evidence.CreateTime = testNow
			case "live_evidence_empty_data":
				link.Evidence.Data = json.RawMessage(`{}`)
			}
			page := audit.Page{Records: []audit.RecordView{early}, FlowLinks: []audit.FlowLink{link}}
			if name == "missing" {
				page.FlowLinks = nil
			}
			h := auditHandler(t, auditPageReader(page), func(d *admin.Deps) {
				if name != "live_no_callback" {
					d.FlowOwner = owner
				}
			})
			rec := do(t, h, call{method: "GET", path: "/users/" + uid + "/auditEvents", headers: operator})
			want := 503
			if name == "evidence" || name == "evidence_after_event" || name == "live" || name == "name_is_not_identity" {
				want = 200
			}
			if rec.Code != want {
				t.Fatalf("flow validation want %d got %d: %s", want, rec.Code, rec.Body.String())
			}
			if name == "live" && ownerCalls != 1 {
				t.Fatalf("trusted owner calls %d", ownerCalls)
			}
			if strings.Contains(rec.Body.String(), proof.ID.String()) || strings.Contains(rec.Body.String(), "Evidence") || strings.Contains(rec.Body.String(), "synthetic-private") {
				t.Fatalf("evidence leaked: %s", rec.Body.String())
			}
		})
	}
}

func TestAuditQueryUnavailable(t *testing.T) {
	for _, name := range []string{"backend", "scope"} {
		t.Run(name, func(t *testing.T) {
			var logs bytes.Buffer
			row := auditRecordAt(t, 1, testNow)
			row.Source = "other/source"
			row.Data = json.RawMessage(`{"secret":"synthetic-private-body"}`)
			reader := &fakeAudit{listByResource: func(context.Context, audit.Query) (audit.Page, error) {
				if name == "backend" {
					return audit.Page{}, errors.New("synthetic-private-error")
				}
				return audit.Page{Records: []audit.RecordView{row}}, nil
			}}
			h := auditHandler(t, reader, func(d *admin.Deps) { d.Logger = slog.New(slog.NewJSONHandler(&logs, nil)) })
			rec := do(t, h, call{method: "GET", path: "/users/" + uid + "/auditEvents", headers: operator})
			if rec.Code != 503 {
				t.Fatalf("status %d", rec.Code)
			}
			if logs.Len() == 0 || !strings.Contains(logs.String(), "reason") || !strings.Contains(logs.String(), "req-audit-validation") {
				t.Fatalf("missing controlled observation: %s", logs.String())
			}
			for _, sensitive := range []string{"synthetic-private", row.ID.String(), "other/source"} {
				if strings.Contains(logs.String()+rec.Body.String(), sensitive) {
					t.Fatalf("unsafe fault observation: %s %s", logs.String(), rec.Body.String())
				}
			}
		})
	}
}

func TestAuditAuthorizationAndDependencyPrecedence(t *testing.T) {
	for _, name := range []string{"writer_only", "unknown_writer_only", "unknown_backend", "role", "super_without_operator", "unconfigured"} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			existsCalls := 0
			var reader audit.Reader
			if name != "writer_only" && name != "unknown_writer_only" {
				reader = &fakeAudit{listByResource: func(context.Context, audit.Query) (audit.Page, error) {
					calls++
					return audit.Page{}, errors.New("private")
				}}
			}
			h := auditHandler(t, reader, func(d *admin.Deps) {
				d.Users = &fakeService{userExists: func(context.Context, string) error {
					existsCalls++
					if strings.HasPrefix(name, "unknown") {
						return user.ErrNotFound
					}
					return nil
				}}
			})
			headers := operator
			want := 503
			reason := "AUDIT_QUERY_NOT_CONFIGURED"
			if strings.HasPrefix(name, "unknown") {
				want = 404
				reason = "NOT_FOUND"
			}
			if name == "role" || name == "super_without_operator" {
				want = 403
				reason = "ROLE_REQUIRED"
				headers = nil
				if name == "super_without_operator" {
					headers = map[string]string{"X-Test-Roles": "super-admin", "X-Test-Admin": adminHdr}
				}
			}
			if name == "unconfigured" {
				h = admin.Unconfigured(func(*http.Request) string { return "req" })
				reason = "ADMIN_NOT_CONFIGURED"
			}
			rec := do(t, h, call{method: "GET", path: "/users/" + uid + "/auditEvents", headers: headers})
			if rec.Code != want || !strings.Contains(rec.Body.String(), reason) || calls != 0 {
				t.Fatalf("precedence: %d calls %d %s", rec.Code, calls, rec.Body.String())
			}
			if (want == 403 || name == "unconfigured") && existsCalls != 0 {
				t.Fatal("unauthorized existence probe")
			}
		})
	}
}
