package code_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/pii"
	"github.com/bbxx111/accountkit/user/code"
	"github.com/redis/go-redis/v9"
)

type realRotation struct {
	rdb       *redis.Client
	prefix    string
	digesters [2]*pii.Digester
	stores    [2]*code.Store
	now       time.Time
}

func newRealRotation(t *testing.T) *realRotation {
	t.Helper()
	url := os.Getenv("ACCOUNTSVC_TEST_REDIS_URL")
	if url == "" {
		t.Skip("ACCOUNTSVC_TEST_REDIS_URL not set; real Redis required")
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal("parse test Redis URL")
	}
	opts.MaxRetries = -1
	f := &realRotation{rdb: redis.NewClient(opts), prefix: fmt.Sprintf("rotation_store_%016x:", rand.Uint64()), now: time.Now().UTC()}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		defer f.rdb.Close()
		keys, err := f.rdb.Keys(ctx, f.prefix+"*").Result()
		if err != nil {
			t.Error("list owned keys", err)
		} else if len(keys) > 0 {
			if err := f.rdb.Del(ctx, keys...).Err(); err != nil {
				t.Error("cleanup owned keys", err)
			}
		}
	})
	if err := f.rdb.Ping(context.Background()).Err(); err != nil {
		t.Fatal("real Redis unavailable")
	}
	for i := range 2 {
		d, err := pii.NewDigester(map[uint16][]byte{1: bytes.Repeat([]byte{7}, 32), 2: bytes.Repeat([]byte{9}, 32)}, uint16(i+1))
		if err != nil {
			t.Fatal(err)
		}
		f.digesters[i] = d
		f.stores[i] = code.NewStore(f.rdb, f.prefix, d, code.Options{TTL: time.Minute, Cooldown: time.Second, MaxAttempts: 3, DailyLimitPerTarget: 3, DailyLimitPerIP: 100, FailureLimitPerTarget: 10, FailureWindow: 15 * time.Minute, Now: func() time.Time { return f.now }})
	}
	return f
}

// Prior-active fixtures preserve challenge identifiers and original deadlines.
func (f *realRotation) key(active int, kind string, ch enum.IdentityKind, purpose enum.CodePurpose) string {
	d, _ := f.digesters[active].Digest(target)
	switch kind {
	case "challenge":
		return f.prefix + "challenge:" + ch.String() + ":" + purpose.Key() + ":" + d
	case "code":
		return f.prefix + "code:" + ch.String() + ":" + purpose.Key() + ":" + d
	case "cooldown":
		return f.prefix + "cooldown:" + ch.String() + ":" + d
	case "quota":
		return f.prefix + "quota:" + ch.String() + ":target:" + d + ":" + f.now.Format("20060102")
	default:
		panic("unknown fixture key")
	}
}

func (f *realRotation) priorChallenge(t *testing.T, active, attempts int, ttl time.Duration) code.Issued {
	t.Helper()
	plain := code.Issued{CodeID: "11111111111111111111111111111111", Code: "482613", ExpireTime: f.now.Add(ttl)}
	h, _ := f.digesters[active].Digest("challenge:PHONE:signin:" + target + ":" + plain.CodeID + ":" + plain.Code)
	k := f.key(active, "challenge", enum.IdentityPhone, enum.PurposeSignIn)
	ctx := context.Background()
	if err := f.rdb.HSet(ctx, k, "h", h, "n", attempts, "code_id", plain.CodeID, "user_id", "", "session_id", "").Err(); err != nil {
		t.Fatal(err)
	}
	if err := f.rdb.PExpire(ctx, k, ttl).Err(); err != nil {
		t.Fatal(err)
	}
	return plain
}

