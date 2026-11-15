package accountsvc

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bbxx111/accountkit/internal/accountsvc/serviceauth"
)

func TestConfigServiceClientRejectsRemovedSettings(t *testing.T) {
	for _, declared := range []bool{false, true} {
		for _, name := range legacyServiceVariables {
			for _, value := range []string{"{}", "[]", "sensitive-removed-config"} {
				t.Run(name+value+map[bool]string{false: "/alone", true: "/mixed"}[declared], func(t *testing.T) {
					if declared {
						unifiedClientEnvironment(t)
					} else {
						serveEnvironment(t)
						clearServiceClientEnvironment(t)
					}
					t.Setenv(name, value)
					_, err := LoadConfig("serve")
					if err == nil || !strings.Contains(err.Error(), name) || !strings.Contains(err.Error(), "removed") || strings.Contains(err.Error(), value) {
						t.Fatalf("removed setting must fail with its name and fixed reason only: %v", err)
					}
				})
			}
		}
	}
}

func TestConfigServiceClientEmptyRegistry(t *testing.T) {
	for _, mode := range []string{"production", "development"} {
		t.Run(mode, func(t *testing.T) {
			for _, raw := range []string{"unset", "", " \t "} {
				t.Run(raw, func(t *testing.T) {
					serveEnvironment(t)
					t.Setenv("ACCOUNTSVC_MODE", mode)
					clearServiceClientEnvironment(t)
					if raw != "unset" {
						t.Setenv("ACCOUNTSVC_SERVICE_CLIENTS", raw)
					}
					for _, name := range legacyServiceVariables {
						t.Setenv(name, " \t ")
					}
					cfg, err := LoadConfig("serve")
					if err != nil {
						t.Fatal(err)
					}
					if cfg.ServiceClients == nil || cfg.ServiceClients.HasPermission("tokens.introspect") || cfg.ServiceClients.HasPermission("purged_users.read") || cfg.Service.Enabled() {
						t.Fatal("missing, empty and whitespace lists must produce an empty registry")
					}
				})
			}
		})
	}
}

func TestServiceAuthenticatorNilRegistryIsEmpty(t *testing.T) {
	auth, err := serviceauth.New(serviceauth.Config{Realm: "test", RequiredPermission: "tokens.introspect"})
	if err != nil {
		t.Fatal(err)
	}
	if auth.Enabled() {
		t.Fatal("nil registry enabled a capability")
	}
	request := httptest.NewRequest("POST", "/", nil)
	request.SetBasicAuth("unregistered", "secret")
	if res := auth.Authenticate(request); res.Outcome != serviceauth.Unauthenticated {
		t.Fatal(res)
	}
	if _, err := serviceauth.New(serviceauth.Config{Realm: "test"}); err == nil {
		t.Fatal("nil registry bypassed required permission validation")
	}
}
