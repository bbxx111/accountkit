package sender_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user/sender"
)

func TestLogSenderWritesMaskedTargetAndCode(t *testing.T) {
	var buf bytes.Buffer
	l := sender.NewLog(slog.New(slog.NewTextHandler(&buf, nil)))
	msg := sender.Message{Purpose: enum.PurposeSignIn, Code: "123456", TTL: 5 * time.Minute}
	if err := l.SendSMS(context.Background(), "+8613812341234", msg); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "123456") || !strings.Contains(out, "SIGN_IN") {
		t.Fatalf("log must contain code and purpose (dev sender): %s", out)
	}
	if strings.Contains(out, "+8613812341234") || !strings.Contains(out, "+86****1234") {
		t.Fatalf("target must be masked: %s", out)
	}
	buf.Reset()
	if err := l.SendEmail(context.Background(), "bo.bai@shifang.co", msg); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "bo.bai@shifang.co") || !strings.Contains(buf.String(), "bo.***g.co") {
		t.Fatalf("email must be masked: %s", buf.String())
	}
}

var _ sender.SMSSender = (*sender.Log)(nil)
var _ sender.EmailSender = (*sender.Log)(nil)
