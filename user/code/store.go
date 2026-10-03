// Package code 用 Redis 保存验证码与发送额度。所有多步操作都是 Lua 脚本，保证并发下的原子性：
// 占额发码"至多一个成功"；校验"计数、比对摘要、删除"同在一次脚本执行内完成，避免两次并发
// 校验都在对方删除前读到"匹配"从而让一次性码被消费两次。数据全部有 TTL，丢失只意味着用户重发一次。
package code

import (
	"context"
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
	// ErrInvalid：该 (channel, purpose, target) 存在存活的码，但 presented 的码与存储的摘要不匹配。
	ErrInvalid = errors.New("code: invalid code")
	// ErrExpired：该 (channel, purpose, target) 当前没有存活的码——从未签发、已过期、已被
	// 成功校验消费、尝试次数已耗尽，或该码是为另一个 purpose 签发的。purpose 是 Redis 键的
	// 一部分（见 keys()），因此跨 purpose 校验必然查无此键，与"过期"在键层面不可区分，
	// 统一报 ErrExpired，不报 ErrInvalid。
	ErrExpired = errors.New("code: code expired")
	// ErrExhausted：尝试次数用尽，码已作废（键已删）。
	ErrExhausted = errors.New("code: attempts exhausted")
)

// RateLimitedError 表示发送被冷却或日限拒绝。
type RateLimitedError struct {
	Dimension  string // COOLDOWN | TARGET_LIMIT | IP_LIMIT
	RetryAfter time.Duration
}

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("code: rate limited (%s), retry after %s", e.Dimension, e.RetryAfter)
}

// Options 是 Store 的参数。
type Options struct {
	TTL                 time.Duration
	Cooldown            time.Duration
	MaxAttempts         int
	DailyLimitPerTarget int
	DailyLimitPerIP     int
	Now                 func() time.Time
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

func (s *Store) targetDigest(target string) string {
	d, _ := s.digester.Digest(target)
	return d
}

func (s *Store) codeHMAC(channel enum.IdentityKind, purpose enum.CodePurpose, target, code string) string {
	d, _ := s.digester.Digest("code:" + channel.String() + ":" + purpose.Key() + ":" + target + ":" + code)
	return d
}

// codeHMACCandidates 返回 presented 码在全部已配置密钥版本下的摘要（active 在前），供 Verify
// 传给 Lua 脚本做原子比对；覆盖密钥轮换窗口内、码签发于旧版本尚未过期的情况。
//
// 注意：这个多版本回退目前是死代码。keys() 里的 Redis key（codeKey）只由 targetDigest 派生
// ——即只用 active 版本的摘要定位键——而不是 codeHMAC 本身的版本。因此一次 HMAC 密钥轮换
// 后，active 版本一变，Issue 时算出的 targetDigest 就变了，键名随之变化，旧键上在飞的验证码
// 直接找不到（在 CodeTTL 窗口内、最多几分钟内失效，这是可接受的：用户重发一次即可）。
// codeHMACCandidates 的多候选比对只有在未来把 key() 的键名也改成与 HMAC 密钥版本无关（例如
// 键名不再依赖 digest 版本）时才会真正生效；在当前实现下这段回退代码永远只会用到 active 版本
// 那一个候选。
func (s *Store) codeHMACCandidates(channel enum.IdentityKind, purpose enum.CodePurpose, target, code string) []string {
	return s.digester.AllDigests("code:" + channel.String() + ":" + purpose.Key() + ":" + target + ":" + code)
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
// GC 宽限只用于键本身的过期时间，不能当作"额度何时重置"汇报给调用方——见 Issue 里
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

// Issue 占额并生成验证码。
func (s *Store) Issue(ctx context.Context, channel enum.IdentityKind, purpose enum.CodePurpose, target, ip string) (string, error) {
	if !channel.IsChannel() || !purpose.Valid() {
		return "", fmt.Errorf("code: invalid channel %q or purpose %q", channel, purpose)
	}
	plain, err := newCode()
	if err != nil {
		return "", err
	}
	now := s.o.Now()
	digest := s.targetDigest(target)
	codeKey, cooldownKey, quotaTargetKey, quotaIPKey := s.keys(channel, purpose, digest, ip, now)
	res, err := issueScript.Run(ctx, s.rdb,
		[]string{cooldownKey, quotaTargetKey, quotaIPKey, codeKey},
		int64(s.o.Cooldown.Seconds()), s.o.DailyLimitPerTarget, s.o.DailyLimitPerIP,
		s.codeHMAC(channel, purpose, target, plain), int64(s.o.TTL.Seconds()), quotaTTL(now),
	).Slice()
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	status, _ := res[0].(string)
	if status != "OK" {
		var retryAfter time.Duration
		switch status {
		case "TARGET_LIMIT", "IP_LIMIT":
			// 这两个键的 Redis TTL 含 quotaTTL 的 +1 小时 GC 宽限，不能直接当作 RetryAfter
			// 汇报给调用方；额度实际在 UTC 次日零点重置，从 now 直接算更准确。
			retryAfter = nextUTCMidnight(now).Sub(now.UTC())
		default: // COOLDOWN
			retry, _ := res[1].(int64)
			if retry < 1 {
				retry = 1
			}
			retryAfter = time.Duration(retry) * time.Second
		}
		return "", &RateLimitedError{Dimension: status, RetryAfter: retryAfter}
	}
	return plain, nil
}

// Verify 原子地计数并在 Lua 脚本内部完成摘要比对与删除：比对和删除必须同在一次脚本执行内
// 完成，否则并发的两次 Verify 都能在对方删除之前读到"匹配"，导致一次性码被消费两次。
func (s *Store) Verify(ctx context.Context, channel enum.IdentityKind, purpose enum.CodePurpose, target, code string) error {
	if !channel.IsChannel() || !purpose.Valid() {
		return ErrInvalid
	}
	digest := s.targetDigest(target)
	codeKey, _, _, _ := s.keys(channel, purpose, digest, "", s.o.Now())
	candidates := s.codeHMACCandidates(channel, purpose, target, code)
	argv := make([]interface{}, 0, len(candidates)+1)
	argv = append(argv, s.o.MaxAttempts)
	for _, c := range candidates {
		argv = append(argv, c)
	}
	res, err := verifyScript.Run(ctx, s.rdb, []string{codeKey}, argv...).Slice()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	status, _ := res[0].(string)
	switch status {
	case "MISSING":
		return ErrExpired
	case "EXHAUSTED":
		return ErrExhausted
	case "MISMATCH":
		return ErrInvalid
	}
	return nil
}
