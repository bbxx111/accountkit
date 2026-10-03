package user

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/ids"
	"github.com/bbxx111/accountkit/user/db"
	"github.com/bbxx111/accountkit/user/idp"
)

// IdpCredential 是 §2.1 凭证 oneof 的 wechat / apple 成员。
type IdpCredential struct {
	Kind    enum.IdentityKind
	AppID   string
	Code    string
	IDToken string
	Nonce   string
}

// SignInWithIdp 用第三方凭证登录即注册。无锚点的账号得到 scope user:bind。
func (s *Service) SignInWithIdp(ctx context.Context, cred IdpCredential, dev Device, meta Meta) (TokenResult, error) {
	if err := validDevice(dev); err != nil {
		return TokenResult{}, err
	}
	ev := audit.Event{IdentityKind: cred.Kind, IP: meta.IP, DeviceID: dev.ID, RequestID: meta.RequestID}
	ident, err := s.verifyIdp(ctx, cred)
	if err != nil {
		if reason := idpFailureReason(err); reason != "" {
			ev.Type, ev.Result, ev.Reason = enum.EventSignInFailed, enum.ResultFailure, reason
			s.record(ctx, ev)
		}
		return TokenResult{}, err
	}
	ev.SubjectHint = audit.Hint(ident.Subject)

	now := s.now()
	var res TokenResult
	var replaced []string
	err = s.withRetryOnUnique(ctx, func(q *db.Queries) error {
		replaced = replaced[:0]
		u, isNew, err := s.findOrCreateUserByProvider(ctx, q, ident, now)
		if err != nil {
			return err
		}
		ev.UserID = u.ID
		if u.State == enum.UserPendingDeletion {
			// §1.1：冷静期账号只接受锚点身份登录（得到 user:undelete）；第三方身份一律拒绝、不建会话——
			// 否则任何曾绑定的微信/Apple 都能替本人"取消注销"。
			return ErrUserPendingDeletion
		}
		hasAnchor := false
		if !isNew {
			if hasAnchor, err = s.userHasAnchor(ctx, q, u.ID); err != nil {
				return err
			}
		}
		sess, r, rep, err := s.establishSession(ctx, q, u, hasAnchor, dev, now)
		if err != nil {
			return err
		}
		res, replaced = r, rep
		res.IsNewUser = isNew
		res.HintEmail = ident.HintEmail
		ev.UserID, ev.SessionID = u.ID, sess.ID
		return nil
	})
	if err != nil {
		if reason := signInRejectReason(err); reason != "" {
			ev.Type, ev.Result, ev.Reason = enum.EventSignInFailed, enum.ResultFailure, reason
			s.record(ctx, ev)
		}
		return TokenResult{}, err
	}
	s.finishSignIn(ctx, ev, res, replaced, dev, meta)
	return res, nil
}

// verifyIdp 按 kind 分派到已配置的校验器。
func (s *Service) verifyIdp(ctx context.Context, cred IdpCredential) (idp.Identity, error) {
	switch cred.Kind {
	case enum.IdentityWeChat:
		if s.d.WeChat == nil {
			return idp.Identity{}, fmt.Errorf("%w: wechat sign-in not configured", idp.ErrAppNotAllowed)
		}
		return s.d.WeChat.VerifyWeChat(ctx, cred.AppID, cred.Code)
	case enum.IdentityApple:
		if s.d.Apple == nil {
			return idp.Identity{}, fmt.Errorf("%w: apple sign-in not configured", idp.ErrAppNotAllowed)
		}
		return s.d.Apple.VerifyApple(ctx, cred.IDToken, cred.Nonce)
	default:
		return idp.Identity{}, fmt.Errorf("%w: kind %s is not an identity provider", ErrInvalidArgument, cred.Kind)
	}
}

