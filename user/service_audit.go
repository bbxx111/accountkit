package user

import (
	"context"

	"github.com/bbxx111/accountkit/ids"
	"github.com/bbxx111/accountkit/user/db"
)

// AuditFlowBelongsToUser checks local flow ownership for the trusted admin adapter.
func (s *Service) AuditFlowBelongsToUser(ctx context.Context, userID, flowID string) (bool, error) {
	if !ids.Valid(ids.User, userID) || !ids.Valid(ids.IdentityFlow, flowID) {
		return false, ErrInvalidArgument
	}
	return s.d.Repo.q.AuditFlowBelongsToUser(ctx, db.AuditFlowBelongsToUserParams{UserID: userID, FlowID: flowID})
}
