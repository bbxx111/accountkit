package code_test

import (
	"context"
	"errors"
	"regexp"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user/code"
)

func challengeFixture(t *testing.T, alias bool) *rotationFixture {
	t.Helper()
	f := newRotation(t, alias)
	f.o.DailyLimitPerTarget = 100
	f.o.FailureLimitPerTarget = 10
	f.o.FailureWindow = 15 * time.Minute
	for i := range 2 {
		f.stores[i] = code.NewStore(f.rdb, "rot:", f.digesters[i], f.o)
	}
	return f
}

func issueChallenge(t *testing.T, f *rotationFixture, active int, purpose enum.CodePurpose, binding code.Binding) code.Credential {
	t.Helper()
	issued, err := f.stores[active].IssueChallenge(context.Background(), enum.IdentityPhone, purpose, target, "test-ip", binding)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(issued.CodeID) || !issued.ExpireTime.Equal(f.now.Add(f.o.TTL)) {
		t.Fatal("invalid challenge metadata")
	}
	return code.Credential{Channel: enum.IdentityPhone, Purpose: purpose, Target: target, CodeID: issued.CodeID, Code: issued.Code, Binding: binding}
}

func advanceChallenge(f *rotationFixture, d time.Duration) { f.mr.FastForward(d); f.now = f.now.Add(d) }
func wrongChallenge(c code.Credential) code.Credential     { c.Code = "wrong-fixture"; return c }
func challengeKey(f *rotationFixture, active int, purpose enum.CodePurpose) string {
	d, _ := f.digesters[active].Digest(target)
	return "rot:challenge:PHONE:" + purpose.Key() + ":" + d
}
func failureKey(f *rotationFixture, active int) string {
	d, _ := f.digesters[active].Digest(target)
	return "rot:verify_failure:PHONE:" + d
}

func TestChallengeIsolation(t *testing.T) {
	ctx := context.Background()
	f := challengeFixture(t, false)
	a := issueChallenge(t, f, 0, enum.PurposeReauth, code.Binding{UserID: "u_fixture", SessionID: "s_fixture"})
	advanceChallenge(f, time.Second)
	b := issueChallenge(t, f, 1, enum.PurposeReauth, a.Binding)
	// Simulate the legitimate collision of two randomly generated six-digit codes.
	h, _ := f.digesters[1].Digest("challenge:PHONE:reauth:" + target + ":" + b.CodeID + ":" + a.Code)
	f.mr.HSet(challengeKey(f, 1, b.Purpose), "h", h)
	b.Code = a.Code
	for _, old := range []code.Credential{a, wrongChallenge(a)} {
		if err := f.stores[0].VerifyChallenge(ctx, old); !errors.Is(err, code.ErrExpired) {
			t.Fatalf("old round: %v", err)
		}
	}
	for _, mutate := range []func(*code.Credential){
		func(c *code.Credential) { c.Target = "other" }, func(c *code.Credential) { c.Channel = enum.IdentityEmail },
		func(c *code.Credential) { c.Purpose = enum.PurposeBind }, func(c *code.Credential) { c.Binding.UserID = "other" },
		func(c *code.Credential) { c.Binding.SessionID = "other" },
	} {
		c := b
		mutate(&c)
		if err := f.stores[0].VerifyChallenge(ctx, c); !errors.Is(err, code.ErrExpired) {
			t.Fatalf("isolation: %v", err)
		}
	}
	if f.mr.HGet(challengeKey(f, 1, b.Purpose), "n") != "0" || f.mr.Exists(failureKey(f, 0)) || f.mr.Exists(failureKey(f, 1)) {
		t.Fatal("stale/misbound credential spent budget")
	}
	if err := f.stores[0].VerifyChallenge(ctx, b); err != nil {
		t.Fatalf("current: %v", err)
	}
}

