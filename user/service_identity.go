package user

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/email"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/ids"
	"github.com/bbxx111/accountkit/phone"
	"github.com/bbxx111/accountkit/user/code"
	"github.com/bbxx111/accountkit/user/db"
	"github.com/bbxx111/accountkit/user/idp"
)

// IdentityInfo 是 identities 端点的数据源（不含任何 digest/密文/provider_subject）。
type IdentityInfo struct {
	ID            string
	Kind          enum.IdentityKind
	MaskedSubject string
	CreateTime    time.Time
}

// identityInfoFrom 由 hint 拼出掩码；第三方身份不展示 subject。
func identityInfoFrom(id db.Identity) IdentityInfo {
	info := IdentityInfo{ID: id.ID, Kind: id.Kind, CreateTime: id.CreateTime}
	if id.HintPrefix != nil && id.HintSuffix != nil {
		switch id.Kind {
		case enum.IdentityPhone:
			info.MaskedSubject = phone.Mask(*id.HintPrefix, *id.HintSuffix)
		case enum.IdentityEmail:
			info.MaskedSubject = email.Mask(*id.HintPrefix, *id.HintSuffix)
		}
	}
	return info
}

// ListIdentities 列出用户的活动身份（create_time, id 升序）。
func (s *Service) ListIdentities(ctx context.Context, userID string) ([]IdentityInfo, error) {
	rows, err := s.d.Repo.Q().ListActiveIdentitiesByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("user: list identities: %w", err)
	}
	out := make([]IdentityInfo, 0, len(rows))
	for _, r := range rows {
		out = append(out, identityInfoFrom(r))
	}
	return out, nil
}

// sendBindCode 向即将绑定的新锚点发送 BIND 码。不预检占用（避免枚举），冲突在绑定时报告。
func (s *Service) sendBindCode(ctx context.Context, p Principal, channel enum.IdentityKind, target string, meta Meta) (CodeChallenge, error) {
	if !channel.IsChannel() {
		return CodeChallenge{}, ErrInvalidArgument
	}
	if err := s.requireIdentityEnabled(channel); err != nil {
		return CodeChallenge{}, err
	}
	norm, _, _, err := s.normalizeTarget(channel, target)
	if err != nil {
		return CodeChallenge{}, err
	}
	return s.sendCode(ctx, channel, enum.PurposeBind, norm, code.Binding{UserID: p.UserID}, meta)
}

// anchorSubject 是一次锚点 subject 互斥所需的类型与归一化值。
type anchorSubject struct {
	kind enum.IdentityKind
	norm string
}

// lockAnchorSubjects 在事务内对锚点 subject 的全部摘要版本加事务级 advisory lock，
// 使可能新建该 subject 身份的事务串行化：唯一索引只约束单个摘要值，密钥轮换的混合
// active 期间不同进程会写入不同版本的摘要。锁序：subject 锁在任何行锁之前取得，
// 全部键在同一条语句内按升序加锁；之后才是用户行 → 身份/会话行。
func (s *Service) lockAnchorSubjects(ctx context.Context, q *db.Queries, subjects ...anchorSubject) error {
	var kinds []string
	var digests []string
	for _, subject := range subjects {
		if !subject.kind.IsChannel() {
			continue
		}
		for _, digest := range s.d.Digester.AllDigests(subject.norm) {
			kinds = append(kinds, string(subject.kind))
			digests = append(digests, digest)
		}
	}
	if len(digests) == 0 {
		return nil
	}
	if err := q.LockAnchorSubjects(ctx, db.LockAnchorSubjectsParams{Kinds: kinds, Digests: digests}); err != nil {
		return fmt.Errorf("user: lock anchor subjects: %w", err)
	}
	return nil
}

// createAnchorIdentity 创建 PHONE/EMAIL 身份行：digest + 密文 + hint，均带密钥版本。
// 调用方须已在同一事务内对该 subject 调用 lockAnchorSubjects。
func (s *Service) createAnchorIdentity(ctx context.Context, q *db.Queries, userID string, kind enum.IdentityKind, norm, hintPrefix, hintSuffix string) (db.Identity, error) {
	iid, err := ids.New(ids.Identity)
	if err != nil {
		return db.Identity{}, err
	}
	digest, dver := s.d.Digester.Digest(norm)
	ct, cver, err := s.d.Cipher.Encrypt(norm)
	if err != nil {
		return db.Identity{}, fmt.Errorf("user: encrypt subject: %w", err)
	}
	dv, cv := int16(dver), int16(cver)
	row, err := q.CreateIdentity(ctx, db.CreateIdentityParams{
		ID: iid, UserID: userID, Kind: kind, SubjectDigest: &digest, DigestKeyVersion: &dv,
		SubjectCiphertext: ct, CipherKeyVersion: &cv, HintPrefix: &hintPrefix, HintSuffix: &hintSuffix,
	})
	if err != nil {
		return db.Identity{}, err // 唯一冲突由调用方判别
	}
	return row, nil
}

