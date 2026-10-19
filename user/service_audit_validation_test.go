package user_test

import (
	"context"
	"errors"
	"testing"

	"github.com/bbxx111/accountkit/user"
)

func TestAuditFlowBelongsToUserValidatesIDsBeforeIO(t *testing.T) {
	for _, tc := range []struct{ user, flow string }{{"", "f_0000000000001"}, {"u_0000000000001", ""}, {"s_0000000000001", "f_0000000000001"}, {"u_0000000000001", "u_0000000000001"}} {
		t.Run(tc.user+"/"+tc.flow, func(t *testing.T) {
			// No repository is available: invalid caller input must not perform I/O.
			defer func() {
				if v := recover(); v != nil {
					t.Errorf("invalid IDs reached repository: %v", v)
				}
			}()
			ok, err := new(user.Service).AuditFlowBelongsToUser(context.Background(), tc.user, tc.flow)
			if ok || !errors.Is(err, user.ErrInvalidArgument) {
				t.Fatalf("invalid IDs: %v %v", ok, err)
			}
		})
	}
}
