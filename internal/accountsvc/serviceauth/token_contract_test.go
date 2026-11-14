package serviceauth_test

import (
	"github.com/bbxx111/accountkit/internal/accountsvc/oidcjwt"
	"github.com/bbxx111/accountkit/internal/accountsvc/serviceauth"
	"github.com/golang-jwt/jwt/v5"
	"testing"
)

func TestBearerScopeAndMachineSubject(t *testing.T) {
	e := newEnv(t, true, true)
	for _, tc := range []struct {
		name   string
		mutate func(jwt.MapClaims)
		want   serviceauth.Outcome
	}{
		{"valid", func(c jwt.MapClaims) {}, serviceauth.OK},
		{"unknown does not grant", func(c jwt.MapClaims) { c["scope"] = "* unknown TOKENS.INTROSPECT" }, serviceauth.Forbidden},
		{"extra and duplicate scope", func(c jwt.MapClaims) { c["scope"] = "unknown tokens.introspect tokens.introspect" }, serviceauth.OK},
		{"multi audience wrong human", func(c jwt.MapClaims) {
			c["aud"] = []string{"admin-api", "accountsvc-service"}
			c["sub"] = "human-admin"
		}, serviceauth.Unauthenticated},
		{"missing scope", func(c jwt.MapClaims) { delete(c, "scope") }, serviceauth.Forbidden},
		{"empty scope", func(c jwt.MapClaims) { c["scope"] = "" }, serviceauth.Forbidden},
		{"null scope", func(c jwt.MapClaims) { c["scope"] = nil }, serviceauth.Unauthenticated},
		{"array scope", func(c jwt.MapClaims) { c["scope"] = []string{"tokens.introspect"} }, serviceauth.Unauthenticated},
		{"wrong subject", func(c jwt.MapClaims) { c["sub"] = "human-admin" }, serviceauth.Unauthenticated},
		{"empty subject", func(c jwt.MapClaims) { c["sub"] = "" }, serviceauth.Unauthenticated},
		{"wrong subject precedes no permission", func(c jwt.MapClaims) { c["azp"] = "purge-client"; c["sub"] = "human"; delete(c, "scope") }, serviceauth.Unauthenticated},
		{"unknown precedes invalid scope", func(c jwt.MapClaims) { c["azp"] = "unknown"; c["scope"] = nil }, serviceauth.Forbidden},
		{"unknown still requires subject", func(c jwt.MapClaims) { c["azp"] = "unknown"; delete(c, "sub") }, serviceauth.Unauthenticated},
		{"scope cannot grant unregistered permission", func(c jwt.MapClaims) { c["azp"] = "purge-client"; c["scope"] = "tokens.introspect" }, serviceauth.Forbidden},
		{"missing subject", func(c jwt.MapClaims) { delete(c, "sub") }, serviceauth.Unauthenticated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := e.p.Claims(e.now, "intro-client")
			c["sub"] = "machine"
			tc.mutate(c)
			res, _ := e.do(e.bearer(c, "initial"))
			if res.Outcome != tc.want {
				t.Fatalf("outcome=%v want %v", res.Outcome, tc.want)
			}
		})
	}
}

func TestBearerScopeGrammar(t *testing.T) {
	e := newEnv(t, false, true)
	for _, scope := range []string{" tokens.introspect", "tokens.introspect ", "tokens.introspect  unknown", "tokens.introspect\tunknown", `tokens.introspect "unknown"`, `tokens.introspect x\y`, "tokens.introspect \u4e2d", "tokens.introspect\x7f", "tokens.introspect\nunknown"} {
		t.Run(scope, func(t *testing.T) {
			c := e.p.Claims(e.now, "intro-client")
			c["scope"] = scope
			res, ch := e.do(e.bearer(c, "initial"))
			expect(t, res, ch, serviceauth.Unauthenticated, []string{`Bearer realm="accountsvc-introspection", error="invalid_token"`})
		})
	}
}

func TestServiceTokenProfilesDoNotFallback(t *testing.T) {
	for _, profile := range []string{"", "default", "rfc9068"} {
		t.Run(profile, func(t *testing.T) {
			e := newEnv(t, false, true)
			v, err := oidcjwt.New(e.p.URL(), "accountsvc-service", oidcjwt.Options{HTTPClient: e.p.Client(), Now: e.clock, Profile: profile})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(v.Close)
			registry := mustRegistry(t, []serviceauth.ClientConfig{{ID: "intro-client", AuthMethods: []string{"bearer"}, BearerSubjects: []string{"machine"}, Permissions: []string{"tokens.introspect"}}})
			e.auth = mustNew(t, serviceauth.Config{Realm: realm, Registry: registry, RequiredPermission: "tokens.introspect", BearerVerifier: v})
			for _, tc := range []struct {
				name   string
				mutate func(jwt.MapClaims)
				want   serviceauth.Outcome
			}{
				{"valid", func(c jwt.MapClaims) {}, serviceauth.OK},
				{"other claim cannot replace selected", func(c jwt.MapClaims) {
					if profile != "rfc9068" {
						delete(c, "azp")
					} else {
						delete(c, "client_id")
					}
				}, serviceauth.Unauthenticated},
				{"other claim ignored", func(c jwt.MapClaims) {
					if profile != "rfc9068" {
						c["client_id"] = "unknown"
					} else {
						c["azp"] = "unknown"
					}
				}, serviceauth.OK},
				{"subject enforced", func(c jwt.MapClaims) { c["sub"] = "human" }, serviceauth.Unauthenticated},
				{"scope enforced", func(c jwt.MapClaims) { delete(c, "scope") }, serviceauth.Forbidden},
			} {
				t.Run(tc.name, func(t *testing.T) {
					c := e.p.Claims(e.now, "intro-client")
					c["client_id"] = "intro-client"
					c["jti"] = "unique"
					tc.mutate(c)
					token := jwt.NewWithClaims(jwt.SigningMethodRS256, c)
					token.Header["kid"] = "initial"
					if profile == "rfc9068" {
						token.Header["typ"] = "at+jwt"
					}
					raw, err := token.SignedString(e.p.Key)
					if err != nil {
						t.Fatal(err)
					}
					res, _ := e.do("Bearer " + raw)
					if res.Outcome != tc.want {
						t.Fatalf("outcome=%v want=%v", res.Outcome, tc.want)
					}
				})
			}
		})
	}
}