// bindGuard 是绑定事务的公共前置：锁用户 → 状态检查 → 返回当前活动身份（供上限与幂等判断）。
func (s *Service) bindGuard(ctx context.Context, q *db.Queries, userID string) (db.UserAccount, []db.Identity, error) {
	u, err := lockUser(ctx, q, userID)
	if err != nil {
		return db.UserAccount{}, nil, err
	}
	if err := accountMayAct(u); err != nil {
		return db.UserAccount{}, nil, err
	}
	idents, err := q.ListActiveIdentitiesByUser(ctx, userID)
	if err != nil {
		return db.UserAccount{}, nil, fmt.Errorf("user: list identities: %w", err)
	}
	return u, idents, nil
}

func countKind(idents []db.Identity, kind enum.IdentityKind) int {
	n := 0
	for _, id := range idents {
		if id.Kind == kind {
			n++
		}
	}
	return n
}

// bindRejectReason 把绑定阶段的领域错误映射为审计 reason；基础设施错误返回空。
func bindRejectReason(err error) string {
	switch {
	case errors.Is(err, ErrIdentityConflict):
		return "IDENTITY_ALREADY_BOUND"
	case errors.Is(err, ErrIdentityKindLimit):
		return "IDENTITY_KIND_LIMIT"
	case errors.Is(err, ErrUserFrozen):
		return "USER_FROZEN"
	}
	return ""
}

// BindWithCode 用 BIND 验证码把 PHONE/EMAIL 绑定到当前账号。
func (s *Service) BindWithCode(ctx context.Context, p Principal, cred CodeCredential, meta Meta) (IdentityInfo, bool, error) {
	if err := validateCodeCredential(cred); err != nil {
		return IdentityInfo{}, false, err
	}
	if err := s.requireIdentityEnabled(cred.Channel); err != nil {
		return IdentityInfo{}, false, err
	}
	channel, target := cred.Channel, cred.Target
	norm, hintPrefix, hintSuffix, err := s.normalizeTarget(channel, target)
	if err != nil {
		return IdentityInfo{}, false, err
	}
	digest, _ := s.d.Digester.Digest(norm)
	ev := audit.AuthenticationEvent{UserID: p.UserID, SessionID: p.SessionID, IdentityKind: channel, SubjectHint: audit.Hint(digest), IP: meta.IP, RequestID: meta.RequestID}
	if reason, err := s.verifyCode(ctx, channel, enum.PurposeBind, norm, cred, code.Binding{UserID: p.UserID}); err != nil {
		if !errors.Is(err, ErrUnavailable) {
			ev.Type, ev.Result, ev.Reason = enum.EventIdentityBindRejected, enum.ResultFailure, reason
			s.record(ctx, ev)
		}
		return IdentityInfo{}, false, err
	}
	var info IdentityInfo
	var created bool
	err = s.d.Repo.withDomainTx(ctx, func(q *db.Queries) error {
		if err := s.lockAnchorSubjects(ctx, q, anchorSubject{channel, norm}); err != nil {
			return err
		}
		_, idents, err := s.bindGuard(ctx, q, p.UserID)
		if err != nil {
			return err
		}
		existing, err := q.FindActiveIdentityByDigests(ctx, db.FindActiveIdentityByDigestsParams{Kind: channel, Digests: s.d.Digester.AllDigests(norm)})
		switch {
		case err == nil && existing.UserID == p.UserID:
			info, created = identityInfoFrom(existing), false
			return nil
		case err == nil:
			return ErrIdentityConflict
		case !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("user: find identity: %w", err)
		}
		if countKind(idents, channel) >= s.d.MaxIdentitiesPerKind {
			return ErrIdentityKindLimit
		}
		row, err := s.createAnchorIdentity(ctx, q, p.UserID, channel, norm, hintPrefix, hintSuffix)
		if err != nil {
			if IsUniqueViolation(err) {
				return ErrIdentityConflict // 并发绑定同一 subject：唯一索引兜底
			}
			return err
		}
		info, created = identityInfoFrom(row), true
		return nil
	})
	if err != nil {
		if reason := bindRejectReason(err); reason != "" {
			ev.Type, ev.Result, ev.Reason = enum.EventIdentityBindRejected, enum.ResultFailure, reason
			s.record(ctx, ev)
		}
		return IdentityInfo{}, false, err
	}
	ev.IdentityID = info.ID
	ev.Type, ev.Result = enum.EventIdentityBound, enum.ResultSuccess
	if !created {
		ev.Reason = "ALREADY_BOUND"
	}
	s.record(ctx, ev)
	return info, created, nil
}

