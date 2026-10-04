package code_test

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/pii"
	"github.com/bbxx111/accountkit/user/code"
)

func newStore(t *testing.T, o code.Options) (*code.Store, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	d, err := pii.NewDigester(map[uint16][]byte{1: bytes.Repeat([]byte{7}, 32)}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if o.TTL == 0 {
		o.TTL = 5 * time.Minute
	}
	if o.Cooldown == 0 {
		o.Cooldown = 60 * time.Second
	}
	if o.MaxAttempts == 0 {
		o.MaxAttempts = 5
	}
	if o.DailyLimitPerTarget == 0 {
		o.DailyLimitPerTarget = 10
	}
	if o.DailyLimitPerIP == 0 {
		o.DailyLimitPerIP = 100
	}
	return code.NewStore(rdb, "t:", d, o), mr
}

const target = "+8613812341234"

func TestIssueThenVerifySucceedsOnceAndDeletes(t *testing.T) {
	s, _ := newStore(t, code.Options{})
	ctx := context.Background()
	c, err := s.Issue(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, "1.2.3.4")
	if err != nil || len(c) != 6 {
		t.Fatalf("issue: %q %v", c, err)
	}
	for _, ch := range c {
		if ch < '0' || ch > '9' {
			t.Fatalf("code must be 6 digits: %q", c)
		}
	}
	if err := s.Verify(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, c); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if err := s.Verify(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, c); !errors.Is(err, code.ErrExpired) {
		t.Fatalf("second verify must be ErrExpired (deleted), got %v", err)
	}
}

func TestVerifyWrongCodeCountsAndExhausts(t *testing.T) {
	s, _ := newStore(t, code.Options{MaxAttempts: 3})
	ctx := context.Background()
	c, _ := s.Issue(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, "1.2.3.4")
	for i := 0; i < 3; i++ {
		if err := s.Verify(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, "wrong-fixture"); !errors.Is(err, code.ErrInvalid) {
			t.Fatalf("attempt %d: err = %v, want ErrInvalid", i+1, err)
		}
	}
	// 第 4 次：超过 MaxAttempts，作废；即使输入正确码也不再接受
	if err := s.Verify(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, c); !errors.Is(err, code.ErrExhausted) {
		t.Fatalf("4th attempt: err = %v, want ErrExhausted", err)
	}
	if err := s.Verify(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, c); !errors.Is(err, code.ErrExpired) {
		t.Fatalf("after exhaustion the code must be gone: %v", err)
	}
}

func TestVerifyPurposeMismatchIsMissing(t *testing.T) {
	s, _ := newStore(t, code.Options{})
	ctx := context.Background()
	c, _ := s.Issue(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, "1.2.3.4")
	// 用途不同 → 键不同 → 查无此键 → ErrExpired（"当前没有存活的码"，与真正过期在键层面
	// 不可区分，因此不是 ErrInvalid）。
	if err := s.Verify(ctx, enum.IdentityPhone, enum.PurposeBind, target, c); !errors.Is(err, code.ErrExpired) {
		t.Fatalf("cross-purpose verify must be ErrExpired, got %v", err)
	}
	// 原用途仍可用（跨用途尝试不消耗原码）
	if err := s.Verify(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, c); err != nil {
		t.Fatalf("original purpose must still verify: %v", err)
	}
}

func TestIssueCooldownAndReissueReplacesCode(t *testing.T) {
	s, mr := newStore(t, code.Options{Cooldown: 60 * time.Second})
	ctx := context.Background()
	c1, _ := s.Issue(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, "1.2.3.4")
	// 可区分的旧码夹具避免两次随机生成相同数字造成伪失败。
	c1 = "legacy-fixture"
	d, _ := pii.NewDigester(map[uint16][]byte{1: bytes.Repeat([]byte{7}, 32)}, 1)
	digest, _ := d.Digest(target)
	h, _ := d.Digest("code:PHONE:signin:" + target + ":" + c1)
	mr.HSet("t:code:PHONE:signin:"+digest, "h", h)
	_, err := s.Issue(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, "1.2.3.4")
	var rl *code.RateLimitedError
	if !errors.As(err, &rl) || rl.Dimension != "COOLDOWN" || rl.RetryAfter <= 0 || rl.RetryAfter > 60*time.Second {
		t.Fatalf("second issue within cooldown: %v", err)
	}
	mr.FastForward(61 * time.Second)
	c2, err := s.Issue(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, "1.2.3.4")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Verify(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, c1); errors.Is(err, nil) {
		t.Fatal("old code must be invalid after reissue")
	}
	if err := s.Verify(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, c2); err != nil {
		t.Fatalf("new code must verify: %v", err)
	}
}

func TestIssueDailyLimits(t *testing.T) {
	s, mr := newStore(t, code.Options{Cooldown: time.Second, DailyLimitPerTarget: 2, DailyLimitPerIP: 3})
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if _, err := s.Issue(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, "9.9.9.9"); err != nil {
			t.Fatalf("issue %d: %v", i+1, err)
		}
		mr.FastForward(2 * time.Second)
	}
	_, err := s.Issue(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, "9.9.9.9")
	var rl *code.RateLimitedError
	if !errors.As(err, &rl) || rl.Dimension != "TARGET_LIMIT" {
		t.Fatalf("3rd issue for same target: %v", err)
	}
	// RetryAfter 必须是到 UTC 次日零点的时长，而不是配额键自身的 Redis TTL（后者额外含
	// quotaTTL 的 +1 小时 GC 宽限，会让 RetryAfter 显著偏大甚至超过 24h）。
	if rl.RetryAfter <= 0 || rl.RetryAfter > 24*time.Hour {
		t.Fatalf("TARGET_LIMIT RetryAfter must be in (0, 24h]: %v", rl.RetryAfter)
	}
	// 同 IP 换 target：IP 计数已到 2，第 3 次成功，第 4 次 IP_LIMIT
	if _, err := s.Issue(ctx, enum.IdentityPhone, enum.PurposeSignIn, "+8613900000001", "9.9.9.9"); err != nil {
		t.Fatalf("3rd IP issue: %v", err)
	}
	mr.FastForward(2 * time.Second)
	_, err = s.Issue(ctx, enum.IdentityPhone, enum.PurposeSignIn, "+8613900000002", "9.9.9.9")
	if !errors.As(err, &rl) || rl.Dimension != "IP_LIMIT" {
		t.Fatalf("4th IP issue: %v", err)
	}
	if rl.RetryAfter <= 0 || rl.RetryAfter > 24*time.Hour {
		t.Fatalf("IP_LIMIT RetryAfter must be in (0, 24h]: %v", rl.RetryAfter)
	}
}

