package auditconfig

import "testing"

func TestForSchemaContract(t *testing.T) {
	for _, tc := range []struct{ schema, source, namespace string }{{"", "accountkit/account", "accountkit/account/resources"}, {"tenant_auth", "accountkit/tenant_auth", "accountkit/tenant_auth/resources"}} {
		got := ForSchema(tc.schema)
		if got.Source != tc.source || got.Namespace != tc.namespace {
			t.Fatalf("ForSchema(%q) = %+v", tc.schema, got)
		}
	}
}
