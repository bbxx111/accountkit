package user

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user/db"
)

// PurgeBatchSize 是 purge 任务每次取批的账号数；批内每个账号独立事务。
const PurgeBatchSize = 100

// PurgeCoveredTables 登记库内每张含 user_id 列的表在 purge 中的处理方式或豁免理由（§3.5 覆盖性测试的依据）。
// 新增含 user_id 的表必须在此登记并在 purgeOne 中处理（或写明豁免理由），否则 TestPurgeCoversEveryTableWithUserID
// 会失败。宿主业务表由宿主的 Anonymizer.Tables 与宿主自己的覆盖性测试负责。
var PurgeCoveredTables = map[string]string{
	"identity":    "全部行（含已解绑的软删行）软删；subject_digest / provider_subject 替换为随机值；密文、provider_meta、hint、密钥版本置 NULL（AnonymizeIdentity）",
	"session":     "豁免 user_id 置空：全部会话在 purge 事务内吊销并写吊销集；账号行与身份已匿名化，user_id 不再指向可识别的自然人；行由 CleanupSessions 在 SessionRetention 后物理删除",
	"audit_event": "保留事件与 user_id（注销后仍需可追溯，§3.5）；ip / device_id / subject_hint 置 NULL（ScrubAuditEventsByUser）",
}

// PurgeDueUsers 匿名化所有 purge_time 已到的 PENDING_DELETION 账号（§3.5），返回成功 purge 的账号数。
// 按批取（PurgeBatchSize）、逐账号独立事务：某账号失败则记 Error 日志、跳过，批结束后返回首个错误
// （维护运行器记日志，下一轮重试），且不再取下一批——避免在同一个坏账号上空转。
// 一批全部成功且批满时继续取下一批。
func (s *Service) PurgeDueUsers(ctx context.Context) (int, error) {
	total := 0
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		now := s.now()
		ids, err := s.d.Repo.Q().ListUsersDueForPurge(ctx, db.ListUsersDueForPurgeParams{Now: now, BatchSize: PurgeBatchSize})
		if err != nil {
			return total, fmt.Errorf("user: list users due for purge: %w", err)
		}
		var firstErr error
		for _, id := range ids {
			if err := ctx.Err(); err != nil {
				return total, err
			}
			purged, err := s.purgeOne(ctx, id)
			if err != nil {
				s.d.Logger.Error("user: purge failed; will retry next round", "user_id", id, "err", err)
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			if purged {
				total++
			}
		}
		if firstErr != nil {
			return total, firstErr
		}
		if len(ids) < PurgeBatchSize {
			return total, nil
		}
	}
}

// purgeOne 在单个事务内匿名化一个账号：锁账号行并复核状态与到期 → 全部身份行 → 账号行 → 兜底吊销会话 →
// 清洗审计字段 → 宿主 Anonymizer（注册顺序）。提交后写吊销集并记 USER_PURGED / SESSION_REVOKED（actor SYSTEM）。
// 返回 purged=false 表示复核未通过（取批与加锁之间被 undelete 或已被处理），未做任何改动。
// now 取本次执行的实际时刻（而非批次开始时的 now）：账号行的 purge_time 记录的是"何时真正被匿名化"，
// 不是取批时刻，逐账号独立事务本就没有共享一个时间戳的理由。
func (s *Service) purgeOne(ctx context.Context, userID string) (bool, error) {
	now := s.now()
	var revoked []string
	purged := false
	err := s.d.Repo.WithTxRaw(ctx, func(tx pgx.Tx, q *db.Queries) error {
		u, err := q.LockUserByID(ctx, userID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // 库内没有硬删路径；防御性跳过
		}
		if err != nil {
			return fmt.Errorf("user: lock user: %w", err)
		}
		if u.State != enum.UserPendingDeletion || u.PurgeTime == nil || u.PurgeTime.After(now) {
			return nil // 取批之后被 undelete，或已被另一副本 purge
		}
		idents, err := q.ListIdentitiesByUserIncludingDeleted(ctx, userID)
		if err != nil {
			return fmt.Errorf("user: list identities for purge: %w", err)
		}
		for _, id := range idents {
			repl, err := randomHex(32)
			if err != nil {
				return err
			}
			if _, err := q.AnonymizeIdentity(ctx, db.AnonymizeIdentityParams{ID: id.ID, UserID: userID, Replacement: repl, Now: now}); err != nil {
				return fmt.Errorf("user: anonymize identity %s: %w", id.ID, err)
			}
		}
		n, err := q.PurgeUser(ctx, db.PurgeUserParams{ID: userID, DeletedState: enum.UserDeleted, PendingState: enum.UserPendingDeletion, Now: now})
		if err != nil {
			return fmt.Errorf("user: purge user row: %w", err)
		}
		if n != 1 {
			return fmt.Errorf("user: purge user row %s: expected 1 row, got %d", userID, n)
		}
		if revoked, err = s.revokeAllForUser(ctx, q, userID, nil, enum.RevokeUserDeleted, now); err != nil {
			return err
		}
		if _, err := q.ScrubAuditEventsByUser(ctx, userID); err != nil {
			return fmt.Errorf("user: scrub audit events: %w", err)
		}
		for _, a := range s.d.Anonymizers {
			if err := a.Anonymize(ctx, tx, userID); err != nil {
				return fmt.Errorf("user: anonymizer %s: %w", a.Name(), err)
			}
		}
		purged = true
		return nil
	})
	if err != nil || !purged {
		return false, err
	}
	s.afterRevokeAll(ctx, revoked, enum.RevokeUserDeleted, audit.Event{Actor: enum.ActorSystem, UserID: userID})
	s.record(ctx, audit.Event{Type: enum.EventUserPurged, Actor: enum.ActorSystem, Result: enum.ResultSuccess, UserID: userID})
	return true, nil
}

// randomHex 返回 n 个随机字节的十六进制串（2n 字符），用作匿名化后的 subject 占位：
// 与原值无关、逐行不同，长度与 HMAC 十六进制摘要一致。
func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("user: random: %w", err)
	}
	return hex.EncodeToString(b), nil
}
