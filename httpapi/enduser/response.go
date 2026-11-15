package enduser

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/httpapi/apierror"
	"github.com/bbxx111/accountkit/httpapi/authn"
	"github.com/bbxx111/accountkit/httpapi/internal/resourcename"
	"github.com/bbxx111/accountkit/internal/httpcontract"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/code"
	"github.com/bbxx111/accountkit/user/idp"
	"github.com/bbxx111/accountkit/user/sender"
)

func fmtTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func fmtTimePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := fmtTime(*t)
	return &s
}

// tokenResponse 是 §2.2 的 token 响应（RFC 6749 字段名）。
type tokenResponse struct {
	AccessToken      string `json:"access_token"`
	TokenType        string `json:"token_type"`
	ExpiresIn        int    `json:"expires_in"`
	RefreshToken     string `json:"refresh_token,omitempty"`
	RefreshExpiresIn int    `json:"refresh_expires_in,omitempty"`
	Scope            string `json:"scope"`
	UserID           string `json:"user_id"`
	IsNewUser        bool   `json:"is_new_user"`
	HintEmail        string `json:"hint_email,omitempty"`
}

func newTokenResponse(t user.TokenResult) tokenResponse {
	return tokenResponse{
		AccessToken: t.AccessToken, TokenType: httpcontract.TokenTypeBearer, ExpiresIn: t.ExpiresIn,
		RefreshToken: t.RefreshToken, RefreshExpiresIn: t.RefreshExpiresIn,
		Scope: t.Scope, UserID: t.UserID, IsNewUser: t.IsNewUser,
		HintEmail: t.HintEmail,
	}
}

// meResource 是 users/me 的 C 端 DTO；显式选字段，绝不序列化领域结构体。
type meResource struct {
	Name        string  `json:"name"`
	State       string  `json:"state"`
	DisplayName string  `json:"display_name"`
	AvatarURL   string  `json:"avatar_url"`
	CreateTime  string  `json:"create_time"`
	DeleteTime  *string `json:"delete_time"`
	PurgeTime   *string `json:"purge_time"`
}

func newMeResource(m user.Me) meResource {
	return meResource{
		Name: resourcename.User(m.ID), State: m.State.String(), DisplayName: m.DisplayName, AvatarURL: m.AvatarURL,
		CreateTime: fmtTime(m.CreateTime), DeleteTime: fmtTimePtr(m.DeleteTime), PurgeTime: fmtTimePtr(m.PurgeTime),
	}
}

type sessionResource struct {
	ClientInfo           *clientInfoResource `json:"client_info,omitempty"`
	ClientInfoUpdateTime *string             `json:"client_info_update_time,omitempty"`
	Name                 string              `json:"name"`
	DeviceID             string              `json:"device_id"`
	DeviceName           string              `json:"device_name"`
	CreateTime           string              `json:"create_time"`
	LastUsedTime         string              `json:"last_used_time"`
	IsCurrent            bool                `json:"is_current"`
}

func newSessionResource(userID string, s user.SessionInfo) sessionResource {
	return sessionResource{
		ClientInfo: newClientInfoResource(s.ClientInfo), ClientInfoUpdateTime: fmtTimePtr(s.ClientInfoUpdateTime),
		Name: resourcename.Session(userID, s.ID), DeviceID: s.DeviceID, DeviceName: s.DeviceName,
		CreateTime: fmtTime(s.CreateTime), LastUsedTime: fmtTime(s.LastUsedTime), IsCurrent: s.IsCurrent,
	}
}

// identityResource 是 users/me/identities 的 C 端 DTO；第三方身份的 masked_subject 为空串。
type identityResource struct {
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	MaskedSubject string `json:"masked_subject"`
	CreateTime    string `json:"create_time"`
}

func newIdentityResource(userID string, i user.IdentityInfo) identityResource {
	return identityResource{Name: resourcename.Identity(userID, i.ID), Kind: i.Kind.String(), MaskedSubject: i.MaskedSubject, CreateTime: fmtTime(i.CreateTime)}
}

type identityList struct {
	Identities []identityResource `json:"identities"`
}

func retryAfterSeconds(d time.Duration) int {
	s := int(math.Ceil(d.Seconds()))
	if s < 1 {
		return 1
	}
	return s
}

