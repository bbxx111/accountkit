package user

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/ids"
	"github.com/bbxx111/accountkit/user/code"
	"github.com/bbxx111/accountkit/user/db"
)

// ReplaceIdentity 使用新目标的 BIND 证明原子替换同类锚点身份。宿主传入已认证的
// Principal；此方法继续复核账号、作用域、近期认证和数据库当前会话。
// 当前会话及 auth_time 保留，其他会话与身份变更一并撤销。已消费的码不随回滚恢复。
func (s *Service) ReplaceIdentity(ctx context.Context, p Principal, identityID string, cred CodeCredential, meta Meta) (info IdentityInfo, err error) {
	if err := validateCodeCredential(cred); err != nil {
		return IdentityInfo{}, err
	}
	channel, target := cred.Channel, cred.Target
	ev := userEvent(p, meta)
	ev.IdentityKind = channel
	rejectReason := ""
	defer func() {
		if err == nil {
			return
		}
		if rejectReason == "" {
			rejectReason = identityReplaceRejectReason(err)
		}
		if rejectReason != "" {
			ev.Type, ev.Result, ev.Reason = enum.EventIdentityReplaceRejected, enum.ResultFailure, rejectReason
			s.record(ctx, ev)
		}
	}()
	if !ids.Valid(ids.User, p.UserID) || !ids.Valid(ids.Session, p.SessionID) {
		return IdentityInfo{}, ErrInvalidToken
	}
	if p.Scope != ScopeUser {
		return IdentityInfo{}, ErrInsufficientScope
	}
	if err = s.replacementRecentAuth(p, s.now()); err != nil {
		return IdentityInfo{}, err
	}
	if !ids.Valid(ids.Identity, identityID) {
		return IdentityInfo{}, ErrInvalidArgument
	}
	norm, hintPrefix, hintSuffix, err := s.normalizeTarget(channel, target)
	if err != nil {
		return IdentityInfo{}, err
	}
	digest, _ := s.d.Digester.Digest(norm)
	ev.SubjectHint = audit.Hint(digest)
	old, _, err := s.replacementPreflight(ctx, s.d.Repo.Q(), p, identityID, channel, norm)
	if err != nil {
		return IdentityInfo{}, err
	}
	if reason, verifyErr := s.verifyCode(ctx, channel, enum.PurposeBind, norm, cred, code.Binding{UserID: p.UserID}); verifyErr != nil {
		if !errors.Is(verifyErr, ErrUnavailable) && reason != "INTERNAL" {
			rejectReason = reason
		}
		return IdentityInfo{}, verifyErr
	}
	var revoked []string
	err = s.d.Repo.WithTx(ctx, func(q *db.Queries) error {
		u, lockErr := lockUser(ctx, q, p.UserID)
		if lockErr != nil {
			return lockErr
		}
		if stateErr := accountMayAct(u); stateErr != nil {
			return stateErr
		}
		sess, lockErr := q.LockSessionByID(ctx, p.SessionID)
		if errors.Is(lockErr, pgx.ErrNoRows) {
			return ErrInvalidToken
		}
		if lockErr != nil {
			return fmt.Errorf("user: lock replacement session: %w", lockErr)
		}
		if sess.UserID != p.UserID || sess.RevokeTime != nil {
			return ErrInvalidToken
		}
		var idents []db.Identity
		old, idents, lockErr = s.replacementIdentity(ctx, q, p.UserID, identityID, channel, norm)
		if lockErr != nil {
			return lockErr
		}
		_, findErr := q.FindActiveIdentityByDigests(ctx, db.FindActiveIdentityByDigestsParams{Kind: channel, Digests: s.d.Digester.AllDigests(norm)})
		if findErr == nil {
			return ErrIdentityConflict
		}
		if !errors.Is(findErr, pgx.ErrNoRows) {
			return fmt.Errorf("user: find replacement target: %w", findErr)
		}
		// 替换前后同类数量相等；不以临时放宽上限来允许写入。
		if countKind(idents, channel) > s.d.MaxIdentitiesPerKind {
			return ErrIdentityKindLimit
		}
		// 等锁和必要身份读取都可能跨过有效期，写入前以同一微秒时刻复核。
		decisionTime := s.now()
		now := sessionTime(decisionTime)
		if !sessionActiveAt(sess, now) {
			return ErrInvalidToken
		}
		if authErr := s.replacementRecentAuth(p, decisionTime); authErr != nil {
			return authErr
		}
		n, deleteErr := q.SoftDeleteIdentity(ctx, db.SoftDeleteIdentityParams{ID: identityID, UserID: p.UserID, Now: now})
		if deleteErr != nil {
			return fmt.Errorf("user: remove replaced identity: %w", deleteErr)
		}
		if n == 0 {
			return ErrNotFound
		}
		row, createErr := s.createAnchorIdentity(ctx, q, p.UserID, channel, norm, hintPrefix, hintSuffix)
		if createErr != nil {
			if IsUniqueViolation(createErr) {
				return ErrIdentityConflict
			}
			return fmt.Errorf("user: create replacement identity: %w", createErr)
		}
		info = identityInfoFrom(row)
		revoked, lockErr = s.revokeAllForUser(ctx, q, p.UserID, &p.SessionID, enum.RevokeIdentityReplaced, now)
		return lockErr
	})
	if err != nil {
		return IdentityInfo{}, err
	}
	s.afterRevokeAll(ctx, revoked, enum.RevokeIdentityReplaced, userEvent(p, meta))
	ev.Type, ev.Result, ev.Reason = enum.EventIdentityUnbound, enum.ResultSuccess, enum.RevokeIdentityReplaced.String()
	if old.SubjectDigest != nil {
		ev.SubjectHint = audit.Hint(*old.SubjectDigest)
	}
	s.record(ctx, ev)
	ev.Type, ev.SubjectHint = enum.EventIdentityBound, audit.Hint(digest)
	s.record(ctx, ev)
	return info, nil
}

