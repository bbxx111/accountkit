package user

import (
	"context"
	"fmt"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/ids"
)

// PublicProfile 是宿主批量读取的最小公开账号资料，不含身份或联系方式。
type PublicProfile struct {
	ID          string
	DisplayName string
	State       enum.UserState
}

// BatchPublicProfiles 一次查询去重后的账号资料；未知账号略去，注销账号清空显示名。
// 宿主应在授权的业务范围内收集账号 ID，再调用此方法。
func (s *Service) BatchPublicProfiles(ctx context.Context, userIDs []string) (map[string]PublicProfile, error) {
	profiles := make(map[string]PublicProfile, len(userIDs))
	if len(userIDs) == 0 {
		return profiles, nil
	}
	uniqueIDs := make([]string, 0, len(userIDs))
	seen := make(map[string]struct{}, len(userIDs))
	for _, id := range userIDs {
		if !ids.Valid(ids.User, id) {
			return nil, fmt.Errorf("%w: invalid user ID", ErrInvalidArgument)
		}
		if _, exists := seen[id]; !exists {
			seen[id] = struct{}{}
			uniqueIDs = append(uniqueIDs, id)
		}
	}
	rows, err := s.d.Repo.Q().BatchPublicProfiles(ctx, uniqueIDs)
	if err != nil {
		return nil, fmt.Errorf("user: batch public profiles: %w", err)
	}
	for _, row := range rows {
		profile := PublicProfile{ID: row.ID, State: row.State}
		if row.State != enum.UserPendingDeletion && row.State != enum.UserDeleted && row.DisplayName != nil {
			profile.DisplayName = *row.DisplayName
		}
		profiles[row.ID] = profile
	}
	return profiles, nil
}
