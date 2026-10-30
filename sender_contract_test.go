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

type recordingSMSSender struct {
	target  string
	message sender.CodeMessage
}

func (s *recordingSMSSender) SendSMS(_ context.Context, target string, message sender.CodeMessage) error {
	s.target, s.message = target, message
	return nil
}

type recordingEmailSender struct {
	target  string
	message sender.CodeMessage
}

func (s *recordingEmailSender) SendEmail(_ context.Context, target string, message sender.CodeMessage) error {
	s.target, s.message = target, message
	return nil
}

var (
	_ sender.SMSCodeSender   = (*recordingSMSSender)(nil)
	_ sender.EmailCodeSender = (*recordingEmailSender)(nil)
)

// 单渠道实现只满足对应接口；accountkit.Deps 与 user.Deps 按渠道传递同一目标和消息。
func TestCodeSenderContract(t *testing.T) {
	want := sender.CodeMessage{Purpose: enum.PurposeSignIn, Code: "123456", TTL: 5 * time.Minute}
	sms := &recordingSMSSender{}
	email := &recordingEmailSender{}
	if _, ok := any(sms).(sender.EmailCodeSender); ok {
		t.Fatal("SMS-only implementation must not satisfy EmailCodeSender")
	}
	deps := accountkit.Deps{SMSSender: sms, EmailSender: email}
	domainDeps := user.Deps{SMS: deps.SMSSender, Email: deps.EmailSender}
	for _, send := range []sender.SMSCodeSender{deps.SMSSender, domainDeps.SMS} {
		*sms = recordingSMSSender{}
		if err := send.SendSMS(context.Background(), "+8613812341234", want); err != nil {
			t.Fatal(err)
		}
		if sms.target != "+8613812341234" || sms.message != want {
			t.Fatal("SMS sender received a different target or message")
		}
	}
	for _, send := range []sender.EmailCodeSender{deps.EmailSender, domainDeps.Email} {
		*email = recordingEmailSender{}
		if err := send.SendEmail(context.Background(), "person@example.org", want); err != nil {
			t.Fatal(err)
		}
		if email.target != "person@example.org" || email.message != want {
			t.Fatal("email sender received a different target or message")
		}
	}
}
