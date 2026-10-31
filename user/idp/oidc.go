package idp

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/observability"
)

const idTokenLeeway = 30 * time.Second

// idTokenVerifier 是 Google 与配置 OIDC 提供方共用的 ID token 校验核心：RS256 + JWKS、
// issuer 精确匹配、aud/azp 命中允许列表、exp 必填且偏差 30 秒、原文 nonce 常量时间比较，
// 通过后按 nonceScope 登记 nonce，期限覆盖 token 剩余有效期。
type idTokenVerifier struct {
	kind          enum.IdentityKind
	label         string // 错误消息前缀，不含秘密
	issuers       []string
	clientIDs     map[string]struct{}
	jwks          *jwksCache
	nonces        *NonceRegistry
	nonceScope    string
	nonceTTL      time.Duration
	now           func() time.Time
	nameClaims    []string
	pictureClaims []string
	// 提供方不给 email_verified 时仍把 email 作提示（Facebook）。
	hintEmailUnverified bool
}

func (v *idTokenVerifier) verify(ctx context.Context, idToken, rawNonce string) (Identity, error) {
	if len(v.clientIDs) == 0 {
		return Identity{}, fmt.Errorf("%w: %s sign-in not configured", ErrAppNotAllowed, v.label)
	}
	if v.nonces == nil {
		return Identity{}, fmt.Errorf("%w: nonce registry not configured", ErrMisconfigured)
	}
	if idToken == "" || rawNonce == "" {
		return Identity{}, fmt.Errorf("%w: id_token and nonce are required", ErrInvalidCredential)
	}
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(idTokenLeeway),
		jwt.WithTimeFunc(v.now),
	)
	claims := jwt.MapClaims{}
	_, err := parser.ParseWithClaims(idToken, claims, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		if kid == "" {
			return nil, errors.New("missing kid")
		}
		return v.jwks.key(ctx, kid)
	})
	if err != nil {
		if errors.Is(err, errJWKSUnavailable) {
			return Identity{}, fmt.Errorf("%w: %s jwks: %v", ErrUnavailable, v.label, err)
		}
		// jwt 库的错误文本不回显 token 本体，可安全拼接。
		return Identity{}, fmt.Errorf("%w: %s id_token: %v", ErrInvalidCredential, v.label, err)
	}
	iss, _ := claims["iss"].(string)
	if iss == "" || !slices.Contains(v.issuers, iss) {
		return Identity{}, fmt.Errorf("%w: %s iss", ErrInvalidCredential, v.label)
	}
	if !v.audienceAllowed(claims["aud"]) {
		return Identity{}, fmt.Errorf("%w: %s aud", ErrAppNotAllowed, v.label)
	}
	if azp, present := claims["azp"]; present {
		s, _ := azp.(string)
		if _, ok := v.clientIDs[s]; !ok {
			return Identity{}, fmt.Errorf("%w: %s azp", ErrAppNotAllowed, v.label)
		}
	}
	sub, _ := claims["sub"].(string)
	if sub == "" {
		return Identity{}, fmt.Errorf("%w: %s sub missing", ErrInvalidCredential, v.label)
	}
	nonceClaim, _ := claims["nonce"].(string)
	if nonceClaim == "" || subtle.ConstantTimeCompare([]byte(nonceClaim), []byte(rawNonce)) != 1 {
		return Identity{}, fmt.Errorf("%w: %s nonce mismatch", ErrInvalidCredential, v.label)
	}
	ttl := v.nonceTTL
	if expTime, err := claims.GetExpirationTime(); err == nil && expTime != nil {
		if extended := expTime.Time.Add(idTokenLeeway).Sub(v.now()); extended > ttl {
			ttl = extended
		}
	}
	if err := v.nonces.RegisterScoped(ctx, v.nonceScope, nonceClaim, ttl); err != nil {
		return Identity{}, err
	}
	id := Identity{Kind: v.kind, Subject: sub}
	if exp, err := claims.GetExpirationTime(); err == nil && exp != nil {
		id.ExpireTime = exp.Time
	}
	if email, _ := claims["email"].(string); email != "" && (v.hintEmailUnverified || emailVerified(claims["email_verified"])) {
		id.HintEmail = email
	}
	id.Name = firstStringClaim(claims, v.nameClaims)
	id.Picture = firstStringClaim(claims, v.pictureClaims)
	return id, nil
}

