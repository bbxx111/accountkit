package admin_test

import (
	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/audit/authentication"
	"testing"
)

func testAuditRecord(t *testing.T, in audit.AuthenticationEvent) audit.RecordView {
	t.Helper()
	e, err := authentication.Map(in, audit.AuthenticationOptions{Source: "test/source", Namespace: "test/ns"})
	if err != nil {
		t.Fatal(err)
	}
	return audit.RecordView{Event: e, CreateTime: e.OccurTime}
}
