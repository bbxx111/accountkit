package admin_test

import "testing"

// Unpublished accounts routes must keep the normal route-not-found response rather than
// invoke authentication, redirect, or act as aliases for users routes.
func TestUnpublishedAccountsRoutesAreNotAliases(t *testing.T) {
	h := newHandler(t, &fakeService{}, nil)
	for _, route := range []struct{ method, path string }{
		{"GET", "/accounts"},
		{"GET", "/accounts/u_0k3f9c2m1xq7z"},
		{"DELETE", "/accounts/u_0k3f9c2m1xq7z"},
		{"POST", "/accounts/u_0k3f9c2m1xq7z:freeze"},
		{"POST", "/accounts/u_0k3f9c2m1xq7z:unfreeze"},
		{"POST", "/accounts/u_0k3f9c2m1xq7z:undelete"},
		{"GET", "/accounts/u_0k3f9c2m1xq7z/identities/i_0k3f9c2m1xq71:reveal"},
		{"GET", "/accounts/u_0k3f9c2m1xq7z/sessions"},
		{"DELETE", "/accounts/u_0k3f9c2m1xq7z/sessions/s_0k3f9c2m1xq71"},
		{"POST", "/accounts/u_0k3f9c2m1xq7z/sessions:revokeAll"},
		{"GET", "/accounts/u_0k3f9c2m1xq7z/auditEvents"},
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
