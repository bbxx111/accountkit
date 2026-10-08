package code_test

import (
	"bytes"
	"context"
	"errors"
	"strconv"
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

type rotationFixture struct {
	mr        *miniredis.Miniredis
	rdb       *redis.Client
	stores    [2]*code.Store
	digesters [2]*pii.Digester
	now       time.Time
	o         code.Options
}

func newRotation(t *testing.T, alias bool) *rotationFixture {
	t.Helper()
	f := &rotationFixture{mr: miniredis.RunT(t), now: time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)}
	f.rdb = redis.NewClient(&redis.Options{Addr: f.mr.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = f.rdb.Close() })
	f.o = code.Options{TTL: 5 * time.Minute, Cooldown: time.Second, MaxAttempts: 3, DailyLimitPerTarget: 3, DailyLimitPerIP: 100, FailureLimitPerTarget: 10, FailureWindow: 15 * time.Minute, Now: func() time.Time { return f.now }}
	k2 := byte(9)
	if alias {
		k2 = 7
	}
	keys := map[uint16][]byte{1: bytes.Repeat([]byte{7}, 32), 2: bytes.Repeat([]byte{k2}, 32)}
	for i := range 2 {
		d, err := pii.NewDigester(keys, uint16(i+1))
		if err != nil {
			t.Fatal("construct digester")
		}
		f.digesters[i] = d
		f.stores[i] = code.NewStore(f.rdb, "rot:", d, f.o)
	}
	return f
}

func requireLimit(t *testing.T, err error, dimension string) *code.RateLimitedError {
	t.Helper()
	var limited *code.RateLimitedError
	if !errors.As(err, &limited) || limited.Dimension != dimension {
		t.Fatalf("expected %s rejection", dimension)
	}
	return limited
}

func TestCodeRotationVerify(t *testing.T) {
	for _, channel := range []enum.IdentityKind{enum.IdentityPhone, enum.IdentityEmail} {
		for _, purpose := range []enum.CodePurpose{enum.PurposeSignIn, enum.PurposeBind, enum.PurposeReauth} {
			for issuer := range 2 {
				t.Run(channel.String()+"/"+purpose.Key()+"/"+string(rune('1'+issuer)), func(t *testing.T) {
					f := newRotation(t, false)
					plain, err := f.stores[issuer].IssueChallenge(context.Background(), channel, purpose, target, "test-ip", code.Binding{})
					if err != nil {
						t.Fatal("issue failed")
					}
					if err := f.stores[1-issuer].VerifyChallenge(context.Background(), credential(plain, channel, purpose, target)); err != nil {
						t.Fatal("cross-active verify failed")
					}
					if err := f.stores[issuer].VerifyChallenge(context.Background(), credential(plain, channel, purpose, target)); !errors.Is(err, code.ErrExpired) {
						t.Fatal("consumed code remained usable")
					}
				})
			}
		}
	}
}

func TestCodeRotationLimits(t *testing.T) {
	t.Run("cooldown", func(t *testing.T) {
		f := newRotation(t, false)
		_, err := f.stores[0].IssueChallenge(context.Background(), enum.IdentityPhone, enum.PurposeSignIn, target, "test-ip", code.Binding{})
		if err != nil {
			t.Fatal("issue failed")
		}
		for _, p := range []enum.CodePurpose{enum.PurposeSignIn, enum.PurposeBind, enum.PurposeReauth} {
			_, err := f.stores[1].IssueChallenge(context.Background(), enum.IdentityPhone, p, target, "test-ip", code.Binding{})
			requireLimit(t, err, "COOLDOWN")
		}
	})
	t.Run("quota", func(t *testing.T) {
		f := newRotation(t, false)
		for _, active := range []int{0, 1, 0} {
			if _, err := f.stores[active].IssueChallenge(context.Background(), enum.IdentityPhone, enum.PurposeSignIn, target, "test-ip", code.Binding{}); err != nil {
				t.Fatal("issue below quota failed")
			}
			f.mr.FastForward(2 * time.Second)
		}
		_, err := f.stores[1].IssueChallenge(context.Background(), enum.IdentityPhone, enum.PurposeBind, target, "test-ip", code.Binding{})
		requireLimit(t, err, "TARGET_LIMIT")
	})
}

