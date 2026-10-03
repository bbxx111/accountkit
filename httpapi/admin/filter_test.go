package admin_test

import (
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/httpapi/admin"
	"github.com/bbxx111/accountkit/user"
)

func strp(s string) *string { return &s }

func TestParseFilterAcceptsSupportedGrammar(t *testing.T) {
	t0, _ := time.Parse(time.RFC3339, "2026-09-01T00:00:00Z")
	t1, _ := time.Parse(time.RFC3339, "2026-09-02T00:00:00+08:00")
	frozen := enum.UserFrozen
	cases := map[string]user.UserFilter{
		"":               {},
		"   ":            {},
		`state = FROZEN`: {State: &frozen},
		`state="FROZEN"`: {State: &frozen},
		`create_time >= "2026-09-01T00:00:00Z" AND create_time < "2026-09-02T00:00:00+08:00"`: {CreateTimeMin: &t0, CreateTimeMax: &t1},
		`identity.phone = "138 1234 1234"`:                                                            {IdentityKind: enum.IdentityPhone, Subject: strp("138 1234 1234")},
		`identity.email=Bind.Me@shifang.co`:                                                           {IdentityKind: enum.IdentityEmail, Subject: strp("Bind.Me@shifang.co")},
		`identity.email_prefix = "Ba"`:                                                                {IdentityKind: enum.IdentityEmail, HintPrefix: strp("ba")},
		`identity.phone_prefix = "+86138" AND identity.phone_suffix = 1234`:                           {IdentityKind: enum.IdentityPhone, HintPrefix: strp("+86138"), HintSuffix: strp("1234")},
		`identity.email_prefix = ba AND identity.email_domain = "Shifang.CO"`:                         {IdentityKind: enum.IdentityEmail, HintPrefix: strp("ba"), HintSuffix: strp("shifang.co")},
		`state = ACTIVE AND identity.phone_suffix = "0001" AND create_time >= "2026-09-01T00:00:00Z"`: {State: ptrState(enum.UserActive), IdentityKind: enum.IdentityPhone, HintSuffix: strp("0001"), CreateTimeMin: &t0},
	}
	for in, want := range cases {
		got, e := admin.ParseFilterForTest(in)
		if e != nil {
			t.Fatalf("%q: %v", in, e)
		}
		if !filterEqual(got, want) {
			t.Fatalf("%q:\n got %s\nwant %s", in, filterString(got), filterString(want))
		}
	}
}

func TestParseFilterRejectsUnsupportedInputWithoutEchoingValues(t *testing.T) {
	for name, in := range map[string]string{
		"unknown field":      `display_name = "x"`,
		"or":                 `state = ACTIVE OR state = FROZEN`,
		"not":                `NOT state = ACTIVE`,
		"parens":             `(state = ACTIVE)`,
		"bad op for state":   `state != ACTIVE`,
		"bad op for phone":   `identity.phone >= "138"`,
		"bad op for time":    `create_time = "2026-09-01T00:00:00Z"`,
		"dup min":            `create_time >= "2026-09-01T00:00:00Z" AND create_time >= "2026-09-02T00:00:00Z"`,
		"dup state":          `state = ACTIVE AND state = FROZEN`,
		"bad state":          `state = frozen`,
		"bad time":           `create_time >= yesterday`,
		"mixed kinds":        `identity.phone_suffix = 1234 AND identity.email_domain = shifang.co`,
		"unterminated quote": `state = "FROZEN`,
		"dangling and":       `state = ACTIVE AND`,
		"missing value":      `state =`,
		"lowercase and":      `state = ACTIVE and identity.phone_suffix = 1234`,
		"empty value":        `identity.phone = ""`,
	} {
		_, e := admin.ParseFilterForTest(in)
		if e == nil {
			t.Fatalf("%s must be rejected: %q", name, in)
		}
		if e.Status != "INVALID_ARGUMENT" || e.Reason != "INVALID_FILTER" {
			t.Fatalf("%s: %+v", name, e)
		}
		for _, secret := range []string{"1234", "shifang.co", "2026-09-02", "yesterday", "frozen"} {
			if strings.Contains(e.Message, secret) {
				t.Fatalf("%s: message must not echo values: %q", name, e.Message)
			}
		}
	}
}

func ptrState(s enum.UserState) *enum.UserState { return &s }

func filterEqual(a, b user.UserFilter) bool { return filterString(a) == filterString(b) }

func filterString(f user.UserFilter) string {
	var sb strings.Builder
	if f.State != nil {
		sb.WriteString("state=" + f.State.String() + ";")
	}
	if f.CreateTimeMin != nil {
		sb.WriteString("min=" + f.CreateTimeMin.UTC().Format(time.RFC3339Nano) + ";")
	}
	if f.CreateTimeMax != nil {
		sb.WriteString("max=" + f.CreateTimeMax.UTC().Format(time.RFC3339Nano) + ";")
	}
	if f.IdentityKind != enum.IdentityKindUnspecified {
		sb.WriteString("kind=" + f.IdentityKind.String() + ";")
	}
	for _, p := range []struct {
		k string
		v *string
	}{{"subject", f.Subject}, {"prefix", f.HintPrefix}, {"suffix", f.HintSuffix}} {
		if p.v != nil {
			sb.WriteString(p.k + "=" + *p.v + ";")
		}
	}
	if f.IncludeDeleted {
		sb.WriteString("deleted;")
	}
	return sb.String()
}
