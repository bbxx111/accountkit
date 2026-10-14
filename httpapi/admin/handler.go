// Package admin 提供管理面 /admin/v1 的 HTTP handler（只做 HTTP 翻译）。
//
// 宿主先在 /admin/v1 上挂 OIDC verifier 的 Middleware（401/403/503 由它产生），再 Mount 本包的 Router；
// 本包按路由用 Verifier.RequireRole 放行，handler 内不判角色（AGENTS.md "Two identity systems"）。
// 本模块不 import oidc-verifier：宿主用 Deps.Verifier / Deps.Principal 适配。
package admin

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bbxx111/accountkit/audit"
	auditdb "github.com/bbxx111/accountkit/audit/db"
	"github.com/bbxx111/accountkit/httpapi/apierror"
	"github.com/bbxx111/accountkit/httpapi/jsonbody"
	"github.com/bbxx111/accountkit/httpapi/reqid"
	"github.com/bbxx111/accountkit/ids"
	"github.com/bbxx111/accountkit/user"
)

// 角色名（Keycloak realm roles，§4.1）。super-admin 是包含 operator 的 composite role，每条路由只检查一个名字。
const (
	RoleOperator   = "operator"
	RoleSuperAdmin = "super-admin"
)

// Principal 是已通过宿主 verifier 校验的管理端主体（Issuer + Subject 才唯一标识一个人）。
type Principal struct {
	Issuer   string
	Subject  string
	Username string
}

// Verifier 是宿主 OIDC verifier 的最小契约：路由级角色检查中间件（须叠加在宿主的 Middleware 之后）。
type Verifier interface {
	RequireRole(role string) func(http.Handler) http.Handler
}

// Service 是 handler 依赖的领域面；*user.Service 满足它。
type Service interface {
	ListUsers(ctx context.Context, f user.UserFilter, after *user.PageCursor, limit int) (user.UserPage, error)
	GetUserDetail(ctx context.Context, userID string) (user.AdminUserDetail, error)
	UserExists(ctx context.Context, userID string) error
	Freeze(ctx context.Context, a user.Admin, userID, reason string, meta user.Meta) (user.AdminUser, error)
	Unfreeze(ctx context.Context, a user.Admin, userID, reason string, meta user.Meta) (user.AdminUser, error)
	AdminDeleteUser(ctx context.Context, a user.Admin, userID string, meta user.Meta) (user.AdminUser, error)
	AdminUndeleteUser(ctx context.Context, a user.Admin, userID string, meta user.Meta) (user.AdminUser, error)
	RevealIdentity(ctx context.Context, a user.Admin, userID, identityID string, meta user.Meta) (user.RevealedIdentity, error)
	AdminListSessions(ctx context.Context, userID string) ([]user.SessionInfo, error)
	AdminRevokeSession(ctx context.Context, a user.Admin, userID, sid string, meta user.Meta) error
	AdminRevokeAllSessions(ctx context.Context, a user.Admin, userID string, meta user.Meta) (int, error)
}

// AuditReader 是审计读侧；*audit.Store 满足它。
type AuditReader interface {
	ListByUser(ctx context.Context, userID string, after *audit.EventCursor, limit int) ([]auditdb.AuditEvent, error)
}

// Deps 是 handler 的依赖。
type Deps struct {
	Users     Service
	Audit     AuditReader
	Verifier  Verifier
	Principal func(ctx context.Context) (Principal, bool)
	Logger    *slog.Logger
	ClientIP  func(*http.Request) string
	RequestID func(*http.Request) string
	// MaxBodyBytes 默认 64 KiB。
	MaxBodyBytes int64
}

// Handler 持有依赖并构建路由。
type Handler struct {
	d Deps
}

// New 校验依赖并返回 Handler（不做 I/O）。
func New(d Deps) (*Handler, error) {
	switch {
	case d.Users == nil, d.Audit == nil:
		return nil, errors.New("admin: Deps.Users and Deps.Audit are required")
	case d.Verifier == nil, d.Principal == nil:
		return nil, errors.New("admin: Deps.Verifier and Deps.Principal are required")
	case d.ClientIP == nil, d.RequestID == nil:
		return nil, errors.New("admin: Deps.ClientIP and Deps.RequestID are required")
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.MaxBodyBytes <= 0 {
		d.MaxBodyBytes = jsonbody.DefaultMaxBytes
	}
	return &Handler{d: d}, nil
}

// Unconfigured 返回“管理面未配置”的 handler：任何请求 503 UNAVAILABLE / ADMIN_NOT_CONFIGURED，
// 响应带 X-Request-Id（与已配置时的行为一致）。accountkit.New 在宿主未注入 AdminVerifier / AdminPrincipal
// 时使用它，保证 AdminHandler() 永不为 nil。
func Unconfigured(requestID func(*http.Request) string) http.Handler {
	return reqid.Middleware(requestID)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		apierror.Write(w, apierror.New(apierror.StatusUnavailable, "ADMIN_NOT_CONFIGURED", "admin surface is not configured on this host (Deps.AdminVerifier / Deps.AdminPrincipal missing)"))
	}))
}

// principal 取管理端主体；缺失说明宿主没有先挂 verifier Middleware → 401（不是 500：对客户端而言就是未认证）。
func (h *Handler) principal(w http.ResponseWriter, r *http.Request) (user.Admin, bool) {
	p, ok := h.d.Principal(r.Context())
	if !ok {
		w.Header().Set("WWW-Authenticate", `Bearer realm="admin"`)
		apierror.Write(w, apierror.New(apierror.StatusUnauthenticated, "TOKEN_MISSING", "admin principal missing; the OIDC verifier middleware must run before this handler"))
		return user.Admin{}, false
	}
	return user.Admin{Issuer: p.Issuer, Subject: p.Subject, Username: p.Username}, true
}

func (h *Handler) meta(r *http.Request) user.Meta {
	return user.Meta{IP: h.d.ClientIP(r), RequestID: reqid.From(r.Context())}
}

// Router 构建相对路由；宿主在 verifier Middleware 之后 Mount 到 /admin/v1。
// 角色：operator 可读、可冻结/解冻、可管理会话；super-admin 才能 :reveal 与代办删除/恢复（§4.3）。
func (h *Handler) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(reqid.Middleware(h.d.RequestID))
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) { apierror.WriteRouteNotFound(w) })
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) { apierror.WriteMethodNotAllowed(w) })

	op := h.d.Verifier.RequireRole(RoleOperator)
	su := h.d.Verifier.RequireRole(RoleSuperAdmin)

	r.With(op).Get("/users", h.listUsers)
	r.With(op).Get("/users/{user}", h.getUser)
	r.With(su).Delete("/users/{user}", h.deleteUser)
	r.With(op).Post("/users/{user}:freeze", h.freeze)
	r.With(op).Post("/users/{user}:unfreeze", h.unfreeze)
	r.With(su).Post("/users/{user}:undelete", h.undeleteUser)
	r.With(su).Get("/users/{user}/identities/{identity}:reveal", h.revealIdentity)
	r.With(op).Get("/users/{user}/sessions", h.listSessions)
	r.With(op).Delete("/users/{user}/sessions/{session}", h.deleteSession)
	r.With(op).Post("/users/{user}/sessions:revokeAll", h.revokeAllSessions)
	r.With(op).Get("/users/{user}/auditEvents", h.listAuditEvents)
	return r
}

// userID 读取并校验路径参数 {user}；非法 → 400 INVALID_ID。
func userID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := chi.URLParam(r, "user")
	if !ids.Valid(ids.User, id) {
		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, "INVALID_ID", "user id is malformed"))
		return "", false
	}
	return id, true
}
