package user_test

import (
	"context"
	"strings"
	"testing"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
)

// backfillKinds 覆盖原补填集合内外的提供方；合成提供方返回请求里的 name/picture。
var backfillKinds = []enum.IdentityKind{enum.IdentityAlipay, enum.IdentityQQ, enum.IdentityWeibo, enum.IdentityGoogle, enum.IdentityFacebook, corpSSO}

func boolRef(b bool) *bool { return &b }

var backfillModes = []struct {
	name string
	on   *bool
	want bool
}{{"default", nil, true}, {"on", boolRef(true), true}, {"off", boolRef(false), false}}

func useBackfillProvider(t *testing.T, f *fixture, kind enum.IdentityKind, on *bool, required []enum.IdentityKind) {
	t.Helper()
	newServiceWith(t, f, func(d *user.Deps) {
		*d = withProvider(*d, alipayProvider(kind, nil))
		d.RequiredIdentityKinds = required
		d.ProviderProfileBackfill = on
	})
}

func laterCred(kind enum.IdentityKind) user.IdpCredential {
	return providerCred(kind, map[string]string{"sub": "person", "app": "app-one", "openid": "open-one", "name": "Later", "picture": "https://example.test/later.png"})
}

func emptyCred(kind enum.IdentityKind) user.IdpCredential {
	return providerCred(kind, map[string]string{"sub": "person", "app": "app-one", "openid": "open-one"})
}

func assertProviderMetaHas(t *testing.T, f *fixture, uid string, kind enum.IdentityKind, text string) {
	t.Helper()
	rows, err := f.repo.Q().ListActiveIdentitiesByUser(context.Background(), uid)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Kind == kind && strings.Contains(string(row.ProviderMeta), text) {
			return
		}
	}
	t.Fatalf("provider metadata for %s lacks %q", kind, text)
}

func expectedProfile(want bool) (string, string) {
	if want {
		return "Later", "https://example.test/later.png"
	}
	return "", ""
}

func TestProviderProfileBackfillOnSignIn(t *testing.T) {
	for _, kind := range backfillKinds {
		for _, mode := range backfillModes {
			t.Run(string(kind)+"/"+mode.name, func(t *testing.T) {
				f := newFixture(t)
				useBackfillProvider(t, f, kind, mode.on, []enum.IdentityKind{})
				first := requireSignInTokens(t, mustSignInWithIdp(t, f, emptyCred(kind)))
				again := requireSignInTokens(t, mustSignInWithIdp(t, f, laterCred(kind)))
				if again.UserID != first.UserID || again.IsNewUser {
					t.Fatal("later sign-in changed user")
				}
				name, avatar := expectedProfile(mode.want)
				assertAlipayProfile(t, f, first.UserID, name, avatar)
				assertProviderMetaHas(t, f, first.UserID, kind, "Later")
			})
		}
	}
}

func TestProviderProfileBackfillKeepsNonEmpty(t *testing.T) {
	f := newFixture(t)
	useBackfillProvider(t, f, enum.IdentityGoogle, nil, []enum.IdentityKind{})
	first := requireSignInTokens(t, mustSignInWithIdp(t, f, emptyCred(enum.IdentityGoogle)))
	mine := "Mine"
	if _, err := f.svc.UpdateProfile(context.Background(), first.UserID, user.ProfilePatch{DisplayName: &mine}); err != nil {
		t.Fatal(err)
	}
	mustSignInWithIdp(t, f, laterCred(enum.IdentityGoogle))
	assertAlipayProfile(t, f, first.UserID, "Mine", "https://example.test/later.png")
}

func TestProviderProfileBackfillOnBinding(t *testing.T) {
	for _, kind := range []enum.IdentityKind{enum.IdentityAlipay, enum.IdentityGoogle} {
		for _, mode := range backfillModes {
			t.Run(string(kind)+"/"+mode.name, func(t *testing.T) {
				ctx := context.Background()
				f := newFixture(t)
				useBackfillProvider(t, f, kind, mode.on, nil)
				phone := f.signIn(t, enum.IdentityPhone, phone1, dev1)
				p := principalOf(t, f, phone)
				if _, created, err := f.svc.BindWithIdp(ctx, p, laterCred(kind), meta1); err != nil || !created {
					t.Fatal(err)
				}
				name, avatar := expectedProfile(mode.want)
				assertAlipayProfile(t, f, phone.UserID, name, avatar)
				empty := ""
				if _, err := f.svc.UpdateProfile(ctx, phone.UserID, user.ProfilePatch{DisplayName: &empty, AvatarURL: &empty}); err != nil {
					t.Fatal(err)
				}
				if _, created, err := f.svc.BindWithIdp(ctx, p, laterCred(kind), meta1); err != nil || created {
					t.Fatal(err)
				}
				assertAlipayProfile(t, f, phone.UserID, name, avatar)
				assertProviderMetaHas(t, f, phone.UserID, kind, "Later")
			})
		}
	}
}

