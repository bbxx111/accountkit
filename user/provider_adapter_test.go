package user_test

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/idp"
)

// Test fakes keep kind-specific verify signatures; these adapters expose them as idp.Provider.
// They decode leniently so fakes can observe empty fields; strict decoding is covered in package idp.
type adaptedProvider struct {
	kind   enum.IdentityKind
	verify func(context.Context, map[string]string) (idp.Identity, error)
	fp     string
}

func (p adaptedProvider) Kind() enum.IdentityKind { return p.kind }
func (p adaptedProvider) PolicyFingerprint() string {
	if p.fp != "" {
		return p.fp
	}
	return string(p.kind)
}
func (p adaptedProvider) Verify(ctx context.Context, raw json.RawMessage) (idp.Identity, error) {
	fields := map[string]string{}
	_ = json.Unmarshal(raw, &fields)
	return p.verify(ctx, fields)
}

func wechatProvider(v interface {
	VerifyWeChat(context.Context, string, string) (idp.Identity, error)
}) idp.Provider {
	return adaptedProvider{kind: enum.IdentityWeChat, verify: func(ctx context.Context, f map[string]string) (idp.Identity, error) {
		return v.VerifyWeChat(ctx, f["app_id"], f["code"])
	}}
}

func appleProvider(v interface {
	VerifyApple(context.Context, string, string) (idp.Identity, error)
}) idp.Provider {
	return adaptedProvider{kind: enum.IdentityApple, verify: func(ctx context.Context, f map[string]string) (idp.Identity, error) {
		return v.VerifyApple(ctx, f["id_token"], f["nonce"])
	}}
}

func googleProvider(v interface {
	VerifyGoogle(context.Context, string, string) (idp.Identity, error)
}) idp.Provider {
	return adaptedProvider{kind: enum.IdentityGoogle, verify: func(ctx context.Context, f map[string]string) (idp.Identity, error) {
		return v.VerifyGoogle(ctx, f["id_token"], f["nonce"])
	}}
}

// withProvider returns deps whose provider list replaces any provider of the same kind.
func withProvider(d user.Deps, p idp.Provider) user.Deps {
	d = withoutProvider(d, p.Kind())
	d.Providers = append(d.Providers, p)
	return d
}

func withoutProvider(d user.Deps, kind enum.IdentityKind) user.Deps {
	d.Providers = slices.DeleteFunc(slices.Clone(d.Providers), func(p idp.Provider) bool { return p.Kind() == kind })
	return d
}

func providerCred(kind enum.IdentityKind, fields map[string]string) user.IdpCredential {
	raw, _ := json.Marshal(fields)
	return user.IdpCredential{Kind: kind, Credential: raw}
}

func wechatCred(appID, code string) user.IdpCredential {
	return providerCred(enum.IdentityWeChat, map[string]string{"app_id": appID, "code": code})
}

func appleCred(idToken, nonce string) user.IdpCredential {
	return providerCred(enum.IdentityApple, map[string]string{"id_token": idToken, "nonce": nonce})
}

func googleCred(idToken, nonce string) user.IdpCredential {
	return providerCred(enum.IdentityGoogle, map[string]string{"id_token": idToken, "nonce": nonce})
}