func (f *rotationFixture) key(active int, kind string, channel enum.IdentityKind, purpose enum.CodePurpose) string {
	d, _ := f.digesters[active].Digest(target)
	switch kind {
	case "challenge":
		return "rot:challenge:" + channel.String() + ":" + purpose.Key() + ":" + d
	case "code":
		return "rot:code:" + channel.String() + ":" + purpose.Key() + ":" + d
	case "cooldown":
		return "rot:cooldown:" + channel.String() + ":" + d
	default:
		return "rot:quota:" + channel.String() + ":target:" + d + ":" + f.now.UTC().Format("20060102")
	}
}

// legacy writes the original h/n hash directly; its nonnumeric code is guaranteed
// to differ from every newly issued six-digit code.
func (f *rotationFixture) legacy(t *testing.T, active, attempts int, ttl time.Duration) string {
	t.Helper()
	plain := "legacy-fixture"
	h, _ := f.digesters[active].Digest("code:PHONE:signin:" + target + ":" + plain)
	k := f.key(active, "code", enum.IdentityPhone, enum.PurposeSignIn)
	f.mr.HSet(k, "h", h, "n", strconv.Itoa(attempts))
	if ttl > 0 {
		f.mr.SetTTL(k, ttl)
	}
	return plain
}

// A prior-active fixture is one complete record under the challenge protocol.
func (f *rotationFixture) priorChallenge(t *testing.T, active, attempts int, ttl time.Duration) code.Issued {
	t.Helper()
	issued := code.Issued{CodeID: "11111111111111111111111111111111", Code: "legacy-fixture", ExpireTime: f.now.Add(ttl)}
	h, _ := f.digesters[active].Digest("challenge:PHONE:signin:" + target + ":" + issued.CodeID + ":" + issued.Code)
	k := f.key(active, "challenge", enum.IdentityPhone, enum.PurposeSignIn)
	f.mr.HSet(k, "h", h, "n", strconv.Itoa(attempts), "code_id", issued.CodeID, "user_id", "", "session_id", "")
	if ttl > 0 {
		f.mr.SetTTL(k, ttl)
	}
	return issued
}

func TestCodeRotationState(t *testing.T) {
	ctx := context.Background()
	t.Run("attempts and ttl", func(t *testing.T) {
		f := newRotation(t, false)
		plain := f.priorChallenge(t, 0, 1, 37*time.Second)
		k := f.key(0, "challenge", enum.IdentityPhone, enum.PurposeSignIn)
		if err := f.stores[1].VerifyChallenge(ctx, credential(wrongIssued(plain), enum.IdentityPhone, enum.PurposeSignIn, target)); !errors.Is(err, code.ErrInvalid) {
			t.Fatal("legacy attempt not continued")
		}
		if f.mr.HGet(k, "n") != "2" || f.mr.TTL(k) != 37*time.Second {
			t.Fatal("attempt count or original ttl changed")
		}
		if err := f.stores[0].VerifyChallenge(ctx, credential(wrongIssued(plain), enum.IdentityPhone, enum.PurposeSignIn, target)); !errors.Is(err, code.ErrInvalid) {
			t.Fatal("third wrong attempt must invalidate round")
		}
		if err := f.stores[0].VerifyChallenge(ctx, credential(plain, enum.IdentityPhone, enum.PurposeSignIn, target)); !errors.Is(err, code.ErrExpired) {
			t.Fatal("legacy budget reset")
		}
		if f.mr.Exists(k) {
			t.Fatal("exhausted record retained")
		}
	})
	t.Run("expiry", func(t *testing.T) {
		f := newRotation(t, false)
		plain := f.priorChallenge(t, 0, 0, 3*time.Second)
		_ = f.stores[1].VerifyChallenge(ctx, credential(wrongIssued(plain), enum.IdentityPhone, enum.PurposeSignIn, target))
		f.mr.FastForward(3 * time.Second)
		if err := f.stores[1].VerifyChallenge(ctx, credential(plain, enum.IdentityPhone, enum.PurposeSignIn, target)); !errors.Is(err, code.ErrExpired) {
			t.Fatal("old expiry extended")
		}
	})
	t.Run("conflict", func(t *testing.T) {
		for verifier := range 2 {
			f := newRotation(t, false)
			plain := f.priorChallenge(t, 0, 0, time.Minute)
			f.priorChallenge(t, 1, 0, 2*time.Minute)
			cool := f.key(0, "cooldown", enum.IdentityPhone, enum.PurposeSignIn)
			quota := f.key(0, "quota", enum.IdentityPhone, enum.PurposeSignIn)
			f.mr.Set(cool, "1")
			f.mr.SetTTL(cool, time.Minute)
			f.mr.Set(quota, "2")
			f.mr.SetTTL(quota, time.Hour)
			if err := f.stores[verifier].VerifyChallenge(ctx, credential(plain, enum.IdentityPhone, enum.PurposeSignIn, target)); !errors.Is(err, code.ErrExpired) {
				t.Fatal("conflict was accepted")
			}
			for active := range 2 {
				if f.mr.Exists(f.key(active, "challenge", enum.IdentityPhone, enum.PurposeSignIn)) {
					t.Fatal("conflict was not fully cleared")
				}
			}
			if !f.mr.Exists(cool) || f.mr.TTL(quota) != time.Hour {
				t.Fatal("conflict modified sending limits")
			}
		}
	})
	t.Run("aliases", func(t *testing.T) {
		f := newRotation(t, true)
		plain := f.priorChallenge(t, 0, 0, time.Minute)
		if err := f.stores[1].VerifyChallenge(ctx, credential(plain, enum.IdentityPhone, enum.PurposeSignIn, target)); err != nil {
			t.Fatal("alias counted as conflicting record")
		}
		for n := range 3 {
			if _, err := f.stores[n%2].IssueChallenge(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, "test-ip", code.Binding{}); err != nil {
				t.Fatal("alias quota counted twice")
			}
			f.mr.FastForward(2 * time.Second)
		}
		_, err := f.stores[0].IssueChallenge(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, "test-ip", code.Binding{})
		requireLimit(t, err, "TARGET_LIMIT")
	})
	t.Run("replacement", func(t *testing.T) {
		f := newRotation(t, false)
		old := f.priorChallenge(t, 0, 0, time.Minute)
		fresh, err := f.stores[1].IssueChallenge(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, "test-ip", code.Binding{})
		if err != nil {
			t.Fatal("reissue failed")
		}
		if err := f.stores[0].VerifyChallenge(ctx, credential(old, enum.IdentityPhone, enum.PurposeSignIn, target)); !errors.Is(err, code.ErrExpired) {
			t.Fatal("replaced legacy code accepted")
		}
		if err := f.stores[0].VerifyChallenge(ctx, credential(fresh, enum.IdentityPhone, enum.PurposeSignIn, target)); err != nil {
			t.Fatal("replacement cannot be consumed")
		}
	})
}

