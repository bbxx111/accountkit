package serviceauth_test

import (
	"bytes"
	"encoding/base64"
	"log/slog"
	"strings"
	"testing"

	"github.com/bbxx111/accountkit/internal/accountsvc/serviceauth"
	"github.com/golang-jwt/jwt/v5"
)

func basicHeader(id, secret string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(id+":"+secret))
}

func TestUnifiedAuthenticationMatrix(t *testing.T) {
	e := newEnv(t, false, true)
	secondSecret := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32))
	registry := mustRegistry(t, []serviceauth.ClientConfig{
		{ID: "both", AuthMethods: []string{"basic", "bearer"}, BearerSubjects: []string{"machine"}, Permissions: []string{"tokens.introspect", "purged_users.read", "identities.resolve"}, BasicSecrets: []string{basicSecret, secondSecret}},
		{ID: "basic-only", AuthMethods: []string{"basic"}, Permissions: []string{"purged_users.read"}, BasicSecrets: []string{base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{10}, 32))}},
		{ID: "bearer-only", AuthMethods: []string{"bearer"}, BearerSubjects: []string{"machine"}, Permissions: []string{"purged_users.read"}},
	})
	for _, permission := range []string{"tokens.introspect", "purged_users.read", "identities.resolve"} {
		e.auth = mustNew(t, serviceauth.Config{Realm: realm, Registry: registry, RequiredPermission: permission, BearerVerifier: tokensFor(t, e), Logger: slog.New(slog.NewTextHandler(&e.logs, nil))})
		for _, header := range []string{basicHeader("both", basicSecret), basicHeader("both", secondSecret), e.bearer(e.p.Claims(e.now, "both"), "initial")} {
			res, ch := e.do(header)
			expect(t, res, ch, serviceauth.OK, nil)
			if res.ClientID != "both" {
				t.Fatal(res)
			}
		}
	}
	e.auth = mustNew(t, serviceauth.Config{Realm: realm, Endpoint: "introspect", Registry: registry, RequiredPermission: "tokens.introspect", BearerVerifier: tokensFor(t, e), Logger: slog.New(slog.NewTextHandler(&e.logs, nil))})
	basicOnlySecret := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{10}, 32))
	for _, tt := range []struct {
		name    string
		headers []string
		outcome serviceauth.Outcome
		ch      []string
		attempt bool
	}{
		{"missing", nil, serviceauth.Unauthenticated, bothChallenges, false},
		{"multiple", []string{"Bearer invalid", basicHeader("both", basicSecret)}, serviceauth.Unauthenticated, bothChallenges, false},
		{"unknown scheme", []string{"Digest abc"}, serviceauth.Unauthenticated, bothChallenges, false},
		{"unknown basic", []string{basicHeader("unknown", basicSecret)}, serviceauth.Unauthenticated, bothChallenges, false},
		{"wrong secret", []string{basicHeader("both", "wrong")}, serviceauth.Unauthenticated, bothChallenges, false},
		{"disabled basic", []string{basicHeader("bearer-only", basicSecret)}, serviceauth.Unauthenticated, bothChallenges, false},
		{"disabled bearer", []string{e.bearer(e.p.Claims(e.now, "basic-only"), "initial")}, serviceauth.Unauthenticated, bothChallenges, false},
		{"unknown bearer", []string{e.bearer(e.p.Claims(e.now, "unknown"), "initial")}, serviceauth.Forbidden, scopeChallenge, false},
		{"basic permission", []string{basicHeader("basic-only", basicOnlySecret)}, serviceauth.Forbidden, nil, false},
		{"bearer permission", []string{e.bearer(e.p.Claims(e.now, "bearer-only"), "initial")}, serviceauth.Forbidden, scopeChallenge, false},
		{"invalid bearer", []string{"Bearer invalid"}, serviceauth.Unauthenticated, invalidChallenges, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			res, ch := e.do(tt.headers...)
			expect(t, res, ch, tt.outcome, tt.ch)
			if res.BearerAttempted != tt.attempt {
				t.Fatal(res)
			}
			for _, header := range tt.headers {
				if strings.HasPrefix(header, "Bearer ") && strings.Contains(e.logs.String(), strings.TrimPrefix(header, "Bearer ")) {
					t.Fatal("logs disclosed Bearer credentials")
				}
			}
		})
	}
	for _, secret := range []string{basicSecret, secondSecret, basicOnlySecret, "wrong"} {
		if strings.Contains(e.logs.String(), secret) {
			t.Fatal("logs disclosed credentials")
		}
	}
	if !strings.Contains(e.logs.String(), "auth_method=basic") || !strings.Contains(e.logs.String(), "endpoint=introspect") {
		t.Fatal("missing authorization log context")
	}
	// A new snapshot removes the old rotation secret without changing permissions.
	rotated := mustRegistry(t, []serviceauth.ClientConfig{{ID: "both", AuthMethods: []string{"basic"}, Permissions: []string{"tokens.introspect"}, BasicSecrets: []string{secondSecret}}})
	e.auth = mustNew(t, serviceauth.Config{Realm: realm, Registry: rotated, RequiredPermission: "tokens.introspect"})
	res, ch := e.do(basicHeader("both", basicSecret))
	expect(t, res, ch, serviceauth.Unauthenticated, []string{`Basic realm="accountsvc-introspection"`})
	res, ch = e.do(basicHeader("both", secondSecret))
	expect(t, res, ch, serviceauth.OK, nil)
}

