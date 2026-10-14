package admin_test

import (
	"context"
	"testing"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
)

func TestRevealIdentity(t *testing.T) {
	var got struct {
		a       user.Admin
		uid, id string
	}
	f := &fakeService{revealIdentity: func(_ context.Context, a user.Admin, userID, identityID string, _ user.Meta) (user.RevealedIdentity, error) {
		got.a, got.uid, got.id = a, userID, identityID
		if identityID != iid {
			return user.RevealedIdentity{}, user.ErrNotFound
		}
		return user.RevealedIdentity{ID: identityID, Kind: enum.IdentityPhone, Subject: "+8613812341234"}, nil
	}}
	h := newHandler(t, f, nil)
	rec := do(t, h, call{method: "GET", path: "/users/" + uid + "/identities/" + iid + ":reveal", headers: superAdmin})
	var out map[string]any
	decode(t, rec, &out)
	if rec.Code != 200 || out["name"] != "users/"+uid+"/identities/"+iid || out["kind"] != "PHONE" || out["subject"] != "+8613812341234" || got.a != wantAdmin {
		t.Fatalf("reveal: %d %v %+v", rec.Code, out, got)
	}
	assertKeys(t, out, "name", "kind", "subject")
	rec = do(t, h, call{method: "GET", path: "/users/" + uid + "/identities/i_0000000000000:reveal", headers: superAdmin})
	if status, _ := aipError(t, rec); rec.Code != 404 || status != "NOT_FOUND/NOT_FOUND" {
		t.Fatalf("unknown identity: %d %s", rec.Code, status)
	}
	rec = do(t, h, call{method: "GET", path: "/users/" + uid + "/identities/nope:reveal", headers: superAdmin})
	if status, _ := aipError(t, rec); rec.Code != 400 || status != "INVALID_ARGUMENT/INVALID_ID" {
		t.Fatalf("bad identity id: %d %s", rec.Code, status)
	}
	// operator 不能 reveal
	if rec = do(t, h, call{method: "GET", path: "/users/" + uid + "/identities/" + iid + ":reveal", headers: operator}); rec.Code != 403 {
		t.Fatalf("operator reveal: %d", rec.Code)
	}
}