func TestChallengeAttemptBoundary(t *testing.T) {
	for _, correct := range []bool{false, true} {
		t.Run(map[bool]string{false: "third wrong", true: "third correct"}[correct], func(t *testing.T) {
			f := challengeFixture(t, false)
			c := issueChallenge(t, f, 0, enum.PurposeSignIn, code.Binding{})
			for range 2 {
				if err := f.stores[1].VerifyChallenge(context.Background(), wrongChallenge(c)); !errors.Is(err, code.ErrInvalid) {
					t.Fatal(err)
				}
			}
			third := wrongChallenge(c)
			if correct {
				third = c
			}
			err := f.stores[1].VerifyChallenge(context.Background(), third)
			if correct && err != nil || !correct && !errors.Is(err, code.ErrInvalid) {
				t.Fatalf("third: %v", err)
			}
			if err := f.stores[0].VerifyChallenge(context.Background(), c); !errors.Is(err, code.ErrExpired) {
				t.Fatalf("round remains: %v", err)
			}
		})
	}
}

func TestChallengeAtomicConsume(t *testing.T) {
	f := challengeFixture(t, false)
	c := issueChallenge(t, f, 0, enum.PurposeSignIn, code.Binding{})
	var success, expired atomic.Int32
	var wg sync.WaitGroup
	for n := range 20 {
		wg.Go(func() {
			err := f.stores[n%2].VerifyChallenge(context.Background(), c)
			if err == nil {
				success.Add(1)
			} else if errors.Is(err, code.ErrExpired) {
				expired.Add(1)
			}
		})
	}
	wg.Wait()
	if success.Load() != 1 || expired.Load() != 19 {
		t.Fatalf("winners=%d expired=%d", success.Load(), expired.Load())
	}
}

func TestChallengeFailureBudget(t *testing.T) {
	ctx := context.Background()
	f := challengeFixture(t, false)
	var current code.Credential
	for n := range 10 {
		if n%3 == 0 {
			current = issueChallenge(t, f, n%2, []enum.CodePurpose{enum.PurposeSignIn, enum.PurposeBind, enum.PurposeReauth}[n/3%3], code.Binding{})
			if n > 0 {
				advanceChallenge(f, time.Second)
			}
		}
		err := f.stores[1].VerifyChallenge(ctx, wrongChallenge(current))
		if n == 9 {
			requireLimit(t, err, "TARGET_VERIFY_LIMIT")
		} else if !errors.Is(err, code.ErrInvalid) {
			t.Fatalf("wrong %d: %v", n, err)
		}
		if n%3 == 2 {
			advanceChallenge(f, time.Second)
		}
	}
	limit := requireLimit(t, f.stores[0].VerifyChallenge(ctx, current), "TARGET_VERIFY_LIMIT")
	if limit.RetryAfter != 15*time.Minute-6*time.Second {
		t.Fatalf("retry=%s", limit.RetryAfter)
	}
	_, err := f.stores[0].IssueChallenge(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, "test-ip", code.Binding{})
	requireLimit(t, err, "TARGET_VERIFY_LIMIT")
	advanceChallenge(f, 15*time.Minute-6*time.Second)
	_ = issueChallenge(t, f, 1, enum.PurposeSignIn, code.Binding{})
}