func TestCodeRotationConcurrent(t *testing.T) {
	ctx := context.Background()
	t.Run("issue", func(t *testing.T) {
		f := newRotation(t, false)
		keys := make(map[uint16][]byte, 20)
		for n := range 20 {
			keys[uint16(n+1)] = bytes.Repeat([]byte{byte(n + 1)}, 32)
		}
		stores := make([]*code.Store, 20)
		for n := range 20 {
			d, err := pii.NewDigester(keys, uint16(n+1))
			if err != nil {
				t.Fatal("construct concurrent digester")
			}
			stores[n] = code.NewStore(f.rdb, "rot:", d, f.o)
		}
		var ok, limited atomic.Int32
		var wg sync.WaitGroup
		for n := range 20 {
			wg.Go(func() {
				_, err := stores[n].IssueChallenge(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, "test-ip", code.Binding{})
				var rl *code.RateLimitedError
				if err == nil {
					ok.Add(1)
				} else if errors.As(err, &rl) && rl.Dimension == "COOLDOWN" {
					limited.Add(1)
				}
			})
		}
		wg.Wait()
		if ok.Load() != 1 || limited.Load() != 19 {
			t.Fatal("cross-active issue did not produce one winner")
		}
	})
	t.Run("verify", func(t *testing.T) {
		f := newRotation(t, false)
		plain := f.priorChallenge(t, 0, 0, time.Minute)
		var ok, expired atomic.Int32
		var wg sync.WaitGroup
		for n := range 20 {
			wg.Go(func() {
				err := f.stores[n%2].VerifyChallenge(ctx, credential(plain, enum.IdentityPhone, enum.PurposeSignIn, target))
				if err == nil {
					ok.Add(1)
				} else if errors.Is(err, code.ErrExpired) {
					expired.Add(1)
				}
			})
		}
		wg.Wait()
		if ok.Load() != 1 || expired.Load() != 19 {
			t.Fatal("cross-active consumption did not produce one winner")
		}
	})
	t.Run("issue verify serializable", func(t *testing.T) {
		for range 20 {
			f := newRotation(t, false)
			old := f.priorChallenge(t, 0, 0, time.Minute)
			var fresh code.Issued
			var issueErr, verifyErr error
			var wg sync.WaitGroup
			wg.Go(func() {
				fresh, issueErr = f.stores[1].IssueChallenge(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, "test-ip", code.Binding{})
			})
			wg.Go(func() {
				verifyErr = f.stores[0].VerifyChallenge(ctx, credential(old, enum.IdentityPhone, enum.PurposeSignIn, target))
			})
			wg.Wait()
			if issueErr != nil || (verifyErr != nil && !errors.Is(verifyErr, code.ErrExpired)) {
				t.Fatal("race is not serializable")
			}
			if err := f.stores[0].VerifyChallenge(ctx, credential(fresh, enum.IdentityPhone, enum.PurposeSignIn, target)); err != nil {
				t.Fatal("race lost fresh record")
			}
			if err := f.stores[1].VerifyChallenge(ctx, credential(old, enum.IdentityPhone, enum.PurposeSignIn, target)); !errors.Is(err, code.ErrExpired) {
				t.Fatal("old record revived")
			}
		}
	})
}

