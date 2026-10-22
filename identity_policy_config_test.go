package accountkit_test

import (
	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user/sender"
	"testing"
)

func TestIdentityKindsConfig(t *testing.T) {
	c := minimal()
	c.EnabledIdentityKinds = []enum.IdentityKind{enum.IdentityEmail}
	if c.Validate() == nil {
		t.Fatal("default required must be a subset")
	}
	c.RequiredIdentityKinds = []enum.IdentityKind{enum.IdentityEmail}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.RequiredIdentityKinds = []enum.IdentityKind{}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.EnabledIdentityKinds = []enum.IdentityKind{}
	if c.Validate() == nil {
		t.Fatal("empty enabled accepted")
	}
}
func TestIdentityKindsEnv(t *testing.T) {
	for name, value := range map[string]string{"JWT_KEYS": "1:" + b64(k(1)), "JWT_ACTIVE_KEY": "1", "JWT_ISSUER": "issuer", "JWT_AUDIENCE": "aud", "SUBJECT_HMAC_KEYS": "1:" + b64(k(2)), "SUBJECT_HMAC_ACTIVE_KEY": "1", "SUBJECT_CIPHER_KEYS": "1:" + b64(k(3)), "SUBJECT_CIPHER_ACTIVE_KEY": "1"} {
		t.Setenv("POLICY_"+name, value)
	}
	for _, s := range []string{"", " ", "phone", "EMAIL,", "EMAIL,EMAIL", "UNSPECIFIED"} {
		t.Run(s, func(t *testing.T) {
			t.Setenv("POLICY_ENABLED_IDENTITY_KINDS", s)
			if _, err := accountkit.ConfigFromEnv("POLICY_"); err == nil {
				t.Fatalf("accepted %q", s)
			}
		})
	}
	t.Setenv("POLICY_ENABLED_IDENTITY_KINDS", "EMAIL")
	t.Setenv("POLICY_REQUIRED_IDENTITY_KINDS", "[]")
	c, err := accountkit.ConfigFromEnv("POLICY_")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.EnabledIdentityKinds) != 1 || c.RequiredIdentityKinds == nil || len(c.RequiredIdentityKinds) != 0 {
		t.Fatal("presence lost")
	}
}

func TestIdentityPolicyNewCapabilities(t *testing.T) {
	for _, tc := range []struct {
		name     string
		explicit bool
		disable  bool
		omit     bool
		bad      bool
	}{
		{"legacy disabled", false, true, false, false}, {"legacy missing", false, false, true, true},
		{"explicit disabled", true, true, false, true}, {"explicit missing", true, false, true, true},
		{"explicit enabled", true, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := minimal()
			d := baseDeps(t)
			if tc.explicit {
				c.EnabledIdentityKinds = []enum.IdentityKind{enum.IdentityEmail}
				c.RequiredIdentityKinds = []enum.IdentityKind{enum.IdentityEmail}
				d.SMSSender = nil
			}
			if tc.disable {
				d.EmailSender = sender.Disabled{}
			}
			if tc.omit {
				d.EmailSender = nil
			}
			a, err := accountkit.New(c, d)
			if (err != nil) != tc.bad {
				t.Fatalf("error=%v", err)
			}
			if err == nil {
				if !a.Users().IdentityPolicy().Allows(enum.IdentityEmail) {
					t.Fatal("policy not wired")
				}
			}
		})
	}
}
func TestIdentityPolicyNewSnapshot(t *testing.T) {
	c := minimal()
	c.EnabledIdentityKinds = []enum.IdentityKind{enum.IdentityEmail}
	c.RequiredIdentityKinds = []enum.IdentityKind{enum.IdentityEmail}
	a, err := accountkit.New(c, baseDeps(t))
	if err != nil {
		t.Fatal(err)
	}
	c.EnabledIdentityKinds[0] = enum.IdentityPhone
	c.RequiredIdentityKinds[0] = enum.IdentityPhone
	if !a.Users().IdentityPolicy().Complete([]enum.IdentityKind{enum.IdentityEmail}) {
		t.Fatal("input mutated policy")
	}
	if a.Config().EnabledIdentityKinds[0] != enum.IdentityEmail {
		t.Fatal("config snapshot mutated")
	}
	got := a.Config()
	got.EnabledIdentityKinds[0] = enum.IdentityPhone
	if a.Config().EnabledIdentityKinds[0] != enum.IdentityEmail {
		t.Fatal("returned config mutated snapshot")
	}
}

func TestIdentityKindsConfigMissingProvider(t *testing.T) {
	for _, kind := range []enum.IdentityKind{enum.IdentityWeChat, enum.IdentityApple, enum.IdentityGoogle} {
		c := minimal()
		c.EnabledIdentityKinds = []enum.IdentityKind{kind}
		c.RequiredIdentityKinds = []enum.IdentityKind{}
		if err := c.Validate(); err != nil {
			t.Fatalf("static validation must allow absent runtime provider: %v", err)
		}
		if _, err := accountkit.New(c, baseDeps(t)); err == nil {
			t.Fatalf("runtime assembly accepted missing provider %s", kind)
		}
	}
}
