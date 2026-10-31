package user_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/idp"
)

// fakeFacebook 按 credential 形状分派：Limited 用 id_token 作 key，Classic 用 access_token 作 key。
type fakeFacebook struct{ identities map[string]idp.Identity }

func (f *fakeFacebook) provider() idp.Provider {
	return adaptedProvider{kind: enum.IdentityFacebook, verify: func(_ context.Context, m map[string]string) (idp.Identity, error) {
		key := m["id_token"]
		if key == "" {
			key = m["access_token"]
		}
		id, ok := f.identities[key]
		if !ok {
			return idp.Identity{}, idp.ErrInvalidCredential
		}
		id.Kind = enum.IdentityFacebook
		return id, nil
	}}
}

func facebookCred(raw string) user.IdpCredential {
	return user.IdpCredential{Kind: enum.IdentityFacebook, Credential: json.RawMessage(raw)}
}

func mustFacebookSignIn(t *testing.T, f *fixture, cred user.IdpCredential) user.TokenResult {
	t.Helper()
	res, err := f.svc.SignInWithIdp(context.Background(), cred, dev1, meta1)
	if err != nil {
		t.Fatal(err)
	}
	return requireSignInTokens(t, res)
}

func TestFacebookLimitedThenClassicSameAccount(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	fb := &fakeFacebook{identities: map[string]idp.Identity{
		"limited": {Subject: "asid-1", Name: "Ada", Picture: "https://example.test/fb.png", HintEmail: "person@example.test"},
		"classic": {Subject: "asid-1", Name: "Renamed"},
	}}
	newServiceWith(t, f, func(d *user.Deps) {
		*d = withProvider(*d, fb.provider())
		d.RequiredIdentityKinds = []enum.IdentityKind{}
	})
	first := mustFacebookSignIn(t, f, facebookCred(`{"id_token":"limited","nonce":"n"}`))
	if !first.IsNewUser || first.HintEmail != "person@example.test" {
		t.Fatalf("first: %+v", first)
	}
	second := mustFacebookSignIn(t, f, facebookCred(`{"access_token":"classic"}`))
	if second.IsNewUser || second.UserID != first.UserID {
		t.Fatalf("classic must hit same account: %+v", second)
	}
	me, err := f.svc.GetMe(ctx, first.UserID)
	if err != nil || me.DisplayName != "Ada" || me.AvatarURL != "https://example.test/fb.png" {
		t.Fatalf("profile must keep first fill: %+v %v", me, err)
	}
	idents, _ := user.TestRepoQueries(f.repo).ListActiveIdentitiesByUser(ctx, first.UserID)
	if len(idents) != 1 || idents[0].Kind != enum.IdentityFacebook {
		t.Fatalf("identities: %+v", idents)
	}
}

// 提供方给出的邮箱只是提示，不得与同邮箱的既有账号合并。
func TestFacebookHintEmailDoesNotMergeAccounts(t *testing.T) {
	f := newFixture(t)
	fb := &fakeFacebook{identities: map[string]idp.Identity{
		"limited": {Subject: "asid-2", Name: "Eve", HintEmail: email1},
	}}
	existing := f.signIn(t, enum.IdentityEmail, email1, dev1)
	newServiceWith(t, f, func(d *user.Deps) {
		*d = withProvider(*d, fb.provider())
		d.RequiredIdentityKinds = []enum.IdentityKind{}
	})
	got := mustFacebookSignIn(t, f, facebookCred(`{"id_token":"limited","nonce":"n"}`))
	if !got.IsNewUser || got.UserID == existing.UserID {
		t.Fatalf("hint email must not merge: new=%+v existing=%+v", got, existing)
	}
}

func TestFacebookNotRegisteredIsAppNotAllowed(t *testing.T) {
	f := newFixture(t)
	for _, raw := range []string{`{"id_token":"x","nonce":"n"}`, `{"access_token":"x"}`} {
		_, err := f.svc.SignInWithIdp(context.Background(), facebookCred(raw), dev1, meta1)
		if !errors.Is(err, idp.ErrAppNotAllowed) {
			t.Fatalf("%s: %v", raw, err)
		}
	}
}
