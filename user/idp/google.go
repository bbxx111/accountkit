package idp

import (
	"context"
	"crypto/rsa"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/observability"
)

const (
	googleIssuerHTTPS     = "https://accounts.google.com"
	googleIssuerBare      = "accounts.google.com"
	googleJWKSDefault     = "https://www.googleapis.com/oauth2/v3/certs"
	googleLeeway          = 30 * time.Second
	googleRefreshThrottle = 60 * time.Second
)

// GoogleOptions 是 Google 校验器的依赖。
type GoogleOptions struct {
	ClientIDs []string
	JWKSURL   string
	HTTP      *http.Client
	Nonces    *NonceRegistry
	Logger    *slog.Logger
	Observer  observability.Observer
	Now       func() time.Time
	// NonceTTL 是登记 nonce 的下限；实际时长取它与 exp+leeway 的较大者。默认 10m。
	NonceTTL time.Duration
}

// Google 校验 Google ID token。nonce 与客户端提交的原始字符串做常量时间比较。
type Google struct {
	clientIDs map[string]struct{}
	jwksURL   string
	hc        *http.Client
	nonces    *NonceRegistry
	logger    *slog.Logger
	observer  *observability.Recorder
	now       func() time.Time
	nonceTTL  time.Duration

	mu            sync.Mutex
	keys          map[string]*rsa.PublicKey
	lastRefresh   time.Time
	lastRefreshOK bool
}

// NewGoogle 构造校验器。ClientIDs 为空表示未启用。不做 I/O。
func NewGoogle(o GoogleOptions) *Google {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.HTTP == nil {
		o.HTTP = &http.Client{Timeout: 10 * time.Second}
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.NonceTTL <= 0 {
		o.NonceTTL = 10 * time.Minute
	}
	if o.JWKSURL == "" {
		o.JWKSURL = googleJWKSDefault
	}
	set := make(map[string]struct{}, len(o.ClientIDs))
	for _, id := range o.ClientIDs {
		set[id] = struct{}{}
	}
	return &Google{
		clientIDs: set, jwksURL: o.JWKSURL, hc: o.HTTP, nonces: o.Nonces,
		logger: o.Logger, observer: observability.NewRecorder(o.Observer, o.Logger),
		now: o.Now, nonceTTL: o.NonceTTL,
	}
}

// Kind 实现 Provider。
func (g *Google) Kind() enum.IdentityKind { return enum.IdentityGoogle }

// Verify 实现 Provider：credential 为 {"id_token","nonce"}。
func (g *Google) Verify(ctx context.Context, credential json.RawMessage) (Identity, error) {
	c, err := decodeTokenCredential(credential)
	if err != nil {
		return Identity{}, err
	}
	return g.VerifyGoogle(ctx, c.IDToken, c.Nonce)
}

// VerifyGoogle 校验 Google ID token 与原始 nonce。
func (g *Google) VerifyGoogle(ctx context.Context, idToken, rawNonce string) (Identity, error) {
	if len(g.clientIDs) == 0 {
		return Identity{}, fmt.Errorf("%w: google sign-in not configured", ErrAppNotAllowed)
	}
	if g.nonces == nil {
		return Identity{}, fmt.Errorf("%w: nonce registry not configured", ErrMisconfigured)
	}
	if idToken == "" || rawNonce == "" {
		return Identity{}, fmt.Errorf("%w: id_token and nonce are required", ErrInvalidCredential)
	}
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(googleLeeway),
		jwt.WithTimeFunc(g.now),
	)
	claims := jwt.MapClaims{}
	_, err := parser.ParseWithClaims(idToken, claims, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		if kid == "" {
			return nil, errors.New("missing kid")
		}
		return g.key(ctx, kid)
	})
	if err != nil {
		if errors.Is(err, errJWKSUnavailable) {
			return Identity{}, fmt.Errorf("%w: google jwks: %v", ErrUnavailable, err)
		}
		return Identity{}, fmt.Errorf("%w: google id_token: %v", ErrInvalidCredential, err)
	}
	iss, _ := claims["iss"].(string)
	if iss != googleIssuerHTTPS && iss != googleIssuerBare {
		return Identity{}, fmt.Errorf("%w: google iss", ErrInvalidCredential)
	}
	if !g.audienceAllowed(claims["aud"]) {
		return Identity{}, fmt.Errorf("%w: google aud", ErrAppNotAllowed)
	}
	if azp, present := claims["azp"]; present {
		s, _ := azp.(string)
		if _, ok := g.clientIDs[s]; !ok {
			return Identity{}, fmt.Errorf("%w: google azp", ErrAppNotAllowed)
		}
	}
	sub, _ := claims["sub"].(string)
	if sub == "" {
		return Identity{}, fmt.Errorf("%w: google sub missing", ErrInvalidCredential)
	}
	nonceClaim, _ := claims["nonce"].(string)
	if nonceClaim == "" || subtle.ConstantTimeCompare([]byte(nonceClaim), []byte(rawNonce)) != 1 {
		return Identity{}, fmt.Errorf("%w: google nonce mismatch", ErrInvalidCredential)
	}
	ttl := g.nonceTTL
	if expTime, err := claims.GetExpirationTime(); err == nil && expTime != nil {
		if extended := expTime.Time.Add(googleLeeway).Sub(g.now()); extended > ttl {
			ttl = extended
		}
	}
	if err := g.nonces.RegisterScoped(ctx, "google", nonceClaim, ttl); err != nil {
		return Identity{}, err
	}
	id := Identity{Kind: enum.IdentityGoogle, Subject: sub}
	if exp, err := claims.GetExpirationTime(); err == nil && exp != nil {
		id.ExpireTime = exp.Time
	}
	if email, _ := claims["email"].(string); email != "" && emailVerified(claims["email_verified"]) {
		id.HintEmail = email
	}
	if name, _ := claims["name"].(string); name != "" {
		id.Name = name
	}
	if picture, _ := claims["picture"].(string); picture != "" {
		id.Picture = picture
	}
	return id, nil
}

