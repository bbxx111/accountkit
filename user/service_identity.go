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

// SendBindCode 向即将绑定的新锚点发送 BIND 码。不预检占用（避免枚举），冲突在绑定时报告。
func (s *Service) SendBindCode(ctx context.Context, p Principal, channel enum.IdentityKind, target string, meta Meta) error {
	norm, _, _, err := s.normalizeTarget(channel, target)
	if err != nil {
		return err
	}
	return s.sendCode(ctx, channel, enum.PurposeBind, norm, p.UserID, meta)
}

// createAnchorIdentity 创建 PHONE/EMAIL 身份行：digest + 密文 + hint，均带密钥版本。
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
func (s *Service) BindWithCode(ctx context.Context, p Principal, channel enum.IdentityKind, target, plainCode string, meta Meta) (IdentityInfo, bool, error) {
	norm, hintPrefix, hintSuffix, err := s.normalizeTarget(channel, target)
	if err != nil {
		return IdentityInfo{}, false, err
	}
	digest, _ := s.d.Digester.Digest(norm)
	ev := audit.Event{UserID: p.UserID, SessionID: p.SessionID, IdentityKind: channel, SubjectHint: audit.Hint(digest), IP: meta.IP, RequestID: meta.RequestID}
	if reason, err := s.verifyCode(ctx, channel, enum.PurposeBind, norm, plainCode); err != nil {
		if !errors.Is(err, ErrUnavailable) {
			ev.Type, ev.Result, ev.Reason = enum.EventIdentityBindRejected, enum.ResultFailure, reason
			s.record(ctx, ev)
		}
		return IdentityInfo{}, false, err
	}
	var info IdentityInfo
	var created bool
	err = s.d.Repo.WithTx(ctx, func(q *db.Queries) error {
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
	ev.Type, ev.Result = enum.EventIdentityBound, enum.ResultSuccess
	if !created {
		ev.Reason = "ALREADY_BOUND"
	}
	s.record(ctx, ev)
	return info, created, nil
}

// createProviderIdentity 创建 WECHAT/APPLE 身份行：provider_subject 明文 + provider_meta（空 openid 不落库）。
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
	if len(openIDs) > 0 {
		meta, err = json.Marshal(providerMeta{OpenIDs: openIDs})
		if err != nil {
			return db.Identity{}, fmt.Errorf("user: encode provider meta: %w", err)
		}
	}
	row, err := q.CreateIdentity(ctx, db.CreateIdentityParams{ID: iid, UserID: userID, Kind: ident.Kind, ProviderSubject: &subject, ProviderMeta: meta})
	if err != nil {
		return db.Identity{}, err
	}
	return row, nil
}

// BindWithIdp 把第三方身份绑定到当前账号。
func (s *Service) BindWithIdp(ctx context.Context, p Principal, cred IdpCredential, meta Meta) (IdentityInfo, bool, error) {
	ev := audit.Event{UserID: p.UserID, SessionID: p.SessionID, IdentityKind: cred.Kind, IP: meta.IP, RequestID: meta.RequestID}
	ident, err := s.verifyIdp(ctx, cred)
	if err != nil {
		if reason := idpFailureReason(err); reason != "" {
			ev.Type, ev.Result, ev.Reason = enum.EventIdentityBindRejected, enum.ResultFailure, reason
			s.record(ctx, ev)
		}
		return IdentityInfo{}, false, err
	}
	ev.SubjectHint = audit.Hint(ident.Subject)
	now := s.now()
	var info IdentityInfo
	var created bool
	err = s.d.Repo.WithTx(ctx, func(q *db.Queries) error {
		_, idents, err := s.bindGuard(ctx, q, p.UserID)
		if err != nil {
			return err
		}
		subject := ident.Subject
		existing, err := q.FindActiveIdentityByProviderSubject(ctx, db.FindActiveIdentityByProviderSubjectParams{Kind: ident.Kind, ProviderSubject: &subject})
		switch {
		case err == nil && existing.UserID == p.UserID:
			if err := s.mergeOpenIDs(ctx, q, existing, ident.OpenIDs, now); err != nil {
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
	ev.Type, ev.Result = enum.EventIdentityBound, enum.ResultSuccess
	if !created {
		ev.Reason = "ALREADY_BOUND"
	}
	s.record(ctx, ev)
	return info, created, nil
}

// UnbindIdentity 软删除本人的一个身份；解绑后必须仍有至少一个 PHONE/EMAIL 锚点。
func (s *Service) UnbindIdentity(ctx context.Context, p Principal, identityID string, meta Meta) error {
	if !ids.Valid(ids.Identity, identityID) {
		return fmt.Errorf("%w: malformed identity id", ErrInvalidArgument)
	}
	now := s.now()
	var target db.Identity
	err := s.d.Repo.WithTx(ctx, func(q *db.Queries) error {
		u, err := lockUser(ctx, q, p.UserID)
		if err != nil {
			return err
		}
		if err := accountMayAct(u); err != nil {
			return err
		}
		row, err := q.GetActiveIdentityByIDAndUser(ctx, db.GetActiveIdentityByIDAndUserParams{ID: identityID, UserID: p.UserID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("user: get identity: %w", err)
		}
		target = row
		if row.Kind.IsAnchor() {
			idents, err := q.ListActiveIdentitiesByUser(ctx, p.UserID)
			if err != nil {
				return fmt.Errorf("user: list identities: %w", err)
			}
			otherAnchor := false
			for _, id := range idents {
				if id.ID != row.ID && id.Kind.IsAnchor() {
					otherAnchor = true
					break
				}
			}
			if !otherAnchor {
				return ErrLastAnchor
			}
		}
		n, err := q.SoftDeleteIdentity(ctx, db.SoftDeleteIdentityParams{ID: identityID, UserID: p.UserID, Now: now})
		if err != nil {
			return fmt.Errorf("user: soft delete identity: %w", err)
		}
		if n == 0 {
			return ErrNotFound // 并发解绑
		}
		return nil
	})
	if err != nil {
		return err
	}
	hint := ""
	switch {
	case target.SubjectDigest != nil:
		hint = audit.Hint(*target.SubjectDigest)
	case target.ProviderSubject != nil:
		hint = audit.Hint(*target.ProviderSubject)
	}
	s.record(ctx, audit.Event{Type: enum.EventIdentityUnbound, Result: enum.ResultSuccess, UserID: p.UserID, SessionID: p.SessionID, IdentityKind: target.Kind, SubjectHint: hint, IP: meta.IP, RequestID: meta.RequestID})
	return nil
}
