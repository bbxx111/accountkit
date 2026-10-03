// Package consumer 提供 C 端 /v1 的 HTTP handler（只做 HTTP 翻译）。
//
// 路由相对、无前缀；宿主 r.Mount("/v1", h.Router())。业务规则全部在 user.Service。
package consumer

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/httpapi/apierror"
	"github.com/bbxx111/accountkit/httpapi/authn"
	"github.com/bbxx111/accountkit/httpapi/reqid"
	"github.com/bbxx111/accountkit/user"
)

// Service 是 handler 依赖的领域面；*user.Service 满足它。
type Service interface {
	SendSignInCode(ctx context.Context, channel enum.IdentityKind, target string, meta user.Meta) error
	SignInWithCode(ctx context.Context, channel enum.IdentityKind, target, code string, dev user.Device, meta user.Meta) (user.TokenResult, error)
	SignInWithIdp(ctx context.Context, cred user.IdpCredential, dev user.Device, meta user.Meta) (user.TokenResult, error)
	Refresh(ctx context.Context, refreshToken string, meta user.Meta) (user.TokenResult, error)
	Revoke(ctx context.Context, refreshToken string, meta user.Meta) error
	Authenticate(ctx context.Context, rawAccess string) (user.Principal, error)
	GetMe(ctx context.Context, userID string) (user.Me, error)
	UpdateDisplayName(ctx context.Context, userID, name string) (user.Me, error)
	SendReauthenticationCode(ctx context.Context, p user.Principal, channel enum.IdentityKind, target string, meta user.Meta) error
	Reauthenticate(ctx context.Context, p user.Principal, channel enum.IdentityKind, target, code string, meta user.Meta) (user.TokenResult, error)
	ListSessions(ctx context.Context, userID, currentSID string) ([]user.SessionInfo, error)
	RevokeSession(ctx context.Context, userID, sid string, meta user.Meta) error
	RevokeOtherSessions(ctx context.Context, userID, currentSID string, meta user.Meta) error
	ListIdentities(ctx context.Context, userID string) ([]user.IdentityInfo, error)
	SendBindCode(ctx context.Context, p user.Principal, channel enum.IdentityKind, target string, meta user.Meta) error
	BindWithCode(ctx context.Context, p user.Principal, channel enum.IdentityKind, target, code string, meta user.Meta) (user.IdentityInfo, bool, error)
	BindWithIdp(ctx context.Context, p user.Principal, cred user.IdpCredential, meta user.Meta) (user.IdentityInfo, bool, error)
	UnbindIdentity(ctx context.Context, p user.Principal, identityID string, meta user.Meta) error
	DeleteMe(ctx context.Context, p user.Principal, meta user.Meta) (user.Me, error)
	Undelete(ctx context.Context, p user.Principal, meta user.Meta) (user.Me, error)
}

// Deps 是 handler 的依赖。
type Deps struct {
	Users                   Service
	Logger                  *slog.Logger
	ClientIP                func(*http.Request) string
	RequestID               func(*http.Request) string
	Now                     func() time.Time
	ReauthMaxAge            time.Duration
	SensitiveOpVerification bool
	MaxBodyBytes            int64
}

const defaultMaxBodyBytes = 64 << 10

// Handler 持有依赖并构建路由。
type Handler struct {
	d Deps
}

// New 校验依赖并返回 Handler（不做 I/O）。
func New(d Deps) (*Handler, error) {
	if d.Users == nil {
		return nil, errors.New("consumer: Deps.Users is required")
	}
	if d.ClientIP == nil || d.RequestID == nil {
		return nil, errors.New("consumer: Deps.ClientIP and Deps.RequestID are required")
	}
	if d.ReauthMaxAge <= 0 {
		return nil, errors.New("consumer: Deps.ReauthMaxAge must be positive")
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.MaxBodyBytes <= 0 {
		d.MaxBodyBytes = defaultMaxBodyBytes
	}
	return &Handler{d: d}, nil
}

// AuthnOptions 返回与本 handler 一致的认证中间件选项，供宿主复用。
func (h *Handler) AuthnOptions() authn.Options {
	return authn.Options{Auth: h.d.Users, Logger: h.d.Logger, Now: h.d.Now}
}

// RequireRecentAuth 返回按 Deps 配置的近期认证中间件。
func (h *Handler) RequireRecentAuth() func(http.Handler) http.Handler {
	return authn.RequireRecentAuth(h.AuthnOptions(), h.d.ReauthMaxAge, h.d.SensitiveOpVerification)
}

func requestIDFrom(ctx context.Context) string {
	return reqid.From(ctx)
}

func (h *Handler) meta(r *http.Request) user.Meta {
	return user.Meta{IP: h.d.ClientIP(r), RequestID: requestIDFrom(r.Context())}
}

// Router 构建相对路由。
func (h *Handler) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(reqid.Middleware(h.d.RequestID))
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		apierror.WriteRouteNotFound(w)
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		apierror.WriteMethodNotAllowed(w)
	})

	o := h.AuthnOptions()
	anyScope := authn.Bearer(o)
	fullScope := authn.RequireScope(o, user.ScopeUser)
	bindScope := authn.RequireScope(o, user.ScopeUser, user.ScopeBind)
	undeleteScope := authn.RequireScope(o, user.ScopeUndelete)

	// 未认证端点（Task 4、5）
	r.Post("/users:sendSignInCode", h.sendSignInCode)
	r.Post("/users:signInWithCode", h.signInWithCode)
	r.Post("/users:signInWithIdp", h.signInWithIdp)
	r.Post("/token", h.token)
	r.Post("/revoke", h.revoke)

	// 认证端点（Task 6、7）
	r.With(anyScope).Get("/users/me", h.getMe)
	r.Group(func(r chi.Router) {
		r.Use(fullScope)
		r.Patch("/users/me", h.updateMe)
		r.Post("/users/me:sendReauthenticationCode", h.sendReauthenticationCode)
		r.Post("/users/me:reauthenticate", h.reauthenticate)
		r.Get("/users/me/sessions", h.listSessions)
		r.Delete("/users/me/sessions/{session}", h.deleteSession)
		r.Post("/users/me/sessions:revokeOthers", h.revokeOtherSessions)
	})

	// 账号生命周期（阶段 5a）：软删除是敏感操作（§1.4）；:undelete 只放行冷静期账号的 user:undelete
	// 凭证，不接受 user 凭证（§2.4）——DeleteMe 会吊销所有会话，但在 Redis fail-open 窗口内一个尚未
	// 过期的 user access token 仍可能撤销本人的注销，因此收窄到专用 scope，而不是依赖领域层的状态校验。
	r.With(fullScope, h.RequireRecentAuth()).Delete("/users/me", h.deleteMe)
	r.With(undeleteScope).Post("/users/me:undelete", h.undelete)

	// 身份绑定端点（Task 5）
	r.Group(func(r chi.Router) {
		r.Use(bindScope)
		r.Post("/users/me:sendBindCode", h.sendBindCode)
		r.Get("/users/me/identities", h.listIdentities)
		r.Post("/users/me/identities", h.bindIdentity)
	})
	r.With(fullScope, h.RequireRecentAuth()).Delete("/users/me/identities/{identity}", h.unbindIdentity)
	return r
}