func (s *Service) replacementRecentAuth(p Principal, now time.Time) error {
	if *s.d.SensitiveOpVerification && (p.AuthTime.IsZero() || now.Sub(p.AuthTime) > s.d.ReauthMaxAge) {
		return ErrReauthenticationRequired
	}
	return nil
}

// replacementPreflight 只读取，不持有数据库写锁。归属及同目标拒绝在消费验证码前发生。
func (s *Service) replacementPreflight(ctx context.Context, q *db.Queries, p Principal, identityID string, channel enum.IdentityKind, norm string) (db.Identity, []db.Identity, error) {
	u, err := q.GetUserByID(ctx, p.UserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.Identity{}, nil, ErrInvalidToken
	}
	if err != nil {
		return db.Identity{}, nil, fmt.Errorf("user: replacement user preflight: %w", err)
	}
	if err := accountMayAct(u); err != nil {
		return db.Identity{}, nil, err
	}
	sess, err := q.GetActiveSessionByIDAndUser(ctx, db.GetActiveSessionByIDAndUserParams{ID: p.SessionID, UserID: p.UserID})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.Identity{}, nil, ErrInvalidToken
	}
	if err != nil {
		return db.Identity{}, nil, fmt.Errorf("user: replacement session preflight: %w", err)
	}
	if !sessionActiveAt(sess, sessionTime(s.now())) {
		return db.Identity{}, nil, ErrInvalidToken
	}
	return s.replacementIdentity(ctx, q, p.UserID, identityID, channel, norm)
}

func (s *Service) replacementIdentity(ctx context.Context, q *db.Queries, userID, identityID string, channel enum.IdentityKind, norm string) (db.Identity, []db.Identity, error) {
	old, err := q.GetActiveIdentityByIDAndUser(ctx, db.GetActiveIdentityByIDAndUserParams{ID: identityID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.Identity{}, nil, ErrNotFound
	}
	if err != nil {
		return db.Identity{}, nil, fmt.Errorf("user: get replaced identity: %w", err)
	}
	if !old.Kind.IsAnchor() || old.Kind != channel {
		return db.Identity{}, nil, ErrInvalidArgument
	}
	if old.SubjectDigest != nil {
		for _, candidate := range s.d.Digester.AllDigests(norm) {
			if *old.SubjectDigest == candidate {
				return db.Identity{}, nil, ErrIdentityUnchanged
			}
		}
	}
	idents, err := q.ListActiveIdentitiesByUser(ctx, userID)
	if err != nil {
		return db.Identity{}, nil, fmt.Errorf("user: replacement identity count: %w", err)
	}
	return old, idents, nil
}

// 基础设施错误不伪装成业务拒绝，审计原因固定且不包含原始输入或错误文本。
func identityReplaceRejectReason(err error) string {
	switch {
	case errors.Is(err, ErrInsufficientScope):
		return "INSUFFICIENT_SCOPE"
	case errors.Is(err, ErrReauthenticationRequired):
		return "REAUTHENTICATION_REQUIRED"
	case errors.Is(err, ErrIdentityUnchanged):
		return "IDENTITY_UNCHANGED"
	case errors.Is(err, ErrInvalidArgument), errors.Is(err, ErrInvalidTarget):
		return "INVALID_ARGUMENT"
	case errors.Is(err, ErrIdentityConflict):
		return "IDENTITY_ALREADY_BOUND"
	case errors.Is(err, ErrIdentityKindLimit):
		return "IDENTITY_KIND_LIMIT"
	case errors.Is(err, ErrNotFound):
		return "NOT_FOUND"
	case errors.Is(err, ErrUserFrozen):
		return "USER_FROZEN"
	case errors.Is(err, ErrInvalidToken):
		return "TOKEN_INVALID"
	default:
		return ""
	}
}
