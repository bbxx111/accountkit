package user

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/enum"
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

// signInWithIdp 验证第三方身份，按完整性策略选择登录、直接注册或待补全。
func (s *Service) signInWithIdp(ctx context.Context, cred IdpCredential, dev Device, meta Meta) (SignInResult, error) {
	normalizedClientInfo, clientInfoErr := NormalizeClientInfo(meta.ClientInfo)
	if clientInfoErr != nil {
		return SignInResult{}, clientInfoErr
	}
	meta.ClientInfo = normalizedClientInfo
	if err := validDevice(dev); err != nil {
		return SignInResult{}, err
	}
	ev := audit.AuthenticationEvent{IdentityKind: cred.Kind, IP: meta.IP, DeviceID: dev.ID, RequestID: meta.RequestID}
	ident, err := s.verifyIdp(ctx, cred)
	if err != nil {
		if reason := idpFailureReason(err); reason != "" {
			ev.Type, ev.Result, ev.Reason = enum.EventSignInFailed, enum.ResultFailure, reason
			s.record(ctx, ev)
		}
		return SignInResult{}, err
	}
	if ident.Kind != cred.Kind || ident.Subject == "" {
		return SignInResult{}, idp.ErrInvalidCredential
	}
	return s.signInVerified(ctx, completionProof{Kind: ident.Kind, Provider: &ident}, ident.ExpireTime, dev, meta)
}

// verifyIdp 按 kind 分派到已配置的校验器。
func (s *Service) verifyIdp(ctx context.Context, cred IdpCredential) (idp.Identity, error) {
	if err := s.requireIdentityEnabled(cred.Kind); err != nil {
		return idp.Identity{}, err
	}
	switch cred.Kind {
	case enum.IdentityWeChat:
		if s.d.WeChat == nil {
			return idp.Identity{}, fmt.Errorf("%w: wechat sign-in not configured", idp.ErrAppNotAllowed)
		}
		return s.verifyWeChat(ctx, cred)
	case enum.IdentityApple:
		if s.d.Apple == nil {
			return idp.Identity{}, fmt.Errorf("%w: apple sign-in not configured", idp.ErrAppNotAllowed)
		}
		return s.verifyApple(ctx, cred)
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
