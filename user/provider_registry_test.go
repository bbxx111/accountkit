package user_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
	ob "github.com/bbxx111/accountkit/observability"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/idp"
)

const corpSSO enum.IdentityKind = "CORP_SSO"

// corpProvider returns a configured-style provider whose subject comes from the credential "sub" field.
func corpProvider(kind enum.IdentityKind, fp string, extra func(*idp.Identity)) idp.Provider {
	return adaptedProvider{kind: kind, fp: fp, verify: func(_ context.Context, f map[string]string) (idp.Identity, error) {
		switch f["sub"] {
		case "":
			return idp.Identity{}, idp.ErrMalformedCredential
		case "unclassified":
			return idp.Identity{}, fmt.Errorf("%w: provider internal detail", user.ErrInvalidArgument)
		case "mismatch":
			return idp.Identity{Kind: "OTHER_SSO", Subject: "s"}, nil
		}
		id := idp.Identity{Kind: kind, Subject: f["sub"]}
		if extra != nil {
			extra(&id)
		}
		return id, nil
	}}
}

func corpCred(kind enum.IdentityKind, sub string) user.IdpCredential {
	return providerCred(kind, map[string]string{"sub": sub})
}

func TestProviderRegistryValidation(t *testing.T) {
	f := newFixture(t)
	for name, providers := range map[string][]idp.Provider{
		"nil":       {nil},
		"duplicate": {corpProvider(corpSSO, "", nil), corpProvider(corpSSO, "", nil)},
		"anchor":    {corpProvider(enum.IdentityPhone, "", nil)},
		"malformed": {corpProvider("corp", "", nil)},
		"empty":     {corpProvider("", "", nil)},
	} {
		d := f.deps
		d.Providers = providers
		if _, err := user.NewService(d); err == nil {
			t.Fatalf("%s providers accepted", name)
		}
	}
	d := f.deps
	d.EnabledIdentityKinds = []enum.IdentityKind{enum.IdentityEmail, corpSSO}
	d.RequiredIdentityKinds = []enum.IdentityKind{enum.IdentityEmail}
	if _, err := user.NewService(d); err == nil {
		t.Fatal("explicitly enabled unregistered kind accepted")
	}
	d.Providers = append(d.Providers, corpProvider(corpSSO, "", nil))
	if _, err := user.NewService(d); err != nil {
		t.Fatalf("enabled registered kind: %v", err)
	}
	d.RequiredIdentityKinds = []enum.IdentityKind{corpSSO}
	if _, err := user.NewService(d); err == nil {
		t.Fatal("third-party kind accepted as required")
	}
}

func TestProviderRegistryDefaultEnabledOrder(t *testing.T) {
	f := newFixture(t)
	newServiceWith(t, f, func(d *user.Deps) {
		d.Providers = append([]idp.Provider{corpProvider("ACME_SSO", "", nil), corpProvider(corpSSO, "", nil)}, d.Providers...)
	})
	got := f.svc.IdentityPolicy().EnabledKinds()
	want := []enum.IdentityKind{enum.IdentityPhone, enum.IdentityEmail, enum.IdentityWeChat, enum.IdentityApple, "ACME_SSO", corpSSO}
	if len(got) != len(want) {
		t.Fatalf("enabled = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("enabled = %v, want builtin order then configured names %v", got, want)
		}
	}
}

