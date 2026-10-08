package user

import (
	"context"
	"fmt"
	"slices"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/ids"
	"github.com/bbxx111/accountkit/user/db"
	"github.com/jackc/pgx/v5"
)

// BeforeDelete 在账号锁和 ACTIVE 状态校验后、注销修改前执行宿主检查。
// 宿主可返回 ErrDeletionBlocked 拒绝注销；业务表必须同库，且不得自行提交或回滚 tx。
type BeforeDelete func(ctx context.Context, tx pgx.Tx, userID string) error

// WithActiveUsers 在同一事务中锁定并确认全部账号 ACTIVE 后执行宿主回调。
// userIDs 必须非空、fn 必须非 nil。账号去重并按 ID 排序锁定；宿主须先取得这些账号锁，
// 再锁业务资源。库负责提交和回滚，fn 不得自行提交或回滚事务。panic 回滚后向上传递。
func (s *Service) WithActiveUsers(ctx context.Context, userIDs []string, fn func(pgx.Tx) error) error {
	if len(userIDs) == 0 || fn == nil {
		return fmt.Errorf("%w: user IDs and callback are required", ErrInvalidArgument)
	}
	unique := make([]string, 0, len(userIDs))
	seen := make(map[string]struct{}, len(userIDs))
	for _, id := range userIDs {
		if !ids.Valid(ids.User, id) {
			return fmt.Errorf("%w: invalid user ID", ErrInvalidArgument)
		}
		if _, ok := seen[id]; !ok {
			seen[id] = struct{}{}
			unique = append(unique, id)
		}
	}
	slices.Sort(unique)
	return s.d.Repo.WithTxRaw(ctx, func(tx pgx.Tx, q *db.Queries) error {
		for _, id := range unique {
			u, err := lockUserAs(ctx, q, id, ErrNotFound)
			if err != nil {
				return err
			}
			if err := requireState(u, enum.UserActive); err != nil {
				return err
			}
		}
		return fn(tx)
	})
}
