package user

import (
	"errors"
	"github.com/bbxx111/accountkit/enum"
	"github.com/jackc/pgx/v5/pgconn"
	"testing"
)

func TestStorageSQLStateClassificationContract(t *testing.T) {
	for _, classify := range []struct {
		name string
		call func(error) error
	}{{"flow", flowStorageError}, {"session context", sessionContextStorageError}} {
		for _, code := range []string{"08000", "08ZZZ", "53000", "53ZZZ", "57P01", "57P02", "57P03", "55P03", "25P04", "57014", "23505", "22000"} {
			input := &pgconn.PgError{Code: code}
			got := classify.call(input)
			want := code[:2] == "08" || code[:2] == "53" || code == "57P01" || code == "57P02" || code == "57P03"
			if errors.Is(got, ErrUnavailable) != want {
				t.Errorf("%s %s unavailable=%v want %v", classify.name, code, errors.Is(got, ErrUnavailable), want)
			}
			if !errors.Is(got, input) {
				t.Errorf("%s lost original cause", classify.name)
			}
		}
	}
}
func TestFlowAADPersistedContract(t *testing.T) {
	s := &Service{flowContext: "instance-context"}
	if got := string(s.flowAAD("f_test")); got != `["accountkit-sign-in-completion-v1","instance-context","f_test"]` {
		t.Fatal(got)
	}
}
func TestAuditUserStateReasonContract(t *testing.T) {
	for _, state := range []enum.UserState{enum.UserActive, enum.UserFrozen, enum.UserPendingDeletion, enum.UserDeleted, 0, 99, -1} {
		want := "USER_" + state.String()
		if got := auditUserStateReason(state); got != want {
			t.Fatalf("state %d: %q != %q", state, got, want)
		}
	}
}