// writeToken 写 token 响应；凭证响应必须 no-store（RFC 6749 §5.1）。
func writeToken(w http.ResponseWriter, res user.TokenResult) {
	httpcontract.SetOAuthNoStore(w)
	apierror.WriteJSON(w, http.StatusOK, newTokenResponse(res))
}

type pendingSignInResponse struct {
	NextAction            string   `json:"next_action"`
	SignInCompletionToken string   `json:"sign_in_completion_token"`
	ExpireTime            string   `json:"expire_time"`
	EnabledIdentityKinds  []string `json:"enabled_identity_kinds"`
	RequiredIdentityKinds []string `json:"required_identity_kinds"`
	HintEmail             string   `json:"hint_email,omitempty"`
}

func identityKindNames(kinds []enum.IdentityKind) []string {
	names := make([]string, len(kinds))
	for i, kind := range kinds {
		names[i] = kind.String()
	}
	return names
}

func (h *Handler) writeSignIn(w http.ResponseWriter, r *http.Request, res user.SignInResult) {
	if (res.Tokens == nil) == (res.Pending == nil) {
		apierror.WriteInternal(w, h.d.Logger, requestIDFrom(r.Context()), errors.New("enduser: sign-in result must contain exactly one member"))
		return
	}
	if res.Tokens != nil {
		writeToken(w, *res.Tokens)
		return
	}
	p := res.Pending
	httpcontract.SetOAuthNoStore(w)
	apierror.WriteJSON(w, http.StatusOK, pendingSignInResponse{
		NextAction:            nextActionCompleteIdentity,
		SignInCompletionToken: p.SignInCompletionToken,
		ExpireTime:            fmtTime(p.ExpireTime),
		EnabledIdentityKinds:  identityKindNames(p.EnabledIdentityKinds),
		RequiredIdentityKinds: identityKindNames(p.RequiredIdentityKinds),
		HintEmail:             p.HintEmail,
	})
}

