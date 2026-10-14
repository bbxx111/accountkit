package admin_test

import "testing"

// Removed users routes must keep the normal route-not-found response rather than
// invoke authentication, redirect, or act as aliases for accounts routes.
func TestLegacyUsersRoutesAreNotAliases(t *testing.T) {
	h := newHandler(t, &fakeService{}, nil)
	for _, route := range []struct{ method, path string }{
		{"GET", "/users"},
		{"GET", "/users/u_0k3f9c2m1xq7z"},
		{"DELETE", "/users/u_0k3f9c2m1xq7z"},
		{"POST", "/users/u_0k3f9c2m1xq7z:freeze"},
		{"POST", "/users/u_0k3f9c2m1xq7z:unfreeze"},
		{"POST", "/users/u_0k3f9c2m1xq7z:undelete"},
		{"GET", "/users/u_0k3f9c2m1xq7z/identities/i_0k3f9c2m1xq71:reveal"},
		{"GET", "/users/u_0k3f9c2m1xq7z/sessions"},
		{"DELETE", "/users/u_0k3f9c2m1xq7z/sessions/s_0k3f9c2m1xq71"},
		{"POST", "/users/u_0k3f9c2m1xq7z/sessions:revokeAll"},
		{"GET", "/users/u_0k3f9c2m1xq7z/auditEvents"},
	} {
		t.Run(route.method+route.path, func(t *testing.T) {
			rec := do(t, h, call{method: route.method, path: route.path})
			if rec.Code != 404 || rec.Header().Get("Location") != "" {
				t.Fatalf("removed route: %d %s", rec.Code, rec.Body.String())
			}
			if reason, _ := aipError(t, rec); reason != "NOT_FOUND/ROUTE_NOT_FOUND" {
				t.Fatalf("removed route error: %s", rec.Body.String())
			}
		})
	}
}
