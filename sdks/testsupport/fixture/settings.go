package main

import (
	"errors"
	"strings"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/migrations"
)

type fixtureSettings struct {
	schema, redisPrefix, apiPrefix, adminPrefix string
	required                                    []enum.IdentityKind
	extended                                    bool
}

func parseSettings(o options) (fixtureSettings, error) {
	s := fixtureSettings{schema: o.schema, redisPrefix: o.redisPrefix, apiPrefix: defaultAPIPrefix, adminPrefix: defaultAdminPrefix}
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
	case "", profileAccountsvc:
	case profileExtended:
		s.extended = true
	case profileCustom:
		s.apiPrefix = customAPIPrefix
		s.adminPrefix = customAdminPrefix
	case profileCustomExtended:
		s.extended = true
		s.apiPrefix = customAPIPrefix
		s.adminPrefix = customAdminPrefix
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
	if o.profile == profileAccountsvc && (o.schema != "" || o.redisPrefix != "" || o.requiredKinds != "") {
		return s, errors.New("explicit isolation and required policy inputs apply to embedded fixture profiles only")
	}
	return s, nil
}
