package accountsvc

import (
	"testing"

	"github.com/bbxx111/accountkit"
)

func TestIdentityAuditNamespacePreservesEmptySchema(t *testing.T) {
	for _, tc := range []struct{ schema, namespace, want string }{
		{"", "", "accountkit//resources"},
		{"tenant", "", "accountkit/tenant/resources"},
		{"tenant", "external/resources", "external/resources"},
	} {
		if got := identityAuditNamespace(accountkit.Config{Schema: tc.schema, AuditNamespace: tc.namespace}); got != tc.want {
			t.Errorf("schema=%q namespace=%q: got %q want %q", tc.schema, tc.namespace, got, tc.want)
		}
	}
}
