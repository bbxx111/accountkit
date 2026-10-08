package user_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/sender"
)

func TestDisabledPublicCodePurposesPreserveIdentityConstraints(t *testing.T) {
	for _, ch := range []enum.IdentityKind{enum.IdentityPhone, enum.IdentityEmail} {
		t.Run(ch.String(), func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			target := email1
			if ch == enum.IdentityPhone {
				target = phone1
			}
			token := f.signIn(t, ch, target, dev1)
			p, err := f.svc.Authenticate(ctx, token.AccessToken)
			if err != nil {
				t.Fatal(err)
			}
			deps := f.deps
			deps.SMS = sender.Disabled{}
			deps.Email = sender.Disabled{}
			disabled, err := user.NewService(deps)
			if err != nil {
				t.Fatal(err)
			}
			before := f.mr.Keys()
			auditBefore := len(f.audit.Events())
			calls := []func() error{
				func() error { _, err := disabled.SendSignInCode(ctx, ch, target, meta1); return err },
				func() error { _, err := disabled.SendBindCode(ctx, p, ch, target, meta1); return err },
				func() error { _, err := disabled.SendReauthenticationCode(ctx, p, ch, target, meta1); return err },
			}
			for _, call := range calls {
				if !errors.Is(call(), sender.ErrDisabled) {
					t.Fatal("public code request did not reject disabled channel")
				}
			}
			if !reflect.DeepEqual(before, f.mr.Keys()) {
				t.Fatal("disabled public code request changed Redis keys")
			}
			events := f.audit.Events()[auditBefore:]
			if len(events) != 3 {
				t.Fatal("disabled public requests must each audit once")
			}
			for _, event := range events {
				if event.Type != enum.EventCodeSendRejected || event.Reason != "CHANNEL_NOT_ENABLED" || len(event.SubjectHint) != 8 {
					t.Fatal("public disabled request audit was unsafe or missing")
				}
			}
			if _, err := disabled.SendSignInCode(ctx, ch, "invalid target", meta1); !errors.Is(err, user.ErrInvalidTarget) {
				t.Fatal("disabled channel masked target validation")
			}
			other := phone2
			if ch == enum.IdentityEmail {
				other = "other@example.org"
			}
			if _, err := disabled.SendReauthenticationCode(ctx, p, ch, other, meta1); !errors.Is(err, user.ErrNotAnchor) {
				t.Fatal("disabled channel bypassed reauthentication anchor constraint")
			}
		})
	}
}

func TestDisabledSenderDoesNotInvalidateIssuedCode(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.sendSignInCode(ctx, enum.IdentityPhone, phone1, meta1); err != nil {
		t.Fatal(err)
	}
	issued := f.sent.code(phone1)
	deps := f.deps
	deps.SMS = sender.Disabled{}
	deps.Email = sender.Disabled{}
	disabled, err := user.NewService(deps)
	if err != nil {
		t.Fatal(err)
	}
	result, err := disabled.SignInWithCode(ctx, f.credential(enum.PurposeSignIn, enum.IdentityPhone, phone1, issued), dev1, meta1)
	if err != nil || result.AccessToken == "" || result.Scope != user.ScopeUser {
		t.Fatal("disabling future delivery invalidated existing code sign-in")
	}
}
