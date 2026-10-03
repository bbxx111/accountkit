package user

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/ids"
	"github.com/bbxx111/accountkit/user/db"
)

// maxReasonRunes 是 :freeze / :unfreeze 的 reason 上限。
const maxReasonRunes = 200

// validReason 归一化管理员填写的 reason：TrimSpace；required 时不得为空；≤ 200 rune；
// 拒绝控制字符与双向/格式控制符（与 display_name 同一名单）。
func validReason(reason string, required bool) (string, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		if required {
			return "", fmt.Errorf("%w: reason is required", ErrInvalidArgument)
		}
		return "", nil
	}
	for _, r := range reason {
		if unicode.IsControl(r) || isForbiddenDisplayNameRune(r) {
			return "", fmt.Errorf("%w: reason must not contain control characters", ErrInvalidArgument)
		}
	}
	if utf8.RuneCountInString(reason) > maxReasonRunes {
		return "", fmt.Errorf("%w: reason longer than %d characters", ErrInvalidArgument, maxReasonRunes)
	}
	return reason, nil
}

// Freeze（§1.1/§4.3）：ACTIVE → FROZEN，写冻结快照（时间、reason、管理员 sub/username），同一事务吊销全部会话
// （reason USER_FROZEN）；提交后写吊销集并审计 USER_FROZEN（Reason = 管理员 reason）与每个会话的 SESSION_REVOKED。
// 已冻结 → ErrUserFrozen；PENDING_DELETION/DELETED → ErrInvalidState；用户不存在 → ErrNotFound。
func (s *Service) Freeze(ctx context.Context, a Admin, userID, reason string, meta Meta) (AdminUser, error) {
	reason, err := validReason(reason, true)
	if err != nil {
		return AdminUser{}, err
	}
	now := s.now()
	var (
		row     db.UserAccount
		revoked []string
	)
	err = s.d.Repo.WithTx(ctx, func(q *db.Queries) error {
		u, err := lockUserAs(ctx, q, userID, ErrNotFound)
		if err != nil {
			return err
		}
		if err := requireState(u, enum.UserActive); err != nil {
			return err
		}
		row, err = q.FreezeUser(ctx, db.FreezeUserParams{ID: userID, State: enum.UserFrozen, Now: now, Reason: reason, ActorSubject: a.Subject, ActorUsername: a.Username})
		if err != nil {
			return fmt.Errorf("user: freeze user: %w", err)
		}
		revoked, err = s.revokeAllForUser(ctx, q, userID, nil, enum.RevokeUserFrozen, now)
		return err
	})
	if err != nil {
		return AdminUser{}, err
	}
	tmpl := adminEvent(a, userID, meta)
	s.afterRevokeAll(ctx, revoked, enum.RevokeUserFrozen, tmpl)
	tmpl.Type, tmpl.Result, tmpl.Reason = enum.EventUserFrozen, enum.ResultSuccess, reason
	s.record(ctx, tmpl)
	return adminUserFrom(row), nil
}

// Unfreeze（§1.1/§4.3）：FROZEN → ACTIVE，清空冻结快照（历史留在审计）；reason 可选。
// 起点不是 FROZEN → ErrInvalidState；用户不存在 → ErrNotFound。不重建任何会话——用户需重新登录。
func (s *Service) Unfreeze(ctx context.Context, a Admin, userID, reason string, meta Meta) (AdminUser, error) {
	reason, err := validReason(reason, false)
	if err != nil {
		return AdminUser{}, err
	}
	now := s.now()
	var row db.UserAccount
	err = s.d.Repo.WithTx(ctx, func(q *db.Queries) error {
		u, err := lockUserAs(ctx, q, userID, ErrNotFound)
		if err != nil {
			return err
		}
		if err := requireState(u, enum.UserFrozen); err != nil {
			return err
		}
		row, err = q.UnfreezeUser(ctx, db.UnfreezeUserParams{ID: userID, State: enum.UserActive, Now: now})
		if err != nil {
			return fmt.Errorf("user: unfreeze user: %w", err)
		}
		return nil
	})
	if err != nil {
		return AdminUser{}, err
	}
	ev := adminEvent(a, userID, meta)
	ev.Type, ev.Result, ev.Reason = enum.EventUserUnfrozen, enum.ResultSuccess, reason
	s.record(ctx, ev)
	return adminUserFrom(row), nil
}