// createProviderIdentity 创建第三方身份行。空 openid 与空 profile 不落库。
func (s *Service) createProviderIdentity(ctx context.Context, q *db.Queries, userID string, ident idp.Identity) (db.Identity, error) {
	iid, err := ids.New(ids.Identity)
	if err != nil {
		return db.Identity{}, err
	}
	subject := ident.Subject
	var meta []byte
	openIDs := make(map[string]string, len(ident.OpenIDs))
	for app, oid := range ident.OpenIDs {
		if oid != "" {
			openIDs[app] = oid
		}
	}
	profile := providerProfileFrom(ident)
	if len(openIDs) > 0 || profile != nil {
		meta, err = json.Marshal(providerMeta{OpenIDs: openIDs, Profile: profile})
		if err != nil {
			return db.Identity{}, fmt.Errorf("user: encode provider meta: %w", err)
		}
	}
	row, err := q.CreateIdentity(ctx, db.CreateIdentityParams{ID: iid, UserID: userID, Kind: ident.Kind, ProviderSubject: &subject, ProviderMeta: meta})
	if err != nil {
		return db.Identity{}, err
	}
	if err := s.seedEmptyProviderProfile(ctx, q, userID, ident); err != nil {
		return db.Identity{}, err
	}
	return row, nil
}

// BindWithIdp 把第三方身份绑定到当前账号。
func (s *Service) BindWithIdp(ctx context.Context, p Principal, cred IdpCredential, meta Meta) (IdentityInfo, bool, error) {
	if !cred.Kind.Valid() || cred.Kind.IsAnchor() {
		return IdentityInfo{}, false, ErrInvalidArgument
	}
	ev := audit.AuthenticationEvent{UserID: p.UserID, SessionID: p.SessionID, IdentityKind: cred.Kind, IP: meta.IP, RequestID: meta.RequestID}
	if err := s.requireIdentityEnabled(cred.Kind); err != nil {
		if reason := idpFailureReason(err); reason != "" {
			ev.Type, ev.Result, ev.Reason = enum.EventIdentityBindRejected, enum.ResultFailure, reason
			s.record(ctx, ev)
		}
		return IdentityInfo{}, false, err
	}
	ident, err := s.verifyIdp(ctx, cred)
	if err != nil {
		if reason := idpFailureReason(err); reason != "" {
			ev.Type, ev.Result, ev.Reason = enum.EventIdentityBindRejected, enum.ResultFailure, reason
			s.record(ctx, ev)
		}
		return IdentityInfo{}, false, err
	}
	if ident.Kind != cred.Kind || ident.Subject == "" {
		return IdentityInfo{}, false, idp.ErrInvalidCredential
	}
	ev.SubjectHint = audit.Hint(ident.Subject)
	now := s.now()
	var info IdentityInfo
	var created bool
	err = s.d.Repo.withDomainTx(ctx, func(q *db.Queries) error {
		_, idents, err := s.bindGuard(ctx, q, p.UserID)
		if err != nil {
			return err
		}
		subject := ident.Subject
		existing, err := q.FindActiveIdentityByProviderSubject(ctx, db.FindActiveIdentityByProviderSubjectParams{Kind: ident.Kind, ProviderSubject: &subject})
		switch {
		case err == nil && existing.UserID == p.UserID:
			if err := s.refreshProviderMeta(ctx, q, existing, ident, now); err != nil {
				return err
			}
			info, created = identityInfoFrom(existing), false
			return nil
		case err == nil:
			return ErrIdentityConflict
		case !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("user: find provider identity: %w", err)
		}
		if countKind(idents, ident.Kind) >= s.d.MaxIdentitiesPerKind {
			return ErrIdentityKindLimit
		}
		row, err := s.createProviderIdentity(ctx, q, p.UserID, ident)
		if err != nil {
			if IsUniqueViolation(err) {
				return ErrIdentityConflict
			}
			return err
		}
		info, created = identityInfoFrom(row), true
		return nil
	})
	if err != nil {
		if reason := bindRejectReason(err); reason != "" {
			ev.Type, ev.Result, ev.Reason = enum.EventIdentityBindRejected, enum.ResultFailure, reason
			s.record(ctx, ev)
		}
		return IdentityInfo{}, false, err
	}
	ev.IdentityID = info.ID
	ev.Type, ev.Result = enum.EventIdentityBound, enum.ResultSuccess
	if !created {
		ev.Reason = "ALREADY_BOUND"
	}
	s.record(ctx, ev)
	return info, created, nil
}

