package user

import (
	"context"
	"fmt"
	"time"

	"github.com/bbxx111/accountkit/internal/userdb"
)

// ErrPurgedUserCursorAhead 表示游标超过了已分配的最大序号：数据库被恢复到较早备份或重新部署后，
// 序号回退到业务方保存的游标之下。拉取方应丢弃游标从头重拉（清理按 user_id 幂等）。它同时满足
// errors.Is(err, ErrInvalidArgument)。
var ErrPurgedUserCursorAhead = fmt.Errorf("%w: purged user cursor is ahead of the feed; restart from the beginning", ErrInvalidArgument)

// MaxPurgedUserPage 是 ListPurgedUsers 单页的最大条数。
const MaxPurgedUserPage = 1000

// MinPurgedUserRetention 是已清理记录可配置保留期的下限（30 天），给外部业务服务留出故障补拉窗口。
const MinPurgedUserRetention = 720 * time.Hour

const purgedUserCleanupBatch = 1000

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
	q := s.d.Repo.queries()
	if after > 0 {
		last, err := q.GetPurgedUserSeqLastValue(ctx)
		if err != nil {
			return nil, after, fmt.Errorf("user: read purged user sequence: %w", err)
		}
		if after > last {
			return nil, after, ErrPurgedUserCursorAhead
		}
	}
	rows, err := q.ListPurgedUsersAfter(ctx, db.ListPurgedUsersAfterParams{After: after, PageSize: int32(limit)})
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

// CleanupPurgedUsers 删除写入时间早于 now − PurgedUserRetention 的已清理记录，分批删到一批不满为止；
// 未配置保留期（0）时不删除。维护任务 cleanup_purged_users 每轮调用一次。
func (s *Service) CleanupPurgedUsers(ctx context.Context) (total int64, err error) {
	if s.d.PurgedUserRetention <= 0 {
		return 0, nil
	}
	before := s.now().Add(-s.d.PurgedUserRetention)
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, err := s.d.Repo.queries().DeletePurgedUsersBefore(ctx, db.DeletePurgedUsersBeforeParams{Before: before, BatchSize: purgedUserCleanupBatch})
		if err != nil {
			return total, fmt.Errorf("user: delete expired purged users: %w", err)
		}
		total += n
		if n < purgedUserCleanupBatch {
			return total, nil
		}
	}
}
