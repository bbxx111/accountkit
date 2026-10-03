package user

import (
	"context"
	"fmt"

	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user/db"
)

// userEvent 构造 C 端写操作的审计事件模板（Actor=USER，带当前会话与请求元数据）。
func userEvent(p Principal, meta Meta) audit.Event {
	return audit.Event{Actor: enum.ActorUser, UserID: p.UserID, SessionID: p.SessionID, IP: meta.IP, RequestID: meta.RequestID}
}

// adminEvent 构造管理端写操作的审计事件模板（Actor=ADMIN，带管理员快照、目标用户与请求元数据；无 SessionID）。
func adminEvent(a Admin, userID string, meta Meta) audit.Event {
	return audit.Event{Actor: enum.ActorAdmin, UserID: userID, AdminIssuer: a.Issuer, AdminSubject: a.Subject, AdminUsername: a.Username, IP: meta.IP, RequestID: meta.RequestID}
}

// DeleteMe 软删除（AIP-164，§1.1/§2.4）：ACTIVE → PENDING_DELETION，delete_time = now，
// purge_time = now + DeletionCoolingPeriod；同一事务内吊销该用户全部会话（含当前）；提交后写吊销集并审计
// USER_DELETED 与每个会话的 SESSION_REVOKED。起点不是 ACTIVE：FROZEN → ErrUserFrozen，其余 → ErrInvalidState。
func (s *Service) DeleteMe(ctx context.Context, p Principal, meta Meta) (Me, error) {
	row, err := s.softDelete(ctx, p.UserID, ErrInvalidToken, userEvent(p, meta))
	if err != nil {
		return Me{}, err
	}
	return meFrom(row), nil
}

// AdminDeleteUser 是客服代办的软删除（§4.3）：与 DeleteMe 同一状态迁移与吊销规则；用户不存在 → ErrNotFound；
// 冻结账号 → ErrUserFrozen（"冻结账号要注销需先解冻"）。审计 actor = ADMIN。
func (s *Service) AdminDeleteUser(ctx context.Context, a Admin, userID string, meta Meta) (AdminUser, error) {
	row, err := s.softDelete(ctx, userID, ErrNotFound, adminEvent(a, userID, meta))
	if err != nil {
		return AdminUser{}, err
	}
	return adminUserFrom(row), nil
}

// softDelete 是 DeleteMe / AdminDeleteUser 的共用体。missing 是"用户不存在"映射的哨兵；tmpl 是预填了
// Actor / Admin* / SessionID / IP / RequestID 的事件模板（USER_DELETED 与 SESSION_REVOKED 都由它派生）。
func (s *Service) softDelete(ctx context.Context, userID string, missing error, tmpl audit.Event) (db.UserAccount, error) {
	now := s.now()
	var (
		row     db.UserAccount
		revoked []string
	)
	err := s.d.Repo.WithTx(ctx, func(q *db.Queries) error {
		u, err := lockUserAs(ctx, q, userID, missing)
		if err != nil {
			return err
		}
		if err := requireState(u, enum.UserActive); err != nil {
			return err
		}
		row, err = q.SoftDeleteUser(ctx, db.SoftDeleteUserParams{
			ID: userID, State: enum.UserPendingDeletion, Now: now, PurgeTime: now.Add(s.d.DeletionCoolingPeriod),
		})
		if err != nil {
			return fmt.Errorf("user: soft delete user: %w", err)
		}
		revoked, err = s.revokeAllForUser(ctx, q, userID, nil, enum.RevokeUserDeleted, now)
		return err
	})
	if err != nil {
		return db.UserAccount{}, err
	}
	s.afterRevokeAll(ctx, revoked, enum.RevokeUserDeleted, tmpl)
	ev := tmpl
	ev.Type, ev.Result = enum.EventUserDeleted, enum.ResultSuccess
	s.record(ctx, ev)
	return row, nil
}

// Undelete 取消注销（§1.1/§2.4）：PENDING_DELETION → ACTIVE，清空 delete_time / purge_time。
// 不吊销、不重签当前会话——scope 在下次签发时由账号状态实时推导（§2.3），客户端刷新一次即得 user。
func (s *Service) Undelete(ctx context.Context, p Principal, meta Meta) (Me, error) {
	row, err := s.undelete(ctx, p.UserID, ErrInvalidToken, userEvent(p, meta))
	if err != nil {
		return Me{}, err
	}
	return meFrom(row), nil
}

// AdminUndeleteUser 是客服代办的撤销注销（§4.3）；已 purge（DELETED）→ ErrInvalidState；用户不存在 → ErrNotFound。
func (s *Service) AdminUndeleteUser(ctx context.Context, a Admin, userID string, meta Meta) (AdminUser, error) {
	row, err := s.undelete(ctx, userID, ErrNotFound, adminEvent(a, userID, meta))
	if err != nil {
		return AdminUser{}, err
	}
	return adminUserFrom(row), nil
}

func (s *Service) undelete(ctx context.Context, userID string, missing error, tmpl audit.Event) (db.UserAccount, error) {
	now := s.now()
	var row db.UserAccount
	err := s.d.Repo.WithTx(ctx, func(q *db.Queries) error {
		u, err := lockUserAs(ctx, q, userID, missing)
		if err != nil {
			return err
		}
		if err := requireState(u, enum.UserPendingDeletion); err != nil {
			return err
		}
		row, err = q.UndeleteUser(ctx, db.UndeleteUserParams{ID: userID, State: enum.UserActive, Now: now})
		if err != nil {
			return fmt.Errorf("user: undelete user: %w", err)
		}
		return nil
	})
	if err != nil {
		return db.UserAccount{}, err
	}
	ev := tmpl
	ev.Type, ev.Result = enum.EventUserUndeleted, enum.ResultSuccess
	s.record(ctx, ev)
	return row, nil
}

// afterRevokeAll 是 revokeAllForUser 的提交后半段：把每个 sid 写入吊销集并按模板记 SESSION_REVOKED。
// 与事务分开是因为吊销集与审计都不能回滚，必须在提交成功之后才做。
func (s *Service) afterRevokeAll(ctx context.Context, sids []string, reason enum.RevokeReason, tmpl audit.Event) {
	for _, sid := range sids {
		s.revokeInSet(ctx, sid)
		ev := tmpl
		ev.Type, ev.Result, ev.Reason, ev.SessionID = enum.EventSessionRevoked, enum.ResultSuccess, reason.String(), sid
		s.record(ctx, ev)
	}
}