func TestCodeRotationBoundaries(t *testing.T) {
	ctx := context.Background()
	t.Run("rejection priority", func(t *testing.T) {
		f := newRotation(t, false)
		cool := f.key(0, "cooldown", enum.IdentityPhone, enum.PurposeSignIn)
		quota := f.key(0, "quota", enum.IdentityPhone, enum.PurposeSignIn)
		f.mr.Set(cool, "1")
		f.mr.SetTTL(cool, time.Millisecond)
		f.mr.Set(quota, "3")
		f.mr.SetTTL(quota, time.Hour)
		f.mr.Set("rot:quota:PHONE:ip:test-ip:20261004", "100")
		_, err := f.stores[1].IssueChallenge(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, "test-ip", code.Binding{})
		if requireLimit(t, err, "COOLDOWN").RetryAfter != time.Second {
			t.Fatal("subsecond cooldown retry is not at least one second")
		}
		f.mr.Del(cool)
		_, err = f.stores[1].IssueChallenge(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, "test-ip", code.Binding{})
		requireLimit(t, err, "TARGET_LIMIT")
		f.mr.Del(quota)
		_, err = f.stores[1].IssueChallenge(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, "test-ip", code.Binding{})
		requireLimit(t, err, "IP_LIMIT")
	})
	t.Run("maximum cooldown rounded up and rejection unchanged", func(t *testing.T) {
		f := newRotation(t, false)
		for active, ttl := range []time.Duration{1100 * time.Millisecond, 2500 * time.Millisecond} {
			k := f.key(active, "cooldown", enum.IdentityPhone, enum.PurposeSignIn)
			f.mr.Set(k, "1")
			f.mr.SetTTL(k, ttl)
		}
		old := f.priorChallenge(t, 0, 0, time.Minute)
		for active := range 2 {
			_, err := f.stores[active].IssueChallenge(ctx, enum.IdentityPhone, enum.PurposeBind, target, "test-ip", code.Binding{})
			if requireLimit(t, err, "COOLDOWN").RetryAfter != 3*time.Second {
				t.Fatal("retry does not cover longest cooldown")
			}
			if f.mr.Exists(f.key(active, "quota", enum.IdentityPhone, enum.PurposeSignIn)) {
				t.Fatal("rejected send spent target quota")
			}
		}
		if f.mr.Exists("rot:quota:PHONE:ip:test-ip:20261004") {
			t.Fatal("rejected send spent ip quota")
		}
		if err := f.stores[1].VerifyChallenge(ctx, credential(old, enum.IdentityPhone, enum.PurposeSignIn, target)); err != nil {
			t.Fatal("cooldown rejection changed original code")
		}
	})
	t.Run("partial quotas summed and preserved", func(t *testing.T) {
		f := newRotation(t, false)
		for active := range 2 {
			k := f.key(active, "quota", enum.IdentityPhone, enum.PurposeSignIn)
			f.mr.Set(k, "1")
			f.mr.SetTTL(k, time.Hour)
		}
		if _, err := f.stores[1].IssueChallenge(ctx, enum.IdentityPhone, enum.PurposeBind, target, "test-ip", code.Binding{}); err != nil {
			t.Fatal("partial quota rejected early")
		}
		v0, _ := f.mr.Get(f.key(0, "quota", enum.IdentityPhone, enum.PurposeSignIn))
		v1, _ := f.mr.Get(f.key(1, "quota", enum.IdentityPhone, enum.PurposeSignIn))
		ip, _ := f.mr.Get("rot:quota:PHONE:ip:test-ip:20261004")
		if v0 != "1" || v1 != "2" || ip != "1" {
			t.Fatal("success did not add exactly one target/ip count")
		}
		for active := range 2 {
			if f.mr.TTL(f.key(active, "quota", enum.IdentityPhone, enum.PurposeSignIn)) != time.Hour {
				t.Fatal("old quota ttl rewritten")
			}
		}
		f.mr.FastForward(2 * time.Second)
		_, err := f.stores[0].IssueChallenge(ctx, enum.IdentityPhone, enum.PurposeReauth, target, "test-ip", code.Binding{})
		requireLimit(t, err, "TARGET_LIMIT")
	})
	t.Run("ip refusal spends no target quota", func(t *testing.T) {
		f := newRotation(t, false)
		f.mr.Set("rot:quota:PHONE:ip:test-ip:20261004", "100")
		_, err := f.stores[1].IssueChallenge(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, "test-ip", code.Binding{})
		requireLimit(t, err, "IP_LIMIT")
		for active := range 2 {
			if f.mr.Exists(f.key(active, "quota", enum.IdentityPhone, enum.PurposeSignIn)) || f.mr.Exists(f.key(active, "cooldown", enum.IdentityPhone, enum.PurposeSignIn)) || f.mr.Exists(f.key(active, "challenge", enum.IdentityPhone, enum.PurposeSignIn)) {
				t.Fatal("ip rejection mutated target state")
			}
		}
	})
	t.Run("utc day", func(t *testing.T) {
		f := newRotation(t, false)
		f.now = time.Date(2026, 10, 5, 7, 59, 58, 0, time.FixedZone("local", 8*3600))
		f.mr.Set(f.key(0, "quota", enum.IdentityPhone, enum.PurposeSignIn), "3")
		_, err := f.stores[1].IssueChallenge(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, "test-ip", code.Binding{})
		if requireLimit(t, err, "TARGET_LIMIT").RetryAfter != 2*time.Second {
			t.Fatal("day retry includes gc grace or local midnight")
		}
		f.now = f.now.Add(2 * time.Second)
		if _, err := f.stores[1].IssueChallenge(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, "test-ip", code.Binding{}); err != nil {
			t.Fatal("new utc day quota unavailable")
		}
		if f.mr.TTL(f.key(1, "quota", enum.IdentityPhone, enum.PurposeSignIn)) != 25*time.Hour {
			t.Fatal("new day quota gc grace changed")
		}
	})
	t.Run("isolation and other purpose retained", func(t *testing.T) {
		f := newRotation(t, false)
		old := f.priorChallenge(t, 0, 0, time.Minute)
		other := code.NewStore(f.rdb, "other:", f.digesters[1], f.o)
		for _, err := range []error{
			f.stores[1].VerifyChallenge(ctx, credential(old, enum.IdentityEmail, enum.PurposeSignIn, target)),
			f.stores[1].VerifyChallenge(ctx, credential(old, enum.IdentityPhone, enum.PurposeBind, target)),
			other.VerifyChallenge(ctx, credential(old, enum.IdentityPhone, enum.PurposeSignIn, target)),
		} {
			if !errors.Is(err, code.ErrExpired) {
				t.Fatal("isolation failed")
			}
		}
		if _, err := f.stores[1].IssueChallenge(ctx, enum.IdentityPhone, enum.PurposeBind, target, "test-ip", code.Binding{}); err != nil {
			t.Fatal("other purpose issue failed")
		}
		if _, err := f.stores[1].IssueChallenge(ctx, enum.IdentityEmail, enum.PurposeSignIn, target, "test-ip", code.Binding{}); err != nil {
			t.Fatal("channel cooldown leaked")
		}
		if _, err := other.IssueChallenge(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, "test-ip", code.Binding{}); err != nil {
			t.Fatal("prefix cooldown leaked")
		}
		if f.mr.HGet(f.key(0, "challenge", enum.IdentityPhone, enum.PurposeSignIn), "n") != "0" {
			t.Fatal("cross boundary used original attempts")
		}
		if err := f.stores[1].VerifyChallenge(ctx, credential(old, enum.IdentityPhone, enum.PurposeSignIn, target)); err != nil {
			t.Fatal("other purpose replaced original code")
		}
	})
}