func TestUnifiedCapabilityAndConstructor(t *testing.T) {
	e := newEnv(t, false, true)
	registry := mustRegistry(t, []serviceauth.ClientConfig{{ID: "svc", AuthMethods: []string{"bearer"}, BearerSubjects: []string{"machine"}, Permissions: []string{"purged_users.read"}}})
	a := mustNew(t, serviceauth.Config{Realm: realm, Registry: registry, RequiredPermission: "tokens.introspect", BearerVerifier: tokensFor(t, e)})
	if a.Enabled() {
		t.Fatal("method enabled a capability without permission")
	}
	e.auth = a
	e.p.SetFail(true)
	res, _ := e.do(e.bearer(e.p.Claims(e.now, "svc"), "initial"))
	if res.Outcome != serviceauth.Unauthenticated || res.BearerAttempted {
		t.Fatalf("disabled capability inspected credentials: %+v", res)
	}
	for _, cfg := range []serviceauth.Config{
		{Realm: realm, Registry: registry, RequiredPermission: "purged_users.read"},
		{Realm: realm, Registry: registry, RequiredPermission: "*", BearerVerifier: tokensFor(t, e)},
		{Realm: realm, Registry: registry, BearerVerifier: tokensFor(t, e)},
		{Realm: realm, Registry: mustRegistry(t, nil), RequiredPermission: "tokens.introspect", BearerVerifier: tokensFor(t, e)},
	} {
		if _, err := serviceauth.New(cfg); err == nil {
			t.Fatal("invalid unified config accepted")
		}
	}
	empty := mustNew(t, serviceauth.Config{Realm: realm, Registry: mustRegistry(t, nil), RequiredPermission: "tokens.introspect"})
	if empty.Enabled() {
		t.Fatal("explicit empty registry enabled")
	}
}

func TestUnifiedBasicSurvivesProviderFailure(t *testing.T) {
	e := newEnv(t, false, true)
	registry := mustRegistry(t, []serviceauth.ClientConfig{{ID: "svc", AuthMethods: []string{"basic", "bearer"}, BearerSubjects: []string{"machine"}, Permissions: []string{"tokens.introspect"}, BasicSecrets: []string{basicSecret}}})
	e.auth = mustNew(t, serviceauth.Config{Realm: realm, Registry: registry, RequiredPermission: "tokens.introspect", BearerVerifier: tokensFor(t, e)})
	e.p.SetFail(true)
	res, ch := e.do(e.bearer(e.p.Claims(e.now, "svc"), "initial"))
	expect(t, res, ch, serviceauth.Unavailable, nil)
	res, ch = e.do(basicHeader("svc", basicSecret))
	expect(t, res, ch, serviceauth.OK, nil)
}

func TestUnifiedRejectsInvalidClaimsAndAlgorithms(t *testing.T) {
	e := newEnv(t, false, true)
	registry := mustRegistry(t, []serviceauth.ClientConfig{{ID: "svc", AuthMethods: []string{"bearer"}, BearerSubjects: []string{"machine"}, Permissions: []string{"tokens.introspect"}}})
	e.auth = mustNew(t, serviceauth.Config{Realm: realm, Registry: registry, RequiredPermission: "tokens.introspect", BearerVerifier: tokensFor(t, e)})
	for _, mutate := range []func(jwt.MapClaims){func(c jwt.MapClaims) { delete(c, "azp") }, func(c jwt.MapClaims) { delete(c, "iat") }, func(c jwt.MapClaims) { c["aud"] = "consumer" }, func(c jwt.MapClaims) { c["iss"] = "consumer" }} {
		c := e.p.Claims(e.now, "svc")
		mutate(c)
		res, ch := e.do(e.bearer(c, "initial"))
		expect(t, res, ch, serviceauth.Unauthenticated, []string{`Bearer realm="accountsvc-introspection", error="invalid_token"`})
	}
	raw, err := jwt.NewWithClaims(jwt.SigningMethodNone, e.p.Claims(e.now, "svc")).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatal(err)
	}
	res, ch := e.do("Bearer " + raw)
	expect(t, res, ch, serviceauth.Unauthenticated, []string{`Bearer realm="accountsvc-introspection", error="invalid_token"`})
	e.p.SetFail(true)
	// Use a fresh verifier so cached signing keys cannot hide provider failure.
	e.auth = mustNew(t, serviceauth.Config{Realm: realm, Registry: registry, RequiredPermission: "tokens.introspect", BearerVerifier: tokensFor(t, e)})
	res, ch = e.do(e.bearer(e.p.Claims(e.now, "svc"), "initial"))
	expect(t, res, ch, serviceauth.Unavailable, nil)
}
