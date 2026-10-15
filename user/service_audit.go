package user

import (
	"context"
	"github.com/bbxx111/accountkit/user/db"
)

// AuditFlowBelongsToUser checks local flow ownership for the trusted admin adapter.
func (s *Service) AuditFlowBelongsToUser(ctx context.Context, userID, flowID string) (bool, error) {
	return s.d.Repo.q.AuditFlowBelongsToUser(ctx, db.AuditFlowBelongsToUserParams{UserID: userID, FlowID: flowID})
}
