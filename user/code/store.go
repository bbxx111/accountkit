// Package code 用 Redis 保存验证码与发送额度。所有多步操作都是 Lua 脚本，保证并发下的原子性：
// 占额发码"至多一个成功"；校验"计数、比对摘要、删除"同在一次脚本执行内完成，避免两次并发
// 校验都在对方删除前读到"匹配"从而让一次性码被消费两次。数据全部有 TTL，丢失只意味着用户重发一次。
package code

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/pii"
)

var (
	// ErrUnavailable：Redis 不可用。调用方按 fail-closed 处理（503）。
	ErrUnavailable = errors.New("code: redis unavailable")
	// ErrInvalid: malformed credential or a mismatch in the current challenge.
	ErrInvalid = errors.New("code: invalid code")
	// ErrExpired: an unknown, expired, replaced, consumed or misbound challenge.
	ErrExpired = errors.New("code: code expired")
	// ErrExhausted is retained for source compatibility; challenge verification returns
	// ErrInvalid on the final wrong attempt and ErrExpired thereafter.
	ErrExhausted = errors.New("code: attempts exhausted")
)

// RateLimitedError 表示发送被冷却或日限拒绝。
type RateLimitedError struct {
	Dimension  string // COOLDOWN | TARGET_LIMIT | IP_LIMIT | TARGET_VERIFY_LIMIT
	RetryAfter time.Duration
}

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("code: rate limited (%s), retry after %s", e.Dimension, e.RetryAfter)
}

// Options 是 Store 的参数。
type Options struct {
	TTL                   time.Duration
	Cooldown              time.Duration
	MaxAttempts           int
	DailyLimitPerTarget   int
	DailyLimitPerIP       int
	FailureLimitPerTarget int
	FailureWindow         time.Duration
	Now                   func() time.Time
}

// Store 是验证码与额度的 Redis 存储。
type Store struct {
	rdb      redis.UniversalClient
	prefix   string
	digester *pii.Digester
	o        Options
}

// NewStore 构造 Store。keyPrefix 以 ':' 结尾（Config.KeyPrefix）。
// Options 字段的合法性（TTL/Cooldown/MaxAttempts 等须为正）由 accountkit.Config.Validate
// 在启动时校验，此处不再重复校验。
func NewStore(rdb redis.UniversalClient, keyPrefix string, digester *pii.Digester, o Options) *Store {
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Store{rdb: rdb, prefix: keyPrefix, digester: digester, o: o}
}

func uniqueDigests(digests []string) []string {
	seen := make(map[string]bool, len(digests))
	out := make([]string, 0, len(digests))
	for _, digest := range digests {
		if !seen[digest] {
			seen[digest] = true
			out = append(out, digest)
		}
	}
	return out
}

func (s *Store) targetDigests(target string) []string {
	return uniqueDigests(s.digester.AllDigests(target))
}

// scriptStatus 校验共同的二元素返回格式；各操作再校验状态和第二个元素类型。
func scriptStatus(result []interface{}) (string, error) {
	if len(result) != 2 {
		return "", ErrUnavailable
	}
	status, ok := result[0].(string)
	if !ok {
		return "", ErrUnavailable
	}
	return status, nil
}

func (s *Store) keys(channel enum.IdentityKind, purpose enum.CodePurpose, digest, ip string, now time.Time) (codeKey, cooldownKey, quotaTargetKey, quotaIPKey string) {
	day := now.UTC().Format("20060102")
	ch := channel.String()
	codeKey = s.prefix + "code:" + ch + ":" + purpose.Key() + ":" + digest
	cooldownKey = s.prefix + "cooldown:" + ch + ":" + digest
	quotaTargetKey = s.prefix + "quota:" + ch + ":target:" + digest + ":" + day
	quotaIPKey = s.prefix + "quota:" + ch + ":ip:" + ip + ":" + day
	return
}

// nextUTCMidnight 返回严格晚于 now 的下一个 UTC 零点，即当日额度重置的时刻。
func nextUTCMidnight(now time.Time) time.Time {
	u := now.UTC()
	return time.Date(u.Year(), u.Month(), u.Day()+1, 0, 0, 0, 0, time.UTC)
}

// quotaTTL 返回到 UTC 次日零点的秒数再加 1 小时，保证日键在跨日后自然消失。这个 +1 小时
// GC 宽限只用于键本身的过期时间，不能当作"额度何时重置"汇报给调用方——见 IssueChallenge 里
// TARGET_LIMIT / IP_LIMIT 的 RetryAfter 改为直接用 nextUTCMidnight 计算。
func quotaTTL(now time.Time) int64 {
	return int64(nextUTCMidnight(now).Sub(now.UTC()).Seconds()) + 3600
}

func newCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", fmt.Errorf("code: random: %w", err)
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}