func (g *Google) audienceAllowed(aud any) bool {
	switch v := aud.(type) {
	case string:
		_, ok := g.clientIDs[v]
		return ok
	case []any:
		for _, x := range v {
			if s, ok := x.(string); ok {
				if _, ok := g.clientIDs[s]; ok {
					return true
				}
			}
		}
	}
	return false
}

func (g *Google) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	g.mu.Lock()
	cachedFallback := false
	defer func() {
		g.mu.Unlock()
		if cachedFallback && g.observer.Enabled() {
			g.observer.Observe(ctx, observability.DegradationObservation{Scenario: observability.DegradationGoogleCachedKeyFallback, Count: 1})
		}
	}()
	if k, ok := g.keys[kid]; ok {
		cachedFallback = !g.lastRefreshOK
		return k, nil
	}
	now := g.now()
	if g.keys == nil {
		if !g.lastRefresh.IsZero() && now.Sub(g.lastRefresh) < googleRefreshThrottle {
			return nil, fmt.Errorf("%w: jwks refresh throttled", errJWKSUnavailable)
		}
		if err := g.refreshLocked(ctx); err != nil {
			g.lastRefresh = now
			g.lastRefreshOK = false
			return nil, fmt.Errorf("%w: %v", errJWKSUnavailable, err)
		}
		g.lastRefreshOK = true
	} else if now.Sub(g.lastRefresh) >= googleRefreshThrottle {
		g.lastRefresh = now
		if err := g.refreshLocked(ctx); err != nil {
			g.lastRefreshOK = false
			g.logger.Warn("google jwks refresh failed; serving cached keys", "err", err)
		} else {
			g.lastRefreshOK = true
		}
	}
	if k, ok := g.keys[kid]; ok {
		return k, nil
	}
	if !g.lastRefreshOK {
		return nil, fmt.Errorf("%w: unknown kid %q", errJWKSUnavailable, kid)
	}
	return nil, fmt.Errorf("unknown kid %q", kid)
}

func (g *Google) refreshLocked(ctx context.Context) error {
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(fctx, http.MethodGet, g.jwksURL, nil)
	if err != nil {
		return redactTransportErr(err)
	}
	resp, err := g.hc.Do(req)
	if err != nil {
		return redactTransportErr(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("jwks http %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxJWKSBody))
	if err != nil {
		return redactTransportErr(err)
	}
	keys, err := parseRSAJWKSet(body)
	if err != nil {
		return err
	}
	g.keys = keys
	return nil
}
