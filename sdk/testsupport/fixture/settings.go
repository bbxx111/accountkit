package main

import (
	"errors"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/migrations"
	"strings"
)

type fixtureSettings struct {
	schema, redisPrefix, apiPrefix, adminPrefix string
	required                                    []enum.IdentityKind
	extended                                    bool
}

func parseSettings(o options) (fixtureSettings, error) {
	s := fixtureSettings{schema: o.schema, redisPrefix: o.redisPrefix, apiPrefix: "/v1", adminPrefix: "/admin/v1"}
	if s.schema == "" {
		s.schema = "sdk_fixture"
	}
	if s.redisPrefix == "" {
		s.redisPrefix = "sdk_fixture:"
	}
	if !migrations.ValidSchema(s.schema) {
		return s, errors.New("SDK_FIXTURE_SCHEMA must be a valid schema name")
	}
	if !strings.HasSuffix(s.redisPrefix, ":") || strings.TrimSpace(s.redisPrefix) != s.redisPrefix {
		return s, errors.New("SDK_FIXTURE_REDIS_PREFIX must end in ':' without surrounding whitespace")
	}
	switch o.profile {
	case "", "accountsvc":
	case "extended":
		s.extended = true
	case "custom":
		s.apiPrefix = "/custom/account"
		s.adminPrefix = "/custom/admin"
	case "custom-extended":
		s.extended = true
		s.apiPrefix = "/custom/account"
		s.adminPrefix = "/custom/admin"
	default:
		return s, errors.New("unknown SDK_FIXTURE_PROFILE")
	}
	if o.requiredKinds != "" {
		s.required = []enum.IdentityKind{}
		if o.requiredKinds != "[]" {
			seen := map[enum.IdentityKind]bool{}
			for _, raw := range strings.Split(o.requiredKinds, ",") {
				kind, e := enum.ParseIdentityKind(strings.TrimSpace(raw))
				if e != nil || !kind.IsChannel() || seen[kind] {
					return s, errors.New("SDK_FIXTURE_REQUIRED_KINDS must be unique PHONE/EMAIL values or []")
				}
				seen[kind] = true
				s.required = append(s.required, kind)
			}
		}
	}
	if o.profile == "accountsvc" && (o.schema != "" || o.redisPrefix != "" || o.requiredKinds != "") {
		return s, errors.New("explicit isolation and required policy inputs apply to embedded fixture profiles only")
	}
	return s, nil
}
