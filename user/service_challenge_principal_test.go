package user_test

import (
	"context"
	"errors"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/code"
	"testing"
)

func TestCodeChallengePrincipalBinding(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	one := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	p, err := f.svc.Authenticate(ctx, one.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	two := f.signIn(t, enum.IdentityPhone, phone2, user.Device{ID: "binding-second"})
	other, err := f.svc.Authenticate(ctx, two.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	challenge, err := f.svc.SendBindCode(ctx, p, enum.IdentityEmail, "Principal.Bind@Example.test ", meta1)
	if err != nil {
		t.Fatal(err)
	}
	bind := user.CodeCredential{Channel: enum.IdentityEmail, Target: "principal.bind@example.test", CodeID: challenge.CodeID, Code: f.sent.code("principal.bind@example.test")}
	if _, _, err := f.svc.BindWithCode(ctx, other, bind, meta1); !errors.Is(err, code.ErrExpired) {
		t.Fatalf("BIND crossed user: %v", err)
	}
	if _, _, err := f.svc.BindWithCode(ctx, p, bind, meta1); err != nil {
		t.Fatalf("original BIND consumed: %v", err)
	}
	sameUser := f.signIn(t, enum.IdentityPhone, phone1, user.Device{ID: "binding-same-user"})
	samePrincipal, err := f.svc.Authenticate(ctx, sameUser.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	challenge, err = f.svc.SendReauthenticationCode(ctx, p, enum.IdentityPhone, phone1, meta1)
	if err != nil {
		t.Fatal(err)
	}
	reauth := user.CodeCredential{Channel: enum.IdentityPhone, Target: phone1, CodeID: challenge.CodeID, Code: f.sent.code(phone1)}
	wrongSession := samePrincipal
	if _, err := f.svc.Reauthenticate(ctx, wrongSession, reauth, meta1); !errors.Is(err, code.ErrExpired) {
		t.Fatalf("REAUTH crossed session: %v", err)
	}
	if _, err := f.svc.Reauthenticate(ctx, other, reauth, meta1); !errors.Is(err, code.ErrExpired) {
		t.Fatalf("REAUTH crossed user: %v", err)
	}
	if _, err := f.svc.Reauthenticate(ctx, p, reauth, meta1); err != nil {
		t.Fatalf("original REAUTH consumed: %v", err)
	}
}
