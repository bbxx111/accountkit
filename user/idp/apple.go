package idp

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/observability"
)

const (
	appleIssuer = "https://appleid.apple.com"
	appleLeeway = 30 * time.Second
	maxJWKSBody = 256 << 10
)

// AppleOptions 是 Apple 校验器的依赖。
type AppleOptions struct {
	BundleIDs []string
	JWKSURL   string
	HTTP      *http.Client
	Nonces    *NonceRegistry // 必填
	Logger    *slog.Logger
	Observer  observability.Observer // 可选；缓存降级不表示最终验证成功
	Now       func() time.Time       // nil → time.Now（验签时钟）
	// NonceTTL 是登记 nonce 的下限 TTL；实际登记时长取它与 id_token 剩余有效期
	// （exp + leeway）中的较大者，以覆盖整个 id_token 生命周期。默认 10m。
	NonceTTL time.Duration
}

// Apple 校验 Sign in with Apple 的 id_token（§5.3）。
type Apple struct {
	bundleIDs map[string]struct{}
	jwksURL   string
	jwks      *jwksCache
	nonces    *NonceRegistry
	now       func() time.Time
	nonceTTL  time.Duration
}

// NewApple 构造校验器；BundleIDs 为空表示未启用。不做 I/O（JWKS 懒加载）。
func NewApple(o AppleOptions) *Apple {
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
	set := make(map[string]struct{}, len(o.BundleIDs))
	for _, b := range o.BundleIDs {
		set[b] = struct{}{}
	}
	jwks := newJWKSCache("apple", o.JWKSURL, o.HTTP, o.Logger, observability.NewRecorder(o.Observer, o.Logger), observability.DegradationAppleCachedKeyFallback, o.Now)
	return &Apple{bundleIDs: set, jwksURL: o.JWKSURL, jwks: jwks, nonces: o.Nonces, now: o.Now, nonceTTL: o.NonceTTL}
}

// errJWKSUnavailable 在 keyfunc 内部标记“取不到 JWKS 且无缓存”，外层映射为 ErrUnavailable。
var errJWKSUnavailable = errors.New("jwks unavailable")

// Kind 实现 Provider。
func (a *Apple) Kind() enum.IdentityKind { return enum.IdentityApple }

// Verify 实现 Provider：credential 为 {"id_token","nonce"}。
func (a *Apple) Verify(ctx context.Context, credential json.RawMessage) (Identity, error) {
	c, err := decodeTokenCredential(credential)
	if err != nil {
		return Identity{}, err
	}
	return a.VerifyApple(ctx, c.IDToken, c.Nonce)
}

// VerifyApple 校验 id_token 与原始 nonce。
func (a *Apple) VerifyApple(ctx context.Context, idToken, rawNonce string) (Identity, error) {
	if len(a.bundleIDs) == 0 {
		return Identity{}, fmt.Errorf("%w: apple sign-in not configured", ErrAppNotAllowed)
	}
	if a.nonces == nil {
		return Identity{}, fmt.Errorf("%w: nonce registry not configured", ErrMisconfigured)
	}
	if idToken == "" || rawNonce == "" {
		return Identity{}, fmt.Errorf("%w: id_token and nonce are required", ErrInvalidCredential)
	}
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}),
		jwt.WithIssuer(appleIssuer),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(appleLeeway),
		jwt.WithTimeFunc(a.now),
	)
	claims := jwt.MapClaims{}
	_, err := parser.ParseWithClaims(idToken, claims, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		if kid == "" {
			return nil, errors.New("missing kid")
		}
		return a.jwks.key(ctx, kid)
	})
	if err != nil {
		if errors.Is(err, errJWKSUnavailable) {
			return Identity{}, fmt.Errorf("%w: apple jwks: %v", ErrUnavailable, err)
		}
		// jwt 库的错误文本不回显 token 本体，可安全拼接。
		return Identity{}, fmt.Errorf("%w: apple id_token: %v", ErrInvalidCredential, err)
	}
	if !a.audienceAllowed(claims["aud"]) {
		return Identity{}, fmt.Errorf("%w: apple aud", ErrAppNotAllowed)
	}
	sub, _ := claims["sub"].(string)
	if sub == "" {
		return Identity{}, fmt.Errorf("%w: apple sub missing", ErrInvalidCredential)
	}
	nonceClaim, _ := claims["nonce"].(string)
	want := sha256.Sum256([]byte(rawNonce))
	wantHex := hex.EncodeToString(want[:])
	if nonceClaim == "" || subtle.ConstantTimeCompare([]byte(nonceClaim), []byte(wantHex)) != 1 {
		return Identity{}, fmt.Errorf("%w: apple nonce mismatch", ErrInvalidCredential)
	}
	// nonce 登记 TTL 必须覆盖 id_token 本身的剩余有效期（+ leeway），否则密钥被截获后，
	// 一个在 nonceTTL 之后、exp 之前仍然合法的 id_token 又能重新登录一次（§5.4）。
	ttl := a.nonceTTL
	if expTime, err := claims.GetExpirationTime(); err == nil && expTime != nil {
		if extended := expTime.Time.Add(appleLeeway).Sub(a.now()); extended > ttl {
			ttl = extended
		}
	}
	if err := a.nonces.Register(ctx, nonceClaim, ttl); err != nil {
		return Identity{}, err // ErrNonceReplayed / ErrUnavailable / ErrInvalidCredential 原样透出
	}
	id := Identity{Kind: enum.IdentityApple, Subject: sub}
	if exp, err := claims.GetExpirationTime(); err == nil && exp != nil {
		id.ExpireTime = exp.Time
	}
	if email, _ := claims["email"].(string); email != "" && emailVerified(claims["email_verified"]) {
		id.HintEmail = email
	}
	return id, nil
}

func (a *Apple) audienceAllowed(aud any) bool {
	switch v := aud.(type) {
	case string:
		_, ok := a.bundleIDs[v]
		return ok
	case []any:
		for _, x := range v {
			if s, ok := x.(string); ok {
				if _, ok := a.bundleIDs[s]; ok {
					return true
				}
			}
		}
	}
	return false
}

// emailVerified 接受 Apple 的两种表示：bool true 或字符串 "true"。
func emailVerified(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x == "true"
	}
	return false
}
