package enduser_test

import "testing"

// Unpublished accounts routes must keep the normal route-not-found response rather than
// invoke authentication, redirect, or act as aliases for users routes.
func TestUnpublishedAccountsRoutesAreNotAliases(t *testing.T) {
	h := newHandler(t, &fakeService{})
	for _, route := range []struct{ method, path string }{
		{"POST", "/accounts:sendSignInCode"},
		{"POST", "/accounts:signInWithCode"},
		{"POST", "/accounts:signInWithIdp"},
		{"POST", "/accounts:sendSignInCompletionCode"},
		{"POST", "/accounts:completeSignIn"},
		{"GET", "/accounts/me"},
		{"PATCH", "/accounts/me"},
		{"POST", "/accounts/me:sendReauthenticationCode"},
		{"POST", "/accounts/me:reauthenticate"},
		{"GET", "/accounts/me/sessions"},
		{"DELETE", "/accounts/me/sessions/s_0k3f9c2m1xq71"},
		{"POST", "/accounts/me/sessions:revokeOthers"},
		{"DELETE", "/accounts/me"},
		{"POST", "/accounts/me:undelete"},
		{"POST", "/accounts/me:sendBindCode"},
		{"GET", "/accounts/me/identities"},
		{"POST", "/accounts/me/identities"},
		{"DELETE", "/accounts/me/identities/i_0k3f9c2m1xq71"},
		{"POST", "/accounts/me/identities/i_0k3f9c2m1xq71:replace"},
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
