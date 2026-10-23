package enduser_test

import (
	"context"
	"testing"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/idp"
)

func TestBindFacebookIdentity(t *testing.T) {
	var got user.IdpCredential
	f := withAuth(&fakeService{
		bindWithIdp: func(_ context.Context, _ user.Principal, cred user.IdpCredential, _ user.Meta) (user.IdentityInfo, bool, error) {
			got = cred
			fields := credFields(cred)
			switch {
			case fields["id_token"] != "" && fields["access_token"] != "":
				return user.IdentityInfo{}, false, idp.ErrMalformedCredential
			case fields["access_token"] == "down":
				return user.IdentityInfo{}, false, idp.ErrUnavailable
			}
			return user.IdentityInfo{ID: "i_0k3f9c2m1xq7d", Kind: cred.Kind, CreateTime: testNow}, true, nil
		},
	}, principal)
	h := newHandler(t, f)
	for _, cred := range []map[string]string{{"id_token": "t", "nonce": "n"}, {"access_token": "at"}} {
		rec := do(t, h, call{method: "POST", path: "/users/me/identities", bearer: "good", body: map[string]any{"kind": "FACEBOOK", "credential": cred}})
		var res map[string]any
		decode(t, rec, &res)
		if rec.Code != 201 || res["kind"] != "FACEBOOK" || got.Kind != enum.IdentityFacebook {
			t.Fatalf("bind %v: %d %v", cred, rec.Code, res)
		}
	}
	rec := do(t, h, call{method: "POST", path: "/users/me/identities", bearer: "good", body: map[string]any{"kind": "FACEBOOK", "credential": map[string]string{"id_token": "t", "nonce": "n", "access_token": "at"}}})
	if status, _ := aipError(t, rec); rec.Code != 400 || status != "INVALID_ARGUMENT/CREDENTIAL_INCOMPLETE" {
		t.Fatalf("both shapes: %d %s", rec.Code, status)
	}
	rec = do(t, h, call{method: "POST", path: "/users/me/identities", bearer: "good", body: map[string]any{"kind": "FACEBOOK", "credential": map[string]string{"access_token": "down"}}})
	if status, _ := aipError(t, rec); rec.Code != 503 || status != "UNAVAILABLE/IDP_UNAVAILABLE" {
		t.Fatalf("unavailable: %d %s", rec.Code, status)
	}
}