// AdminListSessions 列出用户的活跃会话（无"当前会话"概念）；用户不存在 → ErrNotFound。
func (s *Service) AdminListSessions(ctx context.Context, userID string) ([]SessionInfo, error) {
	if err := s.UserExists(ctx, userID); err != nil {
		return nil, err
	}
	return s.ListSessions(ctx, userID, "")
}

// AdminRevokeSession 踢出指定会话（reason ADMIN）；会话不属于该用户或已吊销 → ErrNotFound。
func (s *Service) AdminRevokeSession(ctx context.Context, a Admin, userID, sid string, meta Meta) error {
	now := s.now()
	if _, err := s.d.Repo.Q().GetActiveSessionByIDAndUser(ctx, db.GetActiveSessionByIDAndUserParams{ID: sid, UserID: userID}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("user: lookup session: %w", err)
	}
	reason := enum.RevokeAdmin
	n, err := s.d.Repo.Q().RevokeSession(ctx, db.RevokeSessionParams{ID: sid, Reason: &reason, Now: now})
	if err != nil {
		return fmt.Errorf("user: revoke session: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	s.afterRevokeAll(ctx, []string{sid}, reason, adminEvent(a, userID, meta))
	return nil
}

// AdminRevokeAllSessions 吊销用户全部活跃会话（reason ADMIN），返回数量；用户不存在 → ErrNotFound。
func (s *Service) AdminRevokeAllSessions(ctx context.Context, a Admin, userID string, meta Meta) (int, error) {
	if err := s.UserExists(ctx, userID); err != nil {
		return 0, err
	}
	now := s.now()
	var revoked []string
	err := s.d.Repo.WithTx(ctx, func(q *db.Queries) error {
		var err error
		revoked, err = s.revokeAllForUser(ctx, q, userID, nil, enum.RevokeAdmin, now)
		return err
	})
	if err != nil {
		return 0, err
	}
	s.afterRevokeAll(ctx, revoked, enum.RevokeAdmin, adminEvent(a, userID, meta))
	return len(revoked), nil
}

// UserExists 把"用户不存在"映射为 ErrNotFound（管理面读路径的前置检查；auditEvents 列表也用它给 404）。
func (s *Service) UserExists(ctx context.Context, userID string) error {
	if _, err := s.d.Repo.Q().GetUserByID(ctx, userID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("user: get user: %w", err)
	}
	return nil
}

// 管理端列表分页（§3.4）。
const (
	AdminPageSizeDefault = 20
	AdminPageSizeMax     = 100
)

// ListUsers 管理端用户列表：过滤 + keyset 分页（ORDER BY create_time, id）。多取一行判断是否有下一页。
// f.Subject（identity.phone / identity.email 的原始值）按 f.IdentityKind 归一化并展开为全部密钥版本的摘要，
// 归一化失败 → ErrInvalidTarget。limit 超出 [1, AdminPageSizeMax] → ErrInvalidArgument。
func (s *Service) ListUsers(ctx context.Context, f UserFilter, after *PageCursor, limit int) (UserPage, error) {
	if limit < 1 || limit > AdminPageSizeMax {
		return UserPage{}, fmt.Errorf("%w: page size must be within [1, %d]", ErrInvalidArgument, AdminPageSizeMax)
	}
	p := db.ListUsersAdminParams{IncludeDeleted: f.IncludeDeleted, Digests: []string{}, PageLimit: int32(limit + 1)}
	if f.State != nil {
		st := int16(*f.State)
		p.State = &st
	}
	p.CreateTimeMin, p.CreateTimeMax = f.CreateTimeMin, f.CreateTimeMax
	if f.IdentityKind != enum.IdentityKindUnspecified {
		p.ByIdentity = true
		k := int16(f.IdentityKind)
		p.IdentityKind = &k
		if f.Subject != nil {
			norm, _, _, err := s.normalizeTarget(f.IdentityKind, *f.Subject)
			if err != nil {
				return UserPage{}, err
			}
			p.Digests = s.d.Digester.AllDigests(norm)
		}
		p.HintPrefix, p.HintSuffix = f.HintPrefix, f.HintSuffix
	}
	if after != nil {
		t, id := after.Time, after.ID
		p.AfterTime, p.AfterID = &t, &id
	}
	rows, err := s.d.Repo.Q().ListUsersAdmin(ctx, p)
	if err != nil {
		return UserPage{}, fmt.Errorf("user: list users: %w", err)
	}
	page := UserPage{Users: make([]AdminUser, 0, min(len(rows), limit))}
	for i, r := range rows {
		if i == limit {
			last := rows[limit-1]
			page.NextCursor = &PageCursor{Time: last.CreateTime, ID: last.ID}
			break
		}
		page.Users = append(page.Users, adminUserFrom(r))
	}
	return page, nil
}

// GetUserDetail 管理端用户详情：账号 + 掩码身份 + 活跃会话数；用户不存在 → ErrNotFound。
func (s *Service) GetUserDetail(ctx context.Context, userID string) (AdminUserDetail, error) {
	q := s.d.Repo.Q()
	u, err := q.GetUserByID(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return AdminUserDetail{}, ErrNotFound
	}
	if err != nil {
		return AdminUserDetail{}, fmt.Errorf("user: get user: %w", err)
	}
	idents, err := q.ListActiveIdentitiesByUser(ctx, userID)
	if err != nil {
		return AdminUserDetail{}, fmt.Errorf("user: list identities: %w", err)
	}
	n, err := q.CountActiveSessionsByUser(ctx, userID)
	if err != nil {
		return AdminUserDetail{}, fmt.Errorf("user: count sessions: %w", err)
	}
	d := AdminUserDetail{AdminUser: adminUserFrom(u), Identities: make([]IdentityInfo, 0, len(idents)), ActiveSessionCount: int(n)}
	for _, id := range idents {
		d.Identities = append(d.Identities, identityInfoFrom(id))
	}
	return d, nil
}

// RevealIdentity（§3.3/§4.3，super-admin）：返回该用户某个**活跃**身份的明文——锚点身份用行内密钥版本解密，
// 第三方身份返回 provider_subject。成功审计 IDENTITY_REVEALED（ADMIN，只带 kind + hint）。
// 非本人/已解绑 → ErrNotFound；id 非法 → ErrInvalidArgument；解密失败 → 基础设施错误（500，日志只带 identity_id）。
func (s *Service) RevealIdentity(ctx context.Context, a Admin, userID, identityID string, meta Meta) (RevealedIdentity, error) {
	if !ids.Valid(ids.User, userID) || !ids.Valid(ids.Identity, identityID) {
		return RevealedIdentity{}, fmt.Errorf("%w: malformed id", ErrInvalidArgument)
	}
	row, err := s.d.Repo.Q().GetActiveIdentityByIDAndUser(ctx, db.GetActiveIdentityByIDAndUserParams{ID: identityID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return RevealedIdentity{}, ErrNotFound
	}
	if err != nil {
		return RevealedIdentity{}, fmt.Errorf("user: get identity: %w", err)
	}
	out := RevealedIdentity{ID: row.ID, Kind: row.Kind}
	var hint string
	switch {
	case row.SubjectDigest != nil:
		if row.SubjectCiphertext == nil || row.CipherKeyVersion == nil {
			return RevealedIdentity{}, fmt.Errorf("user: identity %s has no subject ciphertext", row.ID)
		}
		plain, err := s.d.Cipher.Decrypt(row.SubjectCiphertext, uint16(*row.CipherKeyVersion))
		if err != nil {
			return RevealedIdentity{}, fmt.Errorf("user: decrypt identity %s: %w", row.ID, err)
		}
		out.Subject, hint = plain, audit.Hint(*row.SubjectDigest)
	case row.ProviderSubject != nil:
		out.Subject, hint = *row.ProviderSubject, audit.Hint(*row.ProviderSubject)
	}
	ev := adminEvent(a, userID, meta)
	ev.Type, ev.Result, ev.IdentityKind, ev.SubjectHint = enum.EventIdentityRevealed, enum.ResultSuccess, row.Kind, hint
	s.record(ctx, ev)
	return out, nil
}
