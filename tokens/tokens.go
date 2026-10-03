// Package tokens 签发与验签 C 端 access token：HS256，密钥版本化，header.kid 标版本，
// 使密钥轮换的重叠窗口内旧凭证仍能验签。
//
// 验签无共享状态（只依赖签名密钥），吊销由上层的 Redis 吊销集处理。
package tokens

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// MinKeyLen 是 HS256 密钥最小长度（字节）。
const MinKeyLen = 32

// ErrInvalidToken 表示 token 缺失、格式错、验签失败、过期或 iss/aud 不匹配。
// 用 errors.Is 判别；具体原因在错误链中，只应写日志。
var ErrInvalidToken = errors.New("tokens: invalid token")

// Claims 是本模块关心的 access token 内容。
type Claims struct {
	UserID    string    // sub
	SessionID string    // sid
	Scope     string    // scope
	AuthTime  time.Time // auth_time：最近一次登录或重新认证时刻
	JTI       string
	IssuedAt  time.Time
	ExpiresAt time.Time
}

// Options 是 Signer 的配置。
type Options struct {
	Keys     map[uint16][]byte
	Active   uint16
	Issuer   string
	Audience string
	Leeway   time.Duration // 默认 30s
}

// Signer 用 active 版本签发、按 kid 验签。
type Signer struct {
	keys     map[uint16][]byte
	active   uint16
	issuer   string
	audience string
	leeway   time.Duration
}

type wireClaims struct {
	SessionID string `json:"sid"`
	Scope     string `json:"scope"`
	AuthTime  int64  `json:"auth_time"`
	jwt.RegisteredClaims
}

// NewSigner 校验密钥长度、active 存在、iss/aud 非空。
func NewSigner(o Options) (*Signer, error) {
	if len(o.Keys) == 0 {
		return nil, errors.New("tokens: no signing keys configured")
	}
	keys := make(map[uint16][]byte, len(o.Keys))
	for v, k := range o.Keys {
		if len(k) < MinKeyLen {
			return nil, fmt.Errorf("tokens: key version %d is %d bytes, want at least %d", v, len(k), MinKeyLen)
		}
		keys[v] = append([]byte(nil), k...)
	}
	if _, ok := keys[o.Active]; !ok {
		return nil, fmt.Errorf("tokens: active key version %d not configured", o.Active)
	}
	if o.Issuer == "" {
		return nil, errors.New("tokens: issuer must not be empty")
	}
	if o.Audience == "" {
		return nil, errors.New("tokens: audience must not be empty")
	}
	if o.Leeway == 0 {
		o.Leeway = 30 * time.Second
	}
	return &Signer{keys: keys, active: o.Active, issuer: o.Issuer, audience: o.Audience, leeway: o.Leeway}, nil
}

// Active 返回当前签发用的密钥版本。
func (s *Signer) Active() uint16 { return s.active }

// Leeway 返回 Parse 校验 exp/nbf/iat 时容忍的时钟漂移窗口。调用方吊销会话时应把这段窗口
// 计入吊销集 TTL：exp 之后的 Leeway 内，一个已吊销的 token 仍会通过签名与过期校验。
func (s *Signer) Leeway() time.Duration { return s.leeway }

// Sign 用 active 版本签发。jti 为 16 字节随机 hex；auth_time 以秒精度写入。
func (s *Signer) Sign(c Claims, now time.Time, ttl time.Duration) (string, string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", "", fmt.Errorf("tokens: jti: %w", err)
	}
	jti := hex.EncodeToString(b[:])
	wc := wireClaims{
		SessionID: c.SessionID,
		Scope:     c.Scope,
		AuthTime:  c.AuthTime.Unix(),
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        jti,
			Issuer:    s.issuer,
			Audience:  jwt.ClaimStrings{s.audience},
			Subject:   c.UserID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, wc)
	tok.Header["kid"] = strconv.FormatUint(uint64(s.active), 10)
	signed, err := tok.SignedString(s.keys[s.active])
	if err != nil {
		return "", "", fmt.Errorf("tokens: sign: %w", err)
	}
	return signed, jti, nil
}

// Parse 按 header.kid 选钥验签，并校验 iss、aud、exp（必需）、nbf、iat（含 leeway）。
func (s *Signer) Parse(token string) (Claims, error) {
	var wc wireClaims
	_, err := jwt.NewParser(
		jwt.WithValidMethods([]string{"HS256"}),
		jwt.WithIssuer(s.issuer),
		jwt.WithAudience(s.audience),
		jwt.WithLeeway(s.leeway),
		jwt.WithExpirationRequired(),
	).ParseWithClaims(token, &wc, func(t *jwt.Token) (any, error) {
		kidStr, _ := t.Header["kid"].(string)
		if kidStr == "" {
			return nil, errors.New("missing kid")
		}
		kid, err := strconv.ParseUint(kidStr, 10, 16)
		if err != nil {
			return nil, fmt.Errorf("bad kid %q", kidStr)
		}
		k, ok := s.keys[uint16(kid)]
		if !ok {
			return nil, fmt.Errorf("unknown kid %d", kid)
		}
		return k, nil
	})
	if err != nil {
		return Claims{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	out := Claims{
		UserID:    wc.Subject,
		SessionID: wc.SessionID,
		Scope:     wc.Scope,
		AuthTime:  time.Unix(wc.AuthTime, 0),
		JTI:       wc.ID,
	}
	if wc.IssuedAt != nil {
		out.IssuedAt = wc.IssuedAt.Time
	}
	if wc.ExpiresAt != nil {
		out.ExpiresAt = wc.ExpiresAt.Time
	}
	return out, nil
}