func TestCodeRotationRedisIntegration(t *testing.T) {
	if os.Getenv("ACCOUNTSVC_TEST_REDIS_URL") == "" {
		t.Skip("ACCOUNTSVC_TEST_REDIS_URL not set; real Redis required")
	}
	ctx := context.Background()
	t.Run("bidirectional channels and purposes", func(t *testing.T) {
		for _, ch := range []enum.IdentityKind{enum.IdentityPhone, enum.IdentityEmail} {
			for _, purpose := range []enum.CodePurpose{enum.PurposeSignIn, enum.PurposeBind, enum.PurposeReauth} {
				for active := range 2 {
					t.Run(ch.String()+"/"+purpose.Key()+"/"+strconv.Itoa(active), func(t *testing.T) {
						f := newRealRotation(t)
						plain, err := f.stores[active].IssueChallenge(ctx, ch, purpose, target, "test-ip", code.Binding{})
						if err != nil {
							t.Fatal("real Redis issue failed", err)
						}
						if err := f.stores[1-active].VerifyChallenge(ctx, credential(plain, ch, purpose, target)); err != nil {
							t.Fatal("real Redis cross-active verification failed", err)
						}
						if err := f.stores[active].VerifyChallenge(ctx, credential(plain, ch, purpose, target)); !errors.Is(err, code.ErrExpired) {
							t.Fatal("real Redis code consumed twice")
						}
					})
				}
			}
		}
	})
	t.Run("prior active ttl and attempts stay on old hash", func(t *testing.T) {
		f := newRealRotation(t)
		plain := f.priorChallenge(t, 0, 1, 4*time.Second)
		key := f.key(0, "challenge", enum.IdentityPhone, enum.PurposeSignIn)
		before, err := f.rdb.PTTL(ctx, key).Result()
		if err != nil {
			t.Fatal(err)
		}
		if err := f.stores[1].VerifyChallenge(ctx, credential(wrongIssued(plain), enum.IdentityPhone, enum.PurposeSignIn, target)); !errors.Is(err, code.ErrInvalid) {
			t.Fatal("old hash mismatch changed")
		}
		n, err := f.rdb.HGet(ctx, key, "n").Result()
		if err != nil || n != "2" {
			t.Fatal("old attempts reset or moved")
		}
		after, err := f.rdb.PTTL(ctx, key).Result()
		if err != nil || after > before || after < before-time.Second {
			t.Fatal("old expiry renewed or removed")
		}
		if exists, err := f.rdb.Exists(ctx, f.key(1, "challenge", enum.IdentityPhone, enum.PurposeSignIn)).Result(); err != nil || exists != 0 {
			t.Fatal("verification migrated old hash")
		}
		if err := f.stores[0].VerifyChallenge(ctx, credential(wrongIssued(plain), enum.IdentityPhone, enum.PurposeSignIn, target)); !errors.Is(err, code.ErrInvalid) {
			t.Fatal("remaining attempt was not available")
		}
		if err := f.stores[1].VerifyChallenge(ctx, credential(plain, enum.IdentityPhone, enum.PurposeSignIn, target)); !errors.Is(err, code.ErrExpired) {
			t.Fatal("cross-active attempts budget reset")
		}
		if err := f.stores[0].VerifyChallenge(ctx, credential(plain, enum.IdentityPhone, enum.PurposeSignIn, target)); !errors.Is(err, code.ErrExpired) {
			t.Fatal("exhausted code revived")
		}
	})
	t.Run("prior active expires at original deadline", func(t *testing.T) {
		f := newRealRotation(t)
		plain := f.priorChallenge(t, 0, 0, 100*time.Millisecond)
		time.Sleep(150 * time.Millisecond)
		if err := f.stores[1].VerifyChallenge(ctx, credential(plain, enum.IdentityPhone, enum.PurposeSignIn, target)); !errors.Is(err, code.ErrExpired) {
			t.Fatal("legacy deadline extended")
		}
	})
	t.Run("legacy cooldown and cumulative daily quota", func(t *testing.T) {
		f := newRealRotation(t)
		old := f.priorChallenge(t, 0, 0, time.Minute)
		cool := f.key(0, "cooldown", enum.IdentityPhone, enum.PurposeSignIn)
		if err := f.rdb.Set(ctx, cool, "1", 1200*time.Millisecond).Err(); err != nil {
			t.Fatal(err)
		}
		var quotaDeadlines [2]time.Duration
		for active := range 2 {
			if err := f.rdb.Set(ctx, f.key(active, "quota", enum.IdentityPhone, enum.PurposeSignIn), "1", time.Hour).Err(); err != nil {
				t.Fatal(err)
			}
			deadline, err := f.rdb.PExpireTime(ctx, f.key(active, "quota", enum.IdentityPhone, enum.PurposeSignIn)).Result()
			if err != nil || deadline <= 0 {
				t.Fatal("read original quota deadline failed")
			}
			quotaDeadlines[active] = deadline
		}
		_, err := f.stores[1].IssueChallenge(ctx, enum.IdentityPhone, enum.PurposeBind, target, "test-ip", code.Binding{})
		if requireLimit(t, err, "COOLDOWN").RetryAfter < time.Second {
			t.Fatal("cooldown retry lost")
		}
		if err := f.stores[1].VerifyChallenge(ctx, credential(old, enum.IdentityPhone, enum.PurposeSignIn, target)); err != nil {
			t.Fatal("cooldown refusal consumed original")
		}
		time.Sleep(1250 * time.Millisecond)
		if _, err := f.stores[1].IssueChallenge(ctx, enum.IdentityPhone, enum.PurposeBind, target, "test-ip", code.Binding{}); err != nil {
			t.Fatal("quota rejected early", err)
		}
		for active, want := range []string{"1", "2"} {
			key := f.key(active, "quota", enum.IdentityPhone, enum.PurposeSignIn)
			got, err := f.rdb.Get(ctx, key).Result()
			if err != nil || got != want {
				t.Fatal("target quota copied or double counted")
			}
			deadline, err := f.rdb.PExpireTime(ctx, key).Result()
			if err != nil || deadline != quotaDeadlines[active] {
				t.Fatal("old quota expiry changed")
			}
		}
		ip := f.prefix + "quota:PHONE:ip:test-ip:" + f.now.Format("20060102")
		if got, err := f.rdb.Get(ctx, ip).Result(); err != nil || got != "1" {
			t.Fatal("IP quota did not increase exactly once")
		}
		time.Sleep(1100 * time.Millisecond)
		_, err = f.stores[0].IssueChallenge(ctx, enum.IdentityPhone, enum.PurposeReauth, target, "test-ip", code.Binding{})
		requireLimit(t, err, "TARGET_LIMIT")
	})
	t.Run("concurrent issue and consume each have one winner", func(t *testing.T) {
		f := newRealRotation(t)
		var wins, limited, expired, unexpected atomic.Int32
		var mu sync.Mutex
		var plain code.Issued
		var wg sync.WaitGroup
		start := make(chan struct{})
		for n := range 20 {
			wg.Go(func() {
				<-start
				c, err := f.stores[n%2].IssueChallenge(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, "test-ip", code.Binding{})
				var rl *code.RateLimitedError
				if err == nil {
					wins.Add(1)
					mu.Lock()
					plain = c
					mu.Unlock()
				} else if errors.As(err, &rl) && rl.Dimension == "COOLDOWN" {
					limited.Add(1)
				} else {
					unexpected.Add(1)
				}
			})
		}
		close(start)
		wg.Wait()
		if wins.Load() != 1 || limited.Load() != 19 || unexpected.Load() != 0 {
			t.Fatal("real Redis issue not atomic")
		}
		wins.Store(0)
		start = make(chan struct{})
		for n := range 20 {
			wg.Go(func() {
				<-start
				err := f.stores[n%2].VerifyChallenge(ctx, credential(plain, enum.IdentityPhone, enum.PurposeSignIn, target))
				if err == nil {
					wins.Add(1)
				} else if errors.Is(err, code.ErrExpired) {
					expired.Add(1)
				} else {
					unexpected.Add(1)
				}
			})
		}
		close(start)
		wg.Wait()
		if wins.Load() != 1 || expired.Load() != 19 || unexpected.Load() != 0 {
			t.Fatal("real Redis consumption not atomic")
		}
	})
}