// firstStringClaim 按顺序返回第一个非空的顶层字符串声明；非字符串值跳过。
func firstStringClaim(claims jwt.MapClaims, names []string) string {
	for _, name := range names {
		if s, ok := claims[name].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func (v *idTokenVerifier) audienceAllowed(aud any) bool {
	switch x := aud.(type) {
	case string:
		_, ok := v.clientIDs[x]
		return ok
	case []any:
		for _, item := range x {
			if s, ok := item.(string); ok {
				if _, ok := v.clientIDs[s]; ok {
					return true
				}
			}
		}
	}
	return false
}

func (v *idTokenVerifier) fingerprint(endpoint string) string {
	apps := make([]string, 0, len(v.clientIDs))
	for app := range v.clientIDs {
		apps = append(apps, app)
	}
	return configurationFingerprint(strings.ToLower(string(v.kind)), endpoint, apps)
}

func clientIDSet(ids []string) map[string]struct{} {
	set := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return set
}

// OIDCOptions 是一个配置驱动 OIDC 提供方的依赖。Kind、Issuer、JWKSURL 与 ClientIDs
// 由装配方校验；NameClaims/PictureClaims 为 nil 时默认 ["name"]/["picture"]，
// 非 nil 空切片表示不读取该项。
type OIDCOptions struct {
	Kind          enum.IdentityKind
	Issuer        string
	JWKSURL       string
	ClientIDs     []string
	NameClaims    []string
	PictureClaims []string
	HTTP          *http.Client
	Nonces        *NonceRegistry
	Logger        *slog.Logger
	Observer      observability.Observer
	Now           func() time.Time
	NonceTTL      time.Duration // nonce 登记下限，默认 10m
}

// OIDC 校验配置驱动 OIDC 提供方签发的 ID token，以 sub 作为身份主键。
type OIDC struct {
	issuer  string
	jwksURL string
	v       *idTokenVerifier
}

// NewOIDC 构造校验器，不做 I/O（JWKS 懒加载）。
// 构造不承诺完整启动校验；缺少验证依赖等配置问题可能在 Verify 时返回 ErrMisconfigured。
func NewOIDC(o OIDCOptions) *OIDC {
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
	if o.NameClaims == nil {
		o.NameClaims = []string{"name"}
	}
	if o.PictureClaims == nil {
		o.PictureClaims = []string{"picture"}
	}
	label := strings.ToLower(string(o.Kind))
	recorder := observability.NewRecorder(o.Observer, o.Logger)
	return &OIDC{issuer: o.Issuer, jwksURL: o.JWKSURL, v: &idTokenVerifier{
		kind: o.Kind, label: label, issuers: []string{o.Issuer}, clientIDs: clientIDSet(o.ClientIDs),
		jwks:   newJWKSCache(label, o.JWKSURL, o.HTTP, o.Logger, recorder, observability.DegradationOIDCCachedKeyFallback, o.Now),
		nonces: o.Nonces, nonceScope: "oidc:" + string(o.Kind), nonceTTL: o.NonceTTL, now: o.Now,
		nameClaims: slices.Clone(o.NameClaims), pictureClaims: slices.Clone(o.PictureClaims),
	}}
}

// Kind 实现 Provider。
func (o *OIDC) Kind() enum.IdentityKind { return o.v.kind }

// Verify 实现 Provider：credential 为 {"id_token","nonce"}。
func (o *OIDC) Verify(ctx context.Context, credential json.RawMessage) (Identity, error) {
	c, err := decodeTokenCredential(credential)
	if err != nil {
		return Identity{}, err
	}
	return o.VerifyIDToken(ctx, c.IDToken, c.Nonce)
}

// VerifyIDToken 校验 ID token 与原始 nonce。
func (o *OIDC) VerifyIDToken(ctx context.Context, idToken, rawNonce string) (Identity, error) {
	return o.v.verify(ctx, idToken, rawNonce)
}

// PolicyFingerprint 标识类型、issuer、JWKS 地址与 client ID 允许列表，不含秘密。
func (o *OIDC) PolicyFingerprint() string { return o.v.fingerprint(o.issuer + "\n" + o.jwksURL) }
