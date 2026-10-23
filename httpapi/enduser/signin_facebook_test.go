package enduser_test

import (
	"context"
	"testing"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/idp"
)

func TestSignInWithIdpFacebook(t *testing.T) {
	var got user.IdpCredential
	f := &fakeService{signInWithIdp: func(_ context.Context, cred user.IdpCredential, _ user.Device, _ user.Meta) (user.SignInResult, error) {
		got = cred
		fields := credFields(cred)
		switch {
		case fields["id_token"] != "" && fields["access_token"] != "":
			return user.SignInResult{}, idp.ErrMalformedCredential
		case fields["access_token"] == "down":
			return user.SignInResult{}, idp.ErrUnavailable
		}
		res := user.TokenResult{AccessToken: "at", RefreshToken: "rt", ExpiresIn: 900, RefreshExpiresIn: 2592000, Scope: "user:bind", UserID: "u_1", IsNewUser: true}
		return user.SignInResult{Tokens: &res}, nil
	}}
	h := newHandler(t, f)

	for name, c := range map[string]struct {
		cred map[string]string
		raw  string
	}{
		"limited": {map[string]string{"id_token": "t", "nonce": "n"}, `{"id_token":"t","nonce":"n"}`},
		"classic": {map[string]string{"access_token": "at"}, `{"access_token":"at"}`},
	} {
		rec := do(t, h, call{method: "POST", path: "/users:signInWithIdp", body: map[string]any{"kind": "FACEBOOK", "credential": c.cred}, headers: devHeaders()})
		var tok map[string]any
		decode(t, rec, &tok)
		if rec.Code != 200 || tok["access_token"] != "at" || got.Kind != enum.IdentityFacebook || string(got.Credential) != c.raw {
			t.Fatalf("%s: %d %v %+v", name, rec.Code, tok, got)
		}
	}

	rec := do(t, h, call{method: "POST", path: "/users:signInWithIdp", body: map[string]any{"kind": "FACEBOOK", "credential": map[string]string{"id_token": "t", "nonce": "n", "access_token": "at"}}, headers: devHeaders()})
	if status, _ := aipError(t, rec); rec.Code != 400 || status != "INVALID_ARGUMENT/CREDENTIAL_INCOMPLETE" {
		t.Fatalf("both shapes: %d %s", rec.Code, status)
	}
	rec = do(t, h, call{method: "POST", path: "/users:signInWithIdp", body: map[string]any{"kind": "FACEBOOK", "credential": map[string]string{"access_token": "down"}}, headers: devHeaders()})
	if status, _ := aipError(t, rec); rec.Code != 503 || status != "UNAVAILABLE/IDP_UNAVAILABLE" || rec.Header().Get("Retry-After") != "1" {
		t.Fatalf("unavailable: %d %s", rec.Code, status)
	}
}
