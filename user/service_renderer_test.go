package user

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/ids"
	"github.com/bbxx111/accountkit/user/code"
	"github.com/bbxx111/accountkit/user/sender"
	delivery "github.com/bbxx111/accountkit/user/sender/smtp"
)

type failureRenderFunc func(context.Context, sender.CodeMessage) (delivery.EmailContent, error)

func (f failureRenderFunc) Render(ctx context.Context, m sender.CodeMessage) (delivery.EmailContent, error) {
	return f(ctx, m)
}

func rendererSender(t *testing.T, r failureRenderFunc) *delivery.Sender {
	t.Helper()
	// Any unexpected network attempt fails; SMTP package independently asserts zero connections.
	s, err := delivery.NewWithRenderer(delivery.Config{Host: "127.0.0.1", Port: 1, From: "sender@example.org", Timeout: time.Second}, r)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func rendererBinding(t *testing.T, purpose enum.CodePurpose) code.Binding {
	t.Helper()
	switch purpose {
	case enum.PurposeBind:
		id, err := ids.New(ids.User)
		if err != nil {
			t.Fatal(err)
		}
		return code.Binding{UserID: id}
	case enum.PurposeReauth:
		uid, err := ids.New(ids.User)
		if err != nil {
			t.Fatal(err)
		}
		sid, err := ids.New(ids.Session)
		if err != nil {
			t.Fatal(err)
		}
		return code.Binding{UserID: uid, SessionID: sid}
	case enum.PurposeSignInComplete:
		id, err := ids.New(ids.IdentityFlow)
		if err != nil {
			t.Fatal(err)
		}
		return code.Binding{FlowID: id}
	}
	return code.Binding{}
}

func TestSMTPRendererFailurePreservesDeliveryContract(t *testing.T) {
	for _, purpose := range []enum.CodePurpose{enum.PurposeSignIn, enum.PurposeBind, enum.PurposeReauth, enum.PurposeSignInComplete} {
		t.Run(purpose.String(), func(t *testing.T) {
			binding := rendererBinding(t, purpose)
			var calls int
			var private error
			var plain string
			var logs bytes.Buffer
			transport := rendererSender(t, func(ctx context.Context, m sender.CodeMessage) (delivery.EmailContent, error) {
				calls++
				if m.Purpose != purpose || m.Code == "" || m.TTL <= 0 {
					t.Error("message lost purpose, code or TTL")
				}
				if purpose == enum.PurposeSignInComplete && m.TTL > 3*time.Minute {
					t.Error("completion exceeded flow deadline")
				}
				plain = m.Code
				private = fmt.Errorf("private-template person@example.org %s body language-context", m.Code)
				return delivery.EmailContent{}, private
			})
			s, mr, mem := deliveryService(t, &legacyDelivery{}, transport)
			s.d.Logger = slog.New(slog.NewTextHandler(&logs, nil))
			deadline := time.Time{}
			if purpose == enum.PurposeSignInComplete {
				deadline = time.Now().Add(3 * time.Minute)
			}
			send := func(target string) (CodeChallenge, error) {
				return s.sendCodeUntil(context.Background(), enum.IdentityEmail, purpose, target, binding, Meta{IP: "203.0.113.4", RequestID: "render-failure"}, deadline)
			}
			c, err := send("person@example.org")
			if !errors.Is(err, ErrUnavailable) || errors.Is(err, private) || c.CodeID != "" || !c.ExpireTime.IsZero() {
				t.Fatal("render failure returned a usable round or original error")
			}
			s.d.Logger.Error("delivery unavailable", "err", err)
			quotaCount := 0
			for _, key := range mr.Keys() {
				if strings.Contains(key, ":challenge:") {
					t.Fatal("failed render left a usable round")
				}
				if strings.Contains(key, ":quota:") {
					quotaCount++
					v, _ := mr.Get(key)
					if v != "1" {
						t.Fatal("failed render refunded quota")
					}
				}
			}
			if quotaCount != 2 {
				t.Fatal("target and IP quota missing")
			}
			events := mem.Events()
			if len(events) != 1 || events[0].Type != enum.EventCodeSendFailed || events[0].Reason != "SEND_FAILED" || events[0].UserID != binding.UserID || events[0].FlowID != binding.FlowID || events[0].SubjectHint == "" {
				t.Fatal("missing bound, redacted failure audit")
			}
			for _, secret := range []string{"person@example.org", plain, "private-template", "body", "language-context"} {
				if strings.Contains(logs.String(), secret) || strings.Contains(fmt.Sprint(events), secret) {
					t.Fatal("render failure leaked sensitive content")
				}
			}
			var rl *code.RateLimitedError
			if _, err = send("person@example.org"); !errors.As(err, &rl) || rl.Dimension != "COOLDOWN" {
				t.Fatal("render failure refunded cooldown", err)
			}
			if calls != 1 {
				t.Fatal("cooldown caused another render")
			}
			mr.FastForward(61 * time.Second)
			if _, err = send("person@example.org"); !errors.Is(err, ErrUnavailable) {
				t.Fatal("second quota slot did not render", err)
			}
			mr.FastForward(61 * time.Second)
			if _, err = send("person@example.org"); !errors.As(err, &rl) || rl.Dimension != "TARGET_LIMIT" {
				t.Fatal("target quota refunded", err)
			}
			if _, err = send("second@example.org"); !errors.Is(err, ErrUnavailable) {
				t.Fatal("third IP quota slot did not render", err)
			}
			if _, err = send("third@example.org"); !errors.As(err, &rl) || rl.Dimension != "IP_LIMIT" {
				t.Fatal("IP quota refunded", err)
			}
			if calls != 3 {
				t.Fatal("render retried or quota rejection rendered")
			}
		})
	}
}

func TestSMTPRendererFailureCannotDiscardNewerRound(t *testing.T) {
	for _, purpose := range []enum.CodePurpose{enum.PurposeSignIn, enum.PurposeBind, enum.PurposeReauth, enum.PurposeSignInComplete} {
		t.Run(purpose.String(), func(t *testing.T) {
			binding := rendererBinding(t, purpose)
			entered := make(chan sender.CodeMessage, 1)
			release := make(chan struct{})
			transport := rendererSender(t, func(_ context.Context, m sender.CodeMessage) (delivery.EmailContent, error) {
				entered <- m
				<-release
				return delivery.EmailContent{}, errors.New("private render failure")
			})
			s, mr, _ := deliveryService(t, &legacyDelivery{}, transport)
			deadline := time.Time{}
			if purpose == enum.PurposeSignInComplete {
				deadline = time.Now().Add(3 * time.Minute)
			}
			type outcome struct {
				challenge CodeChallenge
				err       error
			}
			done := make(chan outcome, 1)
			go func() {
				c, e := s.sendCodeUntil(context.Background(), enum.IdentityEmail, purpose, "person@example.org", binding, Meta{IP: "203.0.113.4"}, deadline)
				done <- outcome{c, e}
			}()
			var old sender.CodeMessage
			select {
			case old = <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("renderer not entered")
			}
			var oldID string
			for _, key := range mr.Keys() {
				if strings.Contains(key, ":challenge:") {
					oldID = mr.HGet(key, "code_id")
				}
			}
			mr.FastForward(61 * time.Second)
			newer, err := s.d.Codes.IssueChallengeUntil(context.Background(), enum.IdentityEmail, purpose, "person@example.org", "203.0.113.4", binding, deadline)
			close(release)
			if err != nil {
				t.Fatal(err)
			}
			result := <-done
			if !errors.Is(result.err, ErrUnavailable) || result.challenge.CodeID != "" || oldID == "" || newer.CodeID == oldID {
				t.Fatal("bad failed or replacement round")
			}
			if err = s.d.Codes.VerifyChallenge(context.Background(), code.Credential{Channel: enum.IdentityEmail, Purpose: purpose, Target: "person@example.org", CodeID: oldID, Code: old.Code, Binding: binding}); !errors.Is(err, code.ErrExpired) {
				t.Fatal("failed old round survived", err)
			}
			if err = s.d.Codes.VerifyChallenge(context.Background(), code.Credential{Channel: enum.IdentityEmail, Purpose: purpose, Target: "person@example.org", CodeID: newer.CodeID, Code: newer.Code, Binding: binding}); err != nil {
				t.Fatal("failed render discarded newer round", err)
			}
		})
	}
}
