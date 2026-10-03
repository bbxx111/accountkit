// Package revocation 维护 Redis 中的会话吊销集：吊销时写入 sid（TTL = access token 寿命），
// 中间件每请求 EXISTS 一次，使吊销即时生效。Redis 不可用时调用方 fail-open（回到"最多一个 access TTL"语义）。
package revocation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrUnavailable：Redis 不可用。
var ErrUnavailable = errors.New("revocation: redis unavailable")

// Set 是吊销集。
type Set struct {
	rdb    redis.UniversalClient
	prefix string
}

// NewSet 构造 Set。keyPrefix 以 ':' 结尾。
func NewSet(rdb redis.UniversalClient, keyPrefix string) *Set {
	return &Set{rdb: rdb, prefix: keyPrefix}
}

func (s *Set) key(sid string) string { return s.prefix + "revoked:" + sid }

// Revoke 标记 sid 已吊销，ttl 之后自动消失（届时 access 也已过期）。
// ttl 必须为正：go-redis 把 <=0 的过期时间当作"永不过期"处理，调用方传入非正
// ttl 是编程错误，在写入 Redis 之前直接拒绝。
func (s *Set) Revoke(ctx context.Context, sid string, ttl time.Duration) error {
	if ttl <= 0 {
		return errors.New("revocation: ttl must be positive")
	}
	if err := s.rdb.Set(ctx, s.key(sid), "1", ttl).Err(); err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return nil
}

// IsRevoked 报告 sid 是否在吊销集中。
func (s *Set) IsRevoked(ctx context.Context, sid string) (bool, error) {
	n, err := s.rdb.Exists(ctx, s.key(sid)).Result()
	if err != nil {
		return false, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return n == 1, nil
}
