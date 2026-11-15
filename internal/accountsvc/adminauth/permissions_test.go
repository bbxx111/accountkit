package adminauth

import (
	"context"
	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/httpapi/admin"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPolicyCompilation(t *testing.T) {
	p, err := compilePermissions([]RoleBinding{{Name: "support", Permissions: []string{"users.*", "users.read", "identities.*"}}}, admin.Permissions())
	if err != nil {
		t.Fatal(err)
	}
	if !p["support"][admin.PermissionUsersFreeze] || p["support"][admin.PermissionSessionsRead] || !p["support"][admin.PermissionIdentitiesReveal] {
		t.Fatalf("bad expansion: %v", p)
	}
	for _, value := range []string{"*", "*.read", "users.fr*", "users.**", "unknown.*", "identities.resolve", "", " users.read"} {
		if _, err := compilePermissions([]RoleBinding{{Name: "r", Permissions: []string{value}}}, admin.Permissions()); err == nil {
			t.Errorf("accepted %q", value)
		}
	}
	for _, name := range []string{"", " role", "role ", "a\nb", string([]byte{255})} {
		if _, err := compilePermissions([]RoleBinding{{Name: name, Permissions: []string{"users.read"}}}, admin.Permissions()); err == nil {
			t.Errorf("accepted role %q", name)
		}
	}
	if _, err := compilePermissions([]RoleBinding{{Name: "r"}}, admin.Permissions()); err == nil {
		t.Error("accepted empty permissions")
	}
	if _, err := compilePermissions([]RoleBinding{{Name: "r", Permissions: []string{"users.read"}}, {Name: "r", Permissions: []string{"users.read"}}}, admin.Permissions()); err == nil {
		t.Error("accepted duplicate role")
	}
	future := append(admin.Permissions(), admin.Permission("users.future"))
	p, err = compilePermissions([]RoleBinding{{Name: "wild", Permissions: []string{"users.*"}}, {Name: "exact", Permissions: []string{"users.read"}}}, future)
	if err != nil || !p["wild"]["users.future"] || p["exact"]["users.future"] {
		t.Fatal("future expansion", p, err)
	}
}
func TestPermissionAuthenticationAndSnapshot(t *testing.T) {
	f := newFixture(t)
	bindings := []RoleBinding{{Name: "operator", Permissions: []string{"users.read"}}, {Name: "extra, role", Permissions: []string{"sessions.*"}}}
	cfg := f.config()
	cfg.RoleBindings = bindings
	denied := 0
	v, err := New(context.Background(), cfg, Options{HTTPClient: f.server.Client(), OnForbidden: func(*http.Request, accountkit.AdminPrincipal) { denied++ }})
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	bindings[0].Name = "changed"
	bindings[0].Permissions[0] = "users.delete"
	claims := f.claims(time.Now())
	claims["roles"] = []string{"operator", "extra, role", "unknown"}
	claims["scope"] = "* users.delete"
	token := f.token(claims, "initial")
	check := func(permission admin.Permission, raw string, want int) {
		t.Helper()
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/", nil)
		if raw != "" {
			r.Header.Set("Authorization", "Bearer "+raw)
		}
		v.Middleware(v.RequirePermission(permission)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))).ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s: %d want %d", permission, w.Code, want)
		}
	}
	check(admin.PermissionUsersRead, token, 204)
	check(admin.PermissionSessionsRevokeAll, token, 204)
	check(admin.PermissionUsersDelete, token, 403)
	check(admin.PermissionUsersRead, "", 401)
	if denied != 1 {
		t.Fatal("denial audit count", denied)
	}
	empty, err := New(context.Background(), f.config(), Options{HTTPClient: f.server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	defer empty.Close()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	empty.Middleware(empty.RequirePermission(admin.PermissionUsersRead)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("empty policy allowed") }))).ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
	w = httptest.NewRecorder()
	v.Middleware(empty.RequirePermission(admin.PermissionUsersRead)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("foreign context allowed") }))).ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
}
