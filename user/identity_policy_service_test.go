package user

import (
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/pii"
	"github.com/bbxx111/accountkit/session/grace"
	"github.com/bbxx111/accountkit/session/revocation"
	"github.com/bbxx111/accountkit/tokens"
	"github.com/bbxx111/accountkit/user/code"
	"github.com/bbxx111/accountkit/user/sender"
	"testing"
	"time"
)

func TestIdentityPolicyDirectNewService(t *testing.T) {
	base := Deps{Repo: &Repo{}, Codes: &code.Store{}, Revocation: &revocation.Set{}, Grace: &grace.Cache{}, Signer: &tokens.Signer{}, Cipher: &pii.Cipher{}, Digester: &pii.Digester{}, SMS: sender.Disabled{}, Email: sender.Disabled{}, AccessTTL: time.Minute, RefreshTTL: time.Hour, RefreshGrace: time.Second, CodeTTL: time.Minute, DefaultRegion: "CN", MaxIdentitiesPerKind: 1, DeletionCoolingPeriod: time.Hour}
	if _, err := NewService(base); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name            string
		kinds, required []enum.IdentityKind
		email           sender.EmailCodeSender
		bad             bool
	}{
		{"default subset", []enum.IdentityKind{enum.IdentityEmail}, nil, sender.NewLog(nil), true},
		{"disabled", []enum.IdentityKind{enum.IdentityEmail}, []enum.IdentityKind{enum.IdentityEmail}, sender.Disabled{}, true},
		{"missing", []enum.IdentityKind{enum.IdentityEmail}, []enum.IdentityKind{enum.IdentityEmail}, nil, true},
		{"email only", []enum.IdentityKind{enum.IdentityEmail}, []enum.IdentityKind{enum.IdentityEmail}, sender.NewLog(nil), false},
		{"wechat missing", []enum.IdentityKind{enum.IdentityWeChat}, []enum.IdentityKind{}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := base
			d.EnabledIdentityKinds = tc.kinds
			d.RequiredIdentityKinds = tc.required
			d.SMS = nil
			d.Email = tc.email
			s, err := NewService(d)
			if (err != nil) != tc.bad {
				t.Fatalf("error=%v", err)
			}
			if err == nil {
				tc.kinds[0] = enum.IdentityPhone
				tc.required[0] = enum.IdentityPhone
				if !s.IdentityPolicy().Complete([]enum.IdentityKind{enum.IdentityEmail}) {
					t.Fatal("direct policy snapshot changed")
				}
			}
		})
	}
}

func TestIdentityPolicyAccessorCannotReplaceServicePolicy(t *testing.T) {
	original, err := NewIdentityPolicy([]enum.IdentityKind{enum.IdentityEmail}, []enum.IdentityKind{enum.IdentityEmail})
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := NewIdentityPolicy([]enum.IdentityKind{enum.IdentityPhone}, []enum.IdentityKind{enum.IdentityPhone})
	if err != nil {
		t.Fatal(err)
	}
	svc := &Service{policy: original}
	*svc.IdentityPolicy() = *replacement
	if !svc.IdentityPolicy().Allows(enum.IdentityEmail) || svc.IdentityPolicy().Allows(enum.IdentityPhone) {
		t.Fatal("accessor allowed replacing service policy")
	}
}
