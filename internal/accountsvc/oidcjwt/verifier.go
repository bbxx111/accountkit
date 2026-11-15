// Package oidcjwt verifies RS256 JWT access tokens from an external OIDC provider with a
// bounded, instance-local JWKS cache. Administrator and service-caller authentication
// share it; each keeps its own authorization rules and error formats.
package oidcjwt

import (
	"context"
	"crypto/rsa"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Token profiles select an explicit access-token claim contract.
const (
	ProfileDefault = "default"
	ProfileRFC9068 = "rfc9068"
)

const (
	keyLifetime            = time.Hour
	defaultRefreshInterval = time.Minute
	refreshTimeout         = 10 * time.Second
	maxTokenSize           = 16 * 1024
	// Leeway is the fixed clock skew tolerated for exp and iat.
	Leeway = 30 * time.Second
)

var (
	// ErrUnavailable means signing keys could not be obtained; callers answer 503.
	ErrUnavailable = errors.New("oidcjwt: identity provider unavailable")
	// ErrInvalid means the token itself was rejected; callers answer 401.
	ErrInvalid = errors.New("oidcjwt: invalid access token")
	// ErrIssuerMismatch is returned by Load when discovery names another issuer: a
	// configuration error, reported as unavailable to token callers.
	ErrIssuerMismatch = errors.New("oidcjwt: discovered issuer mismatch")
)

// Options customize networking, time, the refresh cooldown and failure logging.
type Options struct {
	// Profile explicitly selects default or rfc9068 token validation.
	Profile    string
	HTTPClient *http.Client
	Now        func() time.Time
	// RefreshInterval is the cooldown after any provider attempt; zero means one minute.
	RefreshInterval time.Duration
	// RefreshTimeout bounds one discovery+JWKS attempt; zero means ten seconds. Keeping it
	// below RefreshInterval lets a hanging provider fail fast inside the cooldown window.
	RefreshTimeout time.Duration
	// Logger receives provider failures (WARN) and issuer mismatches (ERROR); nil disables logging.
	Logger *slog.Logger
	// AllowHTTP also accepts http:// for the issuer, its discovery document and the
	// discovered jwks_uri. Meant for local development against a plain-HTTP provider;
	// redirects stay refused.
	AllowHTTP bool
}

type keyCache struct {
	keys        map[string]*rsa.PublicKey
	loadedAt    time.Time
	lastAttempt time.Time
	lastError   error
	refreshing  chan struct{}
}

// Verifier checks signatures against one issuer and audience.
type Verifier struct {
	profile          string
	issuer, audience string
	client           *http.Client
	now              func() time.Time
	interval         time.Duration
	refreshTimeout   time.Duration
	logger           *slog.Logger
	allowHTTP        bool
	mu               sync.Mutex
	jwksURL          string
	cache            keyCache
	lifecycle        context.Context
	cancel           context.CancelFunc
	closed           bool
	workers          sync.WaitGroup
}

// New validates the issuer and audience without any I/O. Keys are fetched by Load or on
// the first token that needs them.
func New(issuer, audience string, opts Options) (*Verifier, error) {
	if !ValidProfile(opts.Profile) {
		return nil, errors.New("oidcjwt: invalid token profile")
	}
	if opts.Profile == "" {
		opts.Profile = ProfileDefault
	}
	if !validIssuer(issuer, opts.AllowHTTP) {
		return nil, errors.New("oidcjwt: issuer must be an absolute HTTPS URL without credentials, query or fragment")
	}
	if strings.TrimSpace(audience) == "" || strings.TrimSpace(audience) != audience {
		return nil, errors.New("oidcjwt: audience is required and must not have surrounding whitespace")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.RefreshInterval <= 0 {
		opts.RefreshInterval = defaultRefreshInterval
	}
	if opts.RefreshTimeout <= 0 || opts.RefreshTimeout > refreshTimeout {
		opts.RefreshTimeout = refreshTimeout
	}
	client := http.Client{Timeout: refreshTimeout}
	if opts.HTTPClient != nil {
		client = *opts.HTTPClient
	}
	if client.Timeout <= 0 || client.Timeout > refreshTimeout {
		client.Timeout = refreshTimeout
	}
	// Discovery/JWKS endpoints must be the explicit configured or discovered URLs, including
	// when the caller supplies a client with a permissive redirect policy.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("oidcjwt: redirects are not allowed") }
	lifecycle, cancel := context.WithCancel(context.Background())
	return &Verifier{profile: opts.Profile, lifecycle: lifecycle, cancel: cancel, issuer: issuer, audience: audience, client: &client, now: opts.Now, interval: opts.RefreshInterval, refreshTimeout: opts.RefreshTimeout, logger: opts.Logger, allowHTTP: opts.AllowHTTP}, nil
}

// Load shares the same bounded refresh and cooldown as token requests.
func (v *Verifier) Load(ctx context.Context) error { return v.load(ctx) }

// Close cancels and waits for verifier-owned downloads. It is idempotent.
func (v *Verifier) Close() {
	v.mu.Lock()
	v.closed = true
	v.cancel()
	v.mu.Unlock()
	v.workers.Wait()
}

func (v *Verifier) load(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	v.mu.Lock()
	if v.closed {
		v.mu.Unlock()
		return ErrUnavailable
	}
	active := v.cache.refreshing
	if active == nil {
		if !v.cache.lastAttempt.IsZero() && v.now().Sub(v.cache.lastAttempt) < v.interval {
			err := v.cache.lastError
			v.mu.Unlock()
			return err
		}
		active = make(chan struct{})
		v.cache.refreshing = active
		v.cache.lastAttempt = v.now()
		v.workers.Add(1)
		go v.refresh(active)
	}
	v.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-active:
		v.mu.Lock()
		defer v.mu.Unlock()
		return v.cache.lastError
	}
}

// fetch runs discovery when the JWKS URI is still unknown, then loads the keys.
func (v *Verifier) fetch(ctx context.Context) (string, map[string]*rsa.PublicKey, error) {
	v.mu.Lock()
	jwksURL := v.jwksURL
	v.mu.Unlock()
	if jwksURL == "" {
		var err error
		if jwksURL, err = v.discover(ctx); err != nil {
			return "", nil, err
		}
	}
	keys, err := v.loadKeys(ctx, jwksURL)
	return jwksURL, keys, err
}

// Parse verifies a compact JWT and returns its claims. The default profile requires exp and
// validates iat when present; service callers additionally require iat. The rfc9068
// profile requires the access-token typ and all mandatory claims. Errors are ErrInvalid
// or ErrUnavailable.
func (v *Verifier) Parse(ctx context.Context, raw string) (jwt.MapClaims, error) {
	if raw == "" || len(raw) > maxTokenSize {
		return nil, ErrInvalid
	}
	claims := jwt.MapClaims{}
	_, err := jwt.ParseWithClaims(raw, claims, func(token *jwt.Token) (any, error) {
		if v.profile == ProfileRFC9068 {
			typ, ok := token.Header["typ"].(string)
			if !ok || (typ != "at+jwt" && typ != "application/at+jwt") {
				return nil, ErrInvalid
			}
		}
		if token.Method != jwt.SigningMethodRS256 {
			return nil, ErrInvalid
		}
		if _, ok := token.Header["jku"]; ok {
			return nil, ErrInvalid
		}
		if _, ok := token.Header["x5u"]; ok {
			return nil, ErrInvalid
		}
		kid, ok := token.Header["kid"].(string)
		if !ok || kid == "" || len(kid) > 256 {
			return nil, ErrInvalid
		}
		return v.key(ctx, kid)
	}, jwt.WithValidMethods([]string{"RS256"}), jwt.WithIssuer(v.issuer), jwt.WithAudience(v.audience), jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithLeeway(Leeway), jwt.WithTimeFunc(v.now))
	if err != nil {
		if errors.Is(err, ErrUnavailable) {
			return nil, ErrUnavailable
		}
		return nil, ErrInvalid
	}
	if v.profile == ProfileRFC9068 {
		for _, name := range []string{"sub", "client_id", "jti"} {
			value, ok := claims[name].(string)
			if !ok || value == "" {
				return nil, ErrInvalid
			}
		}
		for _, name := range []string{"exp", "iat"} {
			if _, ok := claims[name].(float64); !ok {
				return nil, ErrInvalid
			}
		}
	}
	return claims, nil
}

func (v *Verifier) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	if ctx.Err() != nil {
		return nil, ErrUnavailable
	}
	v.mu.Lock()
	key := v.cache.keys[kid]
	fresh := !v.closed && v.now().Before(v.cache.loadedAt.Add(keyLifetime))
	v.mu.Unlock()
	if key != nil && fresh {
		return key, nil
	}
	if err := v.load(ctx); err != nil {
		return nil, ErrUnavailable
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed {
		return nil, ErrUnavailable
	}
	if key := v.cache.keys[kid]; key != nil && v.now().Before(v.cache.loadedAt.Add(keyLifetime)) {
		return key, nil
	}
	return nil, ErrInvalid
}

func (v *Verifier) refresh(active chan struct{}) {
	// One bounded download may outlive a disconnected caller. Caller cancellation
	// must never become a provider failure or cancel other callers' shared work.
	defer v.workers.Done()
	ctx, cancel := context.WithTimeout(v.lifecycle, v.refreshTimeout)
	defer cancel()
	jwksURL, keys, err := v.fetch(ctx)
	// Log before waking waiters so a caller that observed the failure also observes the log.
	// The cooldown bounds this to one line per window; the cause is omitted because it may
	// echo provider responses.
	if err != nil && v.logger != nil {
		if errors.Is(err, ErrIssuerMismatch) {
			v.logger.Error("oidcjwt: discovered issuer does not match configuration", "issuer", v.issuer)
		} else {
			v.logger.Warn("oidcjwt: identity provider unavailable", "issuer", v.issuer)
		}
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.cache.lastError = err
	if err == nil {
		v.jwksURL = jwksURL
		v.cache.keys = keys
		v.cache.loadedAt = v.now()
	}
	v.cache.refreshing = nil
	close(active)
}

// ValidProfile reports supported explicit external token profiles.
func ValidProfile(profile string) bool {
	return profile == "" || profile == ProfileDefault || profile == ProfileRFC9068
}

// ClientIDClaim names the sole client identifier accepted by the configured profile.
func (v *Verifier) ClientIDClaim() string {
	if v.profile == ProfileRFC9068 {
		return "client_id"
	}
	return "azp"
}
