// Package idp 把第三方登录凭证换成已验证的身份（微信 UnionID、Apple sub）。
//
// 只做校验与取值，不碰数据库；换回的第三方 access/refresh token 用完即弃，
// 不落日志、不返回给调用方。错误以哨兵表达，HTTP 层按 errors.Is 映射。
package idp

import (
	"context"
	"errors"

	"github.com/bbxx111/accountkit/enum"
)

// Identity 是已通过第三方校验的身份。
type Identity struct {
	Kind      enum.IdentityKind
	Subject   string
	OpenIDs   map[string]string
	HintEmail string
}

var (
	// ErrInvalidCredential：code 无效/已用/过期，或 id_token 验签、iss、exp、nonce 不通过。
	ErrInvalidCredential = errors.New("idp: invalid credential")
	// ErrAppNotAllowed：app_id 不在 WeChatApps，或 aud 不在 AppleBundleIDs（含 IdP 未启用）。
	ErrAppNotAllowed = errors.New("idp: app not allowed")
	// ErrNonceReplayed：同一 Apple nonce 在 TTL 内第二次出现。
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
