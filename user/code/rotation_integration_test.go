package code_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
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
		f.stores[i] = code.NewStore(f.rdb, f.prefix, d, code.Options{TTL: time.Minute, Cooldown: time.Second, MaxAttempts: 3, DailyLimitPerTarget: 3, DailyLimitPerIP: 100, Now: func() time.Time { return f.now }})
	}
	return f
}

// Only the legacy fixture describes the pre-upgrade wire format; assertions use public Store calls.
func (f *realRotation) key(active int, kind string, ch enum.IdentityKind, purpose enum.CodePurpose) string {
	d, _ := f.digesters[active].Digest(target)
	switch kind {
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

func (f *realRotation) legacy(t *testing.T, active, attempts int, ttl time.Duration) string {
	t.Helper()
	const plain = "482613"
	h, _ := f.digesters[active].Digest("code:PHONE:signin:" + target + ":" + plain)
	k := f.key(active, "code", enum.IdentityPhone, enum.PurposeSignIn)
	ctx := context.Background()
	if err := f.rdb.HSet(ctx, k, "h", h, "n", attempts).Err(); err != nil {
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
						plain, err := f.stores[active].Issue(ctx, ch, purpose, target, "test-ip")
						if err != nil {
							t.Fatal("real Redis issue failed", err)
						}
						if err := f.stores[1-active].Verify(ctx, ch, purpose, target, plain); err != nil {
							t.Fatal("real Redis cross-active verification failed", err)
						}
						if err := f.stores[active].Verify(ctx, ch, purpose, target, plain); !errors.Is(err, code.ErrExpired) {
							t.Fatal("real Redis code consumed twice")
						}
					})
				}
			}
		}
	})
	t.Run("legacy ttl and attempts stay on old hash", func(t *testing.T) {
		f := newRealRotation(t)
		plain := f.legacy(t, 0, 1, 4*time.Second)
		key := f.key(0, "code", enum.IdentityPhone, enum.PurposeSignIn)
		before, err := f.rdb.PTTL(ctx, key).Result()
		if err != nil {
			t.Fatal(err)
		}
		if err := f.stores[1].Verify(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, "000000"); !errors.Is(err, code.ErrInvalid) {
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
		if exists, err := f.rdb.Exists(ctx, f.key(1, "code", enum.IdentityPhone, enum.PurposeSignIn)).Result(); err != nil || exists != 0 {
			t.Fatal("verification migrated old hash")
		}
		if err := f.stores[0].Verify(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, "000000"); !errors.Is(err, code.ErrInvalid) {
			t.Fatal("remaining attempt was not available")
		}
		if err := f.stores[1].Verify(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, plain); !errors.Is(err, code.ErrExhausted) {
			t.Fatal("cross-active attempts budget reset")
		}
		if err := f.stores[0].Verify(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, plain); !errors.Is(err, code.ErrExpired) {
			t.Fatal("exhausted code revived")
		}
	})
	t.Run("legacy expires at original deadline", func(t *testing.T) {
		f := newRealRotation(t)
		plain := f.legacy(t, 0, 0, 100*time.Millisecond)
		time.Sleep(150 * time.Millisecond)
		if err := f.stores[1].Verify(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, plain); !errors.Is(err, code.ErrExpired) {
			t.Fatal("legacy deadline extended")
		}
	})
	t.Run("legacy cooldown and cumulative daily quota", func(t *testing.T) {
		f := newRealRotation(t)
		old := f.legacy(t, 0, 0, time.Minute)
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
		_, err := f.stores[1].Issue(ctx, enum.IdentityPhone, enum.PurposeBind, target, "test-ip")
		if requireLimit(t, err, "COOLDOWN").RetryAfter < time.Second {
			t.Fatal("cooldown retry lost")
		}
		if err := f.stores[1].Verify(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, old); err != nil {
			t.Fatal("cooldown refusal consumed original")
		}
		time.Sleep(1250 * time.Millisecond)
		if _, err := f.stores[1].Issue(ctx, enum.IdentityPhone, enum.PurposeBind, target, "test-ip"); err != nil {
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
		_, err = f.stores[0].Issue(ctx, enum.IdentityPhone, enum.PurposeReauth, target, "test-ip")
		requireLimit(t, err, "TARGET_LIMIT")
	})
	t.Run("concurrent issue and consume each have one winner", func(t *testing.T) {
		f := newRealRotation(t)
		var wins, limited, expired, unexpected atomic.Int32
		var mu sync.Mutex
		var plain string
		var wg sync.WaitGroup
		start := make(chan struct{})
		for n := range 20 {
			wg.Go(func() {
				<-start
				c, err := f.stores[n%2].Issue(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, "test-ip")
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
				err := f.stores[n%2].Verify(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, plain)
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
