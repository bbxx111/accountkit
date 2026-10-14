package user_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/db"
)

func setIdentityPolicy(t *testing.T, f *fixture, enabled, required []enum.IdentityKind) {
	t.Helper()
	d := f.deps
	d.EnabledIdentityKinds, d.RequiredIdentityKinds = enabled, required
	svc, err := user.NewService(d)
	if err != nil {
		t.Fatal(err)
	}
	f.svc = svc
}

func bindPolicyIdentity(t *testing.T, f *fixture, p user.Principal, kind enum.IdentityKind, target string) user.IdentityInfo {
	t.Helper()
	if err := f.sendBindCode(context.Background(), p, kind, target, meta1); err != nil {
		t.Fatal(err)
	}
	info, _, err := f.svc.BindWithCode(context.Background(), p, f.credential(enum.PurposeBind, kind, target, f.sent.code(target)), meta1)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

// Removing the policy gate must allow use of a previously issued disabled code and consume it.
func TestIdentityPolicyDisabledCodeIsNotConsumed(t *testing.T) {
	for _, operation := range []string{"bind", "replace", "reauth"} {
		t.Run(operation, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			login := f.signIn(t, enum.IdentityPhone, phone1, dev1)
			p := principalOf(t, f, login)
			list, _ := f.svc.ListIdentities(ctx, p.UserID)
			target, purpose := phone2, enum.PurposeBind
			if operation == "reauth" {
				target, purpose = phone1, enum.PurposeReauth
				if err := f.sendReauthenticationCode(ctx, p, enum.IdentityPhone, target, meta1); err != nil {
					t.Fatal(err)
				}
			} else if err := f.sendBindCode(ctx, p, enum.IdentityPhone, target, meta1); err != nil {
				t.Fatal(err)
			}
			plain := f.sent.code(target)
			credential := f.credential(purpose, enum.IdentityPhone, target, plain)
			storeCredential := f.storeCredential(purpose, enum.IdentityPhone, target, plain, p)
			setIdentityPolicy(t, f, []enum.IdentityKind{enum.IdentityEmail}, []enum.IdentityKind{enum.IdentityEmail})
			var err error
			switch operation {
			case "bind":
				_, _, err = f.svc.BindWithCode(ctx, p, credential, meta1)
			case "replace":
				_, err = f.svc.ReplaceIdentity(ctx, p, list[0].ID, credential, meta1)
			case "reauth":
				_, err = f.svc.Reauthenticate(ctx, p, credential, meta1)
			}
			if !errors.Is(err, user.ErrIdentityKindNotEnabled) {
				t.Fatalf("disabled %s: %v", operation, err)
			}
			if err := f.deps.Codes.VerifyChallenge(ctx, storeCredential); err != nil {
				t.Fatalf("policy rejection consumed proof: %v", err)
			}
			identities, err := f.svc.ListIdentities(ctx, p.UserID)
			if err != nil || len(identities) != 1 || identities[0].ID != list[0].ID {
				t.Fatalf("disabled identity was modified: %+v %v", identities, err)
			}
		})
	}
}

// Apple verification consumes its nonce; rejecting by policy must leave the nonce reusable.
func TestIdentityPolicyDisabledProviderBindingPreservesNonce(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	p := principalOf(t, f, f.signIn(t, enum.IdentityEmail, email1, dev1))
	setIdentityPolicy(t, f, []enum.IdentityKind{enum.IdentityEmail}, []enum.IdentityKind{enum.IdentityEmail})
	cred := user.IdpCredential{Kind: enum.IdentityApple, IDToken: "policy", Nonce: "policy-nonce"}
	if _, _, err := f.svc.BindWithIdp(ctx, p, cred, meta1); !errors.Is(err, user.ErrIdentityKindNotEnabled) {
		t.Fatalf("disabled Apple: %v", err)
	}
	setIdentityPolicy(t, f, []enum.IdentityKind{enum.IdentityEmail, enum.IdentityApple}, []enum.IdentityKind{enum.IdentityEmail})
	if _, created, err := f.svc.BindWithIdp(ctx, p, cred, meta1); err != nil || !created {
		t.Fatalf("nonce must remain reusable: %v %v", created, err)
	}
}

