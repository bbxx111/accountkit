package main

import (
	"github.com/bbxx111/accountkit/enum"
	"testing"
)

func TestFixtureSettings(t *testing.T) {
	s, e := parseSettings(options{profile: "custom-extended", schema: "sdk_fixture_extended", redisPrefix: "sdk_fixture_extended:", requiredKinds: "EMAIL"})
	if e != nil {
		t.Fatal(e)
	}
	if s.schema != "sdk_fixture_extended" || s.redisPrefix != "sdk_fixture_extended:" || s.apiPrefix != "/custom/account" || s.adminPrefix != "/custom/admin" || !s.extended || len(s.required) != 1 || s.required[0] != enum.IdentityEmail {
		t.Fatal("combined fixture settings lost isolation, policy, or capabilities")
	}
	empty, e := parseSettings(options{requiredKinds: "[]"})
	if e != nil || empty.required == nil || len(empty.required) != 0 {
		t.Fatal("explicit empty required list must remain non-nil")
	}
	defaults, e := parseSettings(options{})
	if e != nil || defaults.required != nil || defaults.schema != "sdk_fixture" || defaults.redisPrefix != "sdk_fixture:" {
		t.Fatal("default settings changed")
	}
	for _, o := range []options{{requiredKinds: "GOOGLE"}, {requiredKinds: "EMAIL,EMAIL"}, {requiredKinds: "email"}, {schema: "bad-name"}, {redisPrefix: "missing-colon"}, {profile: "typo"}} {
		if _, e := parseSettings(o); e == nil {
			t.Fatalf("invalid settings accepted: %+v", o)
		}
	}
}
