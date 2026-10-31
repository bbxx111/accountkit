package idp

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/observability"
)

const (
	googleIssuerHTTPS = "https://accounts.google.com"
	googleIssuerBare  = "accounts.google.com"
	googleJWKSDefault = "https://www.googleapis.com/oauth2/v3/certs"
)

// GoogleOptions 是 Google ID token 校验器的依赖。
type GoogleOptions struct {
	ClientIDs []string
	JWKSURL   string
	HTTP      *http.Client
	Nonces    *NonceRegistry
	Logger    *slog.Logger
	Observer  observability.Observer
	Now       func() time.Time
	// NonceTTL 是 nonce 登记下限；实际期限覆盖 ID token 剩余有效期。默认 10m。
	NonceTTL time.Duration
}

// Google 校验 Google ID token，并保留登录不依赖的身份声明。它是共用 OIDC 校验核心的预设：
// 两个固定签发者、默认 JWKS、nonce 作用域 google、声明 name/picture。
type Google struct {
	jwksURL string
	v       *idTokenVerifier
}

// NewGoogle 构造校验器；ClientIDs 为空表示未启用。不做 I/O，JWKS 首次验证时加载。
// 构造不承诺完整启动校验；缺少验证依赖等配置问题可能在 Verify 时返回 ErrMisconfigured。
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
	recorder := observability.NewRecorder(o.Observer, o.Logger)
	return &Google{jwksURL: o.JWKSURL, v: &idTokenVerifier{
		kind: enum.IdentityGoogle, label: "google", issuers: []string{googleIssuerHTTPS, googleIssuerBare},
		clientIDs: clientIDSet(o.ClientIDs),
		jwks:      newJWKSCache("google", o.JWKSURL, o.HTTP, o.Logger, recorder, observability.DegradationGoogleCachedKeyFallback, o.Now),
		nonces:    o.Nonces, nonceScope: "google", nonceTTL: o.NonceTTL, now: o.Now,
		nameClaims: []string{"name"}, pictureClaims: []string{"picture"},
	}}
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
	return g.v.verify(ctx, idToken, rawNonce)
}