func TestChallengeRedisIntegration(t *testing.T) {
	ctx := context.Background()
	t.Run("cross active channels and purposes", func(t *testing.T) {
		for _, ch := range []enum.IdentityKind{enum.IdentityPhone, enum.IdentityEmail} {
			for _, purpose := range []enum.CodePurpose{enum.PurposeSignIn, enum.PurposeBind, enum.PurposeReauth} {
				for active := range 2 {
					t.Run(ch.String()+"/"+purpose.Key()+"/"+strconv.Itoa(active), func(t *testing.T) {
						f := newRealRotation(t)
						binding := code.Binding{UserID: "u_fixture", SessionID: "s_fixture"}
						issued, err := f.stores[active].IssueChallenge(ctx, ch, purpose, target, "test-ip", binding)
						if err != nil {
							t.Fatal(err)
						}
						c := code.Credential{Channel: ch, Purpose: purpose, Target: target, CodeID: issued.CodeID, Code: issued.Code, Binding: binding}
						if err := f.stores[1-active].VerifyChallenge(ctx, c); err != nil {
							t.Fatal("cross active verification failed", err)
						}
						if err := f.stores[active].VerifyChallenge(ctx, c); !errors.Is(err, code.ErrExpired) {
							t.Fatal("challenge consumed twice")
						}
					})
				}
			}
		}
	})
	t.Run("fixed failure deadline survives rotation and success", func(t *testing.T) {
		f := newRealRotation(t)
		for active := range 2 {
			f.stores[active] = code.NewStore(f.rdb, f.prefix, f.digesters[active], code.Options{TTL: time.Minute, Cooldown: time.Second, MaxAttempts: 20, DailyLimitPerTarget: 20, DailyLimitPerIP: 100, FailureLimitPerTarget: 10, FailureWindow: 15 * time.Minute})
		}
		issued, err := f.stores[0].IssueChallenge(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, "test-ip", code.Binding{})
		if err != nil {
			t.Fatal(err)
		}
		c := code.Credential{Channel: enum.IdentityPhone, Purpose: enum.PurposeSignIn, Target: target, CodeID: issued.CodeID, Code: issued.Code}
		wrong := wrongChallenge(c)
		if err := f.stores[0].VerifyChallenge(ctx, wrong); !errors.Is(err, code.ErrInvalid) {
			t.Fatal(err)
		}
		var keys [2]string
		for active := range 2 {
			d, _ := f.digesters[active].Digest(target)
			keys[active] = f.prefix + "verify_failure:PHONE:" + d
		}
		deadline, err := f.rdb.PExpireTime(ctx, keys[0]).Result()
		if err != nil {
			t.Fatal(err)
		}
		if err := f.stores[1].VerifyChallenge(ctx, wrong); !errors.Is(err, code.ErrInvalid) {
			t.Fatal(err)
		}
		moved, err := f.rdb.PExpireTime(ctx, keys[1]).Result()
		if err != nil || moved != deadline {
			t.Fatal("rotation changed fixed failure deadline")
		}
		if err := f.stores[0].VerifyChallenge(ctx, c); err != nil {
			t.Fatal(err)
		}
		retained, err := f.rdb.PExpireTime(ctx, keys[1]).Result()
		if err != nil || retained != deadline {
			t.Fatal("success changed failure deadline")
		}
		// Preserve sending state while permitting the next test round without sleeps.
		for active := range 2 {
			if err := f.rdb.Del(ctx, f.key(active, "cooldown", c.Channel, c.Purpose)).Err(); err != nil {
				t.Fatal(err)
			}
		}
		issued, err = f.stores[1].IssueChallenge(ctx, c.Channel, enum.PurposeBind, target, "test-ip", code.Binding{UserID: "u_fixture"})
		if err != nil {
			t.Fatal(err)
		}
		c.Purpose = enum.PurposeBind
		c.CodeID = issued.CodeID
		c.Code = issued.Code
		c.Binding = code.Binding{UserID: "u_fixture"}
		for n := range 8 {
			err := f.stores[n%2].VerifyChallenge(ctx, wrongChallenge(c))
			if n == 7 {
				requireLimit(t, err, "TARGET_VERIFY_LIMIT")
			} else if !errors.Is(err, code.ErrInvalid) {
				t.Fatal(err)
			}
		}
		requireLimit(t, f.stores[0].VerifyChallenge(ctx, c), "TARGET_VERIFY_LIMIT")
		_, err = f.stores[1].IssueChallenge(ctx, c.Channel, enum.PurposeReauth, target, "test-ip", code.Binding{})
		requireLimit(t, err, "TARGET_VERIFY_LIMIT")
		for active := range 2 {
			ttl, err := f.rdb.PTTL(ctx, keys[active]).Result()
			if err != nil {
				t.Fatal(err)
			}
			if ttl >= 0 {
				if ttl > 15*time.Minute || ttl < 14*time.Minute {
					t.Fatal("failure window lost")
				}
				if err := f.rdb.PExpire(ctx, keys[active], time.Millisecond).Err(); err != nil {
					t.Fatal(err)
				}
			}
		}
		time.Sleep(5 * time.Millisecond)
		for active := range 2 {
			if err := f.rdb.Del(ctx, f.key(active, "cooldown", c.Channel, c.Purpose)).Err(); err != nil {
				t.Fatal(err)
			}
		}
		if _, err = f.stores[0].IssueChallenge(ctx, c.Channel, enum.PurposeReauth, target, "test-ip", code.Binding{}); err != nil {
			t.Fatal("budget did not recover", err)
		}
	})
	t.Run("concurrent issue consume and replacement", func(t *testing.T) {
		f := newRealRotation(t)
		var issued code.Issued
		var mu sync.Mutex
		var wg sync.WaitGroup
		var wins, limited, expired, unexpected atomic.Int32
		start := make(chan struct{})
		for n := range 20 {
			wg.Go(func() {
				<-start
				v, err := f.stores[n%2].IssueChallenge(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, "test-ip", code.Binding{})
				var rl *code.RateLimitedError
				if err == nil {
					wins.Add(1)
					mu.Lock()
					issued = v
					mu.Unlock()
				} else if errors.As(err, &rl) && rl.Dimension == "COOLDOWN" {
					limited.Add(1)
				} else {
					unexpected.Add(1)
				}
			})
		}
		close(start)
		wg.Wait()
		if wins.Load() != 1 || limited.Load() != 19 || unexpected.Load() != 0 {
			t.Fatal("issue did not have one winner")
		}
		c := code.Credential{Channel: enum.IdentityPhone, Purpose: enum.PurposeSignIn, Target: target, CodeID: issued.CodeID, Code: issued.Code}
		// The late delivery cleanup for A cannot discard the replacement B.
		for active := range 2 {
			if err := f.rdb.Del(ctx, f.key(active, "cooldown", c.Channel, c.Purpose)).Err(); err != nil {
				t.Fatal(err)
			}
		}
		replacement, err := f.stores[1].IssueChallenge(ctx, c.Channel, c.Purpose, c.Target, "test-ip", c.Binding)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.stores[0].DiscardChallenge(ctx, c.Channel, c.Purpose, c.Target, c.CodeID); err != nil {
			t.Fatal(err)
		}
		if err := f.stores[1].VerifyChallenge(ctx, c); !errors.Is(err, code.ErrExpired) {
			t.Fatal("old id accepted")
		}
		c.CodeID = replacement.CodeID
		c.Code = replacement.Code
		wins.Store(0)
		start = make(chan struct{})
		for n := range 20 {
			wg.Go(func() {
				<-start
				err := f.stores[n%2].VerifyChallenge(ctx, c)
				if err == nil {
					wins.Add(1)
				} else if errors.Is(err, code.ErrExpired) {
					expired.Add(1)
				} else {
					unexpected.Add(1)
				}
			})
		}
		close(start)
		wg.Wait()
		if wins.Load() != 1 || expired.Load() != 19 || unexpected.Load() != 0 {
			t.Fatal("consume did not have one winner")
		}
	})
}