// writeServiceError 把领域错误映射为 AIP-193 响应。
func (h *Handler) writeServiceError(w http.ResponseWriter, r *http.Request, err error) {
	var rl *code.RateLimitedError
	switch {
	case errors.Is(err, user.ErrInvalidClientInfo):
		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, reasonInvalidClientInfo, "invalid client information"))
	case errors.Is(err, user.ErrSignInCompletionInvalid):
		apierror.Write(w, apierror.New(apierror.StatusFailedPrecondition, reasonSignInCompletionInvalid, "sign-in completion token is missing, invalid, or expired"))
	case errors.Is(err, user.ErrSignInAlreadyCompleted):
		apierror.Write(w, apierror.New(apierror.StatusFailedPrecondition, reasonSignInAlreadyCompleted, "sign-in is already completed; start again"))
	case errors.Is(err, user.ErrIdentityKindNotEnabled):
		apierror.Write(w, apierror.New(apierror.StatusFailedPrecondition, reasonIdentityKindNotEnabled, "this identity kind is not enabled"))
	case errors.As(err, &rl):
		apierror.Write(w, &apierror.Error{Status: apierror.StatusResourceExhausted, Reason: rl.Dimension, Message: "too many requests, retry later", RetryAfterSeconds: retryAfterSeconds(rl.RetryAfter)})
	case errors.Is(err, idp.ErrMalformedCredential):
		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, reasonCredentialIncomplete, "credential is malformed or incomplete"))
	case errors.Is(err, idp.ErrNonceReplayed):
		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, reasonIDPNonceReplayed, "this sign-in attempt was already used; start again"))
	case errors.Is(err, idp.ErrAppNotAllowed):
		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, reasonIDPAppNotAllowed, "this application is not allowed to sign in"))
	case errors.Is(err, idp.ErrInvalidCredential):
		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, reasonIDPCredentialInvalid, "identity provider credential is invalid or expired"))
	case errors.Is(err, idp.ErrUnavailable):
		apierror.Write(w, &apierror.Error{Status: apierror.StatusUnavailable, Reason: reasonIDPUnavailable, Message: "identity provider temporarily unavailable, retry later", RetryAfterSeconds: 1})
	case errors.Is(err, idp.ErrMisconfigured):
		apierror.WriteInternal(w, h.d.Logger, requestIDFrom(r.Context()), err)
	case errors.Is(err, code.ErrInvalid):
		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, reasonCodeInvalid, "verification code is incorrect"))
	case errors.Is(err, code.ErrExpired):
		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, reasonCodeExpired, "verification code has expired or was never issued for this purpose"))
	case errors.Is(err, user.ErrInvalidTarget):
		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, reasonInvalidTarget, "target is not a valid phone number or email address"))
	case errors.Is(err, user.ErrInvalidArgument):
		msg := strings.TrimPrefix(strings.TrimPrefix(err.Error(), user.ErrInvalidArgument.Error()), ": ")
		if msg == "" {
			msg = "invalid argument"
		}
		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, httpcontract.ReasonInvalidArgument, msg))
	case errors.Is(err, user.ErrInsufficientScope):
		apierror.Write(w, apierror.New(apierror.StatusPermissionDenied, httpcontract.ReasonInsufficientScope, "token scope does not permit this operation"))
	case errors.Is(err, user.ErrReauthenticationRequired):
		apierror.Write(w, apierror.New(apierror.StatusFailedPrecondition, httpcontract.ReasonReauthenticationRequired, "recent authentication required for this operation"))
	case errors.Is(err, user.ErrNotAnchor):
		apierror.Write(w, apierror.New(apierror.StatusFailedPrecondition, reasonTargetNotAnchor, "target must be a phone or email already bound to this account"))
	case errors.Is(err, sender.ErrDisabled):
		apierror.Write(w, apierror.New(apierror.StatusFailedPrecondition, reasonChannelNotEnabled, "verification channel is not enabled"))
	case errors.Is(err, sender.ErrTargetNotSupported):
		apierror.Write(w, apierror.New(apierror.StatusFailedPrecondition, reasonTargetNotSupported, "verification target is not supported by this deployment"))
	case errors.Is(err, user.ErrUserFrozen):
		apierror.Write(w, apierror.New(apierror.StatusPermissionDenied, httpcontract.ReasonUserFrozen, "this account is frozen"))
	case errors.Is(err, user.ErrUserPendingDeletion):
		apierror.Write(w, apierror.New(apierror.StatusPermissionDenied, reasonUserPendingDeletion, "this account is pending deletion; sign in with its phone or email to restore it"))
	case errors.Is(err, user.ErrDeletionBlocked):
		apierror.Write(w, apierror.New(apierror.StatusFailedPrecondition, httpcontract.ReasonDeletionBlocked, "account deletion is blocked"))
	case errors.Is(err, user.ErrInvalidState):
		apierror.Write(w, apierror.New(apierror.StatusFailedPrecondition, httpcontract.ReasonInvalidAccountState, "operation not allowed in the account's current state"))
	case errors.Is(err, user.ErrIdentityConflict):
		apierror.Write(w, apierror.New(apierror.StatusAlreadyExists, reasonIdentityAlreadyBound, "this identity is already bound to another account; sign in with that account instead"))
	case errors.Is(err, user.ErrIdentityKindLimit):
		apierror.Write(w, apierror.New(apierror.StatusAlreadyExists, reasonIdentityKindLimit, "the maximum number of identities of this kind is already bound"))
	case errors.Is(err, user.ErrLastAnchor):
		apierror.Write(w, apierror.New(apierror.StatusFailedPrecondition, reasonLastAnchorIdentity, "the account must retain at least one usable identity and satisfy its identity policy"))
	case errors.Is(err, user.ErrNotFound):
		apierror.Write(w, apierror.New(apierror.StatusNotFound, httpcontract.ReasonNotFound, "resource not found"))
	case errors.Is(err, user.ErrUnavailable):
		apierror.Write(w, &apierror.Error{Status: apierror.StatusUnavailable, Reason: httpcontract.ReasonDependencyUnavailable, Message: "service temporarily unavailable, retry later", RetryAfterSeconds: 1})
	case errors.Is(err, user.ErrInvalidToken):
		authn.WriteUnauthenticated(w, authn.ReasonTokenInvalid, "session is no longer valid")
	default:
		apierror.WriteInternal(w, h.d.Logger, requestIDFrom(r.Context()), err)
	}
}

