package user

import (
	"github.com/bbxx111/accountkit/enum"
	"testing"
)

func TestIdentityPolicy(t *testing.T) {
	phone, email, wechat := enum.IdentityPhone, enum.IdentityEmail, enum.IdentityWeChat
	for _, tc := range []struct {
		name string
		e, r []enum.IdentityKind
		bad  bool
	}{
		{"defaults", nil, nil, false}, {"empty enabled", []enum.IdentityKind{}, nil, true},
		{"empty required", []enum.IdentityKind{wechat}, []enum.IdentityKind{}, false},
		{"invalid", []enum.IdentityKind{0}, nil, true}, {"unknown", []enum.IdentityKind{99}, nil, true},
		{"duplicate", []enum.IdentityKind{phone, phone}, nil, true},
		{"required duplicate", nil, []enum.IdentityKind{phone, phone}, true},
		{"third party required", []enum.IdentityKind{wechat}, []enum.IdentityKind{wechat}, true},
		{"default subset", []enum.IdentityKind{email}, nil, true},
		{"explicit subset", []enum.IdentityKind{email}, []enum.IdentityKind{phone}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewIdentityPolicy(tc.e, tc.r)
			if (err != nil) != tc.bad {
				t.Fatalf("error=%v", err)
			}
		})
	}
	e, r := []enum.IdentityKind{email, phone}, []enum.IdentityKind{email}
	p, err := NewIdentityPolicy(e, r)
	if err != nil {
		t.Fatal(err)
	}
	e[0] = wechat
	r[0] = phone
	p.EnabledKinds()[0] = wechat
	p.RequiredKinds()[0] = phone
	if p.Allows(wechat) || !p.Complete([]enum.IdentityKind{email}) || p.Complete([]enum.IdentityKind{phone}) || p.Complete(nil) {
		t.Fatal("mutable or incorrect policy")
	}
	q, _ := NewIdentityPolicy([]enum.IdentityKind{phone, email}, []enum.IdentityKind{email})
	if p.Fingerprint() != q.Fingerprint() {
		t.Fatal("order changes fingerprint")
	}
	q, _ = NewIdentityPolicy([]enum.IdentityKind{wechat}, []enum.IdentityKind{})
	if q.Complete([]enum.IdentityKind{phone}) || !q.Complete([]enum.IdentityKind{wechat}) {
		t.Fatal("disabled identity counted")
	}
}
