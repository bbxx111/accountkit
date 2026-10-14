package enduser_test

import "testing"

// Removed users routes must keep the normal route-not-found response rather than
// invoke authentication, redirect, or act as aliases for accounts routes.
func TestLegacyUsersRoutesAreNotAliases(t *testing.T) {
	h := newHandler(t, &fakeService{})
	for _, route := range []struct{ method, path string }{
		{"POST", "/users:sendSignInCode"},
		{"POST", "/users:signInWithCode"},
		{"POST", "/users:signInWithIdp"},
		{"GET", "/users/me"},
		{"PATCH", "/users/me"},
		{"POST", "/users/me:sendReauthenticationCode"},
		{"POST", "/users/me:reauthenticate"},
		{"GET", "/users/me/sessions"},
		{"DELETE", "/users/me/sessions/s_0k3f9c2m1xq71"},
		{"POST", "/users/me/sessions:revokeOthers"},
		{"DELETE", "/users/me"},
		{"POST", "/users/me:undelete"},
		{"POST", "/users/me:sendBindCode"},
		{"GET", "/users/me/identities"},
		{"POST", "/users/me/identities"},
		{"DELETE", "/users/me/identities/i_0k3f9c2m1xq71"},
		{"POST", "/users/me/identities/i_0k3f9c2m1xq71:replace"},
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
