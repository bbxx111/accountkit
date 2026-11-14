package serviceauth_test

import (
	"strings"
	"testing"

	"github.com/bbxx111/accountkit/internal/accountsvc/serviceauth"
)

func mustRegistry(t *testing.T, clients []serviceauth.ClientConfig) *serviceauth.Registry {
	t.Helper()
	r, err := serviceauth.NewRegistry(clients)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRegistryRejectsInvalidClients(t *testing.T) {
	valid := func() serviceauth.ClientConfig {
		return serviceauth.ClientConfig{ID: "svc", AuthMethods: []string{"basic"}, Permissions: []string{"tokens.introspect"}, BasicSecrets: []string{basicSecret}}
	}
	for _, field := range []string{"id empty", "id colon", "id whitespace", "id control", "id too long", "methods empty", "method unknown", "method duplicate", "permissions empty", "permission unknown", "permission duplicate", "secret missing", "secret invalid", "secret duplicate", "secret without basic"} {
		t.Run(field, func(t *testing.T) {
			c := valid()
			switch field {
			case "id empty":
				c.ID = ""
			case "id colon":
				c.ID = "a:b"
			case "id whitespace":
				c.ID = "a b"
			case "id control":
				c.ID = "a\x00b"
			case "id too long":
				c.ID = strings.Repeat("a", 257)
			case "methods empty":
				c.AuthMethods = nil
			case "method unknown":
				c.AuthMethods = []string{"Basic"}
			case "method duplicate":
				c.AuthMethods = []string{"basic", "basic"}
			case "permissions empty":
				c.Permissions = nil
			case "permission unknown":
				c.Permissions = []string{"identity.resolve"}
			case "permission duplicate":
				c.Permissions = []string{"tokens.introspect", "tokens.introspect"}
			case "secret missing":
				c.BasicSecrets = nil
			case "secret invalid":
				c.BasicSecrets = []string{"private-invalid-secret"}
			case "secret duplicate":
				c.BasicSecrets = []string{basicSecret, basicSecret}
			case "secret without basic":
				c.AuthMethods = []string{"bearer"}
			}
			_, err := serviceauth.NewRegistry([]serviceauth.ClientConfig{c})
			if err == nil {
				t.Fatal("invalid client accepted")
			}
			if strings.Contains(err.Error(), basicSecret) || strings.Contains(err.Error(), "private-invalid-secret") {
				t.Fatal("error disclosed secret")
			}
		})
	}
	for _, sameID := range []bool{false, true} {
		a, b := valid(), valid()
		if !sameID {
			b.ID = "other"
		}
		if _, err := serviceauth.NewRegistry([]serviceauth.ClientConfig{a, b}); err == nil {
			t.Fatal("duplicate identity or shared secret accepted")
		}
	}
}

func TestRegistryEmptyAndSnapshot(t *testing.T) {
	empty := mustRegistry(t, nil)
	if empty.HasPermission("tokens.introspect") || empty.BearerEnabled() {
		t.Fatal("empty registry enabled")
	}
	clients := []serviceauth.ClientConfig{{ID: "svc", AuthMethods: []string{"basic", "bearer"}, Permissions: []string{"tokens.introspect", "purged_users.read"}, BasicSecrets: []string{basicSecret}}}
	registry := mustRegistry(t, clients)
	clients[0].ID = "changed"
	clients[0].AuthMethods[1] = "changed"
	clients[0].Permissions[0] = "changed"
	clients[0].BasicSecrets[0] = "changed"
	if !registry.HasPermission("tokens.introspect") || !registry.HasPermission("purged_users.read") || !registry.BearerEnabled() || registry.HasPermission("*") {
		t.Fatal("registry changed with caller's slices")
	}
	e := newEnv(t, false, true)
	e.auth = mustNew(t, serviceauth.Config{Realm: realm, Registry: registry, RequiredPermission: "tokens.introspect", BearerVerifier: tokensFor(t, e)})
	res, ch := e.do(basicHeader("svc", basicSecret))
	expect(t, res, ch, serviceauth.OK, nil)
	res, ch = e.do(e.bearer(e.p.Claims(e.now, "svc"), "initial"))
	expect(t, res, ch, serviceauth.OK, nil)
}
