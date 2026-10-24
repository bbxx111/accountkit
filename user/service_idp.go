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

// IdpCredential 是第三方类型的凭证；Credential 为客户端 credential 对象的原始 JSON，由提供方解析。
type IdpCredential struct {
	Kind       enum.IdentityKind
	Credential json.RawMessage
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
	return s.signInVerified(ctx, completionProof{Kind: ident.Kind, Provider: &ident}, ident.ExpireTime, dev, meta)
}

// verifyIdp 按 kind 分派到已配置的校验器。
func (s *Service) verifyIdp(ctx context.Context, cred IdpCredential) (idp.Identity, error) {
	if err := s.requireIdentityEnabled(cred.Kind); err != nil {
		return idp.Identity{}, err
	}
	if cred.Kind.IsAnchor() {
		return idp.Identity{}, fmt.Errorf("%w: kind %s is not an identity provider", ErrInvalidArgument, cred.Kind)
	}
	provider := s.providers[cred.Kind]
	if provider == nil {
		return idp.Identity{}, fmt.Errorf("%w: %s sign-in not configured", idp.ErrAppNotAllowed, cred.Kind)
	}
	ident, err := s.verifyProvider(ctx, cred.Kind, provider, cred.Credential)
	if err != nil {
		return idp.Identity{}, err
	}
	// Pending proofs and later backfill retain only the current, sanitized claims.
	profile := providerProfileFrom(ident)
	ident.Name, ident.Picture = "", ""
	if profile != nil {
		ident.Name, ident.Picture = profile.Name, profile.Picture
	}
	return ident, nil
}

// backfillProviderProfile 用本次声明填充空的显示名与头像：新建账号始终填充，
// 已有账号仅在 ProviderProfileBackfill 开启时补填。
func (s *Service) backfillProviderProfile(ctx context.Context, q *db.Queries, userID string, ident idp.Identity, newAccount bool) error {
	if !newAccount && !s.providerProfileBackfill {
		return nil
	}
	return s.seedEmptyProviderProfile(ctx, q, userID, ident)
}

// providerFailure 隔离宿主提供方的非哨兵错误：保留原文供日志，但不解包，
// 避免其包装的 user/idp 哨兵改变 HTTP 状态或把内部文本带进响应。
type providerFailure struct {
	kind enum.IdentityKind
	err  error
}

func (e providerFailure) Error() string {
	return fmt.Sprintf("user: identity provider %s failed: %v", e.kind, e.err)
}

func isolateProviderError(kind enum.IdentityKind, err error) error {
	for _, sentinel := range []error{idp.ErrInvalidCredential, idp.ErrAppNotAllowed, idp.ErrNonceReplayed, idp.ErrUnavailable, idp.ErrMisconfigured, idp.ErrMalformedCredential, context.Canceled, context.DeadlineExceeded} {
		if errors.Is(err, sentinel) {
			return err
		}
	}
	return providerFailure{kind: kind, err: err}
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
	UnionID string            `json:"unionid,omitempty"`
	OpenIDs map[string]string `json:"openids,omitempty"`
	Profile *providerProfile  `json:"profile,omitempty"`
}

// refreshProviderMeta 合并本次 openid，并按非空字段更新提供方 profile。缺省字段保持原值。
func (s *Service) refreshProviderMeta(ctx context.Context, q *db.Queries, existing db.Identity, ident idp.Identity, now time.Time) error {
	pm, changed, err := mergeProviderMeta(existing.ProviderMeta, ident)
	if err != nil {
		return err
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

// mergeProviderMeta 合并白名单元数据；标识矛盾拒绝整个操作。
func mergeProviderMeta(existing []byte, ident idp.Identity) (providerMeta, bool, error) {
	var pm providerMeta
	if len(existing) > 0 {
		if err := json.Unmarshal(existing, &pm); err != nil {
			return providerMeta{}, false, fmt.Errorf("user: decode provider meta: %w", err)
		}
	}
	changed := false
	if ident.UnionID != "" {
		if pm.UnionID != "" && pm.UnionID != ident.UnionID {
			return providerMeta{}, false, ErrIdentityConflict
		}
		if pm.UnionID != ident.UnionID {
			pm.UnionID = ident.UnionID
			changed = true
		}
	}
	for app, openid := range ident.OpenIDs {
		if openid == "" {
			continue
		}
		if pm.OpenIDs == nil {
			pm.OpenIDs = map[string]string{}
		}
		if previous := pm.OpenIDs[app]; previous != "" && previous != openid {
			return providerMeta{}, false, ErrIdentityConflict
		}
		if pm.OpenIDs[app] != openid {
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
	return pm, changed, nil
}

// providerProfileFrom 不按类型区分：任何提供方返回的 name/picture 都按同一规则净化。
func providerProfileFrom(ident idp.Identity) *providerProfile {
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

func (s *Service) seedEmptyProviderProfile(ctx context.Context, q *db.Queries, userID string, ident idp.Identity) error {
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
		return fmt.Errorf("user: seed provider profile: %w", err)
	}
	return nil
}
