package code

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/bbxx111/accountkit/enum"
)

// Binding 将敏感用途的轮次绑定到发码时的用户和会话。
type Binding struct {
	UserID    string
	SessionID string
}

// Issued 是成功占额后的轮次；Code 仅供调用方投递，不应记录到日志。
type Issued struct {
	CodeID     string
	Code       string
	ExpireTime time.Time
}

// Credential 是按渠道、用途、归一化目标及主体绑定的完整验证码凭证。
type Credential struct {
	Channel enum.IdentityKind
	Purpose enum.CodePurpose
	Target  string
	CodeID  string
	Code    string
	Binding Binding
}

func validCodeID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func (s *Store) challengeKeys(channel enum.IdentityKind, purpose enum.CodePurpose, target string) []string {
	digests := s.targetDigests(target)
	keys := make([]string, 0, 2*len(digests))
	for _, d := range digests {
		keys = append(keys, s.prefix+"challenge:"+channel.String()+":"+purpose.Key()+":"+d, s.prefix+"verify_failure:"+channel.String()+":"+d)
	}
	return keys
}

func (s *Store) challengeHMAC(c Credential) string {
	return "challenge:" + c.Channel.String() + ":" + c.Purpose.Key() + ":" + c.Target + ":" + c.CodeID + ":" + c.Code
}

// IssueChallenge 原子检查目标失败预算、占用发送额度并替换该用途的全部旧轮次。
// 投递由调用方处理；投递失败不退还额度和冷却。
func (s *Store) IssueChallenge(ctx context.Context, channel enum.IdentityKind, purpose enum.CodePurpose, target, ip string, binding Binding) (Issued, error) {
	if !channel.IsChannel() || !purpose.Valid() {
		return Issued{}, ErrInvalid
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return Issued{}, fmt.Errorf("code: random: %w", err)
	}
	plain, err := newCode()
	if err != nil {
		return Issued{}, err
	}
	now := s.o.Now()
	issued := Issued{CodeID: hex.EncodeToString(id[:]), Code: plain, ExpireTime: now.Add(time.Duration(int64(s.o.TTL.Seconds())) * time.Second)}
	c := Credential{Channel: channel, Purpose: purpose, Target: target, CodeID: issued.CodeID, Code: plain, Binding: binding}
	digest, _ := s.digester.Digest(s.challengeHMAC(c))
	digests := s.targetDigests(target)
	keys := make([]string, 0, 4*len(digests)+1)
	var ipKey string
	for _, d := range digests {
		_, cool, quota, k := s.keys(channel, purpose, d, ip, now)
		ipKey = k
		keys = append(keys, cool, quota, s.prefix+"challenge:"+channel.String()+":"+purpose.Key()+":"+d, s.prefix+"verify_failure:"+channel.String()+":"+d)
	}
	keys = append(keys, ipKey)
	res, err := issueChallengeScript.Run(ctx, s.rdb, keys, int64(s.o.Cooldown.Seconds()), s.o.DailyLimitPerTarget, s.o.DailyLimitPerIP, digest, int64(s.o.TTL.Seconds()), quotaTTL(now), issued.CodeID, binding.UserID, binding.SessionID, s.o.FailureLimitPerTarget).Slice()
	if err != nil {
		return Issued{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	err = challengeResult(res, now, "OK", "COOLDOWN", "TARGET_LIMIT", "IP_LIMIT", "TARGET_VERIFY_LIMIT")
	if err != nil {
		return Issued{}, err
	}
	return issued, nil
}

// VerifyChallenge 匹配轮次和主体后，在同一次 Lua 中完成预算检查、摘要比对及消费。
// 未知或旧轮次不会消耗当前轮次或累计失败预算。
func (s *Store) VerifyChallenge(ctx context.Context, c Credential) error {
	if !c.Channel.IsChannel() || !c.Purpose.Valid() || !validCodeID(c.CodeID) {
		return ErrInvalid
	}
	argv := []interface{}{c.CodeID, c.Binding.UserID, c.Binding.SessionID, s.o.MaxAttempts, s.o.FailureLimitPerTarget, s.o.FailureWindow.Milliseconds()}
	for _, d := range uniqueDigests(s.digester.AllDigests(s.challengeHMAC(c))) {
		argv = append(argv, d)
	}
	res, err := verifyChallengeScript.Run(ctx, s.rdb, s.challengeKeys(c.Channel, c.Purpose, c.Target), argv...).Slice()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return challengeResult(res, s.o.Now(), "OK", "MISSING", "MISMATCH", "TARGET_VERIFY_LIMIT")
}

// DiscardChallenge 只清理仍属于指定轮次的记录，不恢复发送额度或失败预算。
func (s *Store) DiscardChallenge(ctx context.Context, channel enum.IdentityKind, purpose enum.CodePurpose, target, codeID string) error {
	if !channel.IsChannel() || !purpose.Valid() || !validCodeID(codeID) {
		return ErrInvalid
	}
	keys := s.challengeKeys(channel, purpose, target)
	records := make([]string, 0, len(keys)/2)
	for i := 0; i < len(keys); i += 2 {
		records = append(records, keys[i])
	}
	res, err := discardChallengeScript.Run(ctx, s.rdb, records, codeID).Slice()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return challengeResult(res, s.o.Now(), "OK")
}

func challengeResult(res []interface{}, now time.Time, allowed ...string) error {
	status, err := scriptStatus(res)
	if err != nil {
		return err
	}
	valid := false
	for _, candidate := range allowed {
		if status == candidate {
			valid = true
			break
		}
	}
	if !valid {
		return ErrUnavailable
	}
	retry, ok := res[1].(int64)
	if !ok {
		return ErrUnavailable
	}
	switch status {
	case "OK":
		if retry == 0 {
			return nil
		}
	case "MISSING":
		if retry == 0 {
			return ErrExpired
		}
	case "MISMATCH":
		if retry == 0 {
			return ErrInvalid
		}
	case "TARGET_LIMIT", "IP_LIMIT":
		if retry == 0 {
			return &RateLimitedError{Dimension: status, RetryAfter: nextUTCMidnight(now).Sub(now.UTC())}
		}
	case "COOLDOWN", "TARGET_VERIFY_LIMIT":
		if retry > 0 && retry <= int64((1<<63-1)/time.Second) {
			return &RateLimitedError{Dimension: status, RetryAfter: time.Duration(retry) * time.Second}
		}
	}
	return ErrUnavailable
}
