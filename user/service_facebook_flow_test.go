package user_test

import (
	"context"
	"errors"
	"testing"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/idp"
)

// 补全流程配置指纹包含 Facebook 提供方的真实 PolicyFingerprint：App ID 或 Graph 版本变化后，
// 旧流程必须被拒绝；配置不变则继续有效。
func TestCompletionFlowRejectedAfterFacebookConfigurationChange(t *testing.T) {
	ctx := context.Background()
	base := idp.FacebookOptions{AppID: "123", AppSecret: "s", GraphAPIVersion: "v26.0", GraphAPIBaseURL: "https://graph.facebook.com", JWKSURL: "https://www.facebook.com/.well-known/oauth/openid/jwks/"}
	provider := func(o idp.FacebookOptions) idp.Provider {
		return adaptedProvider{kind: enum.IdentityFacebook, fp: idp.NewFacebook(o).PolicyFingerprint(), verify: func(context.Context, map[string]string) (idp.Identity, error) {
			return idp.Identity{Kind: enum.IdentityFacebook, Subject: "fb-flow"}, nil
		}}
	}
	if provider(base).PolicyFingerprint() == "" {
		t.Fatal("empty fingerprint")
	}
	f := newFixture(t)
	newServiceWith(t, f, func(d *user.Deps) { *d = withProvider(*d, provider(base)) })
	r, err := f.svc.SignInWithIdp(ctx, facebookCred(`{"access_token":"t"}`), dev1, meta1)
	if err != nil || r.Pending == nil {
		t.Fatalf("pending: %+v %v", r, err)
	}
	newServiceWith(t, f, func(d *user.Deps) { *d = withProvider(*d, provider(base)) })
	if _, err := f.svc.SendSignInCompletionCode(ctx, r.Pending.SignInCompletionToken, enum.IdentityEmail, email1, user.Device{}, meta1); err != nil {
		t.Fatalf("unchanged configuration must keep the flow: %v", err)
	}
	for name, mutate := range map[string]func(*idp.FacebookOptions){
		"app id":  func(o *idp.FacebookOptions) { o.AppID = "456" },
		"version": func(o *idp.FacebookOptions) { o.GraphAPIVersion = "v25.0" },
	} {
		o := base
		mutate(&o)
		newServiceWith(t, f, func(d *user.Deps) { *d = withProvider(*d, provider(o)) })
		if _, err := f.svc.SendSignInCompletionCode(ctx, r.Pending.SignInCompletionToken, enum.IdentityEmail, "other.flow@example.test", user.Device{}, meta1); !errors.Is(err, user.ErrSignInCompletionInvalid) {
			t.Fatalf("%s change must reject the flow: %v", name, err)
		}
	}
}
