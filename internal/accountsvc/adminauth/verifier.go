// Package adminauth adapts external OIDC access tokens to accountkit admin authentication.
package adminauth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/httpapi/apierror"
	"github.com/bbxx111/accountkit/internal/accountsvc/oidcjwt"
)

// Options customize networking, time and the existing forbidden-audit hook.
type Options struct {
	HTTPClient  *http.Client
	Now         func() time.Time
	OnForbidden func(*http.Request, accountkit.AdminPrincipal)
}

var errInvalid = errors.New("adminauth: invalid access token")

// Verifier verifies administrator JWTs through a shared, instance-local JWKS cache.
type Verifier struct {
	cfg         Config
	tokens      *oidcjwt.Verifier
	onForbidden func(*http.Request, accountkit.AdminPrincipal)
}

// New discovers the configured issuer and loads its signing keys before startup.
func New(ctx context.Context, cfg Config, opts Options) (*Verifier, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if cfg.RolesClaim == "" {
		cfg.RolesClaim = "/roles"
	}
	if cfg.UsernameClaim == "" {
		cfg.UsernameClaim = "/preferred_username"
	}
	tokens, err := oidcjwt.New(cfg.Issuer, cfg.Audience, oidcjwt.Options{HTTPClient: opts.HTTPClient, Now: opts.Now, RefreshInterval: time.Minute, AllowHTTP: cfg.AllowHTTP})
	if err != nil {
		return nil, err
	}
	if err := tokens.Load(ctx); err != nil {
		return nil, err
	}
	return &Verifier{cfg: cfg, tokens: tokens, onForbidden: opts.OnForbidden}, nil
}

type principalKey struct{}
type authenticated struct {
	verifier  *Verifier
	principal accountkit.AdminPrincipal
	roles     map[string]bool
}

// PrincipalFrom returns only a principal established by this package's middleware.
func PrincipalFrom(ctx context.Context) (accountkit.AdminPrincipal, bool) {
	a, ok := ctx.Value(principalKey{}).(authenticated)
	return a.principal, ok
}

// Middleware verifies a single Bearer access token and establishes the admin principal.
func (v *Verifier) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers := r.Header.Values("Authorization")
		if len(headers) != 1 {
			writeUnauthorized(w)
			return
		}
		parts := strings.Fields(headers[0])
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || len(parts[1]) > 16*1024 {
			writeUnauthorized(w)
			return
		}
		claims, err := v.tokens.Parse(r.Context(), parts[1])
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
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, authenticated{v, principal, roles})))
	})
}

// RequireRole preserves per-route authorization and records one authenticated denial.
func (v *Verifier) RequireRole(role string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			a, ok := r.Context().Value(principalKey{}).(authenticated)
			if !ok || a.verifier != v {
				writeUnauthorized(w)
				return
			}
			if !a.roles[role] {
				if v.onForbidden != nil {
					v.onForbidden(r, a.principal)
				}
				apierror.Write(w, apierror.New(apierror.StatusPermissionDenied, "ADMIN_FORBIDDEN", "administrator role is required"))
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
