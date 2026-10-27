// Package tokens 签发与验签 C 端 access token：ES256（P-256），header 带 typ=at+jwt（RFC 9068），
// 密钥版本化，header.kid 标版本，使密钥轮换的重叠窗口内旧凭证仍能验签。
//
// 验签无共享状态（只依赖公钥），公钥经 JWKS 发布供业务服务本地验签；吊销由上层处理。
package tokens

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	// Algorithm 是唯一接受的签名算法。
	Algorithm = "ES256"
	// TokenType 是 access token 的 header typ（RFC 9068）。
	TokenType = "at+jwt"
)

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
	Keys     map[uint16]*ecdsa.PrivateKey // 仅 P-256
	Active   uint16
	Issuer   string
	Audience string
	Leeway   time.Duration // 默认 30s
}

// Signer 用 active 版本签发、按 kid 验签。
type Signer struct {
	keys     map[uint16]*ecdsa.PrivateKey
	active   uint16
	issuer   string
	audience string
	leeway   time.Duration
	jwks     []byte
}

type wireClaims struct {
	SessionID string `json:"sid"`
	Scope     string `json:"scope"`
	AuthTime  int64  `json:"auth_time"`
	jwt.RegisteredClaims
}

// CheckKeys 校验密钥非空、均为 P-256 私钥且 active 版本已配置；错误不含密钥内容。
func CheckKeys(keys map[uint16]*ecdsa.PrivateKey, active uint16) error {
	if len(keys) == 0 {
		return errors.New("tokens: no signing keys configured")
	}
	for v, k := range keys {
		if k == nil || k.Curve != elliptic.P256() {
			return fmt.Errorf("tokens: key version %d must be a P-256 private key", v)
		}
	}
	if _, ok := keys[active]; !ok {
		return fmt.Errorf("tokens: active key version %d not configured", active)
	}
	return nil
}

// NewSigner 校验密钥、iss/aud 非空，并预生成 JWKS。
func NewSigner(o Options) (*Signer, error) {
	if err := CheckKeys(o.Keys, o.Active); err != nil {
		return nil, err
	}
	keys := make(map[uint16]*ecdsa.PrivateKey, len(o.Keys))
	for v, k := range o.Keys {
		keys[v] = k
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
	jwks, err := marshalJWKS(keys)
	if err != nil {
		return nil, err
	}
	return &Signer{keys: keys, active: o.Active, issuer: o.Issuer, audience: o.Audience, leeway: o.Leeway, jwks: jwks}, nil
}

type jwk struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
	Kid string `json:"kid"`
	Alg string `json:"alg"`
	Use string `json:"use"`
}

// marshalJWKS 按版本升序输出公钥；坐标取非压缩点的定长 32 字节，不含私钥参数。
func marshalJWKS(keys map[uint16]*ecdsa.PrivateKey) ([]byte, error) {
	versions := make([]uint16, 0, len(keys))
	for v := range keys {
		versions = append(versions, v)
	}
	slices.Sort(versions)
	set := struct {
		Keys []jwk `json:"keys"`
	}{Keys: make([]jwk, 0, len(versions))}
	for _, v := range versions {
		point, err := keys[v].PublicKey.Bytes()
		if err != nil || len(point) != 65 {
			return nil, fmt.Errorf("tokens: key version %d: invalid public key", v)
		}
		set.Keys = append(set.Keys, jwk{
			Kty: "EC", Crv: "P-256", Kid: strconv.FormatUint(uint64(v), 10), Alg: Algorithm, Use: "sig",
			X: base64.RawURLEncoding.EncodeToString(point[1:33]),
			Y: base64.RawURLEncoding.EncodeToString(point[33:65]),
		})
	}
	return json.Marshal(set)
}

// JWKS 返回全部已配置版本公钥的 JWK Set（RFC 7517）副本。
func (s *Signer) JWKS() []byte { return slices.Clone(s.jwks) }

// Active 返回当前签发用的密钥版本。
func (s *Signer) Active() uint16 { return s.active }

// ContextFingerprint 标识令牌接收域，不包含签名密钥。
func (s *Signer) ContextFingerprint() string {
	b, _ := json.Marshal([]string{s.issuer, s.audience})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

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
	tok := jwt.NewWithClaims(jwt.SigningMethodES256, wc)
	tok.Header["typ"] = TokenType
	tok.Header["kid"] = strconv.FormatUint(uint64(s.active), 10)
	signed, err := tok.SignedString(s.keys[s.active])
	if err != nil {
		return "", "", fmt.Errorf("tokens: sign: %w", err)
	}
	return signed, jti, nil
}

// Parse 只接受 ES256 与 typ=at+jwt，按 header.kid 选公钥验签，并校验 iss、aud、exp（必需）、nbf、iat（含 leeway）。
func (s *Signer) Parse(token string) (Claims, error) {
	var wc wireClaims
	_, err := jwt.NewParser(
		jwt.WithValidMethods([]string{Algorithm}),
		jwt.WithIssuer(s.issuer),
		jwt.WithAudience(s.audience),
		jwt.WithLeeway(s.leeway),
		jwt.WithExpirationRequired(),
	).ParseWithClaims(token, &wc, func(t *jwt.Token) (any, error) {
		if typ, _ := t.Header["typ"].(string); typ != TokenType {
			return nil, errors.New("bad typ")
		}
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
		return &k.PublicKey, nil
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
