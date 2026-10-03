package idp

import (
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/bbxx111/accountkit/enum"
)

const (
	appleIssuer          = "https://appleid.apple.com"
	appleLeeway          = 30 * time.Second
	appleRefreshThrottle = 60 * time.Second
	maxJWKSBody          = 256 << 10
)

// AppleOptions 是 Apple 校验器的依赖。
type AppleOptions struct {
	BundleIDs []string
	JWKSURL   string
	HTTP      *http.Client
	Nonces    *NonceRegistry // 必填
	Logger    *slog.Logger
	Now       func() time.Time // nil → time.Now（验签时钟）
	// NonceTTL 是登记 nonce 的下限 TTL；实际登记时长取它与 id_token 剩余有效期
	// （exp + leeway）中的较大者，以覆盖整个 id_token 生命周期。默认 10m。
	NonceTTL time.Duration
}

// Apple 校验 Sign in with Apple 的 id_token（§5.3）。
type Apple struct {
	bundleIDs map[string]struct{}
	jwksURL   string
	hc        *http.Client
	nonces    *NonceRegistry
	logger    *slog.Logger
	now       func() time.Time
	nonceTTL  time.Duration

	mu            sync.Mutex
	keys          map[string]*rsa.PublicKey
	lastRefresh   time.Time
	lastRefreshOK bool
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
	return &Apple{bundleIDs: set, jwksURL: o.JWKSURL, hc: o.HTTP, nonces: o.Nonces, logger: o.Logger, now: o.Now, nonceTTL: o.NonceTTL}
}

// errJWKSUnavailable 在 keyfunc 内部标记“取不到 JWKS 且无缓存”，外层映射为 ErrUnavailable。
var errJWKSUnavailable = errors.New("jwks unavailable")

// VerifyApple 实现 AppleVerifier。
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
		return a.key(ctx, kid)
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

// key 返回 kid 对应的公钥；未知 kid 触发一次节流刷新（≥ 60 秒一次）。
//
// 节流计时分两种情形，都以"上一次刷新尝试"的时间戳起算，但更新时机不同：
//   - a.keys 尚为 nil（从未成功拉取过）：一次成功的拉取不更新 lastRefresh
//     （保持零值），这样密钥轮换后紧邻着的第一次未知 kid 刷新不会被首次懒
//     加载的时间戳误判为"刚刷新过"而被节流掉；但一次失败的拉取会记录
//     lastRefresh，避免 JWKS 故障期间每次登录都无节流地打到 Apple 并在锁
//     内串行等待网络往返。
//   - a.keys 已建立（至少成功过一次）：沿用既有节流——只有过了节流窗口才
//     尝试刷新；节流期间沿用上一次刷新的成败结果。
//
// lastRefreshOK 记录"最近一次实际发起的刷新"是否成功（节流期间不发起刷新，
// 沿用之前的结果）：kid 最终仍未找到时，只有 lastRefreshOK 为 true（拉取确实
// 成功过，只是 Apple 没有发这个 kid）才是客户端的错，映射 400 unknown kid；
// 只要最近一次刷新尝试失败或被节流沿用了失败结果，就映射 503
// errJWKSUnavailable——否则一次 JWKS 故障会被误判成"这是个坏 kid"。
func (a *Apple) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if k, ok := a.keys[kid]; ok {
		return k, nil
	}
	now := a.now()
	if a.keys == nil {
		if !a.lastRefresh.IsZero() && now.Sub(a.lastRefresh) < appleRefreshThrottle {
			return nil, fmt.Errorf("%w: jwks refresh throttled", errJWKSUnavailable)
		}
		if err := a.refreshLocked(ctx); err != nil {
			a.lastRefresh = now
			a.lastRefreshOK = false
			return nil, fmt.Errorf("%w: %v", errJWKSUnavailable, err)
		}
		a.lastRefreshOK = true
	} else if now.Sub(a.lastRefresh) >= appleRefreshThrottle {
		a.lastRefresh = now
		if err := a.refreshLocked(ctx); err != nil {
			a.lastRefreshOK = false
			a.logger.Warn("apple jwks refresh failed; serving cached keys", "err", err)
		} else {
			a.lastRefreshOK = true
		}
	}
	if k, ok := a.keys[kid]; ok {
		return k, nil
	}
	if !a.lastRefreshOK {
		return nil, fmt.Errorf("%w: unknown kid %q", errJWKSUnavailable, kid)
	}
	return nil, fmt.Errorf("unknown kid %q", kid)
}

// refreshLocked 拉取并替换 JWKS；调用方持锁；节流时间戳由调用方（key）维护。
//
// Apple 的 JWKS URL 不带查询串，但取错误仍统一经 redactTransportErr 处理后
// 再进入日志或错误消息，与 wechat.go 的做法保持一致，避免任何传输层细节
// （包括 URL）意外落入日志。
//
// 拉取用的 context 特意与调用方的请求 context 脱钩（保留 value，去掉取消/
// 截止时间），只套一个固定的 10 秒超时：调用方（HTTP 请求）随时可能被客户端
// 断开取消，但这里持有 a.mu 且会更新 lastRefresh 节流时间戳——如果借用调用方
// 的 context，一次客户端断开会让这次刷新以 context.Canceled 失败，进而把接下
// 来 60 秒内所有 Apple 登录都节流拒绝，这与客户端断开毫无关系。
func (a *Apple) refreshLocked(ctx context.Context) error {
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(fctx, http.MethodGet, a.jwksURL, nil)
	if err != nil {
		return redactTransportErr(err)
	}
	resp, err := a.hc.Do(req)
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
	a.keys = keys
	return nil
}