// Counting PHONE/EMAIL instead of current policy incorrectly permits removing the required EMAIL.
func TestIdentityPolicyUnbindPreservesCurrentRequirements(t *testing.T) {
	for _, scenario := range []string{"required-email", "disabled-phone", "already-incomplete", "last-provider", "remove-disabled-history"} {
		t.Run(scenario, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			login := f.signIn(t, enum.IdentityPhone, phone1, dev1)
			p := principalOf(t, f, login)
			list, _ := f.svc.ListIdentities(ctx, p.UserID)
			phoneID := list[0].ID
			wx, _, err := f.svc.BindWithIdp(ctx, p, user.IdpCredential{Kind: enum.IdentityWeChat, AppID: "wx1", Code: "union:policy@o"}, meta1)
			if err != nil {
				t.Fatal(err)
			}
			target, want := wx.ID, user.ErrLastAnchor
			switch scenario {
			case "required-email":
				em := bindPolicyIdentity(t, f, p, enum.IdentityEmail, email1)
				setIdentityPolicy(t, f, []enum.IdentityKind{enum.IdentityPhone, enum.IdentityEmail, enum.IdentityWeChat}, []enum.IdentityKind{enum.IdentityEmail})
				target = em.ID
			case "disabled-phone":
				setIdentityPolicy(t, f, []enum.IdentityKind{enum.IdentityWeChat}, []enum.IdentityKind{})
			case "already-incomplete":
				setIdentityPolicy(t, f, []enum.IdentityKind{enum.IdentityPhone, enum.IdentityEmail, enum.IdentityWeChat}, []enum.IdentityKind{enum.IdentityEmail})
			case "last-provider":
				setIdentityPolicy(t, f, []enum.IdentityKind{enum.IdentityWeChat}, []enum.IdentityKind{})
				if err := f.svc.UnbindIdentity(ctx, p, phoneID, meta1); err != nil {
					t.Fatalf("remove disabled history: %v", err)
				}
			case "remove-disabled-history":
				bindPolicyIdentity(t, f, p, enum.IdentityEmail, email1)
				setIdentityPolicy(t, f, []enum.IdentityKind{enum.IdentityEmail}, []enum.IdentityKind{enum.IdentityEmail})
				target, want = phoneID, nil
			}
			err = f.svc.UnbindIdentity(ctx, p, target, meta1)
			if !errors.Is(err, want) {
				t.Fatalf("unbind %s: got %v want %v", scenario, err, want)
			}
			if want != nil {
				list, _ := f.svc.ListIdentities(ctx, p.UserID)
				found := false
				for _, i := range list {
					if i.ID == target {
						found = true
					}
				}
				if !found {
					t.Fatal("rejected unbind changed identity")
				}
			}
		})
	}
}

// A formerly complete access token must not authorize a replacement while the account is incomplete.
func TestIdentityPolicyReplacementRejectsIncompleteAccountBeforeConsumption(t *testing.T) {
	f, p, iid, _ := replacementSetup(t, enum.IdentityPhone, phone1)
	ctx := context.Background()
	plain := replacementCode(t, f, p, enum.IdentityPhone, phone2)
	cred := f.credential(enum.PurposeBind, enum.IdentityPhone, phone2, plain)
	stored := f.storeCredential(enum.PurposeBind, enum.IdentityPhone, phone2, plain, p)
	setIdentityPolicy(t, f, []enum.IdentityKind{enum.IdentityPhone, enum.IdentityEmail}, []enum.IdentityKind{enum.IdentityEmail})
	if _, err := f.svc.ReplaceIdentity(ctx, p, iid, cred, meta1); !errors.Is(err, user.ErrLastAnchor) {
		t.Fatalf("incomplete replacement: %v", err)
	}
	if err := f.deps.Codes.VerifyChallenge(ctx, stored); err != nil {
		t.Fatalf("incomplete replacement consumed code: %v", err)
	}
}

func TestIdentityPolicyReplacementAllowsEnabledNonRequiredKind(t *testing.T) {
	f, p, iid, _ := replacementSetup(t, enum.IdentityPhone, phone1)
	ctx := context.Background()
	em := bindPolicyIdentity(t, f, p, enum.IdentityEmail, email1)
	setIdentityPolicy(t, f, []enum.IdentityKind{enum.IdentityPhone, enum.IdentityEmail, enum.IdentityWeChat}, []enum.IdentityKind{enum.IdentityEmail})
	plain := replacementCode(t, f, p, enum.IdentityPhone, phone2)
	out, err := f.svc.ReplaceIdentity(ctx, p, iid, f.credential(enum.PurposeBind, enum.IdentityPhone, phone2, plain), meta1)
	if err != nil || out.Kind != enum.IdentityPhone || out.ID == iid {
		t.Fatalf("enabled non-required PHONE replacement: %+v %v", out, err)
	}
	if _, created, err := f.svc.BindWithIdp(ctx, p, user.IdpCredential{Kind: enum.IdentityWeChat, AppID: "wx1", Code: "union:nonrequired@o"}, meta1); err != nil || !created {
		t.Fatalf("ordinary WECHAT binding must ignore Required: %v %v", created, err)
	}
	list, err := f.svc.ListIdentities(ctx, p.UserID)
	if err != nil || len(list) != 3 {
		t.Fatalf("replacement/bind final identities: %+v %v", list, err)
	}
	if err := f.svc.UnbindIdentity(ctx, p, em.ID, meta1); !errors.Is(err, user.ErrLastAnchor) {
		t.Fatalf("EMAIL still required: %v", err)
	}
}

