// Package authn 提供 Bearer 认证与授权中间件（任何 router 可用）。
//
// 三个中间件：Bearer（解析 access token → Principal 入 ctx）、RequireScope（含 Bearer）、
// RequireRecentAuth（auth_time 新鲜度）。业务域只接触 PrincipalFrom 与这些中间件。
package authn

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/bbxx111/accountkit/httpapi/apierror"
	"github.com/bbxx111/accountkit/user"
)

// Authenticator 校验 access token。*user.Service 满足此接口。
type Authenticator interface {
	Authenticate(ctx context.Context, rawAccess string) (user.Principal, error)
}

// Options 是中间件的共享依赖。
type Options struct {
	Auth   Authenticator
	Logger *slog.Logger
	Now    func() time.Time
}

func (o Options) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

type ctxKey struct{}

// WithPrincipal 把 Principal 放入 ctx。
func WithPrincipal(ctx context.Context, p user.Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

// PrincipalFrom 取出 ctx 中的 Principal。
func PrincipalFrom(ctx context.Context) (user.Principal, bool) {
	p, ok := ctx.Value(ctxKey{}).(user.Principal)
	return p, ok
}

const (
	ReasonTokenMissing = "TOKEN_MISSING"
	ReasonTokenInvalid = "TOKEN_INVALID"
)

// WriteUnauthenticated 写 401：AIP-193 body + RFC 6750 WWW-Authenticate。
func WriteUnauthenticated(w http.ResponseWriter, reason, message string) {
	challenge := `Bearer realm="user"`
	if reason == ReasonTokenInvalid {
		challenge += `, error="invalid_token"`
	}
	w.Header().Set("WWW-Authenticate", challenge)
	apierror.Write(w, apierror.New(apierror.StatusUnauthenticated, reason, message))
}

// bearerToken 从 Authorization 头取 token；scheme 大小写不敏感；形状不对返回 ""。
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h == "" {
		return ""
	}
	scheme, rest, ok := strings.Cut(h, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	rest = strings.TrimSpace(rest)
	if rest == "" || strings.ContainsAny(rest, " \t") {
		return ""
	}
	return rest
}

// Bearer 解析 access token 并把 Principal 放入 ctx；已带 Principal 的请求直接放行。
func Bearer(o Options) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, ok := PrincipalFrom(r.Context()); ok {
				next.ServeHTTP(w, r)
				return
			}
			raw := bearerToken(r)
			if raw == "" {
				WriteUnauthenticated(w, ReasonTokenMissing, "bearer token required")
				return
			}
			p, err := o.Auth.Authenticate(r.Context(), raw)
			switch {
			case err == nil:
				next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), p)))
			case errors.Is(err, user.ErrUnavailable):
				apierror.Write(w, &apierror.Error{Status: apierror.StatusUnavailable, Reason: "DEPENDENCY_UNAVAILABLE", Message: "authentication temporarily unavailable", RetryAfterSeconds: 1})
			default:
				// 无效/过期/已吊销：统一 401，不泄露细节
				WriteUnauthenticated(w, ReasonTokenInvalid, "invalid or expired access token")
			}
		})
	}
}

// RequireScope 确保已认证且 scope 在 allowed 中。
func RequireScope(o Options, allowed ...string) func(http.Handler) http.Handler {
	set := make(map[string]struct{}, len(allowed))
	for _, s := range allowed {
		set[s] = struct{}{}
	}
	bearer := Bearer(o)
	return func(next http.Handler) http.Handler {
		return bearer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, _ := PrincipalFrom(r.Context())
			if _, ok := set[p.Scope]; !ok {
				apierror.Write(w, apierror.New(apierror.StatusPermissionDenied, "INSUFFICIENT_SCOPE", "token scope does not permit this operation"))
				return
			}
			next.ServeHTTP(w, r)
		}))
	}
}

// RequireRecentAuth 要求 now - auth_time <= maxAge；enabled=false 时只跳过新鲜度检查，
// 认证本身仍是必须的。必须在 Bearer/RequireScope 之后使用；缺 Principal → 401。
func RequireRecentAuth(o Options, maxAge time.Duration, enabled bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, ok := PrincipalFrom(r.Context())
			if !ok {
				WriteUnauthenticated(w, ReasonTokenMissing, "bearer token required")
				return
			}
			if !enabled {
				next.ServeHTTP(w, r)
				return
			}
			// AuthTime 为零值（claim 缺失）时 Sub 得到巨大的正数 → 视为过期，fail-safe。
			if p.AuthTime.IsZero() || o.now().Sub(p.AuthTime) > maxAge {
				apierror.Write(w, apierror.New(apierror.StatusFailedPrecondition, "REAUTHENTICATION_REQUIRED", "recent authentication required for this operation"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