func TestProviderProfileBackfillOnCompletionLink(t *testing.T) {
	for _, mode := range backfillModes {
		t.Run("new-identity/"+mode.name, func(t *testing.T) {
			ctx := context.Background()
			f := newFixture(t)
			useBackfillProvider(t, f, enum.IdentityGoogle, mode.on, nil)
			owner := f.signIn(t, enum.IdentityEmail, email1, dev2)
			pending := mustSignInWithIdp(t, f, laterCred(enum.IdentityGoogle))
			if pending.Pending == nil {
				t.Fatal("expected pending")
			}
			cred := completionCredential(t, f, pending.Pending, enum.IdentityEmail, email1)
			done, err := f.svc.CompleteSignIn(ctx, pending.Pending.SignInCompletionToken, cred, dev1, meta1)
			if err != nil || requireSignInTokens(t, done).UserID != owner.UserID {
				t.Fatalf("completion did not link owner: %v", err)
			}
			name, avatar := expectedProfile(mode.want)
			assertAlipayProfile(t, f, owner.UserID, name, avatar)
		})
		t.Run("owned-proof/"+mode.name, func(t *testing.T) {
			ctx := context.Background()
			f := newFixture(t)
			useBackfillProvider(t, f, enum.IdentityAlipay, mode.on, nil)
			owner := f.signIn(t, enum.IdentityEmail, email1, dev2)
			pending := mustSignInWithIdp(t, f, laterCred(enum.IdentityAlipay))
			if pending.Pending == nil {
				t.Fatal("expected pending")
			}
			bound, _, err := f.svc.BindWithIdp(ctx, principalOf(t, f, owner), emptyCred(enum.IdentityAlipay), meta1)
			if err != nil {
				t.Fatal(err)
			}
			alipayPendingPayload(t, f, func(p map[string]any) {
				proof := p["Proofs"].([]any)[0].(map[string]any)
				proof["UserID"] = owner.UserID
				proof["IdentityID"] = bound.ID
			})
			cred := completionCredential(t, f, pending.Pending, enum.IdentityEmail, email1)
			done, err := f.svc.CompleteSignIn(ctx, pending.Pending.SignInCompletionToken, cred, dev1, meta1)
			if err != nil || requireSignInTokens(t, done).UserID != owner.UserID {
				t.Fatalf("completion did not use owned proof: %v", err)
			}
			name, avatar := expectedProfile(mode.want)
			assertAlipayProfile(t, f, owner.UserID, name, avatar)
		})
	}
}

func TestProviderProfileBackfillDisabledStillFillsNewAccounts(t *testing.T) {
	ctx := context.Background()
	direct := newFixture(t)
	useBackfillProvider(t, direct, enum.IdentityGoogle, boolRef(false), []enum.IdentityKind{})
	res := requireSignInTokens(t, mustSignInWithIdp(t, direct, laterCred(enum.IdentityGoogle)))
	if !res.IsNewUser {
		t.Fatal("expected direct registration")
	}
	assertAlipayProfile(t, direct, res.UserID, "Later", "https://example.test/later.png")

	completed := newFixture(t)
	useBackfillProvider(t, completed, enum.IdentityAlipay, boolRef(false), nil)
	pending := mustSignInWithIdp(t, completed, laterCred(enum.IdentityAlipay))
	if pending.Pending == nil {
		t.Fatal("expected pending")
	}
	cred := completionCredential(t, completed, pending.Pending, enum.IdentityEmail, email1)
	done, err := completed.svc.CompleteSignIn(ctx, pending.Pending.SignInCompletionToken, cred, dev1, meta1)
	if err != nil || !requireSignInTokens(t, done).IsNewUser {
		t.Fatalf("completion did not create an account: %v", err)
	}
	assertAlipayProfile(t, completed, requireSignInTokens(t, done).UserID, "Later", "https://example.test/later.png")
}