// The policy must be evaluated after the user lock, not only before verification.
func TestIdentityPolicyReplacementRechecksCompletenessAfterLock(t *testing.T) {
	f, p, iid, _ := replacementSetup(t, enum.IdentityPhone, phone1)
	em := bindPolicyIdentity(t, f, p, enum.IdentityEmail, email1)
	setIdentityPolicy(t, f, []enum.IdentityKind{enum.IdentityPhone, enum.IdentityEmail}, []enum.IdentityKind{enum.IdentityEmail})
	plain := replacementCode(t, f, p, enum.IdentityPhone, phone2)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := db.New(tx).LockUserByID(ctx, p.UserID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := f.svc.ReplaceIdentity(ctx, p, iid, f.credential(enum.PurposeBind, enum.IdentityPhone, phone2, plain), meta1)
		done <- err
	}()
	replacementWait(t, ctx, f, tx, done)
	if _, err := db.New(tx).SoftDeleteIdentity(ctx, db.SoftDeleteIdentityParams{ID: em.ID, UserID: p.UserID, Now: *f.clock}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, user.ErrLastAnchor) {
		t.Fatalf("locked incomplete account: %v", err)
	}
	list, err := f.svc.ListIdentities(ctx, p.UserID)
	if err != nil || len(list) != 1 || list[0].ID != iid {
		t.Fatalf("rejected replacement changed identity: %+v %v", list, err)
	}
}

func TestIdentityPolicyCodeSendAndMalformedKinds(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	p := principalOf(t, f, f.signIn(t, enum.IdentityPhone, phone1, dev1))
	setIdentityPolicy(t, f, []enum.IdentityKind{enum.IdentityEmail}, []enum.IdentityKind{enum.IdentityEmail})
	for _, kind := range []enum.IdentityKind{enum.IdentityPhone, enum.IdentityKind(99), enum.IdentityWeChat} {
		want := user.ErrInvalidArgument
		if kind == enum.IdentityPhone {
			want = user.ErrIdentityKindNotEnabled
		}
		if _, err := f.svc.SendBindCode(ctx, p, kind, phone2, meta1); !errors.Is(err, want) {
			t.Fatalf("send bind kind %v: %v", kind, err)
		}
		if _, err := f.svc.SendReauthenticationCode(ctx, p, kind, phone1, meta1); !errors.Is(err, want) {
			t.Fatalf("send reauth kind %v: %v", kind, err)
		}
	}
	if _, _, err := f.svc.BindWithIdp(ctx, p, user.IdpCredential{Kind: enum.IdentityPhone}, meta1); !errors.Is(err, user.ErrInvalidArgument) {
		t.Fatalf("PHONE is not an IdP credential: %v", err)
	}
	if f.sent.code(phone2) != "" {
		t.Fatal("disabled channel sent a code")
	}
}

// A new rotation/reauth must recompute EMAIL requirements; grace retries preserve their original pair.
func TestIdentityPolicyRefreshAndReauthenticationScopes(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	login := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	p := principalOf(t, f, login)
	first, err := f.svc.Refresh(ctx, login.RefreshToken, meta1)
	if err != nil {
		t.Fatal(err)
	}
	setIdentityPolicy(t, f, []enum.IdentityKind{enum.IdentityPhone, enum.IdentityEmail}, []enum.IdentityKind{enum.IdentityEmail})
	grace, err := f.svc.Refresh(ctx, login.RefreshToken, meta1)
	if err != nil || grace.Scope != user.ScopeUser || grace.AccessToken != first.AccessToken || grace.RefreshToken != first.RefreshToken {
		t.Fatalf("grace changed pair: %+v %v", grace, err)
	}
	rotated, err := f.svc.Refresh(ctx, first.RefreshToken, meta1)
	if err != nil || rotated.Scope != user.ScopeBind {
		t.Fatalf("new rotation must require EMAIL: %+v %v", rotated, err)
	}
	before, err := f.repo.Q().GetSessionByRefreshHash(ctx, sha256Of(rotated.RefreshToken))
	if err != nil {
		t.Fatal(err)
	}
	f.advance(time.Minute)
	if err := f.sendReauthenticationCode(ctx, p, enum.IdentityPhone, phone1, meta1); err != nil {
		t.Fatal(err)
	}
	res, err := f.svc.Reauthenticate(ctx, p, f.credential(enum.PurposeReauth, enum.IdentityPhone, phone1, f.sent.code(phone1)), meta1)
	if err != nil || res.Scope != user.ScopeBind || res.RefreshToken != "" || res.RefreshExpiresIn != 0 {
		t.Fatalf("reauth must require EMAIL and preserve refresh: %+v %v", res, err)
	}
	after, err := f.repo.Q().GetSessionByRefreshHash(ctx, sha256Of(rotated.RefreshToken))
	if err != nil || !after.RefreshExpireTime.Equal(before.RefreshExpireTime) || !after.AuthTime.After(before.AuthTime) {
		t.Fatalf("reauth changed expiry or failed freshness: %+v %v", after, err)
	}
	bindPolicyIdentity(t, f, p, enum.IdentityEmail, email1)
	completed, err := f.svc.Refresh(ctx, rotated.RefreshToken, meta1)
	if err != nil || completed.Scope != user.ScopeUser {
		t.Fatalf("bound EMAIL must complete next refresh: %+v %v", completed, err)
	}
}
