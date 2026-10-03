// Package grace 缓存 refresh 轮换后的新凭证对（宽限期内重放返回同一结果）。
// 值用 AES-GCM 加密：refresh token 是凭证，即便在 30 秒 TTL 的 Redis 里也不以明文存放。
package grace

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/bbxx111/accountkit/pii"
)

// ErrUnavailable：Redis 不可用。
var ErrUnavailable = errors.New("grace: redis unavailable")

// Pair 是要在宽限期内原样返回的凭证对。
type Pair struct {
	AccessToken      string    `json:"at"`
	RefreshToken     string    `json:"rt"`
	AccessExpiresAt  time.Time `json:"ae"`
	RefreshExpiresAt time.Time `json:"re"`
	Scope            string    `json:"sc"`
}

// Cache 是宽限缓存。
type Cache struct {
	rdb    redis.UniversalClient
	prefix string
	cipher *pii.Cipher
}

// NewCache 构造 Cache。
func NewCache(rdb redis.UniversalClient, keyPrefix string, cipher *pii.Cipher) *Cache {
	return &Cache{rdb: rdb, prefix: keyPrefix, cipher: cipher}
}

func (c *Cache) key(oldHash []byte) string { return c.prefix + "grace:" + hex.EncodeToString(oldHash) }

// Put 加密并写入。ttl 必须为正：go-redis 把 <=0 的过期时间当作"永不过期"处理，
// 对宽限缓存而言这会让重放窗口永不关闭，因此在写入 Redis 之前直接拒绝。
func (c *Cache) Put(ctx context.Context, oldRefreshHash []byte, p Pair, ttl time.Duration) error {
	if ttl <= 0 {
		return errors.New("grace: ttl must be positive")
	}
	plain, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("grace: marshal: %w", err)
	}
	ct, ver, err := c.cipher.Encrypt(string(plain))
	if err != nil {
		return fmt.Errorf("grace: encrypt: %w", err)
	}
	val := make([]byte, 2+len(ct))
	binary.BigEndian.PutUint16(val[:2], ver)
	copy(val[2:], ct)
	if err := c.rdb.Set(ctx, c.key(oldRefreshHash), val, ttl).Err(); err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return nil
}

// Get 读取并解密。未命中返回 (Pair{}, false, nil)。
func (c *Cache) Get(ctx context.Context, oldRefreshHash []byte) (Pair, bool, error) {
	val, err := c.rdb.Get(ctx, c.key(oldRefreshHash)).Bytes()
	if errors.Is(err, redis.Nil) {
		return Pair{}, false, nil
	}
	if err != nil {
		return Pair{}, false, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if len(val) < 2 {
		return Pair{}, false, errors.New("grace: corrupt value")
	}
	plain, err := c.cipher.Decrypt(val[2:], binary.BigEndian.Uint16(val[:2]))
	if err != nil {
		return Pair{}, false, fmt.Errorf("grace: decrypt: %w", err)
	}
	var p Pair
	if err := json.Unmarshal([]byte(plain), &p); err != nil {
		return Pair{}, false, fmt.Errorf("grace: unmarshal: %w", err)
	}
	return p, true, nil
}
