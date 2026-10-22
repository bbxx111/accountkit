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

// IdpCredential 是第三方凭证 oneof 的 wechat / apple / google 成员。
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
	case enum.IdentityGoogle:
		if s.d.Google == nil {
			return idp.Identity{}, fmt.Errorf("%w: google sign-in not configured", idp.ErrAppNotAllowed)
		}
		return s.verifyGoogle(ctx, cred)
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

// providerMeta 是 identity.provider_meta 的形状。openids 与 profile 共存。
type providerProfile struct {
	Name    string `json:"name,omitempty"`
	Picture string `json:"picture,omitempty"`
}

type providerMeta struct {
	OpenIDs map[string]string `json:"openids,omitempty"`
	Profile *providerProfile  `json:"profile,omitempty"`
}

// refreshProviderMeta 合并本次 openid，并按非空字段更新 Google profile。缺省字段保持原值。
func (s *Service) refreshProviderMeta(ctx context.Context, q *db.Queries, existing db.Identity, ident idp.Identity, now time.Time) error {
	var pm providerMeta
	if len(existing.ProviderMeta) > 0 {
		if err := json.Unmarshal(existing.ProviderMeta, &pm); err != nil {
			return fmt.Errorf("user: decode provider meta: %w", err)
		}
	}
	changed := false
	for app, openid := range ident.OpenIDs {
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
	if incoming := providerProfileFrom(ident); incoming != nil {
		if pm.Profile == nil {
			pm.Profile = &providerProfile{}
		}
		if incoming.Name != "" && pm.Profile.Name != incoming.Name {
			pm.Profile.Name = incoming.Name
			changed = true
		}
		if incoming.Picture != "" && pm.Profile.Picture != incoming.Picture {
			pm.Profile.Picture = incoming.Picture
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

func providerProfileFrom(ident idp.Identity) *providerProfile {
	if ident.Kind != enum.IdentityGoogle {
		return nil
	}
	name := sanitizeProviderName(ident.Name)
	var picture string
	if ident.Picture != "" {
		if value, err := normalizeAvatarURL(ident.Picture); err == nil {
			picture = value
		}
	}
	if name == "" && picture == "" {
		return nil
	}
	return &providerProfile{Name: name, Picture: picture}
}

func (s *Service) seedEmptyGoogleProfile(ctx context.Context, q *db.Queries, userID string, ident idp.Identity) error {
	profile := providerProfileFrom(ident)
	if profile == nil {
		return nil
	}
	row, err := q.LockUserByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("user: lock profile seed: %w", err)
	}
	if row.State != enum.UserActive {
		return nil
	}
	name := displayNameFromProvider(ident.Name)
	setName := name != "" && (row.DisplayName == nil || *row.DisplayName == "")
	setAvatar := profile.Picture != "" && (row.AvatarUrl == nil || *row.AvatarUrl == "")
	if !setName && !setAvatar {
		return nil
	}
	var display, avatar *string
	if setName {
		display = &name
	}
	if setAvatar {
		avatar = &profile.Picture
	}
	if _, err := q.UpdateUserProfile(ctx, db.UpdateUserProfileParams{
		SetDisplayName: setName, DisplayName: display, SetAvatarUrl: setAvatar, AvatarUrl: avatar,
		Now: s.now(), ID: userID, ActiveState: enum.UserActive,
	}); err != nil {
		return fmt.Errorf("user: seed google profile: %w", err)
	}
	return nil
}
