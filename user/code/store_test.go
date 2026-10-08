package code_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
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
	if o.FailureLimitPerTarget == 0 {
		o.FailureLimitPerTarget = 10
	}
	if o.FailureWindow == 0 {
		o.FailureWindow = 15 * time.Minute
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
	c, err := s.IssueChallenge(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, "1.2.3.4", code.Binding{})
	if err != nil || len(c.Code) != 6 {
		t.Fatalf("issue: %v %v", c, err)
	}
	for _, ch := range c.Code {
		if ch < '0' || ch > '9' {
			t.Fatalf("code must be 6 digits: %q", c.Code)
		}
	}
	if err := s.VerifyChallenge(ctx, credential(c, enum.IdentityPhone, enum.PurposeSignIn, target)); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if err := s.VerifyChallenge(ctx, credential(c, enum.IdentityPhone, enum.PurposeSignIn, target)); !errors.Is(err, code.ErrExpired) {
		t.Fatalf("second verify must be ErrExpired (deleted), got %v", err)
	}
}

func TestVerifyWrongCodeCountsAndExhausts(t *testing.T) {
	s, _ := newStore(t, code.Options{MaxAttempts: 3})
	ctx := context.Background()
	c, _ := s.IssueChallenge(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, "1.2.3.4", code.Binding{})
	for i := 0; i < 3; i++ {
		if err := s.VerifyChallenge(ctx, credential(wrongIssued(c), enum.IdentityPhone, enum.PurposeSignIn, target)); !errors.Is(err, code.ErrInvalid) {
			t.Fatalf("attempt %d: err = %v, want ErrInvalid", i+1, err)
		}
	}
	// The third wrong attempt already discarded the round; even the correct code is now expired.
	if err := s.VerifyChallenge(ctx, credential(c, enum.IdentityPhone, enum.PurposeSignIn, target)); !errors.Is(err, code.ErrExpired) {
		t.Fatalf("4th attempt: err = %v, want ErrExpired", err)
	}
	if err := s.VerifyChallenge(ctx, credential(c, enum.IdentityPhone, enum.PurposeSignIn, target)); !errors.Is(err, code.ErrExpired) {
		t.Fatalf("after exhaustion the code must be gone: %v", err)
	}
}

func TestVerifyPurposeMismatchIsMissing(t *testing.T) {
	s, _ := newStore(t, code.Options{})
	ctx := context.Background()
	c, _ := s.IssueChallenge(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, "1.2.3.4", code.Binding{})
	// 用途不同 → 键不同 → 查无此键 → ErrExpired（"当前没有存活的码"，与真正过期在键层面
	// 不可区分，因此不是 ErrInvalid）。
	if err := s.VerifyChallenge(ctx, credential(c, enum.IdentityPhone, enum.PurposeBind, target)); !errors.Is(err, code.ErrExpired) {
		t.Fatalf("cross-purpose verify must be ErrExpired, got %v", err)
	}
	// 原用途仍可用（跨用途尝试不消耗原码）
	if err := s.VerifyChallenge(ctx, credential(c, enum.IdentityPhone, enum.PurposeSignIn, target)); err != nil {
		t.Fatalf("original purpose must still verify: %v", err)
	}
}

