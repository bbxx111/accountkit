package user

import (
	"context"
	"fmt"
	"time"

	"github.com/bbxx111/accountkit/user/db"
)

// MaxPurgedUserPage 是 ListPurgedUsers 单页的最大条数。
const MaxPurgedUserPage = 1000

// PurgedUser 是一条已清理用户记录：账号已匿名化，外部业务服务据此按 UserID 幂等清理自身数据。
type PurgedUser struct {
	UserID    string
	PurgeTime time.Time
}

// ListPurgedUsers 返回游标 after 之后按写入顺序排列的至多 limit 条已清理记录，以及新的游标。
// 从头读取时 after 传 0；没有新记录时返回空切片且游标等于 after。记录在清理事务内写入并按提交顺序
// 分配序号，因此保存游标、持续拉取不会漏读；接口没有确认语义，重复读取同一位置返回相同记录
// （已被保留期清理的除外）。
func (s *Service) ListPurgedUsers(ctx context.Context, after int64, limit int) ([]PurgedUser, int64, error) {
	if after < 0 || limit < 1 || limit > MaxPurgedUserPage {
		return nil, after, fmt.Errorf("%w: cursor must be non-negative and limit within 1..%d", ErrInvalidArgument, MaxPurgedUserPage)
	}
	rows, err := s.d.Repo.Q().ListPurgedUsersAfter(ctx, db.ListPurgedUsersAfterParams{After: after, PageSize: int32(limit)})
	if err != nil {
		return nil, after, fmt.Errorf("user: list purged users: %w", err)
	}
	out := make([]PurgedUser, 0, len(rows))
	next := after
	for _, r := range rows {
		out = append(out, PurgedUser{UserID: r.UserID, PurgeTime: r.PurgeTime})
		next = r.Seq
	}
	return out, next, nil
}