// writeOAuthError 把领域错误映射为 RFC 6749 §5.2 响应（/token、/revoke）。
func (h *Handler) writeOAuthError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, user.ErrInvalidClientInfo):
		apierror.WriteOAuth(w, http.StatusBadRequest, httpcontract.OAuthErrorInvalidRequest, "invalid client information", nil)
	case errors.Is(err, errMalformedBody):
		apierror.WriteOAuth(w, http.StatusBadRequest, httpcontract.OAuthErrorInvalidRequest, "request body must be application/json (a single object with known fields) or application/x-www-form-urlencoded (no repeated parameters)", nil)
	case errors.Is(err, user.ErrUserFrozen):
		apierror.WriteOAuth(w, http.StatusForbidden, httpcontract.OAuthErrorInvalidGrant, "user is frozen", map[string]any{"reason": httpcontract.ReasonUserFrozen})
	case errors.Is(err, user.ErrInvalidGrant):
		apierror.WriteOAuth(w, http.StatusBadRequest, httpcontract.OAuthErrorInvalidGrant, "refresh token is invalid, expired, or revoked", nil)
	case errors.Is(err, user.ErrUnavailable):
		w.Header().Set("Retry-After", "1")
		apierror.WriteOAuth(w, http.StatusServiceUnavailable, httpcontract.OAuthErrorTemporarilyUnavailable, "service temporarily unavailable, retry later", nil)
	default:
		h.d.Logger.Error("internal error", "request_id", requestIDFrom(r.Context()), "err", err)
		apierror.WriteOAuth(w, http.StatusInternalServerError, httpcontract.OAuthErrorServerError, httpcontract.MessageInternal, nil)
	}
}

// challengeResponse 是发码成功的凭证 DTO。
type challengeResponse struct {
	CodeID     string    `json:"code_id"`
	ExpireTime time.Time `json:"expire_time"`
}

func writeChallenge(w http.ResponseWriter, c user.CodeChallenge) {
	httpcontract.SetNoStore(w)
	apierror.WriteJSON(w, http.StatusOK, challengeResponse{CodeID: c.CodeID, ExpireTime: c.ExpireTime})
}

// clientInfoResource 显式定义嵌套客户端描述；两面各自维护 DTO。
type clientInfoResource struct {
	Type       string                     `json:"type,omitempty"`
	OS         *clientSoftwareResource    `json:"os,omitempty"`
	Device     *clientDeviceResource      `json:"device,omitempty"`
	App        *clientAppResource         `json:"app,omitempty"`
	Browser    *clientSoftwareResource    `json:"browser,omitempty"`
	Extensions map[string]json.RawMessage `json:"extensions,omitempty"`
}

type clientSoftwareResource struct {
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
}
type clientAppResource struct {
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
	Build   string `json:"build,omitempty"`
}
type clientDeviceResource struct {
	Type         string `json:"type,omitempty"`
	Manufacturer string `json:"manufacturer,omitempty"`
	Model        string `json:"model,omitempty"`
}

func newClientInfoResource(info *user.ClientInfo) *clientInfoResource {
	if info == nil {
		return nil
	}
	out := &clientInfoResource{}
	if info.ClientType.Valid() {
		out.Type = info.ClientType.String()
	}
	if info.OSName != "" || info.OSVersion != "" {
		out.OS = &clientSoftwareResource{Name: info.OSName, Version: info.OSVersion}
	}
	if info.BrowserName != "" || info.BrowserVersion != "" {
		out.Browser = &clientSoftwareResource{Name: info.BrowserName, Version: info.BrowserVersion}
	}
	if info.AppName != "" || info.AppVersion != "" || info.AppBuild != "" {
		out.App = &clientAppResource{Name: info.AppName, Version: info.AppVersion, Build: info.AppBuild}
	}
	if info.DeviceType.Valid() || info.DeviceManufacturer != "" || info.DeviceModel != "" {
		out.Device = &clientDeviceResource{Manufacturer: info.DeviceManufacturer, Model: info.DeviceModel}
		if info.DeviceType.Valid() {
			out.Device.Type = info.DeviceType.String()
		}
	}
	if len(info.Extensions) > 0 {
		out.Extensions = make(map[string]json.RawMessage, len(info.Extensions))
		for k, v := range info.Extensions {
			out.Extensions[k] = append(json.RawMessage(nil), v...)
		}
	}
	return out
}