func TestChallengeQuotaCorruptionRedisIntegration(t *testing.T) {
	ctx := context.Background()
	for _, kind := range []string{"target", "ip"} {
		for _, value := range []string{"1.0", "1e0", "01", "+1", "9223372036854775807", "9223372036854775808"} {
			t.Run(kind+"/"+value, func(t *testing.T) {
				f := newRealRotation(t)
				issued, err := f.stores[0].IssueChallenge(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, "test-ip", code.Binding{})
				if err != nil {
					t.Fatal(err)
				}
				c := code.Credential{Channel: enum.IdentityPhone, Purpose: enum.PurposeSignIn, Target: target, CodeID: issued.CodeID, Code: issued.Code}
				if err := f.stores[0].VerifyChallenge(ctx, wrongChallenge(c)); !errors.Is(err, code.ErrInvalid) {
					t.Fatal(err)
				}
				for active := range 2 {
					if err := f.rdb.Del(ctx, f.key(active, "cooldown", c.Channel, c.Purpose)).Err(); err != nil {
						t.Fatal(err)
					}
				}
				corrupt := f.key(1, "quota", c.Channel, c.Purpose)
				if kind == "ip" {
					corrupt = f.prefix + "quota:PHONE:ip:test-ip:" + f.now.Format("20060102")
				}
				if err := f.rdb.Set(ctx, corrupt, value, time.Hour).Err(); err != nil {
					t.Fatal(err)
				}
				type state struct {
					value    string
					deadline time.Duration
				}
				snapshot := func() map[string]state {
					keys, err := f.rdb.Keys(ctx, f.prefix+"*").Result()
					if err != nil {
						t.Fatal(err)
					}
					states := make(map[string]state, len(keys))
					for _, key := range keys {
						dump, err := f.rdb.Dump(ctx, key).Result()
						if err != nil {
							t.Fatal(err)
						}
						deadline, err := f.rdb.PExpireTime(ctx, key).Result()
						if err != nil {
							t.Fatal(err)
						}
						states[key] = state{dump, deadline}
					}
					return states
				}
				before := snapshot()
				_, err = f.stores[1].IssueChallenge(ctx, c.Channel, c.Purpose, c.Target, "test-ip", c.Binding)
				if !errors.Is(err, code.ErrUnavailable) {
					t.Fatalf("corrupt quota: %v", err)
				}
				if !reflect.DeepEqual(before, snapshot()) {
					t.Error("failed issue changed Redis values, keys or expiry deadlines")
				}
			})
		}
	}
}
