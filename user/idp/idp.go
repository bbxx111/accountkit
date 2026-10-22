// Package idp 把第三方登录凭证换成已验证的身份（微信 UnionID、Apple sub）。
//
// 只做校验与取值，不碰数据库；换回的第三方 access/refresh token 用完即弃，
// 不落日志、不返回给调用方。错误以哨兵表达，HTTP 层按 errors.Is 映射。
package idp

import (
	"context"
	"errors"
	"time"

	"github.com/bbxx111/accountkit/enum"
)

// Identity 是已通过第三方校验的身份。
type Identity struct {
	Kind      enum.IdentityKind
	Subject   string
	OpenIDs   map[string]string
	HintEmail string
	// Name 和 Picture 是 Google 身份声明。登录不因这两项失败；落库前再净化。
	Name    string
	Picture string
	// ExpireTime 是已验证凭证的原始期限；未知期限保持零值。
	ExpireTime time.Time
}

var (
	// ErrInvalidCredential：code 无效/已用/过期，或 id_token 验签、iss、exp、nonce 不通过。
	ErrInvalidCredential = errors.New("idp: invalid credential")
	// ErrAppNotAllowed：app_id 不在 WeChatApps，或 aud 不在允许的客户端标识（含 IdP 未启用）。
	ErrAppNotAllowed = errors.New("idp: app not allowed")
	// ErrNonceReplayed：同一 nonce 在 TTL 内第二次出现。
	ErrNonceReplayed = errors.New("idp: nonce already used")
	// ErrUnavailable：IdP 网络/5xx、JWKS 不可得、Redis 不可用 → 503。
	ErrUnavailable = errors.New("idp: provider unavailable")
	// ErrMisconfigured：服务端配置问题（appid/secret 无效、微信未返回 unionid）→ 500 + 告警日志。
	ErrMisconfigured = errors.New("idp: provider misconfigured")
)

// WeChatVerifier 用 code 换取 UnionID。
type WeChatVerifier interface {
	VerifyWeChat(ctx context.Context, appID, code string) (Identity, error)
}

// AppleVerifier 校验 id_token 与 nonce。
type AppleVerifier interface {
	VerifyApple(ctx context.Context, idToken, rawNonce string) (Identity, error)
}

// GoogleVerifier 校验 Google ID token 与原始 nonce。
type GoogleVerifier interface {
	VerifyGoogle(ctx context.Context, idToken, rawNonce string) (Identity, error)
}