func TestIssueCooldownAndReissueReplacesCode(t *testing.T) {
	s, mr := newStore(t, code.Options{Cooldown: 60 * time.Second})
	ctx := context.Background()
	c1, _ := s.IssueChallenge(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, "1.2.3.4", code.Binding{})
	_, err := s.IssueChallenge(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, "1.2.3.4", code.Binding{})
	var rl *code.RateLimitedError
	if !errors.As(err, &rl) || rl.Dimension != "COOLDOWN" || rl.RetryAfter <= 0 || rl.RetryAfter > 60*time.Second {
		t.Fatalf("second issue within cooldown: %v", err)
	}
	mr.FastForward(61 * time.Second)
	c2, err := s.IssueChallenge(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, "1.2.3.4", code.Binding{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.VerifyChallenge(ctx, credential(c1, enum.IdentityPhone, enum.PurposeSignIn, target)); !errors.Is(err, code.ErrExpired) {
		t.Fatal("old code must be invalid after reissue")
	}
	if err := s.VerifyChallenge(ctx, credential(c2, enum.IdentityPhone, enum.PurposeSignIn, target)); err != nil {
		t.Fatalf("new code must verify: %v", err)
	}
}

func TestIssueDailyLimits(t *testing.T) {
	s, mr := newStore(t, code.Options{Cooldown: time.Second, DailyLimitPerTarget: 2, DailyLimitPerIP: 3})
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if _, err := s.IssueChallenge(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, "9.9.9.9", code.Binding{}); err != nil {
			t.Fatalf("issue %d: %v", i+1, err)
		}
		mr.FastForward(2 * time.Second)
	}
	_, err := s.IssueChallenge(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, "9.9.9.9", code.Binding{})
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
	if _, err := s.IssueChallenge(ctx, enum.IdentityPhone, enum.PurposeSignIn, "+8613900000001", "9.9.9.9", code.Binding{}); err != nil {
		t.Fatalf("3rd IP issue: %v", err)
	}
	mr.FastForward(2 * time.Second)
	_, err = s.IssueChallenge(ctx, enum.IdentityPhone, enum.PurposeSignIn, "+8613900000002", "9.9.9.9", code.Binding{})
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
			_, err := s.IssueChallenge(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, "1.2.3.4", code.Binding{})
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
	c, err := s.IssueChallenge(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, "1.2.3.4", code.Binding{})
	if err != nil {
		t.Fatal(err)
	}
	var ok, expired atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			err := s.VerifyChallenge(ctx, credential(c, enum.IdentityPhone, enum.PurposeSignIn, target))
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
	c, _ := s.IssueChallenge(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, "1.2.3.4", code.Binding{})
	mr.Close()
	if _, err := s.IssueChallenge(ctx, enum.IdentityPhone, enum.PurposeSignIn, "+8613900000009", "1.2.3.4", code.Binding{}); !errors.Is(err, code.ErrUnavailable) {
		t.Fatalf("issue while down: %v", err)
	}
	if err := s.VerifyChallenge(ctx, credential(c, enum.IdentityPhone, enum.PurposeSignIn, target)); !errors.Is(err, code.ErrUnavailable) {
		t.Fatalf("verify while down: %v", err)
	}
}

func TestCodeExpiresWithTTL(t *testing.T) {
	s, mr := newStore(t, code.Options{TTL: 5 * time.Minute})
	ctx := context.Background()
	c, _ := s.IssueChallenge(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, "1.2.3.4", code.Binding{})
	mr.FastForward(5*time.Minute + time.Second)
	if err := s.VerifyChallenge(ctx, credential(c, enum.IdentityPhone, enum.PurposeSignIn, target)); !errors.Is(err, code.ErrExpired) {
		t.Fatalf("expired code: %v", err)
	}
}

// Historical target-only credentials cannot consume or debit a new challenge.
func TestCodeProtocolRejectsUnidentifiedLegacyCredential(t *testing.T) {
	f := newRotation(t, false)
	old := f.legacy(t, 0, 0, time.Minute)
	var unknownID [16]byte
	if _, err := rand.Read(unknownID[:]); err != nil {
		t.Fatal(err)
	}
	unknown := code.Credential{Channel: enum.IdentityPhone, Purpose: enum.PurposeSignIn, Target: target, CodeID: hex.EncodeToString(unknownID[:]), Code: old}
	if err := f.stores[1].VerifyChallenge(context.Background(), unknown); !errors.Is(err, code.ErrExpired) {
		t.Fatal("target-only legacy record adopted")
	}
	issued, err := f.stores[1].IssueChallenge(context.Background(), unknown.Channel, unknown.Purpose, target, "test-ip", code.Binding{})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []code.Credential{unknown, {Channel: unknown.Channel, Purpose: unknown.Purpose, Target: target, Code: issued.Code}} {
		err := f.stores[0].VerifyChallenge(context.Background(), c)
		if c.CodeID == "" && !errors.Is(err, code.ErrInvalid) || c.CodeID != "" && !errors.Is(err, code.ErrExpired) {
			t.Fatalf("unidentified/stale credential accepted: %v", err)
		}
	}
	if f.mr.HGet(f.key(1, "challenge", unknown.Channel, unknown.Purpose), "n") != "0" || f.mr.Exists(failureKey(f, 0)) || f.mr.Exists(failureKey(f, 1)) {
		t.Fatal("legacy credential spent new round budget")
	}
	if err := f.stores[0].VerifyChallenge(context.Background(), credential(issued, unknown.Channel, unknown.Purpose, target)); err != nil {
		t.Fatal("legacy rejection consumed active challenge")
	}
}

// Each credential receives its issued identifier explicitly.
func credential(issued code.Issued, channel enum.IdentityKind, purpose enum.CodePurpose, target string) code.Credential {
	return code.Credential{Channel: channel, Purpose: purpose, Target: target, CodeID: issued.CodeID, Code: issued.Code}
}
func wrongIssued(issued code.Issued) code.Issued { issued.Code = "wrong-fixture"; return issued }
