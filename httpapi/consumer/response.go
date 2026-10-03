package consumer

import (
	"errors"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/bbxx111/accountkit/httpapi/apierror"
	"github.com/bbxx111/accountkit/httpapi/authn"
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
		AccessToken: t.AccessToken, TokenType: "Bearer", ExpiresIn: t.ExpiresIn,
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
	CreateTime  string  `json:"create_time"`
	DeleteTime  *string `json:"delete_time"`
	PurgeTime   *string `json:"purge_time"`
}

func newMeResource(m user.Me) meResource {
	return meResource{
		Name: "users/" + m.ID, State: m.State.String(), DisplayName: m.DisplayName,
		CreateTime: fmtTime(m.CreateTime), DeleteTime: fmtTimePtr(m.DeleteTime), PurgeTime: fmtTimePtr(m.PurgeTime),
	}
}

type sessionResource struct {
	Name         string `json:"name"`
	DeviceID     string `json:"device_id"`
	DeviceName   string `json:"device_name"`
	CreateTime   string `json:"create_time"`
	LastUsedTime string `json:"last_used_time"`
	IsCurrent    bool   `json:"is_current"`
}

func newSessionResource(userID string, s user.SessionInfo) sessionResource {
	return sessionResource{
		Name: "users/" + userID + "/sessions/" + s.ID, DeviceID: s.DeviceID, DeviceName: s.DeviceName,
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
	return identityResource{Name: "users/" + userID + "/identities/" + i.ID, Kind: i.Kind.String(), MaskedSubject: i.MaskedSubject, CreateTime: fmtTime(i.CreateTime)}
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
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	apierror.WriteJSON(w, http.StatusOK, newTokenResponse(res))
}

// writeServiceError 把领域错误映射为 AIP-193 响应。
func (h *Handler) writeServiceError(w http.ResponseWriter, r *http.Request, err error) {
	var rl *code.RateLimitedError
	switch {
	case errors.As(err, &rl):
		apierror.Write(w, &apierror.Error{Status: apierror.StatusResourceExhausted, Reason: rl.Dimension, Message: "too many requests, retry later", RetryAfterSeconds: retryAfterSeconds(rl.RetryAfter)})
	case errors.Is(err, idp.ErrNonceReplayed):
		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, "IDP_NONCE_REPLAYED", "this sign-in attempt was already used; start again"))
	case errors.Is(err, idp.ErrAppNotAllowed):
		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, "IDP_APP_NOT_ALLOWED", "this application is not allowed to sign in"))
	case errors.Is(err, idp.ErrInvalidCredential):
		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, "IDP_CREDENTIAL_INVALID", "identity provider credential is invalid or expired"))
	case errors.Is(err, idp.ErrUnavailable):
		apierror.Write(w, &apierror.Error{Status: apierror.StatusUnavailable, Reason: "IDP_UNAVAILABLE", Message: "identity provider temporarily unavailable, retry later", RetryAfterSeconds: 1})
	case errors.Is(err, idp.ErrMisconfigured):
		apierror.WriteInternal(w, h.d.Logger, requestIDFrom(r.Context()), err)
	case errors.Is(err, code.ErrInvalid):
		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, "CODE_INVALID", "verification code is incorrect"))
	case errors.Is(err, code.ErrExpired):
		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, "CODE_EXPIRED", "verification code has expired or was never issued for this purpose"))
	case errors.Is(err, code.ErrExhausted):
		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, "CODE_ATTEMPTS_EXHAUSTED", "too many incorrect attempts; request a new code"))
	case errors.Is(err, user.ErrInvalidTarget):
		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, "INVALID_TARGET", "target is not a valid phone number or email address"))
	case errors.Is(err, user.ErrInvalidArgument):
		msg := strings.TrimPrefix(strings.TrimPrefix(err.Error(), user.ErrInvalidArgument.Error()), ": ")
		if msg == "" {
			msg = "invalid argument"
		}
		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, "INVALID_ARGUMENT", msg))
	case errors.Is(err, user.ErrNotAnchor):
		apierror.Write(w, apierror.New(apierror.StatusFailedPrecondition, "TARGET_NOT_ANCHOR", "target must be a phone or email already bound to this account"))
	case errors.Is(err, sender.ErrDisabled):
		apierror.Write(w, apierror.New(apierror.StatusFailedPrecondition, "CHANNEL_NOT_ENABLED", "verification channel is not enabled"))
	case errors.Is(err, user.ErrUserFrozen):
		apierror.Write(w, apierror.New(apierror.StatusPermissionDenied, "USER_FROZEN", "this account is frozen"))
	case errors.Is(err, user.ErrUserPendingDeletion):
		apierror.Write(w, apierror.New(apierror.StatusPermissionDenied, "USER_PENDING_DELETION", "this account is pending deletion; sign in with its phone or email to restore it"))
	case errors.Is(err, user.ErrInvalidState):
		apierror.Write(w, apierror.New(apierror.StatusFailedPrecondition, "INVALID_ACCOUNT_STATE", "operation not allowed in the account's current state"))
	case errors.Is(err, user.ErrIdentityConflict):
		apierror.Write(w, apierror.New(apierror.StatusAlreadyExists, "IDENTITY_ALREADY_BOUND", "this identity is already bound to another account; sign in with that account instead"))
	case errors.Is(err, user.ErrIdentityKindLimit):
		apierror.Write(w, apierror.New(apierror.StatusAlreadyExists, "IDENTITY_KIND_LIMIT", "the maximum number of identities of this kind is already bound"))
	case errors.Is(err, user.ErrLastAnchor):
		apierror.Write(w, apierror.New(apierror.StatusFailedPrecondition, "LAST_ANCHOR_IDENTITY", "cannot unbind the last phone or email identity"))
	case errors.Is(err, user.ErrNotFound):
		apierror.Write(w, apierror.New(apierror.StatusNotFound, "NOT_FOUND", "resource not found"))
	case errors.Is(err, user.ErrUnavailable):
		apierror.Write(w, &apierror.Error{Status: apierror.StatusUnavailable, Reason: "DEPENDENCY_UNAVAILABLE", Message: "service temporarily unavailable, retry later", RetryAfterSeconds: 1})
	case errors.Is(err, user.ErrInvalidToken):
		authn.WriteUnauthenticated(w, authn.ReasonTokenInvalid, "session is no longer valid")
	default:
		apierror.WriteInternal(w, h.d.Logger, requestIDFrom(r.Context()), err)
	}
}

// writeOAuthError 把领域错误映射为 RFC 6749 §5.2 响应（/token、/revoke）。
func (h *Handler) writeOAuthError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, errMalformedBody):
		apierror.WriteOAuth(w, http.StatusBadRequest, "invalid_request", "request body must be a single JSON object with known fields", nil)
	case errors.Is(err, user.ErrUserFrozen):
		apierror.WriteOAuth(w, http.StatusForbidden, "invalid_grant", "user is frozen", map[string]any{"reason": "USER_FROZEN"})
	case errors.Is(err, user.ErrInvalidGrant):
		apierror.WriteOAuth(w, http.StatusBadRequest, "invalid_grant", "refresh token is invalid, expired, or revoked", nil)
	case errors.Is(err, user.ErrUnavailable):
		w.Header().Set("Retry-After", "1")
		apierror.WriteOAuth(w, http.StatusServiceUnavailable, "temporarily_unavailable", "service temporarily unavailable, retry later", nil)
	default:
		h.d.Logger.Error("internal error", "request_id", requestIDFrom(r.Context()), "err", err)
		apierror.WriteOAuth(w, http.StatusInternalServerError, "server_error", "internal error", nil)
	}
}
