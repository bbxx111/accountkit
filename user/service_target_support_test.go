package user_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/idp"
	"github.com/bbxx111/accountkit/user/sender"
)

// regionalSMS 只支持中国大陆号码，模拟声明号码支持能力的发送器。
type regionalSMS struct {
	inner *capture
	calls atomic.Int32
}

func (r *regionalSMS) SendSMS(ctx context.Context, to string, m sender.CodeMessage) error {
	r.calls.Add(1)
	return r.inner.SendSMS(ctx, to, m)
}

func (r *regionalSMS) SupportsTarget(e164 string) bool { return strings.HasPrefix(e164, "+86") }

const foreignPhone = "+447911123456"
const foreignPhone2 = "+447911123457"

func TestUnsupportedTargetRejectedBeforeIssuing(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	// 先用不声明能力的发送器建立一个以境外号码为锚点的账号，供绑定与重新认证使用。
	token := f.signIn(t, enum.IdentityPhone, foreignPhone, dev1)
	p, err := f.svc.Authenticate(ctx, token.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	sms := &regionalSMS{inner: f.sent}
	deps := f.deps
	deps.SMS = sms
	regional, err := user.NewService(deps)
	if err != nil {
		t.Fatal(err)
	}
	before := f.mr.Keys()
	auditBefore := len(f.audit.Events())
	for name, call := range map[string]func() error{
		"sign in": func() error {
			_, err := regional.SendSignInCode(ctx, enum.IdentityPhone, foreignPhone, meta1)
			return err
		},
		"bind": func() error {
			_, err := regional.SendBindCode(ctx, p, enum.IdentityPhone, foreignPhone2, meta1)
			return err
		},
		"reauth": func() error {
			_, err := regional.SendReauthenticationCode(ctx, p, enum.IdentityPhone, foreignPhone, meta1)
			return err
		},
	} {
		if err := call(); !errors.Is(err, sender.ErrTargetNotSupported) {
			t.Fatalf("%s: err = %v, want ErrTargetNotSupported", name, err)
		}
	}
	if !reflect.DeepEqual(before, f.mr.Keys()) {
		t.Fatal("unsupported target changed challenge, cooldown or quota state")
	}
	if sms.calls.Load() != 0 {
		t.Fatal("unsupported target reached the sender")
	}
	events := f.audit.Events()[auditBefore:]
	if len(events) != 3 {
		t.Fatalf("each rejected request must audit once, got %d", len(events))
	}
	for _, e := range events {
		if e.Type != enum.EventCodeSendRejected || e.Reason != "TARGET_NOT_SUPPORTED" || len(e.SubjectHint) != 8 {
			t.Fatalf("unsafe or missing audit: %+v", e)
		}
	}
	// 支持的号码照常发送。
	if _, err := regional.SendSignInCode(ctx, enum.IdentityPhone, phone2, meta1); err != nil || sms.calls.Load() != 1 {
		t.Fatalf("supported target: %v calls=%d", err, sms.calls.Load())
	}
	// 发送器能力只影响短信渠道。
	if _, err := regional.SendSignInCode(ctx, enum.IdentityEmail, email1, meta1); err != nil {
		t.Fatalf("email must not consult SMS capability: %v", err)
	}
}

func TestUnsupportedTargetRejectedForSignInCompletion(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	sms := &regionalSMS{inner: f.sent}
	google := adaptedProvider{kind: enum.IdentityGoogle, fp: "client-a", verify: func(context.Context, map[string]string) (idp.Identity, error) {
		return idp.Identity{Kind: enum.IdentityGoogle, Subject: "g-target-support"}, nil
	}}
	newServiceWith(t, f, func(d *user.Deps) { *d = withProvider(*d, google); d.SMS = sms })
	r, err := f.svc.SignInWithIdp(ctx, googleCred("t", "n"), dev1, meta1)
	if err != nil || r.Pending == nil {
		t.Fatalf("pending: %+v %v", r, err)
	}
	before := f.mr.Keys()
	if _, err := f.svc.SendSignInCompletionCode(ctx, r.Pending.SignInCompletionToken, enum.IdentityPhone, foreignPhone, dev1, meta1); !errors.Is(err, sender.ErrTargetNotSupported) {
		t.Fatalf("completion: %v", err)
	}
	if !reflect.DeepEqual(before, f.mr.Keys()) || sms.calls.Load() != 0 {
		t.Fatal("completion with unsupported target changed state or reached the sender")
	}
}
