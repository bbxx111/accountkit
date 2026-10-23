// Package idp 把第三方登录凭证换成已验证的身份（微信 UnionID、Apple/Google/OIDC sub）。
//
// 只做校验与取值，不碰数据库；换回的第三方 access/refresh token 用完即弃，
// 不落日志、不返回给调用方。错误以哨兵表达，HTTP 层按 errors.Is 映射。
package idp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/bbxx111/accountkit/enum"
)

// Identity 是已通过第三方校验的身份。
type Identity struct {
	Kind    enum.IdentityKind
	Subject string
	// UnionID 是微信已验证的跨应用标识，仅用作白名单元数据。
	UnionID   string
	OpenIDs   map[string]string
	HintEmail string
	// Name 和 Picture 是提供方返回的资料声明。登录不因这两项失败；落库前再净化。
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
	// ErrMalformedCredential：凭证不是对象、缺字段、含未知字段或类型错误；在任何外部调用与 nonce 登记前返回。
	ErrMalformedCredential = errors.New("idp: malformed credential")
)

// Provider 是一个已登记的第三方身份类型。Verify 收到客户端 credential 对象的原始 JSON，
// 自行严格解码；返回的 Identity.Kind 必须等于 Kind()。错误应使用本包哨兵，其余错误按内部错误处理。
type Provider interface {
	Kind() enum.IdentityKind
	Verify(ctx context.Context, credential json.RawMessage) (Identity, error)
	// PolicyFingerprint 标识允许的应用与验证配置，不含秘密；配置变化时必须变化。
	PolicyFingerprint() string
}

// tokenCredential 是 ID token 类提供方（Apple、Google、OIDC）的凭证。
type tokenCredential struct {
	IDToken string `json:"id_token"`
	Nonce   string `json:"nonce"`
}

func decodeTokenCredential(raw json.RawMessage) (tokenCredential, error) {
	var c tokenCredential
	if err := decodeCredential(raw, &c); err != nil {
		return c, err
	}
	if c.IDToken == "" || c.Nonce == "" {
		return c, fmt.Errorf("%w: id_token and nonce are required", ErrMalformedCredential)
	}
	return c, nil
}

// decodeCredential 严格解码单个 JSON 对象：拒绝非对象、未知字段与尾随内容。
func decodeCredential(raw json.RawMessage, dst any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return fmt.Errorf("%w: credential must be a JSON object", ErrMalformedCredential)
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("%w: %v", ErrMalformedCredential, err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf("%w: trailing data", ErrMalformedCredential)
	}
	return nil
}
