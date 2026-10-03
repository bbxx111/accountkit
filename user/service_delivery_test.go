package user

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/pii"
	"github.com/bbxx111/accountkit/user/code"
	"github.com/bbxx111/accountkit/user/sender"
	"github.com/redis/go-redis/v9"
)

type unavailableChannel struct{}

func (unavailableChannel) Enabled() bool { return false }
func (unavailableChannel) SendSMS(context.Context, string, sender.Message) error {
	return errors.New("disabled sender must never be called")
}
func (unavailableChannel) SendEmail(context.Context, string, sender.Message) error {
	return errors.New("disabled sender must never be called")
}

type legacyDelivery struct {
	err      error
	messages []sender.Message
}

func (d *legacyDelivery) SendSMS(_ context.Context, _ string, m sender.Message) error {
	d.messages = append(d.messages, m)
	return d.err
}
func (d *legacyDelivery) SendEmail(ctx context.Context, to string, m sender.Message) error {
	return d.SendSMS(ctx, to, m)
}

func deliveryService(t *testing.T, sms sender.SMSSender, email sender.EmailSender) (*Service, *miniredis.Miniredis, *audit.Memory) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	dig, _ := pii.NewDigester(map[uint16][]byte{1: bytes.Repeat([]byte{1}, 32)}, 1)
	mem := &audit.Memory{}
	return &Service{d: Deps{Now: time.Now, SMS: sms, Email: email, Digester: dig, Audit: mem, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), DefaultRegion: "CN", CodeTTL: 5 * time.Minute, Codes: code.NewStore(rdb, "delivery:", dig, code.Options{TTL: 5 * time.Minute, Cooldown: time.Minute, MaxAttempts: 5, DailyLimitPerTarget: 2, DailyLimitPerIP: 3})}}, mr, mem
}

func TestDisabledChannel(t *testing.T) {
	for _, ch := range []enum.IdentityKind{enum.IdentityPhone, enum.IdentityEmail} {
		for _, purpose := range []enum.CodePurpose{enum.PurposeSignIn, enum.PurposeBind, enum.PurposeReauth} {
			t.Run(ch.String()+purpose.String(), func(t *testing.T) {
				s, mr, mem := deliveryService(t, unavailableChannel{}, unavailableChannel{})
				target := "person@example.org"
				if ch == enum.IdentityPhone {
					target = "+8613812341234"
				}
				if err := s.sendCode(context.Background(), ch, purpose, target, "", Meta{IP: "203.0.113.4"}); !errors.Is(err, sender.ErrDisabled) {
					t.Fatal("disabled channel must reject")
				}
				if keys := mr.Keys(); len(keys) != 0 {
					t.Fatalf("disabled channel consumed code/cooldown/quota: %d keys", len(keys))
				}
				events := mem.Events()
				if len(events) != 1 || events[0].Type != enum.EventCodeSendRejected || events[0].Reason != "CHANNEL_NOT_ENABLED" {
					t.Fatal("missing safe channel rejection audit")
				}
			})
		}
	}
}

func TestLegacyDeliveryPurposesAndFailureQuota(t *testing.T) {
	for _, purpose := range []enum.CodePurpose{enum.PurposeSignIn, enum.PurposeBind, enum.PurposeReauth} {
		d := &legacyDelivery{}
		s, _, _ := deliveryService(t, d, d)
		if err := s.sendCode(context.Background(), enum.IdentityEmail, purpose, "person@example.org", "", Meta{IP: "203.0.113.4"}); err != nil {
			t.Fatal(err)
		}
		if len(d.messages) != 1 || d.messages[0].Purpose != purpose || d.messages[0].TTL != 5*time.Minute || len(d.messages[0].Code) != 6 {
			t.Fatal("legacy delivery lost code message")
		}
	}
	oldErr := errors.New("old sender error")
	d := &legacyDelivery{err: oldErr}
	s, _, mem := deliveryService(t, d, d)
	if err := s.SendSignInCode(context.Background(), enum.IdentityEmail, "person@example.org", Meta{IP: "203.0.113.4"}); !errors.Is(err, oldErr) || errors.Is(err, ErrUnavailable) {
		t.Fatal("ordinary sender error contract changed")
	}
	var rl *code.RateLimitedError
	if err := s.SendSignInCode(context.Background(), enum.IdentityEmail, "person@example.org", Meta{IP: "203.0.113.4"}); !errors.As(err, &rl) || rl.Dimension != "COOLDOWN" {
		t.Fatal("failed delivery must retain cooldown")
	}
	if ev := mem.Events(); len(ev) != 2 || ev[0].Type != enum.EventCodeSendFailed {
		t.Fatal("failed delivery audit missing")
	}
}

func TestUnavailableDeliveryIsSafeAndRetainsQuota(t *testing.T) {
	d := &legacyDelivery{err: fmt.Errorf("%w: person@example.org password body 123456", sender.ErrUnavailable)}
	s, mr, mem := deliveryService(t, d, d)
	err := s.SendSignInCode(context.Background(), enum.IdentityEmail, "person@example.org", Meta{IP: "203.0.113.4"})
	if !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), "person@example.org") || strings.Contains(err.Error(), "123456") {
		t.Fatal("unavailable delivery must map to safe dependency error")
	}
	var rl *code.RateLimitedError
	if err := s.SendSignInCode(context.Background(), enum.IdentityEmail, "person@example.org", Meta{IP: "203.0.113.4"}); !errors.As(err, &rl) {
		t.Fatal("unavailable delivery refunded cooldown")
	}
	if events := mem.Events(); len(events) != 2 || events[0].Reason != "SEND_FAILED" {
		t.Fatal("delivery failure audit missing")
	}
	// 真实 Redis 额度逻辑：失败提交和立即拒绝的冷却请求只有前者消耗额度。
	for _, key := range mr.Keys() {
		if strings.Contains(key, ":quota:") {
			if value, err := mr.Get(key); err != nil || value != "1" {
				t.Fatal("failed submission quota was refunded or cooldown consumed extra quota")
			}
		}
	}
	mr.FastForward(61 * time.Second)
	if err := s.SendSignInCode(context.Background(), enum.IdentityEmail, "person@example.org", Meta{IP: "203.0.113.4"}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("second quota slot must attempt delivery")
	}
	mr.FastForward(61 * time.Second)
	if err := s.SendSignInCode(context.Background(), enum.IdentityEmail, "person@example.org", Meta{IP: "203.0.113.4"}); !errors.As(err, &rl) || rl.Dimension != "TARGET_LIMIT" {
		t.Fatal("failed submissions did not exhaust target quota")
	}
	if err := s.SendSignInCode(context.Background(), enum.IdentityEmail, "second@example.org", Meta{IP: "203.0.113.4"}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("third IP quota slot must attempt delivery")
	}
	if err := s.SendSignInCode(context.Background(), enum.IdentityEmail, "third@example.org", Meta{IP: "203.0.113.4"}); !errors.As(err, &rl) || rl.Dimension != "IP_LIMIT" {
		t.Fatal("failed submissions did not exhaust IP quota")
	}
}
