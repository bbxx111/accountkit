package accountkit_test

import (
	"context"
	"testing"
	"time"

	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/sender"
)

type legacySMSCodeSender struct {
	target  string
	message sender.Message
}

func (s *legacySMSCodeSender) SendSMS(_ context.Context, target string, message sender.Message) error {
	s.target, s.message = target, message
	return nil
}

type legacyEmailCodeSender struct {
	target  string
	message sender.Message
}

func (s *legacyEmailCodeSender) SendEmail(_ context.Context, target string, message sender.Message) error {
	s.target, s.message = target, message
	return nil
}

var (
	_ sender.SMSSender       = (*legacySMSCodeSender)(nil)
	_ sender.SMSCodeSender   = (*legacySMSCodeSender)(nil)
	_ sender.EmailSender     = (*legacyEmailCodeSender)(nil)
	_ sender.EmailCodeSender = (*legacyEmailCodeSender)(nil)
)

func TestCodeSenderCompatibility(t *testing.T) {
	legacy := sender.Message{Purpose: enum.PurposeSignIn, Code: "123456", TTL: 5 * time.Minute}
	var current sender.CodeMessage = legacy
	var roundTrip sender.Message = current
	if roundTrip.Purpose != enum.PurposeSignIn || roundTrip.Code != "123456" || roundTrip.TTL != 5*time.Minute {
		t.Fatal("message aliases must preserve purpose, code and TTL")
	}
	sms := &legacySMSCodeSender{}
	email := &legacyEmailCodeSender{}
	if _, ok := any(sms).(sender.EmailCodeSender); ok {
		t.Fatal("SMS-only implementation must not satisfy EmailCodeSender")
	}
	deps := accountkit.Deps{SMSSender: sms, EmailSender: email}
	domainDeps := user.Deps{SMS: deps.SMSSender, Email: deps.EmailSender}
	for _, send := range []sender.SMSCodeSender{deps.SMSSender, domainDeps.SMS} {
		if err := send.SendSMS(context.Background(), "+8613812341234", current); err != nil {
			t.Fatal(err)
		}
		if sms.target != "+8613812341234" || sms.message != legacy {
			t.Fatal("SMS sender received a different target or message")
		}
	}
	for _, send := range []sender.EmailSender{deps.EmailSender, domainDeps.Email} {
		if err := send.SendEmail(context.Background(), "person@example.org", roundTrip); err != nil {
			t.Fatal(err)
		}
		if email.target != "person@example.org" || email.message != legacy {
			t.Fatal("email sender received a different target or message")
		}
	}
}