func TestIssueConcurrentBurstOnlyOneWins(t *testing.T) {
	s, _ := newStore(t, code.Options{})
	ctx := context.Background()
	var ok, limited atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			_, err := s.Issue(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, "1.2.3.4")
			var rl *code.RateLimitedError
			switch {
			case err == nil:
				ok.Add(1)
			case errors.As(err, &rl):
				limited.Add(1)
			}
		})
	}
	wg.Wait()
	if ok.Load() != 1 || limited.Load() != 19 {
		t.Fatalf("ok=%d limited=%d, want 1/19", ok.Load(), limited.Load())
	}
}

func TestVerifyConcurrentOnlyOneSucceeds(t *testing.T) {
	s, _ := newStore(t, code.Options{})
	ctx := context.Background()
	c, err := s.Issue(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, "1.2.3.4")
	if err != nil {
		t.Fatal(err)
	}
	var ok, expired atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			err := s.Verify(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, c)
			switch {
			case err == nil:
				ok.Add(1)
			case errors.Is(err, code.ErrExpired):
				expired.Add(1)
			}
		})
	}
	wg.Wait()
	if ok.Load() != 1 || expired.Load() != 19 {
		t.Fatalf("ok=%d expired=%d, want 1/19 (compare-then-delete must be atomic)", ok.Load(), expired.Load())
	}
}

func TestRedisDownIsUnavailable(t *testing.T) {
	s, mr := newStore(t, code.Options{})
	ctx := context.Background()
	c, _ := s.Issue(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, "1.2.3.4")
	mr.Close()
	if _, err := s.Issue(ctx, enum.IdentityPhone, enum.PurposeSignIn, "+8613900000009", "1.2.3.4"); !errors.Is(err, code.ErrUnavailable) {
		t.Fatalf("issue while down: %v", err)
	}
	if err := s.Verify(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, c); !errors.Is(err, code.ErrUnavailable) {
		t.Fatalf("verify while down: %v", err)
	}
}

func TestCodeExpiresWithTTL(t *testing.T) {
	s, mr := newStore(t, code.Options{TTL: 5 * time.Minute})
	ctx := context.Background()
	c, _ := s.Issue(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, "1.2.3.4")
	mr.FastForward(5*time.Minute + time.Second)
	if err := s.Verify(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, c); !errors.Is(err, code.ErrExpired) {
		t.Fatalf("expired code: %v", err)
	}
}
