// Package adminauth adapts external OIDC access tokens to accountkit admin authentication.
package adminauth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/httpapi/admin"
	"github.com/bbxx111/accountkit/httpapi/apierror"
	"github.com/bbxx111/accountkit/internal/accountsvc/oidcjwt"
	"github.com/bbxx111/accountkit/internal/httpcredentials"
)

// Options customize networking, time and the existing forbidden-audit hook.
type Options struct {
	Logger      *slog.Logger
	HTTPClient  *http.Client
	Now         func() time.Time
	OnForbidden func(*http.Request, accountkit.AdminPrincipal)
}

var errInvalid = errors.New("adminauth: invalid access token")

// Verifier verifies administrator JWTs through a shared, instance-local JWKS cache.
type Verifier struct {
	policy      permissionPolicy
	cfg         Config
	tokens      *oidcjwt.Verifier
	onForbidden func(*http.Request, accountkit.AdminPrincipal)
}

// New validates and assembles authentication without network I/O.
func New(ctx context.Context, cfg Config, opts Options) (*Verifier, error) {
	if err := cfg.validateAuthentication(); err != nil {
		return nil, err
	}
	policy, err := compilePermissions(cfg.RoleBindings, admin.Permissions())
	if err != nil {
		return nil, err
	}
	if cfg.RolesClaim == "" {
		cfg.RolesClaim = DefaultRolesClaim
	}
	if cfg.UsernameClaim == "" {
		cfg.UsernameClaim = DefaultUsernameClaim
	}
	tokens, err := oidcjwt.New(cfg.Issuer, cfg.Audience, oidcjwt.Options{Profile: cfg.TokenProfile, HTTPClient: opts.HTTPClient, Now: opts.Now, RefreshInterval: time.Minute, AllowHTTP: cfg.AllowHTTP, Logger: opts.Logger})
	if err != nil {
		return nil, err
	}
	cfg.RoleBindings = nil
	return &Verifier{cfg: cfg, tokens: tokens, policy: policy, onForbidden: opts.OnForbidden}, nil
}

// Load preloads keys through the request refresh mechanism.
func (v *Verifier) Load(ctx context.Context) error { return v.tokens.Load(ctx) }

// Close cancels and waits for outstanding key refreshes.
func (v *Verifier) Close() { v.tokens.Close() }

type principalKey struct{}
type authenticated struct {
	verifier    *Verifier
	principal   accountkit.AdminPrincipal
	roles       map[string]bool
	permissions map[admin.Permission]bool
}

// PrincipalFrom returns only a principal established by this package's middleware.
func PrincipalFrom(ctx context.Context) (accountkit.AdminPrincipal, bool) {
	a, ok := ctx.Value(principalKey{}).(authenticated)
	return a.principal, ok
}

// Middleware verifies a single Bearer access token and establishes the admin principal.
func (v *Verifier) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scheme, credential, ok := httpcredentials.Parse(r)
		if !ok || scheme != httpcredentials.SchemeBearer {
			writeUnauthorized(w)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		claims, err := v.tokens.Parse(ctx, credential)
		if err != nil {
			if errors.Is(err, oidcjwt.ErrUnavailable) {
				apierror.Write(w, apierror.New(apierror.StatusUnavailable, "ADMIN_AUTH_UNAVAILABLE", "administrator authentication is unavailable"))
			} else {
				writeUnauthorized(w)
			}
			return
		}
		principal, roles, err := v.extractClaims(claims)
		if err != nil {
			writeUnauthorized(w)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, authenticated{verifier: v, principal: principal, roles: roles, permissions: v.policy.forRoles(roles)})))
	})
}

// RequireRole preserves per-route authorization and records one authenticated denial.
func (v *Verifier) RequireRole(role string) func(http.Handler) http.Handler {
	return v.requireAuthorization(func(a authenticated) bool { return a.roles[role] }, "administrator role is required")
}

func (v *Verifier) requireAuthorization(allowed func(authenticated) bool, message string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			a, ok := r.Context().Value(principalKey{}).(authenticated)
			if !ok || a.verifier != v {
				writeUnauthorized(w)
				return
			}
			if !allowed(a) {
				if v.onForbidden != nil {
					v.onForbidden(r, a.principal)
				}
				apierror.Write(w, apierror.New(apierror.StatusPermissionDenied, "ADMIN_FORBIDDEN", message))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
	apierror.Write(w, apierror.New(apierror.StatusUnauthenticated, "ADMIN_UNAUTHENTICATED", "valid administrator access token is required"))
}
