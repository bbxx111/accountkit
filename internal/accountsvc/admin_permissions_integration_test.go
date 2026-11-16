package accountsvc_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/bbxx111/accountkit/enum"
)

func TestServiceIntegrationAdminPermissions(t *testing.T) {
	f := newIntegration(t)
	f.oidc()
	p := f.start(nil)
	login := f.login(p, "permissions@example.test")
	uid := login["user_id"].(string)
	var identity, sid string
	if err := f.pool.QueryRow(context.Background(), "SELECT id FROM identity WHERE user_id=$1", uid).Scan(&identity); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(context.Background(), "SELECT id FROM session WHERE user_id=$1", uid).Scan(&sid); err != nil {
		t.Fatal(err)
	}
	base := "/admin/v1/users/" + uid
	cases := []struct {
		method, path string
		body         any
		want         int
	}{
		{"GET", "/admin/v1/users", nil, 200}, {"GET", base, nil, 200},
		{"GET", base + "/identities/" + identity + ":reveal", nil, 200},
		{"GET", base + "/sessions", nil, 200}, {"GET", base + "/auditEvents", nil, 200},
		{"DELETE", base + "/sessions/" + sid, nil, 204},
		{"POST", base + "/sessions:revokeAll", nil, 200},
		{"POST", base + ":freeze", map[string]string{"reason": "permissions test"}, 200},
		{"POST", base + ":unfreeze", map[string]string{"reason": "permissions test"}, 200},
		{"DELETE", base, nil, 200}, {"POST", base + ":undelete", nil, 200},
	}
	for i, tc := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			f.call(tc.method, p.baseURL+tc.path, f.env["FIXTURE_unmapped"], tc.body, 403)
			f.call(tc.method, p.baseURL+tc.path, f.env["FIXTURE_super-admin"], tc.body, tc.want)
		})
	}
	p.stop()
	var denied int
	if err := f.pool.QueryRow(context.Background(), "SELECT count(*) FROM audit_event WHERE action=$1", enum.EventAdminForbidden.String()).Scan(&denied); err != nil {
		t.Fatal(err)
	}
	if denied != len(cases) {
		t.Fatalf("denial audit count=%d want %d", denied, len(cases))
	}
	f.assertAuditPrivate()
}

func TestServiceIntegrationAdminPolicyRestart(t *testing.T) {
	f := newIntegration(t)
	f.oidc()
	p := f.start(nil)
	login := f.login(p, "policy-restart@example.test")
	uid := login["user_id"].(string)
	token := f.env["FIXTURE_operator"]
	path := "/admin/v1/users/" + uid + ":freeze"
	f.call("POST", p.baseURL+path, token, map[string]string{"reason": "before policy restart"}, 200)
	f.env["ACCOUNTSVC_ADMIN_ROLE_OPERATOR_PERMISSIONS"] = "users.read"
	// Editing source configuration does not change the running snapshot.
	f.call("POST", p.baseURL+"/admin/v1/users/"+uid+":unfreeze", token, map[string]string{"reason": "old snapshot"}, 200)
	p.stop()
	p = f.start(nil)
	f.call("POST", p.baseURL+path, token, map[string]string{"reason": "after policy restart"}, 403)
	f.call("GET", p.baseURL+"/admin/v1/users/"+uid, token, nil, 200)
	p.stop()
	for _, name := range []string{"ACCOUNTSVC_ADMIN_ROLE_BINDINGS", "ACCOUNTSVC_ADMIN_ROLE_OPERATOR_NAME", "ACCOUNTSVC_ADMIN_ROLE_OPERATOR_PERMISSIONS", "ACCOUNTSVC_ADMIN_ROLE_SUPER_ADMIN_NAME", "ACCOUNTSVC_ADMIN_ROLE_SUPER_ADMIN_PERMISSIONS"} {
		delete(f.env, name)
	}
	p = f.start(nil)
	for _, role := range []string{"operator", "super-admin"} {
		f.call("GET", p.baseURL+"/admin/v1/users", f.env["FIXTURE_"+role], nil, 403)
	}
	f.call("GET", p.baseURL+"/admin/v1/users", "", nil, http.StatusUnauthorized)
	f.call("GET", p.baseURL+"/v1/users/me", f.env["FIXTURE_super-admin"], nil, http.StatusUnauthorized)
	p.stop()
}