// UnbindIdentity 软删除本人的一个身份；当前账号及解绑后的身份集合均须满足策略。
func (s *Service) UnbindIdentity(ctx context.Context, p Principal, identityID string, meta Meta) error {
	if !ids.Valid(ids.Identity, identityID) {
		return fmt.Errorf("%w: malformed identity id", ErrInvalidArgument)
	}
	if !ids.Valid(ids.User, p.UserID) || !ids.Valid(ids.Session, p.SessionID) {
		return ErrInvalidToken
	}
	if p.Scope != ScopeUser {
		return ErrInsufficientScope
	}
	if err := s.sensitiveRecentAuth(p, s.now()); err != nil {
		return err
	}
	var target db.Identity
	var revoked []string
	err := s.d.Repo.withDomainTx(ctx, func(q *db.Queries) error {
		u, err := lockUser(ctx, q, p.UserID)
		if err != nil {
			return err
		}
		if err := accountMayAct(u); err != nil {
			return err
		}
		sess, err := q.LockSessionByID(ctx, p.SessionID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidToken
		}
		if err != nil {
			return fmt.Errorf("user: lock unbind session: %w", err)
		}
		if sess.UserID != p.UserID || !sessionActiveAt(sess, sessionTime(s.now())) {
			return ErrInvalidToken
		}
		if _, err := s.requireSessionAuthenticationIdentities(ctx, q, sess); err != nil {
			return err
		}
		missingSources, err := q.HasSessionsWithoutAuthenticationIdentities(ctx, p.UserID)
		if err != nil {
			return fmt.Errorf("user: check authentication source integrity: %w", err)
		}
		if missingSources {
			return fmt.Errorf("user: invariant violated: unrevoked session has empty authentication identities")
		}
		row, err := q.GetActiveIdentityByIDAndUser(ctx, db.GetActiveIdentityByIDAndUserParams{ID: identityID, UserID: p.UserID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("user: get identity: %w", err)
		}
		target = row
		idents, err := q.ListActiveIdentitiesByUser(ctx, p.UserID)
		if err != nil {
			return fmt.Errorf("user: list identities: %w", err)
		}
		remaining := make([]db.Identity, 0, len(idents))
		for _, id := range idents {
			if id.ID != row.ID {
				remaining = append(remaining, id)
			}
		}
		if !s.identitiesComplete(idents) || !s.identitiesComplete(remaining) {
			return ErrLastAnchor
		}
		decisionTime := s.now()
		now := sessionTime(decisionTime)
		if !sessionActiveAt(sess, now) {
			return ErrInvalidToken
		}
		if err := s.sensitiveRecentAuth(p, decisionTime); err != nil {
			return err
		}
		n, err := q.SoftDeleteIdentity(ctx, db.SoftDeleteIdentityParams{ID: identityID, UserID: p.UserID, Now: now})
		if err != nil {
			return fmt.Errorf("user: soft delete identity: %w", err)
		}
		if n == 0 {
			return ErrNotFound // 并发解绑
		}
		reason := enum.RevokeIdentityUnbound
		revoked, err = q.RevokeSessionsByIdentity(ctx, db.RevokeSessionsByIdentityParams{UserID: p.UserID, IdentityID: identityID, Now: now, Reason: &reason})
		if err != nil {
			return fmt.Errorf("user: revoke identity sessions: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.afterRevokeAll(ctx, revoked, enum.RevokeIdentityUnbound, userEvent(p, meta))
	hint := ""
	switch {
	case target.SubjectDigest != nil:
		hint = audit.Hint(*target.SubjectDigest)
	case target.ProviderSubject != nil:
		hint = audit.Hint(*target.ProviderSubject)
	}
	s.record(ctx, audit.AuthenticationEvent{Type: enum.EventIdentityUnbound, Result: enum.ResultSuccess, UserID: p.UserID, SessionID: p.SessionID, IdentityID: target.ID, IdentityKind: target.Kind, SubjectHint: hint, IP: meta.IP, RequestID: meta.RequestID})
	return nil
}
