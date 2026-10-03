// Package adminauth adapts external OIDC access tokens to accountkit admin authentication.
package adminauth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/httpapi/apierror"
	"github.com/golang-jwt/jwt/v5"
)

// Options customize networking, time and the existing forbidden-audit hook.
type Options struct {
	HTTPClient  *http.Client
	Now         func() time.Time
	OnForbidden func(*http.Request, accountkit.AdminPrincipal)
}

var (
	errUnavailable = errors.New("adminauth: identity provider unavailable")
	errInvalid     = errors.New("adminauth: invalid access token")
)

// Verifier verifies administrator JWTs and keeps a bounded, instance-local JWKS cache.
type Verifier struct {
	cfg         Config
	client      *http.Client
	now         func() time.Time
	onForbidden func(*http.Request, accountkit.AdminPrincipal)
	jwksURL     string
	mu          sync.Mutex
	cache       keyCache
}

// New discovers the configured HTTPS issuer and loads its signing keys before startup.
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
	if opts.Now == nil {
		opts.Now = time.Now
	}
	client := http.Client{Timeout: 10 * time.Second}
	if opts.HTTPClient != nil {
		client = *opts.HTTPClient
	}
	if client.Timeout <= 0 || client.Timeout > 10*time.Second {
		client.Timeout = 10 * time.Second
	}
	// Discovery/JWKS endpoints must be explicit HTTPS URLs, including when the
	// caller supplies a client with a permissive redirect policy.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("adminauth: redirects are not allowed") }
	v := &Verifier{cfg: cfg, client: &client, now: opts.Now, onForbidden: opts.OnForbidden}
	var discovery struct {
		Issuer  string `json:"issuer"`
		JWKSURI string `json:"jwks_uri"`
	}
	if err := v.fetchJSON(ctx, strings.TrimRight(cfg.Issuer, "/")+"/.well-known/openid-configuration", &discovery); err != nil {
		return nil, err
	}
	if discovery.Issuer != cfg.Issuer {
		return nil, errors.New("adminauth: discovered issuer mismatch")
	}
	if err := validateHTTPS(discovery.JWKSURI); err != nil {
		return nil, errors.New("adminauth: invalid JWKS URI")
	}
	v.jwksURL = discovery.JWKSURI
	keys, err := v.loadKeys(ctx)
	if err != nil {
		return nil, err
	}
	v.cache.keys = keys
	v.cache.loadedAt = v.now()
	return v, nil
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
		claims := jwt.MapClaims{}
		_, err := jwt.ParseWithClaims(parts[1], claims, func(token *jwt.Token) (any, error) {
			if token.Method != jwt.SigningMethodRS256 {
				return nil, errInvalid
			}
			if _, ok := token.Header["jku"]; ok {
				return nil, errInvalid
			}
			if _, ok := token.Header["x5u"]; ok {
				return nil, errInvalid
			}
			kid, ok := token.Header["kid"].(string)
			if !ok || kid == "" || len(kid) > 256 {
				return nil, errInvalid
			}
			return v.key(r.Context(), kid)
		}, jwt.WithValidMethods([]string{"RS256"}), jwt.WithIssuer(v.cfg.Issuer), jwt.WithAudience(v.cfg.Audience), jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithLeeway(30*time.Second), jwt.WithTimeFunc(v.now))
		if err != nil {
			if errors.Is(err, errUnavailable) {
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
