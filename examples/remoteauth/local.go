package remoteauth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/bbxx111/accountkit/ids"
)

// VerifierConfig configures local access-token verification against the
// accountsvc JWKS. Issuer and Audience must equal the service's JWT settings.
// MinRefreshInterval throttles every JWKS fetch attempt, successful or not.
// MaxCacheAge bounds how long a fetched key set is trusted: older sets are
// refreshed, and if refreshing keeps failing they are dropped after
// MaxCacheAge+StaleGrace so removed or compromised keys stop being accepted.
type VerifierConfig struct {
	JWKSURL, Issuer, Audience string
	HTTPClient                *http.Client
	Timeout                   time.Duration // per JWKS request, default 5s
	Leeway                    time.Duration // clock skew, default 30s
	MinRefreshInterval        time.Duration // default 60s
	MaxCacheAge               time.Duration // default 5m, matching the service's Cache-Control
	StaleGrace                time.Duration // default 10m
	AllowHTTP                 bool
	Now                       func() time.Time
}

// Verifier checks ES256 access tokens locally. It never calls introspection, so
// a revoked session's access stays accepted until it expires (at most the access
// TTL plus leeway). Routes that need immediate revocation must also introspect.
type Verifier struct {
	cfg        VerifierConfig
	httpClient http.Client

	mu          sync.Mutex // guards the fields below; never held during network I/O
	keys        map[string]*ecdsa.PublicKey
	keysTime    time.Time // when keys were last successfully fetched
	lastAttempt time.Time // last fetch attempt, successful or not
	attempted   bool
	lastFailed  bool          // whether the last attempt failed
	inflight    chan struct{} // non-nil while a fetch is running; closed when it ends
}

var (
	errJWKSUnavailable = errors.New("jwks unavailable")
	errUnknownKey      = errors.New("unknown kid")
)

