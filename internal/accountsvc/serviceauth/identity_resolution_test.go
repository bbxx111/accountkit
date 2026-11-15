package serviceauth_test

import (
	"github.com/bbxx111/accountkit/internal/accountsvc/serviceauth"
	"testing"
)

func TestIdentityResolvePermissionCatalog(t *testing.T) {
	for _, permissions := range [][]string{{"identities.resolve"}, {"tokens.introspect", "purged_users.read", "identities.resolve"}} {
		r, err := serviceauth.NewRegistry([]serviceauth.ClientConfig{{ID: "resolver", AuthMethods: []string{"bearer"}, BearerSubjects: []string{"machine"}, Permissions: permissions}})
		if err != nil || !r.HasPermission("identities.resolve") {
			t.Fatalf("resolution permission rejected: %v", err)
		}
	}
	for _, permission := range []string{"identity.resolve", "*", "unknown", ""} {
		if _, err := serviceauth.NewRegistry([]serviceauth.ClientConfig{{ID: "resolver", AuthMethods: []string{"bearer"}, BearerSubjects: []string{"machine"}, Permissions: []string{permission}}}); err == nil {
			t.Fatalf("accepted %q", permission)
		}
	}
}
