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
// MinRefreshInterval throttles JWKS refreshes triggered by unknown key IDs.
type VerifierConfig struct {
	JWKSURL, Issuer, Audience string
	HTTPClient                *http.Client
	Timeout                   time.Duration // per JWKS request, default 5s
	Leeway                    time.Duration // clock skew, default 30s
	MinRefreshInterval        time.Duration // default 60s
	AllowHTTP                 bool
	Now                       func() time.Time
}

// Verifier checks ES256 access tokens locally. It never calls introspection, so
// a revoked session's access stays accepted until it expires (at most the access
// TTL plus leeway). Routes that need immediate revocation must also introspect.
type Verifier struct {
	cfg        VerifierConfig
	httpClient http.Client

	mu        sync.Mutex
	keys      map[string]*ecdsa.PublicKey
	fetched   bool
	lastFetch time.Time
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

// key returns the cached key, refreshing the JWKS at most once per
// MinRefreshInterval when the key ID is unknown.
func (v *Verifier) key(ctx context.Context, kid string) (*ecdsa.PublicKey, error) {
	if kid == "" {
		return nil, errUnknownKey
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if k, ok := v.keys[kid]; ok {
		return k, nil
	}
	if v.fetched && v.cfg.Now().Sub(v.lastFetch) < v.cfg.MinRefreshInterval {
		return nil, errUnknownKey
	}
	keys, err := v.fetch(ctx)
	if err != nil {
		return nil, errJWKSUnavailable
	}
	v.keys, v.fetched, v.lastFetch = keys, true, v.cfg.Now()
	if k, ok := v.keys[kid]; ok {
		return k, nil
	}
	return nil, errUnknownKey
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
	return keys, nil
}
