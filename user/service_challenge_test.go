package user

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user/code"
	"github.com/bbxx111/accountkit/user/sender"
	"log/slog"
	"strings"
	"testing"
	"time"
)

type challengeDelivery struct {
	send func(context.Context, string, sender.Message) error
}

func (d challengeDelivery) SendSMS(ctx context.Context, target string, m sender.Message) error {
	return d.send(ctx, target, m)
}
func (d challengeDelivery) SendEmail(ctx context.Context, target string, m sender.Message) error {
	return d.send(ctx, target, m)
}

func TestCodeChallengeDeliveryCleanup(t *testing.T) {
	t.Run("failed delivery invalidates only its challenge and retains quota", func(t *testing.T) {
		d := &legacyDelivery{err: sender.ErrUnavailable}
		s, mr, mem := deliveryService(t, d, d)
		c, err := s.SendSignInCode(context.Background(), enum.IdentityEmail, "person@example.org", Meta{IP: "203.0.113.4"})
		if !errors.Is(err, ErrUnavailable) || c.CodeID != "" || !c.ExpireTime.IsZero() {
			t.Fatal(c, err)
		}
		for _, key := range mr.Keys() {
			if strings.Contains(key, ":code:") || strings.Contains(key, ":challenge:") {
				t.Fatal("failed delivery left usable challenge", key)
			}
			if strings.Contains(key, ":quota:") {
				v, _ := mr.Get(key)
				if v != "1" {
					t.Fatal("quota refunded", v)
				}
			}
		}
		_, err = s.SendSignInCode(context.Background(), enum.IdentityEmail, "person@example.org", Meta{IP: "203.0.113.4"})
		var limit *code.RateLimitedError
		if !errors.As(err, &limit) || limit.Dimension != "COOLDOWN" {
			t.Fatal(err)
		}
		if events := mem.Events(); len(events) != 2 || events[0].Type != enum.EventCodeSendFailed {
			t.Fatal(events)
		}
	})
	t.Run("late failure cannot discard newer challenge", func(t *testing.T) {
		ctx := context.Background()
		var s *Service
		var newer code.Issued
		var oldID string
		d := challengeDelivery{send: func(_ context.Context, target string, _ sender.Message) error { return nil }}
		s, mr, _ := deliveryService(t, d, d)
		s.d.Email = challengeDelivery{send: func(_ context.Context, target string, _ sender.Message) error {
			for _, key := range mr.Keys() {
				if strings.Contains(key, ":challenge:") {
					oldID = mr.HGet(key, "code_id")
				}
			}
			mr.FastForward(61 * time.Second)
			var err error
			newer, err = s.d.Codes.IssueChallenge(ctx, enum.IdentityEmail, enum.PurposeSignIn, target, "203.0.113.4", code.Binding{})
			if err != nil {
				t.Fatal(err)
			}
			return sender.ErrUnavailable
		}}
		c, err := s.SendSignInCode(ctx, enum.IdentityEmail, "person@example.org", Meta{IP: "203.0.113.4"})
		if !errors.Is(err, ErrUnavailable) || c.CodeID != "" {
			t.Fatal(c, err)
		}
		if oldID == "" || newer.CodeID == oldID {
			t.Fatal("did not create distinct rounds")
		}
		if err = s.d.Codes.VerifyChallenge(ctx, code.Credential{Channel: enum.IdentityEmail, Purpose: enum.PurposeSignIn, Target: "person@example.org", CodeID: newer.CodeID, Code: newer.Code}); err != nil {
			t.Fatal("newer challenge discarded", err)
		}
	})
	t.Run("provider failure is safe in internal logs and audit", func(t *testing.T) {
		var logs bytes.Buffer
		secretID := "0123456789abcdef0123456789abcdef"
		providerErr := fmt.Errorf("provider response person@example.org 123456 %s", secretID)
		d := &legacyDelivery{err: providerErr}
		s, _, mem := deliveryService(t, d, d)
		s.d.Logger = slog.New(slog.NewTextHandler(&logs, nil))
		c, err := s.SendSignInCode(context.Background(), enum.IdentityEmail, "person@example.org", Meta{IP: "203.0.113.4"})
		if !errors.Is(err, providerErr) || c.CodeID != "" {
			t.Fatal(c, err)
		}
		s.d.Logger.Error("internal error", "err", err)
		for _, secret := range []string{"person@example.org", "123456", secretID} {
			if strings.Contains(logs.String(), secret) || strings.Contains(fmt.Sprint(mem.Events()), secret) {
				t.Fatal("delivery failure leaked secret")
			}
		}
	})
	t.Run("redis unavailable returns dependency error", func(t *testing.T) {
		d := &legacyDelivery{}
		s, mr, _ := deliveryService(t, d, d)
		mr.Close()
		c, err := s.SendSignInCode(context.Background(), enum.IdentityEmail, "person@example.org", Meta{IP: "203.0.113.4"})
		if !errors.Is(err, ErrUnavailable) || c.CodeID != "" || len(d.messages) != 0 {
			t.Fatal(c, err)
		}
	})
}