// NewVerifier validates the configuration. Like NewClient it requires HTTPS
// unless AllowHTTP is set, and never follows redirects.
func NewVerifier(cfg VerifierConfig) (*Verifier, error) {
	u, err := url.Parse(cfg.JWKSURL)
	if err != nil || u.Host == "" || (u.Scheme != "https" && !(cfg.AllowHTTP && u.Scheme == "http")) || u.User != nil || u.Fragment != "" || cfg.Issuer == "" || cfg.Audience == "" || cfg.Timeout < 0 || cfg.Leeway < 0 || cfg.MinRefreshInterval < 0 {
		return nil, errors.New("remoteauth: valid JWKS URL, issuer, audience and durations required")
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Second
	}
	if cfg.Leeway == 0 {
		cfg.Leeway = 30 * time.Second
	}
	if cfg.MinRefreshInterval == 0 {
		cfg.MinRefreshInterval = time.Minute
	}
	if cfg.MaxCacheAge <= 0 {
		cfg.MaxCacheAge = 5 * time.Minute
	}
	if cfg.StaleGrace <= 0 {
		cfg.StaleGrace = 10 * time.Minute
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	v := &Verifier{cfg: cfg}
	if cfg.HTTPClient != nil {
		v.httpClient = *cfg.HTTPClient
	}
	v.httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return v, nil
}

type accessClaims struct {
	SessionID string `json:"sid"`
	Scope     string `json:"scope"`
	AuthTime  *int64 `json:"auth_time"`
	jwt.RegisteredClaims
}

// Verify returns Active=false for any invalid token (bad signature, alg, typ,
// issuer, audience, expiry or IDs) and ErrUnavailable only when the JWKS cannot
// be fetched for an unknown key ID.
func (v *Verifier) Verify(ctx context.Context, raw string) (Result, error) {
	var c accessClaims
	_, err := jwt.NewParser(
		jwt.WithValidMethods([]string{"ES256"}),
		jwt.WithIssuer(v.cfg.Issuer),
		jwt.WithAudience(v.cfg.Audience),
		jwt.WithLeeway(v.cfg.Leeway),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(v.cfg.Now),
	).ParseWithClaims(raw, &c, func(t *jwt.Token) (any, error) {
		if typ, _ := t.Header["typ"].(string); typ != "at+jwt" {
			return nil, errors.New("typ")
		}
		kid, _ := t.Header["kid"].(string)
		return v.key(ctx, kid)
	})
	if errors.Is(err, errJWKSUnavailable) {
		return Result{}, ErrUnavailable
	}
	if err != nil || c.AuthTime == nil || !ids.Valid(ids.User, c.Subject) || !ids.Valid(ids.Session, c.SessionID) || strings.TrimSpace(c.Scope) == "" {
		return Result{}, nil
	}
	return Result{Active: true, Subject: c.Subject, SessionID: c.SessionID, Scope: c.Scope, AuthTime: time.Unix(*c.AuthTime, 0)}, nil
}

// key returns a cached key. Unknown key IDs and expired key sets trigger a
// refresh; attempts are throttled by MinRefreshInterval whether they succeed or
// fail, concurrent callers share one fetch, and no lock is held during I/O.
func (v *Verifier) key(ctx context.Context, kid string) (*ecdsa.PublicKey, error) {
	if kid == "" {
		return nil, errUnknownKey
	}
	for {
		v.mu.Lock()
		now := v.cfg.Now()
		age := now.Sub(v.keysTime)
		usable := v.keys != nil && age < v.cfg.MaxCacheAge+v.cfg.StaleGrace
		k, known := v.keys[kid]
		if usable && known && age < v.cfg.MaxCacheAge {
			v.mu.Unlock()
			return k, nil
		}
		if wait := v.inflight; wait != nil {
			v.mu.Unlock()
			select {
			case <-wait:
				continue // re-evaluate with the fetch result
			case <-ctx.Done():
				return nil, errJWKSUnavailable
			}
		}
		if v.attempted && now.Sub(v.lastAttempt) < v.cfg.MinRefreshInterval {
			v.mu.Unlock()
			switch {
			case usable && known:
				return k, nil // stale but within grace while refresh is throttled
			case usable && !v.lastFailed:
				return nil, errUnknownKey // the set fetched moments ago lacks this kid
			default:
				return nil, errJWKSUnavailable
			}
		}
		done := make(chan struct{})
		v.inflight, v.attempted, v.lastAttempt = done, true, now
		v.mu.Unlock()

		// The shared fetch must not fail just because this caller went away.
		keys, err := v.fetch(context.WithoutCancel(ctx))

		v.mu.Lock()
		if err == nil {
			v.keys, v.keysTime = keys, v.cfg.Now()
		}
		v.lastFailed = err != nil
		v.inflight = nil
		close(done)
		v.mu.Unlock()
		if err != nil {
			if usable && known {
				return k, nil // refresh failed; stale key still within grace
			}
			return nil, errJWKSUnavailable
		}
		// Loop once more to read the refreshed set under the lock.
	}
}

func (v *Verifier) fetch(ctx context.Context) (map[string]*ecdsa.PublicKey, error) {
	ctx, cancel := context.WithTimeout(ctx, v.cfg.Timeout)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, v.cfg.JWKSURL, nil)
	if err != nil {
		return nil, err
	}
	r.Header.Set("Accept", "application/json")
	response, err := v.httpClient.Do(r)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	ct, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if response.StatusCode != http.StatusOK || err != nil || ct != "application/json" {
		return nil, errJWKSUnavailable
	}
	const maxJWKS = 64 * 1024
	body, err := io.ReadAll(io.LimitReader(response.Body, maxJWKS+1))
	if err != nil || len(body) > maxJWKS {
		return nil, errJWKSUnavailable
	}
	var set struct {
		Keys []struct {
			Kty, Crv, X, Y, Kid, Alg, Use string
		} `json:"keys"`
	}
	if err := json.Unmarshal(body, &set); err != nil {
		return nil, errJWKSUnavailable
	}
	keys := make(map[string]*ecdsa.PublicKey, len(set.Keys))
	for _, k := range set.Keys {
		// Only P-256 signing keys are usable; anything else is ignored.
		if k.Kty != "EC" || k.Crv != "P-256" || (k.Alg != "" && k.Alg != "ES256") || (k.Use != "" && k.Use != "sig") || k.Kid == "" {
			continue
		}
		x, errX := base64.RawURLEncoding.DecodeString(k.X)
		y, errY := base64.RawURLEncoding.DecodeString(k.Y)
		if errX != nil || errY != nil || len(x) != 32 || len(y) != 32 {
			continue
		}
		pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), append(append([]byte{4}, x...), y...))
		if err != nil {
			continue
		}
		keys[k.Kid] = pub
	}
	if len(keys) == 0 {
		// A set without usable keys (e.g. a misconfigured proxy) must not wipe
		// the previous cache.
		return nil, errJWKSUnavailable
	}
	return keys, nil
}