func TestProviderDispatchErrors(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	if _, err := f.svc.SignInWithIdp(ctx, corpCred(corpSSO, "s"), dev1, meta1); !errors.Is(err, idp.ErrAppNotAllowed) {
		t.Fatalf("unregistered kind with default Enabled: %v", err)
	}
	newServiceWith(t, f, func(d *user.Deps) {
		d.EnabledIdentityKinds = []enum.IdentityKind{enum.IdentityPhone, enum.IdentityEmail}
	})
	if _, err := f.svc.SignInWithIdp(ctx, corpCred(corpSSO, "s"), dev1, meta1); !errors.Is(err, user.ErrIdentityKindNotEnabled) {
		t.Fatalf("unregistered kind with explicit Enabled: %v", err)
	}

	g := newFixture(t)
	newServiceWith(t, g, func(d *user.Deps) {
		d.Providers = append(d.Providers, corpProvider(corpSSO, "", nil))
		d.RequiredIdentityKinds = []enum.IdentityKind{}
	})
	mem := resetAudit(g)
	if _, err := g.svc.SignInWithIdp(ctx, corpCred(corpSSO, ""), dev1, meta1); !errors.Is(err, idp.ErrMalformedCredential) {
		t.Fatalf("malformed: %v", err)
	}
	if _, err := g.svc.SignInWithIdp(ctx, corpCred(corpSSO, "unclassified"), dev1, meta1); err == nil || errors.Is(err, idp.ErrInvalidCredential) || errors.Is(err, user.ErrInvalidArgument) {
		t.Fatalf("unclassified provider error must be isolated from sentinels: %v", err)
	}
	wechat, err := idp.NewWeChat(idp.WeChatConfig{Apps: []idp.App{{AppID: "wx1", Secret: "s"}}, BaseURL: "http://127.0.0.1:1"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	h := newFixture(t)
	newServiceWith(t, h, func(d *user.Deps) { *d = withProvider(*d, wechat) })
	if _, err := h.svc.SignInWithIdp(ctx, providerCred(enum.IdentityWeChat, map[string]string{}), dev1, meta1); !errors.Is(err, idp.ErrMalformedCredential) {
		t.Fatalf("real provider malformed credential: %v", err)
	}
	for _, e := range append(mem.Events(), resetAudit(h).Events()...) {
		if e.Type == enum.EventSignInFailed {
			t.Fatalf("malformed or internal provider errors must not be audited as rejections: %+v", e)
		}
	}
	if _, err := g.svc.SignInWithIdp(ctx, corpCred(corpSSO, "mismatch"), dev1, meta1); !errors.Is(err, idp.ErrInvalidCredential) {
		t.Fatalf("mismatched provider result: %v", err)
	}
	p := principalOf(t, g, g.signIn(t, enum.IdentityPhone, phone1, dev2))
	if _, _, err := g.svc.BindWithIdp(ctx, p, corpCred(corpSSO, "mismatch"), meta1); !errors.Is(err, idp.ErrInvalidCredential) {
		t.Fatalf("mismatched provider result on bind: %v", err)
	}
	audited := map[enum.EventType]bool{}
	for _, e := range mem.Events() {
		if e.Reason == "IDP_CREDENTIAL_INVALID" {
			if e.IdentityKind != corpSSO {
				t.Fatalf("audit must record the configured kind name: %+v", e)
			}
			audited[e.Type] = true
		}
	}
	if !audited[enum.EventSignInFailed] || !audited[enum.EventIdentityBindRejected] {
		t.Fatalf("mismatched results must be audited like invalid credentials: %+v", mem.Events())
	}
	if _, err := g.svc.SignInWithIdp(ctx, corpCred("corp_sso", "x"), dev1, meta1); !errors.Is(err, user.ErrInvalidArgument) {
		t.Fatalf("malformed kind through the Go API: %v", err)
	}
	res := requireSignInTokens(t, mustSignInWithIdp(t, g, corpCred(corpSSO, "corp-1")))
	again := requireSignInTokens(t, mustSignInWithIdp(t, g, corpCred(corpSSO, "corp-1")))
	if !res.IsNewUser || again.IsNewUser || again.UserID != res.UserID {
		t.Fatalf("configured kind must register once and then sign in: %+v %+v", res, again)
	}
}

func mustSignInWithIdp(t *testing.T, f *fixture, cred user.IdpCredential) user.SignInResult {
	t.Helper()
	r, err := f.svc.SignInWithIdp(context.Background(), cred, dev1, meta1)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestProviderProfileClaimsAreKindIndependent(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	newServiceWith(t, f, func(d *user.Deps) {
		d.Providers = append(d.Providers, corpProvider(corpSSO, "", func(id *idp.Identity) {
			id.Name, id.Picture = "Corp Person", "https://example.test/corp.png"
		}))
		d.RequiredIdentityKinds = []enum.IdentityKind{}
	})
	res := requireSignInTokens(t, mustSignInWithIdp(t, f, corpCred(corpSSO, "corp-profile")))
	me, err := f.svc.GetMe(ctx, res.UserID)
	if err != nil || me.DisplayName != "Corp Person" || me.AvatarURL != "https://example.test/corp.png" {
		t.Fatalf("configured kind profile seed: %+v %v", me, err)
	}
	idents, _ := user.TestRepoQueries(f.repo).ListActiveIdentitiesByUser(ctx, res.UserID)
	if len(idents) != 1 || idents[0].Kind != corpSSO || !strings.Contains(string(idents[0].ProviderMeta), "Corp Person") {
		t.Fatalf("provider meta: %+v", idents)
	}

	w := newFixture(t)
	newServiceWith(t, w, func(d *user.Deps) { d.RequiredIdentityKinds = []enum.IdentityKind{} })
	wres := requireSignInTokens(t, mustSignInWithIdp(t, w, wechatCred("wx1", "union:no-profile@o")))
	me, _ = w.svc.GetMe(ctx, wres.UserID)
	idents, _ = user.TestRepoQueries(w.repo).ListActiveIdentitiesByUser(ctx, wres.UserID)
	if me.DisplayName != "" || me.AvatarURL != "" || len(idents) != 1 || strings.Contains(string(idents[0].ProviderMeta), "profile") {
		t.Fatalf("wechat must not write profile: %+v %s", me, idents[0].ProviderMeta)
	}
}

func TestCompletionFlowRejectedAfterProviderConfigurationChange(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	google := func(fp string) idp.Provider {
		return adaptedProvider{kind: enum.IdentityGoogle, fp: fp, verify: func(context.Context, map[string]string) (idp.Identity, error) {
			return idp.Identity{Kind: enum.IdentityGoogle, Subject: "g-flow"}, nil
		}}
	}
	newServiceWith(t, f, func(d *user.Deps) { *d = withProvider(*d, google("client-a")) })
	r, err := f.svc.SignInWithIdp(ctx, googleCred("t", "n"), dev1, meta1)
	if err != nil || r.Pending == nil {
		t.Fatalf("pending: %+v %v", r, err)
	}
	newServiceWith(t, f, func(d *user.Deps) { *d = withProvider(*d, google("client-a")) })
	if _, err := f.svc.SendSignInCompletionCode(ctx, r.Pending.SignInCompletionToken, enum.IdentityEmail, email1, user.Device{}, meta1); err != nil {
		t.Fatalf("unchanged configuration must keep the flow: %v", err)
	}
	newServiceWith(t, f, func(d *user.Deps) { *d = withProvider(*d, google("client-b")) })
	if _, err := f.svc.SendSignInCompletionCode(ctx, r.Pending.SignInCompletionToken, enum.IdentityEmail, "other.flow@example.test", user.Device{}, meta1); !errors.Is(err, user.ErrSignInCompletionInvalid) {
		t.Fatalf("google configuration change must reject the flow: %v", err)
	}
}

func TestUnregisteredHistoricalKind(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	acme := enum.IdentityKind("ACME_SSO")
	newServiceWith(t, f, func(d *user.Deps) { d.Providers = append(d.Providers, corpProvider(acme, "", nil)) })
	phone := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	p := principalOf(t, f, phone)
	info, created, err := f.svc.BindWithIdp(ctx, p, corpCred(acme, "acme-1"), meta1)
	if err != nil || !created || info.Kind != acme {
		t.Fatalf("bind: %+v %v %v", info, created, err)
	}

	// The provider is removed from configuration; history stays visible and removable.
	newServiceWith(t, f, func(d *user.Deps) { *d = withoutProvider(*d, acme) })
	list, err := f.svc.ListIdentities(ctx, phone.UserID)
	if err != nil || len(list) != 2 {
		t.Fatalf("list: %+v %v", list, err)
	}
	found := false
	for _, it := range list {
		found = found || (it.ID == info.ID && it.Kind == acme)
	}
	if !found {
		t.Fatalf("historical kind missing from list: %+v", list)
	}
	page, err := f.svc.ListUsers(ctx, user.UserFilter{IdentityKind: acme}, nil, 10)
	if err != nil || len(page.Users) != 1 || page.Users[0].ID != phone.UserID {
		t.Fatalf("admin filter by historical kind: %+v %v", page, err)
	}
	if _, err := f.svc.SignInWithIdp(ctx, corpCred(acme, "acme-1"), dev2, meta1); !errors.Is(err, idp.ErrAppNotAllowed) {
		t.Fatalf("sign-in with removed provider: %v", err)
	}
	if _, _, err := f.svc.BindWithIdp(ctx, p, corpCred(acme, "acme-2"), meta1); !errors.Is(err, idp.ErrAppNotAllowed) {
		t.Fatalf("new bind with removed provider: %v", err)
	}
	if err := f.svc.UnbindIdentity(ctx, p, info.ID, meta1); err != nil {
		t.Fatalf("unbind historical kind: %v", err)
	}

	// Anonymization covers historical kinds, including unbound rows.
	if _, err := f.svc.DeleteMe(ctx, principalOf(t, f, f.signIn(t, enum.IdentityPhone, phone1, dev1)), meta1); err != nil {
		t.Fatal(err)
	}
	f.advance(360*time.Hour + time.Second)
	if n, err := f.svc.PurgeDueUsers(ctx); err != nil || n != 1 {
		t.Fatalf("purge: %d %v", n, err)
	}
	rows, _ := user.TestRepoQueries(f.repo).ListIdentitiesByUserIncludingDeleted(ctx, phone.UserID)
	for _, row := range rows {
		if row.Kind == acme && (row.ProviderSubject == nil || *row.ProviderSubject == "acme-1" || row.DeleteTime == nil) {
			t.Fatalf("historical identity not anonymized: %+v", row)
		}
	}
}

func TestProviderObservationLabels(t *testing.T) {
	ctx := context.Background()
	f, r := observedFixture(t)
	f.deps = withProvider(f.deps, corpProvider(corpSSO, "", nil))
	f.deps.RequiredIdentityKinds = []enum.IdentityKind{}
	rebuildMetricsService(t, f)
	if _, err := f.svc.SignInWithIdp(ctx, corpCred(corpSSO, ""), dev1, meta1); !errors.Is(err, idp.ErrMalformedCredential) {
		t.Fatal(err)
	}
	deps := dependencyValues(r, ob.ComponentIDP, ob.DependencyVerifyProvider)
	auth := authValues(r)
	if len(deps) != 1 || deps[0].Result != ob.ResultRejected || len(auth) != 1 || auth[0].Result != ob.ResultRejected || auth[0].Reason != ob.ReasonInvalidArgument || auth[0].Method != ob.MethodProvider {
		t.Fatalf("malformed credential must be a client rejection with fixed labels: deps=%+v auth=%+v", deps, auth)
	}
	for _, v := range r.Snapshot() {
		if strings.Contains(fmt.Sprintf("%+v", v), string(corpSSO)) {
			t.Fatalf("configured kind leaked into observations: %+v", v)
		}
	}

	o, or := observedFixture(t)
	o.deps = withProvider(o.deps, idp.NewOIDC(idp.OIDCOptions{Kind: "PARTNER_SSO", Issuer: "https://partner.example", JWKSURL: "http://127.0.0.1:1/jwks", ClientIDs: []string{"p"}}))
	o.deps.RequiredIdentityKinds = []enum.IdentityKind{}
	rebuildMetricsService(t, o)
	if _, err := o.svc.SignInWithIdp(ctx, providerCred("PARTNER_SSO", map[string]string{"id_token": "t"}), dev1, meta1); !errors.Is(err, idp.ErrMalformedCredential) {
		t.Fatal(err)
	}
	deps = dependencyValues(or, ob.ComponentIDP, ob.DependencyVerifyOIDC)
	auth = authValues(or)
	if len(deps) != 1 || len(auth) != 1 || auth[0].Method != ob.MethodOIDC {
		t.Fatalf("OIDC must use fixed oidc labels: deps=%+v auth=%+v", deps, auth)
	}
	for _, v := range or.Snapshot() {
		if strings.Contains(fmt.Sprintf("%+v", v), "PARTNER_SSO") {
			t.Fatalf("configured kind leaked into observations: %+v", v)
		}
	}
}
