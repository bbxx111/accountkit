package sender_test

import (
	"context"
	"errors"
	"github.com/bbxx111/accountkit/user/sender"
	"testing"
)

func TestDisabledNeverReportsDelivery(t *testing.T) {
	d := sender.Disabled{}
	if d.Enabled() {
		t.Fatal("disabled sender reports enabled")
	}
	var sms sender.SMSCodeSender = d
	var email sender.EmailCodeSender = d
	if !errors.Is(sms.SendSMS(context.Background(), "+8613812341234", sender.CodeMessage{}), sender.ErrDisabled) {
		t.Fatal("disabled SMS reported success")
	}
	if !errors.Is(email.SendEmail(context.Background(), "person@example.org", sender.CodeMessage{}), sender.ErrDisabled) {
		t.Fatal("disabled email reported success")
	}
}
