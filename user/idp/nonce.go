package idp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// NonceRegistry 登记已使用的 Apple nonce（§5.4 nonce:apple:*），杜绝 id_token 重放。
type NonceRegistry struct {
	rdb    redis.UniversalClient
	prefix string
}

// NewNonceRegistry 构造登记表。TTL 不在此处固定：Apple id_token 的有效期约 24 小时，
// 远长于任何登录场景的固定时限，调用方必须在每次 Register 时按该 token 的剩余有效期
// 计算并传入 ttl（见 Apple.VerifyApple），否则一个在登记 TTL 之后仍未过期的 id_token
// 会在密钥被重放者截获的情况下再次登录成功。
func NewNonceRegistry(rdb redis.UniversalClient, keyPrefix string) *NonceRegistry {
	return &NonceRegistry{rdb: rdb, prefix: keyPrefix}
}

// Register 登记 Apple nonce，键空间保持 nonce:apple:。
func (n *NonceRegistry) Register(ctx context.Context, nonceClaim string, ttl time.Duration) error {
	return n.RegisterScoped(ctx, "apple", nonceClaim, ttl)
}

// RegisterScoped 以 SETNX 登记 nonce。scope 只允许调用方传入的固定提供方名。
// ttl 必须 > 0（否则 ErrMisconfigured）；第二次登记 → ErrNonceReplayed；
// Redis 错误 → ErrUnavailable（fail-closed）。
func (n *NonceRegistry) RegisterScoped(ctx context.Context, scope, nonceClaim string, ttl time.Duration) error {
	if scope == "" || nonceClaim == "" {
		return fmt.Errorf("%w: empty nonce", ErrInvalidCredential)
	}
	if ttl <= 0 {
		return fmt.Errorf("%w: nonce ttl must be positive", ErrMisconfigured)
	}
	sum := sha256.Sum256([]byte(nonceClaim))
	key := n.prefix + "nonce:" + scope + ":" + hex.EncodeToString(sum[:])
	ok, err := n.rdb.SetNX(ctx, key, "1", ttl).Result()
	if err != nil {
		return fmt.Errorf("%w: nonce registry: %v", ErrUnavailable, err)
	}
	if !ok {
		return ErrNonceReplayed
	}
	return nil
}