func TestChallengeSuccessPreservesWindow(t *testing.T) {
	f := challengeFixture(t, false)
	c := issueChallenge(t, f, 0, enum.PurposeSignIn, code.Binding{})
	_ = f.stores[0].VerifyChallenge(context.Background(), wrongChallenge(c))
	advanceChallenge(f, time.Second)
	if err := f.stores[1].VerifyChallenge(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	c = issueChallenge(t, f, 1, enum.PurposeBind, code.Binding{})
	_ = f.stores[1].VerifyChallenge(context.Background(), wrongChallenge(c))
	if f.mr.TTL(failureKey(f, 1)) != 15*time.Minute-time.Second {
		t.Fatal("success/reissue extended window")
	}
	advanceChallenge(f, 15*time.Minute-time.Second)
	if f.mr.Exists(failureKey(f, 0)) || f.mr.Exists(failureKey(f, 1)) {
		t.Fatal("window did not expire")
	}
}

func TestChallengeDiscardRace(t *testing.T) {
	f := challengeFixture(t, false)
	a := issueChallenge(t, f, 0, enum.PurposeSignIn, code.Binding{})
	advanceChallenge(f, time.Second)
	b := issueChallenge(t, f, 1, enum.PurposeSignIn, code.Binding{})
	if err := f.stores[0].DiscardChallenge(context.Background(), a.Channel, a.Purpose, a.Target, a.CodeID); err != nil {
		t.Fatal(err)
	}
	if err := f.stores[0].VerifyChallenge(context.Background(), b); err != nil {
		t.Fatalf("discard A deleted B: %v", err)
	}
	advanceChallenge(f, time.Second)
	c := issueChallenge(t, f, 0, enum.PurposeSignIn, code.Binding{})
	if err := f.stores[1].DiscardChallenge(context.Background(), c.Channel, c.Purpose, c.Target, c.CodeID); err != nil {
		t.Fatal(err)
	}
	if err := f.stores[0].VerifyChallenge(context.Background(), c); !errors.Is(err, code.ErrExpired) {
		t.Fatal("discard failed")
	}
}

func TestChallengeRotationBudget(t *testing.T) {
	for _, alias := range []bool{false, true} {
		t.Run(map[bool]string{false: "distinct", true: "aliases"}[alias], func(t *testing.T) {
			f := challengeFixture(t, alias)
			c := issueChallenge(t, f, 0, enum.PurposeSignIn, code.Binding{})
			_ = f.stores[0].VerifyChallenge(context.Background(), wrongChallenge(c))
			advanceChallenge(f, time.Second)
			_ = f.stores[1].VerifyChallenge(context.Background(), wrongChallenge(c))
			n, _ := f.mr.Get(failureKey(f, 1))
			if n != "2" || f.mr.TTL(failureKey(f, 1)) != 15*time.Minute-time.Second {
				t.Fatal("rotation reset, duplicated or extended budget")
			}
			if err := f.stores[1].VerifyChallenge(context.Background(), c); err != nil {
				t.Fatal(err)
			}
		})
	}
	t.Run("partial historic counts", func(t *testing.T) {
		f := challengeFixture(t, false)
		c := issueChallenge(t, f, 0, enum.PurposeSignIn, code.Binding{})
		f.mr.Set(failureKey(f, 0), "4")
		f.mr.SetTTL(failureKey(f, 0), 2*time.Minute)
		f.mr.Set(failureKey(f, 1), "5")
		f.mr.SetTTL(failureKey(f, 1), 3*time.Minute)
		requireLimit(t, f.stores[1].VerifyChallenge(context.Background(), wrongChallenge(c)), "TARGET_VERIFY_LIMIT")
		if requireLimit(t, f.stores[0].VerifyChallenge(context.Background(), c), "TARGET_VERIFY_LIMIT").RetryAfter != 2*time.Minute {
			t.Fatal("historic window extended")
		}
	})
}

func TestChallengeCorruptState(t *testing.T) {
	ctx := context.Background()
	for _, kind := range []string{"persistent failure", "bad failure", "wrong failure type", "missing binding", "persistent challenge", "bad attempts"} {
		t.Run(kind, func(t *testing.T) {
			f := challengeFixture(t, false)
			c := issueChallenge(t, f, 0, enum.PurposeSignIn, code.Binding{})
			k := failureKey(f, 0)
			switch kind {
			case "persistent failure":
				f.mr.Set(k, "1")
			case "bad failure":
				f.mr.Set(k, "bad")
				f.mr.SetTTL(k, time.Minute)
			case "wrong failure type":
				f.mr.HSet(k, "n", "1")
				f.mr.SetTTL(k, time.Minute)
			case "missing binding":
				f.mr.HDel(challengeKey(f, 0, c.Purpose), "user_id")
			case "persistent challenge":
				if err := f.rdb.Persist(ctx, challengeKey(f, 0, c.Purpose)).Err(); err != nil {
					t.Fatal(err)
				}
			case "bad attempts":
				f.mr.HSet(challengeKey(f, 0, c.Purpose), "n", "bad")
			}
			if err := f.stores[1].VerifyChallenge(ctx, c); !errors.Is(err, code.ErrUnavailable) {
				t.Fatalf("verify: %v", err)
			}
			if kind == "persistent failure" || kind == "bad failure" || kind == "wrong failure type" {
				advanceChallenge(f, time.Second)
				_, err := f.stores[1].IssueChallenge(ctx, c.Channel, c.Purpose, c.Target, "test-ip", code.Binding{})
				if !errors.Is(err, code.ErrUnavailable) {
					t.Fatalf("issue: %v", err)
				}
			}
		})
	}
}

func TestChallengeExpiryAndProtocol(t *testing.T) {
	f := challengeFixture(t, false)
	ctx := context.Background()
	c := issueChallenge(t, f, 0, enum.PurposeSignIn, code.Binding{})
	for _, id := range []string{"", "short", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaag"} {
		bad := c
		bad.CodeID = id
		if err := f.stores[0].VerifyChallenge(ctx, bad); !errors.Is(err, code.ErrInvalid) {
			t.Fatal("malformed id accepted")
		}
	}
	other := code.NewStore(f.rdb, "other:", f.digesters[1], f.o)
	if err := other.VerifyChallenge(ctx, c); !errors.Is(err, code.ErrExpired) {
		t.Fatal("cross instance accepted")
	}
	if err := f.stores[1].VerifyChallenge(ctx, wrongChallenge(c)); !errors.Is(err, code.ErrInvalid) {
		t.Fatal(err)
	}
	original := f.mr.TTL(challengeKey(f, 0, c.Purpose))
	advanceChallenge(f, 37*time.Second)
	if err := f.stores[1].VerifyChallenge(ctx, wrongChallenge(c)); !errors.Is(err, code.ErrInvalid) {
		t.Fatal(err)
	}
	if f.mr.TTL(challengeKey(f, 0, c.Purpose)) != original-37*time.Second {
		t.Fatal("rotation renewed challenge")
	}
	advanceChallenge(f, original-37*time.Second)
	if err := f.stores[0].VerifyChallenge(ctx, c); !errors.Is(err, code.ErrExpired) {
		t.Fatal("expired challenge accepted")
	}
	// Old target-only records are intentionally unreachable by the new protocol.
	f.legacy(t, 0, 0, time.Minute)
	if err := f.stores[1].VerifyChallenge(ctx, c); !errors.Is(err, code.ErrExpired) {
		t.Fatal("legacy record adopted")
	}
}

func TestChallengeSendingLimits(t *testing.T) {
	ctx := context.Background()
	t.Run("cooldown and daily quota survive discard", func(t *testing.T) {
		f := newRotation(t, false)
		for n := range 3 {
			issued, err := f.stores[n%2].IssueChallenge(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, "test-ip", code.Binding{})
			if err != nil {
				t.Fatal(err)
			}
			if err := f.stores[1-n%2].DiscardChallenge(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, issued.CodeID); err != nil {
				t.Fatal(err)
			}
			_, err = f.stores[1-n%2].IssueChallenge(ctx, enum.IdentityPhone, enum.PurposeBind, target, "test-ip", code.Binding{})
			requireLimit(t, err, "COOLDOWN")
			advanceChallenge(f, time.Second)
		}
		_, err := f.stores[1].IssueChallenge(ctx, enum.IdentityPhone, enum.PurposeReauth, target, "test-ip", code.Binding{})
		requireLimit(t, err, "TARGET_LIMIT")
	})
	t.Run("ip rejection spends no target state", func(t *testing.T) {
		f := challengeFixture(t, false)
		f.mr.Set("rot:quota:PHONE:ip:test-ip:20261004", "100")
		_, err := f.stores[1].IssueChallenge(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, "test-ip", code.Binding{})
		requireLimit(t, err, "IP_LIMIT")
		for active := range 2 {
			if f.mr.Exists(f.key(active, "quota", enum.IdentityPhone, enum.PurposeSignIn)) || f.mr.Exists(f.key(active, "cooldown", enum.IdentityPhone, enum.PurposeSignIn)) || f.mr.Exists(challengeKey(f, active, enum.PurposeSignIn)) {
				t.Fatal("rejected issue mutated target")
			}
		}
	})
}

func TestChallengeConcurrentReplaceAndDiscard(t *testing.T) {
	ctx := context.Background()
	for range 20 {
		f := challengeFixture(t, false)
		old := issueChallenge(t, f, 0, enum.PurposeSignIn, code.Binding{})
		advanceChallenge(f, time.Second)
		var fresh code.Issued
		var issueErr, discardErr error
		var wg sync.WaitGroup
		wg.Go(func() {
			fresh, issueErr = f.stores[1].IssueChallenge(ctx, old.Channel, old.Purpose, old.Target, "test-ip", old.Binding)
		})
		wg.Go(func() {
			discardErr = f.stores[0].DiscardChallenge(ctx, old.Channel, old.Purpose, old.Target, old.CodeID)
		})
		wg.Wait()
		if issueErr != nil || discardErr != nil {
			t.Fatal("concurrent issue/discard failed")
		}
		current := old
		current.CodeID = fresh.CodeID
		current.Code = fresh.Code
		if err := f.stores[0].VerifyChallenge(ctx, current); err != nil {
			t.Fatal("late discard removed replacement")
		}
	}
}

func TestChallengeConcurrentReplaceAndVerify(t *testing.T) {
	ctx := context.Background()
	for range 20 {
		f := challengeFixture(t, false)
		old := issueChallenge(t, f, 0, enum.PurposeSignIn, code.Binding{})
		advanceChallenge(f, time.Second)
		var fresh code.Issued
		var issueErr, verifyErr error
		var wg sync.WaitGroup
		wg.Go(func() {
			fresh, issueErr = f.stores[1].IssueChallenge(ctx, old.Channel, old.Purpose, old.Target, "test-ip", old.Binding)
		})
		wg.Go(func() { verifyErr = f.stores[0].VerifyChallenge(ctx, old) })
		wg.Wait()
		if issueErr != nil || (verifyErr != nil && !errors.Is(verifyErr, code.ErrExpired)) {
			t.Fatal("issue/verify not serializable")
		}
		current := old
		current.CodeID = fresh.CodeID
		current.Code = fresh.Code
		if f.mr.HGet(challengeKey(f, 1, old.Purpose), "n") != "0" {
			t.Fatal("old credential spent new attempts")
		}
		if err := f.stores[0].VerifyChallenge(ctx, current); err != nil {
			t.Fatal("race lost fresh challenge")
		}
	}
}

func TestChallengeQuotaCorruptionIsReadOnly(t *testing.T) {
	ctx := context.Background()
	for _, kind := range []string{"target", "ip"} {
		for _, value := range []string{"1.0", "1e0", "01", "+1", "9223372036854775807", "9223372036854775808"} {
			t.Run(kind+"/"+value, func(t *testing.T) {
				f := challengeFixture(t, false)
				c := issueChallenge(t, f, 0, enum.PurposeSignIn, code.Binding{})
				if err := f.stores[0].VerifyChallenge(ctx, wrongChallenge(c)); !errors.Is(err, code.ErrInvalid) {
					t.Fatal(err)
				}
				advanceChallenge(f, time.Second)
				ipKey := "rot:quota:PHONE:ip:test-ip:20261004"
				corrupt := f.key(1, "quota", c.Channel, c.Purpose)
				if kind == "ip" {
					corrupt = ipKey
				}
				f.mr.Set(corrupt, value)
				f.mr.SetTTL(corrupt, time.Hour)
				count0, _ := f.mr.Get(f.key(0, "quota", c.Channel, c.Purpose))
				count1, _ := f.mr.Get(f.key(1, "quota", c.Channel, c.Purpose))
				ip, _ := f.mr.Get(ipKey)
				_, err := f.stores[1].IssueChallenge(ctx, c.Channel, c.Purpose, c.Target, "test-ip", c.Binding)
				if !errors.Is(err, code.ErrUnavailable) {
					t.Fatalf("corrupt quota: %v", err)
				}
				if f.mr.Exists(f.key(0, "cooldown", c.Channel, c.Purpose)) || f.mr.Exists(f.key(1, "cooldown", c.Channel, c.Purpose)) {
					t.Error("rejection wrote cooldown")
				}
				got0, _ := f.mr.Get(f.key(0, "quota", c.Channel, c.Purpose))
				got1, _ := f.mr.Get(f.key(1, "quota", c.Channel, c.Purpose))
				gotIP, _ := f.mr.Get(ipKey)
				if got0 != count0 || got1 != count1 || gotIP != ip {
					t.Error("rejection changed quota")
				}
				if f.mr.HGet(challengeKey(f, 0, c.Purpose), "code_id") != c.CodeID || f.mr.Exists(challengeKey(f, 1, c.Purpose)) {
					t.Error("rejection changed old challenge")
				}
				failure, _ := f.mr.Get(failureKey(f, 0))
				if failure != "1" || f.mr.TTL(failureKey(f, 0)) != 15*time.Minute-time.Second {
					t.Error("rejection changed failure budget")
				}
			})
		}
	}
}
