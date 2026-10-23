package user

import (
	"errors"
	"testing"

	"github.com/bbxx111/accountkit/user/idp"
)

func TestMergeProviderMetadata(t *testing.T) {
	old := []byte(`{"unionid":"u1","openids":{"wx1":"o1"},"profile":{"name":"keep","picture":"https://example.test/p.png"}}`)
	for _, tc := range []struct {
		name     string
		ident    idp.Identity
		conflict bool
	}{
		{"missing preserves", idp.Identity{}, false},
		{"add app", idp.Identity{UnionID: "u1", OpenIDs: map[string]string{"wx2": "o2"}}, false},
		{"union conflict", idp.Identity{UnionID: "u2"}, true},
		{"openid conflict", idp.Identity{OpenIDs: map[string]string{"wx1": "different"}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pm, _, err := mergeProviderMeta(old, tc.ident)
			if tc.conflict {
				if !errors.Is(err, ErrIdentityConflict) {
					t.Fatalf("want conflict, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if pm.UnionID != "u1" || pm.OpenIDs["wx1"] != "o1" || pm.Profile == nil || pm.Profile.Name != "keep" || pm.Profile.Picture != "https://example.test/p.png" {
				t.Fatalf("lost metadata: %+v", pm)
			}
			if tc.name == "add app" && pm.OpenIDs["wx2"] != "o2" {
				t.Fatal("missing new app")
			}
		})
	}
	pm, changed, err := mergeProviderMeta(nil, idp.Identity{UnionID: "u1", OpenIDs: map[string]string{"wx1": "o1", "empty": ""}, Name: "Person"})
	if err != nil || !changed || pm.UnionID != "u1" || pm.OpenIDs["wx1"] != "o1" || len(pm.OpenIDs) != 1 || pm.Profile == nil || pm.Profile.Name != "Person" {
		t.Fatalf("initial metadata: %+v %v %v", pm, changed, err)
	}
}
