package user_test

import (
	"testing"

	"github.com/bbxx111/accountkit/user"
)

func TestPurgeCoveredTablesReturnsIndependentSnapshots(t *testing.T) {
	snapshot := user.PurgeCoveredTables
	first := snapshot()
	identityReason := first["identity"]
	if identityReason == "" {
		t.Fatal("identity coverage reason missing")
	}
	delete(first, "identity")
	first["host_table"] = "caller-owned coverage"
	second := snapshot()
	if second["identity"] != identityReason || second["host_table"] != "" {
		t.Fatal("caller mutation changed a later coverage snapshot")
	}
}