// idpFailureReason 把可审计的 IdP 拒绝映射为 reason；基础设施/配置错误返回空（只进日志）。
func idpFailureReason(err error) string {
	switch {
	case errors.Is(err, idp.ErrNonceReplayed):
		return "IDP_NONCE_REPLAYED"
	case errors.Is(err, idp.ErrAppNotAllowed):
		return "IDP_APP_NOT_ALLOWED"
	case errors.Is(err, idp.ErrInvalidCredential):
		return "IDP_CREDENTIAL_INVALID"
	}
	return ""
}

// providerMeta 是 identity.provider_meta 的形状（§3.2）。
type providerMeta struct {
	OpenIDs map[string]string `json:"openids,omitempty"`
}

// findOrCreateUserByProvider 按 (kind, provider_subject) 查身份；命中则锁用户并增量合并 openid；
// 不存在则创建账号 + 第三方身份（subject 明文，无 digest/密文）。
func (s *Service) findOrCreateUserByProvider(ctx context.Context, q *db.Queries, ident idp.Identity, now time.Time) (db.UserAccount, bool, error) {
	subject := ident.Subject
	existing, err := q.FindActiveIdentityByProviderSubject(ctx, db.FindActiveIdentityByProviderSubjectParams{Kind: ident.Kind, ProviderSubject: &subject})
	switch {
	case err == nil:
		u, err := q.LockUserByID(ctx, existing.UserID)
		if err != nil {
			return db.UserAccount{}, false, fmt.Errorf("user: lock user %s: %w", existing.UserID, err)
		}
		if err := s.mergeOpenIDs(ctx, q, existing, ident.OpenIDs, now); err != nil {
			return db.UserAccount{}, false, err
		}
		return u, false, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return db.UserAccount{}, false, fmt.Errorf("user: find provider identity: %w", err)
	}
	uid, err := ids.New(ids.User)
	if err != nil {
		return db.UserAccount{}, false, err
	}
	u, err := q.CreateUser(ctx, db.CreateUserParams{ID: uid, State: enum.UserActive})
	if err != nil {
		return db.UserAccount{}, false, err // 唯一冲突由 withRetryOnUnique 重试
	}
	if _, err := s.createProviderIdentity(ctx, q, uid, ident); err != nil {
		return db.UserAccount{}, false, err // 并发首次登录同一 subject → 唯一冲突 → 重试后走"已存在"分支
	}
	return u, true, nil
}

// mergeOpenIDs 把本次登录 App 的 openid 并入 provider_meta.openids（只增不改）。
func (s *Service) mergeOpenIDs(ctx context.Context, q *db.Queries, existing db.Identity, incoming map[string]string, now time.Time) error {
	if len(incoming) == 0 {
		return nil
	}
	var pm providerMeta
	if len(existing.ProviderMeta) > 0 {
		if err := json.Unmarshal(existing.ProviderMeta, &pm); err != nil {
			return fmt.Errorf("user: decode provider meta: %w", err)
		}
	}
	changed := false
	for app, openid := range incoming {
		if openid == "" {
			continue
		}
		if pm.OpenIDs == nil {
			pm.OpenIDs = map[string]string{}
		}
		if _, seen := pm.OpenIDs[app]; !seen {
			pm.OpenIDs[app] = openid
			changed = true
		}
	}
	if !changed {
		return nil
	}
	b, err := json.Marshal(pm)
	if err != nil {
		return fmt.Errorf("user: encode provider meta: %w", err)
	}
	if _, err := q.UpdateIdentityProviderMeta(ctx, db.UpdateIdentityProviderMetaParams{ID: existing.ID, ProviderMeta: b, UpdateTime: now}); err != nil {
		return fmt.Errorf("user: merge provider meta: %w", err)
	}
	return nil
}

// userHasAnchor 判断用户是否有 PHONE/EMAIL 活动身份。
func (s *Service) userHasAnchor(ctx context.Context, q *db.Queries, userID string) (bool, error) {
	idents, err := q.ListActiveIdentitiesByUser(ctx, userID)
	if err != nil {
		return false, fmt.Errorf("user: list identities: %w", err)
	}
	for _, id := range idents {
		if id.Kind.IsAnchor() {
			return true, nil
		}
	}
	return false, nil
}
