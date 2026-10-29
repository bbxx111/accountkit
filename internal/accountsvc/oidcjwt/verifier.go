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
}

// New validates the issuer and audience without any I/O. Keys are fetched by Load or on
// the first token that needs them.
func New(issuer, audience string, opts Options) (*Verifier, error) {
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
	return &Verifier{issuer: issuer, audience: audience, client: &client, now: opts.Now, interval: opts.RefreshInterval, refreshTimeout: opts.RefreshTimeout, logger: opts.Logger, allowHTTP: opts.AllowHTTP}, nil
}

// Load discovers the provider and loads its keys now. It does not count as a refresh
// attempt, so the first unknown kid afterwards still refreshes immediately.
func (v *Verifier) Load(ctx context.Context) error {
	jwksURL, keys, err := v.fetch(ctx)
	if err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.jwksURL = jwksURL
	v.cache.keys = keys
	v.cache.loadedAt = v.now()
	return nil
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

// Parse verifies a compact JWT and returns its claims. It requires exp and validates iat
// when present; callers needing iat (service tokens) enforce it themselves. Errors are
// ErrInvalid or ErrUnavailable.
func (v *Verifier) Parse(ctx context.Context, raw string) (jwt.MapClaims, error) {
	if raw == "" || len(raw) > maxTokenSize {
		return nil, ErrInvalid
	}
	claims := jwt.MapClaims{}
	_, err := jwt.ParseWithClaims(raw, claims, func(token *jwt.Token) (any, error) {
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
	return claims, nil
}

func (v *Verifier) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	for {
		if ctx.Err() != nil {
			return nil, ErrUnavailable
		}
		v.mu.Lock()
		now := v.now()
		if key := v.cache.keys[kid]; key != nil && now.Before(v.cache.loadedAt.Add(keyLifetime)) {
			v.mu.Unlock()
			return key, nil
		}
		if active := v.cache.refreshing; active != nil {
			v.mu.Unlock()
			select {
			case <-active:
				continue
			case <-ctx.Done():
				return nil, ErrUnavailable
			}
		}
		// Any attempt, successful or not, opens a cooldown window: inside it an unknown kid
		// is invalid after a success and unavailable after a failure, so random kids cannot
		// turn each request into a provider fetch.
		if !v.cache.lastAttempt.IsZero() && now.Sub(v.cache.lastAttempt) < v.interval {
			err := ErrInvalid
			if v.cache.lastError != nil {
				err = ErrUnavailable
			}
			v.mu.Unlock()
			return nil, err
		}
		active := make(chan struct{})
		v.cache.refreshing = active
		v.cache.lastAttempt = now
		v.mu.Unlock()
		// The download belongs to the verifier, not its first caller. Every
		// request, including the trigger, waits through its own cancellable ctx.
		go v.refresh(active)
	}
}

func (v *Verifier) refresh(active chan struct{}) {
	// One bounded download may outlive a disconnected caller. Caller cancellation
	// must never become a provider failure or cancel other callers' shared work.
	ctx, cancel := context.WithTimeout(context.Background(), v.refreshTimeout)
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
