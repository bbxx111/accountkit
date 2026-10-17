package admin_test

import (
	"context"
	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/httpapi/admin"
	"github.com/bbxx111/accountkit/user"
	"net/http"
	"strings"
	"testing"
)

func TestAuditReaderNilDisablesOnlyAuditRoute(t *testing.T) {
	f := &fakeService{userExists: func(context.Context, string) error { return nil }, adminListSessions: func(context.Context, string) ([]user.SessionInfo, error) { return nil, nil }}
	h, err := admin.New(admin.Deps{Users: f, AuditSource: "test/source", AuditNamespace: "test/ns", Verifier: fakeVerifier{}, Principal: principalFromHeader, ClientIP: func(*http.Request) string { return "127.0.0.1" }, RequestID: func(*http.Request) string { return "test" }})
	if err != nil {
		t.Fatal(err)
	}
	handler := withAdminHeader(h.Router())
	rec := do(t, handler, call{method: "GET", path: "/users/" + uid + "/auditEvents", headers: operator})
	if rec.Code != 503 || !strings.Contains(rec.Body.String(), "AUDIT_QUERY_NOT_CONFIGURED") {
		t.Fatalf("audit query: %d %s", rec.Code, rec.Body)
	}
	rec = do(t, handler, call{method: "GET", path: "/users/" + uid + "/sessions", headers: operator})
	if rec.Code != 200 {
		t.Fatalf("other admin route: %d %s", rec.Code, rec.Body)
	}
}
func TestAuditProjectionDoesNotExposeGenericData(t *testing.T) {
	record := testAuditRecord(t, audit.AuthenticationEvent{Type: enum.EventSignIn, UserID: uid, Result: enum.ResultSuccess})
	record.Data = []byte(`{"reason":"NEW_USER","private_business_data":"never-expose","accountkit":{"actor_kind":"USER"}}`)
	f := &fakeService{userExists: func(context.Context, string) error { return nil }}
	reader := &fakeAudit{listByUser: func(context.Context, string, *audit.EventCursor, int) ([]audit.RecordView, error) {
		return []audit.RecordView{record}, nil
	}}
	rec := do(t, newHandler(t, f, reader), call{method: "GET", path: "/users/" + uid + "/auditEvents", headers: operator})
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "never-expose") || strings.Contains(rec.Body.String(), "private_business_data") {
		t.Fatalf("private projection: %d %s", rec.Code, rec.Body)
	}
}
