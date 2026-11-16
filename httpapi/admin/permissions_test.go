package admin_test

import (
	"context"
	"github.com/bbxx111/accountkit/httpapi/admin"
	"net/http"
	"net/http/httptest"
	"testing"
)

type permissionGate struct {
	t   *testing.T
	got admin.Permission
}

func (g *permissionGate) RequireRole(string) func(http.Handler) http.Handler {
	g.t.Fatal("legacy role called")
	return nil
}
func (g *permissionGate) RequirePermission(p admin.Permission) func(http.Handler) http.Handler {
	return func(http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { g.got = p; w.WriteHeader(403) })
	}
}
func TestPermissionsGateEveryRoute(t *testing.T) {
	g := &permissionGate{t: t}
	h, err := admin.New(admin.Deps{Users: &fakeService{}, Verifier: g, Principal: func(context.Context) (admin.Principal, bool) { return admin.Principal{}, true }, ClientIP: func(*http.Request) string { return "" }, RequestID: func(*http.Request) string { return "" }, AuditSource: "test/source", AuditNamespace: "test/ns"})
	if err != nil {
		t.Fatal(err)
	}
	routes := []struct {
		method, path string
		p            admin.Permission
	}{
		{"GET", "/users", admin.PermissionUsersRead}, {"GET", "/users/bad", admin.PermissionUsersRead}, {"POST", "/users/bad:freeze", admin.PermissionUsersFreeze}, {"POST", "/users/bad:unfreeze", admin.PermissionUsersUnfreeze}, {"DELETE", "/users/bad", admin.PermissionUsersDelete}, {"POST", "/users/bad:undelete", admin.PermissionUsersUndelete}, {"GET", "/users/bad/identities/bad:reveal", admin.PermissionIdentitiesReveal}, {"GET", "/users/bad/sessions", admin.PermissionSessionsRead}, {"DELETE", "/users/bad/sessions/bad", admin.PermissionSessionsRevoke}, {"POST", "/users/bad/sessions:revokeAll", admin.PermissionSessionsRevokeAll}, {"GET", "/users/bad/auditEvents", admin.PermissionAuditEventsRead}}
	router := h.Router()
	for _, rt := range routes {
		g.got = ""
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(rt.method, rt.path, nil))
		if rt.p == admin.PermissionIdentitiesReveal && w.Header().Get("Cache-Control") != "no-store" {
			t.Error("authorization denial on reveal must be no-store")
		}
		if w.Code != 403 || g.got != rt.p {
			t.Errorf("%s %s: %d %s", rt.method, rt.path, w.Code, g.got)
		}
	}
	catalog := admin.Permissions()
	if len(catalog) != 10 {
		t.Fatal(catalog)
	}
	catalog[0] = "changed"
	if admin.Permissions()[0] == "changed" {
		t.Fatal("mutable catalog")
	}
}
func TestUnconfiguredRevealNoStore(t *testing.T) {
	w := httptest.NewRecorder()
	admin.Unconfigured(func(*http.Request) string { return "request" }).ServeHTTP(w, httptest.NewRequest("GET", "/users/bad/identities/bad:reveal", nil))
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unconfigured reveal: status=%d cache=%q", w.Code, w.Header().Get("Cache-Control"))
	}
}
