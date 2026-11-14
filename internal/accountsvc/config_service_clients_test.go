package accountsvc

import (
	"bytes"
	"encoding/base64"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bbxx111/accountkit/internal/accountsvc/serviceauth"
)

func unifiedClientEnvironment(t *testing.T) {
	t.Helper()
	serveEnvironment(t)
	clearServiceClientEnvironment(t)
	t.Setenv("ACCOUNTSVC_SERVICE_CLIENTS", "AI_FOOD")
	t.Setenv("ACCOUNTSVC_SERVICE_CLIENT_AI_FOOD_ID", "ai-food-server")
	t.Setenv("ACCOUNTSVC_SERVICE_CLIENT_AI_FOOD_AUTH_METHODS", "basic")
	t.Setenv("ACCOUNTSVC_SERVICE_CLIENT_AI_FOOD_PERMISSIONS", "tokens.introspect,purged_users.read")
	t.Setenv("ACCOUNTSVC_SERVICE_CLIENT_AI_FOOD_BASIC_SECRETS", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{11}, 32)))
}

func TestConfigServiceClientRejectsInvalidDeclarations(t *testing.T) {
	secret := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{11}, 32))
	for _, tc := range []struct{ name, field, value string }{
		{"alias lowercase", "SERVICE_CLIENTS", "sensitive-alias"},
		{"alias numeric", "SERVICE_CLIENTS", "1BAD"},
		{"alias too long", "SERVICE_CLIENTS", strings.Repeat("A", 33)},
		{"alias duplicate", "SERVICE_CLIENTS", "AI_FOOD,AI_FOOD"},
		{"alias empty item", "SERVICE_CLIENTS", "AI_FOOD,"},
		{"unknown field", "SERVICE_CLIENT_AI_FOOD_AUTH_METHOD", "sensitive-value"},
		{"undeclared empty field", "SERVICE_CLIENT_OTHER_ID", ""},
		{"empty ID", "SERVICE_CLIENT_AI_FOOD_ID", ""},
		{"padded ID", "SERVICE_CLIENT_AI_FOOD_ID", " ai-food-server "},
		{"colon ID", "SERVICE_CLIENT_AI_FOOD_ID", "sensitive:id"},
		{"control ID", "SERVICE_CLIENT_AI_FOOD_ID", "sensitive\x01id"},
		{"unicode whitespace ID", "SERVICE_CLIENT_AI_FOOD_ID", "sensitive\u00a0id"},
		{"long ID", "SERVICE_CLIENT_AI_FOOD_ID", strings.Repeat("x", 257)},
		{"empty methods", "SERVICE_CLIENT_AI_FOOD_AUTH_METHODS", ""},
		{"unknown method", "SERVICE_CLIENT_AI_FOOD_AUTH_METHODS", "sensitive-method"},
		{"case method", "SERVICE_CLIENT_AI_FOOD_AUTH_METHODS", "Basic"},
		{"wildcard method", "SERVICE_CLIENT_AI_FOOD_AUTH_METHODS", "*"},
		{"empty method item", "SERVICE_CLIENT_AI_FOOD_AUTH_METHODS", "basic,"},
		{"duplicate method", "SERVICE_CLIENT_AI_FOOD_AUTH_METHODS", "basic,basic"},
		{"empty permissions", "SERVICE_CLIENT_AI_FOOD_PERMISSIONS", ""},
		{"unknown permission", "SERVICE_CLIENT_AI_FOOD_PERMISSIONS", "sensitive-permission"},
		{"future permission", "SERVICE_CLIENT_AI_FOOD_PERMISSIONS", "identities.resolve"},
		{"case permission", "SERVICE_CLIENT_AI_FOOD_PERMISSIONS", "TOKENS.INTROSPECT"},
		{"wildcard permission", "SERVICE_CLIENT_AI_FOOD_PERMISSIONS", "*"},
		{"empty permission item", "SERVICE_CLIENT_AI_FOOD_PERMISSIONS", ",tokens.introspect"},
		{"duplicate permission", "SERVICE_CLIENT_AI_FOOD_PERMISSIONS", "tokens.introspect,tokens.introspect"},
		{"missing secrets", "SERVICE_CLIENT_AI_FOOD_BASIC_SECRETS", ""},
		{"invalid base64", "SERVICE_CLIENT_AI_FOOD_BASIC_SECRETS", "sensitive-secret"},
		{"short secrets", "SERVICE_CLIENT_AI_FOOD_BASIC_SECRETS", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{11}, 31))},
		{"unpadded base64", "SERVICE_CLIENT_AI_FOOD_BASIC_SECRETS", strings.TrimRight(secret, "=")},
		{"noncanonical pad bits", "SERVICE_CLIENT_AI_FOOD_BASIC_SECRETS", secret[:len(secret)-2] + "t="},
		{"base64 newline", "SERVICE_CLIENT_AI_FOOD_BASIC_SECRETS", secret[:5] + "\n" + secret[5:]},
		{"empty secret item", "SERVICE_CLIENT_AI_FOOD_BASIC_SECRETS", secret + ","},
		{"duplicate secret", "SERVICE_CLIENT_AI_FOOD_BASIC_SECRETS", secret + "," + secret},
	} {
		t.Run(tc.name, func(t *testing.T) {
			unifiedClientEnvironment(t)
			t.Setenv(envPrefix+tc.field, tc.value)
			_, err := LoadConfig("serve")
			if err == nil || !strings.Contains(err.Error(), envPrefix+tc.field) || strings.Contains(err.Error(), "sensitive-") || strings.Contains(err.Error(), secret) {
				t.Fatalf("expected redacted variable error for %s, got %v", tc.field, err)
			}
		})
	}
}

func TestConfigServiceClientModeConflicts(t *testing.T) {
	for _, name := range []string{"INTROSPECTION_BASIC_CLIENTS", "PURGE_FEED_BASIC_CLIENTS", "INTROSPECTION_BEARER_CLIENTS", "PURGE_FEED_BEARER_CLIENTS"} {
		for _, value := range []string{"{}", "[]", "sensitive-old-value"} {
			t.Run(name+value, func(t *testing.T) {
				unifiedClientEnvironment(t)
				t.Setenv(envPrefix+name, value)
				if _, err := LoadConfig("serve"); err == nil || !strings.Contains(err.Error(), envPrefix+name) || strings.Contains(err.Error(), "sensitive-old-value") {
					t.Fatalf("expected mode conflict, got %v", err)
				}
			})
		}
	}
	for _, raw := range []string{"unset", "", " \t "} {
		t.Run("orphan field without clients/"+raw, func(t *testing.T) {
			serveEnvironment(t)
			clearServiceClientEnvironment(t)
			if raw != "unset" {
				t.Setenv("ACCOUNTSVC_SERVICE_CLIENTS", raw)
			}
			t.Setenv("ACCOUNTSVC_SERVICE_CLIENT_ORPHAN_ID", "")
			if _, err := LoadConfig("serve"); err == nil || !strings.Contains(err.Error(), "ACCOUNTSVC_SERVICE_CLIENT_ORPHAN_ID") {
				t.Fatalf("expected orphan field rejection: %v", err)
			}
		})
	}
}

func unifiedBearerEnvironment(t *testing.T) {
	t.Helper()
	unifiedClientEnvironment(t)
	t.Setenv("ACCOUNTSVC_SERVICE_CLIENT_AI_FOOD_AUTH_METHODS", "basic,bearer")
	t.Setenv("ACCOUNTSVC_SERVICE_ISSUER", "https://idp.example.test/realms/svc")
	t.Setenv("ACCOUNTSVC_SERVICE_AUDIENCE", "accountsvc-service")
}

func TestConfigServiceClientTrustRejections(t *testing.T) {
	for _, tc := range []struct {
		name     string
		set      map[string]string
		variable string
	}{
		{"missing issuer", map[string]string{"SERVICE_ISSUER": ""}, "SERVICE_ISSUER"},
		{"missing audience", map[string]string{"SERVICE_AUDIENCE": ""}, "SERVICE_AUDIENCE"},
		{"http production", map[string]string{"SERVICE_ISSUER": "http://sensitive.example.test"}, "SERVICE_ISSUER"},
		{"issuer consumer", map[string]string{"JWT_ISSUER": "https://idp.example.test/realms/svc/"}, "SERVICE_ISSUER"},
		{"issuer credentials", map[string]string{"SERVICE_ISSUER": "https://sensitive:password@example.test"}, "SERVICE_ISSUER"},
		{"issuer query", map[string]string{"SERVICE_ISSUER": "https://idp.example.test?secret=sensitive"}, "SERVICE_ISSUER"},
		{"issuer fragment", map[string]string{"SERVICE_ISSUER": "https://idp.example.test#sensitive"}, "SERVICE_ISSUER"},
		{"consumer audience", map[string]string{"SERVICE_AUDIENCE": "consumer"}, "SERVICE_AUDIENCE"},
		{"client audience", map[string]string{"SERVICE_AUDIENCE": "ai-food-server"}, "SERVICE_AUDIENCE"},
		{"padded audience", map[string]string{"SERVICE_AUDIENCE": " sensitive "}, "SERVICE_AUDIENCE"},
		{"Google audience", map[string]string{"GOOGLE_CLIENT_IDS": "web.apps.googleusercontent.com", "SERVICE_AUDIENCE": "web.apps.googleusercontent.com"}, "SERVICE_AUDIENCE"},
		{"Apple audience", map[string]string{"APPLE_BUNDLE_IDS": "com.example.app", "SERVICE_AUDIENCE": "com.example.app"}, "SERVICE_AUDIENCE"},
		{"QQ audience", map[string]string{"QQ_APP_IDS": "1100000001", "SERVICE_AUDIENCE": "1100000001"}, "SERVICE_AUDIENCE"},
		{"WeChat audience", map[string]string{"WECHAT_APPS": "test-app:synthetic-provider-secret", "SERVICE_AUDIENCE": "test-app"}, "SERVICE_AUDIENCE"},
		{"Weibo audience", map[string]string{"WEIBO_APP_IDS": "1234567890", "SERVICE_AUDIENCE": "1234567890"}, "SERVICE_AUDIENCE"},
		{"Facebook audience", map[string]string{"FACEBOOK_APP_ID": "synthetic-app-id", "FACEBOOK_APP_SECRET": "synthetic-secret", "SERVICE_AUDIENCE": "synthetic-app-id"}, "SERVICE_AUDIENCE"},
		{"OIDC audience", map[string]string{"OIDC_PROVIDERS": "CORP_SSO", "OIDC_CORP_SSO_ISSUER": "https://sso.corp.example", "OIDC_CORP_SSO_JWKS_URL": "https://sso.corp.example/jwks", "OIDC_CORP_SSO_CLIENT_IDS": "synthetic-corp-client", "SERVICE_AUDIENCE": "synthetic-corp-client"}, "SERVICE_AUDIENCE"},
		{"admin audience", map[string]string{"ADMIN_ENABLED": "true", "ADMIN_ISSUER": "https://idp.example.test/realms/svc", "ADMIN_AUDIENCE": "accountsvc-service"}, "SERVICE_AUDIENCE"},
		{"basic only with issuer", map[string]string{"SERVICE_CLIENT_AI_FOOD_AUTH_METHODS": "basic", "SERVICE_AUDIENCE": ""}, "SERVICE_ISSUER"},
		{"basic only with audience", map[string]string{"SERVICE_CLIENT_AI_FOOD_AUTH_METHODS": "basic", "SERVICE_ISSUER": ""}, "SERVICE_AUDIENCE"},
		{"bearer with secrets", map[string]string{"SERVICE_CLIENT_AI_FOOD_AUTH_METHODS": "bearer"}, "SERVICE_CLIENT_AI_FOOD_BASIC_SECRETS"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			unifiedBearerEnvironment(t)
			for k, v := range tc.set {
				t.Setenv(envPrefix+k, v)
			}
			if _, err := LoadConfig("serve"); err == nil || !strings.Contains(err.Error(), envPrefix+tc.variable) || strings.Contains(err.Error(), "sensitive") {
				t.Fatalf("expected redacted trust error for %s, got %v", tc.variable, err)
			}
		})
	}
}

func TestConfigServiceClientDuplicateIdentityOrSecret(t *testing.T) {
	for _, duplicateID := range []bool{false, true} {
		t.Run(map[bool]string{true: "duplicate ID", false: "shared secret"}[duplicateID], func(t *testing.T) {
			unifiedClientEnvironment(t)
			t.Setenv("ACCOUNTSVC_SERVICE_CLIENTS", "AI_FOOD,AI_FOOD_OTHER")
			id := "other-server"
			if duplicateID {
				id = "ai-food-server"
			}
			t.Setenv("ACCOUNTSVC_SERVICE_CLIENT_AI_FOOD_OTHER_ID", id)
			t.Setenv("ACCOUNTSVC_SERVICE_CLIENT_AI_FOOD_OTHER_AUTH_METHODS", "basic")
			t.Setenv("ACCOUNTSVC_SERVICE_CLIENT_AI_FOOD_OTHER_PERMISSIONS", "tokens.introspect")
			t.Setenv("ACCOUNTSVC_SERVICE_CLIENT_AI_FOOD_OTHER_BASIC_SECRETS", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{11}, 32)))
			field := "BASIC_SECRETS"
			if duplicateID {
				field = "ID"
			}
			if _, err := LoadConfig("serve"); err == nil || !strings.Contains(err.Error(), "ACCOUNTSVC_SERVICE_CLIENT_AI_FOOD_OTHER_"+field) {
				t.Fatalf("expected duplicate rejection: %v", err)
			}
		})
	}
}

func TestConfigServiceClientLoadedPolicies(t *testing.T) {
	unifiedClientEnvironment(t)
	oldSecret := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{11}, 32))
	newSecret := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{12}, 33))
	t.Setenv("ACCOUNTSVC_SERVICE_CLIENTS", " AI_FOOD ")
	t.Setenv("ACCOUNTSVC_SERVICE_CLIENT_AI_FOOD_AUTH_METHODS", " basic ")
	t.Setenv("ACCOUNTSVC_SERVICE_CLIENT_AI_FOOD_BASIC_SECRETS", " "+oldSecret+", "+newSecret+" ")
	c, err := LoadConfig("serve")
	if err != nil {
		t.Fatal(err)
	}
	if c.ServiceClients == nil || c.Service.Enabled() {
		t.Fatal("wrong unified Basic-only mode")
	}
	for _, permission := range []string{"tokens.introspect", "purged_users.read"} {
		if !c.ServiceClients.HasPermission(permission) {
			t.Fatalf("missing configured permission %s", permission)
		}
		auth, err := serviceauth.New(serviceauth.Config{Registry: c.ServiceClients, RequiredPermission: permission, Realm: "test", Endpoint: "test"})
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{oldSecret, newSecret} {
			r := httptest.NewRequest("POST", "/", nil)
			r.SetBasicAuth("ai-food-server", secret)
			if result := auth.Authenticate(r); result.Outcome != serviceauth.OK || result.ClientID != "ai-food-server" {
				t.Fatalf("loaded credentials rejected: %+v", result)
			}
		}
	}
}

func TestConfigServiceClientBearerModes(t *testing.T) {
	for _, mode := range []string{"production", "development"} {
		t.Run(mode, func(t *testing.T) {
			unifiedBearerEnvironment(t)
			t.Setenv("ACCOUNTSVC_MODE", mode)
			t.Setenv("ACCOUNTSVC_SERVICE_CLIENT_AI_FOOD_AUTH_METHODS", "bearer")
			t.Setenv("ACCOUNTSVC_SERVICE_CLIENT_AI_FOOD_BASIC_SECRETS", "")
			issuer := "https://idp.example.test/realms/svc"
			if mode == "development" {
				issuer = "http://idp.example.test/realms/svc"
			}
			t.Setenv("ACCOUNTSVC_SERVICE_ISSUER", issuer)
			t.Setenv("ACCOUNTSVC_ADMIN_ENABLED", "true")
			t.Setenv("ACCOUNTSVC_ADMIN_ISSUER", issuer)
			t.Setenv("ACCOUNTSVC_ADMIN_AUDIENCE", "admin-api")
			c, err := LoadConfig("serve")
			if err != nil {
				t.Fatal(err)
			}
			if c.ServiceClients == nil || !c.Service.Enabled() || c.Service.Issuer != issuer || c.Service.AllowHTTP != (mode == "development") {
				t.Fatal("unified Bearer not enabled independently from legacy lists")
			}
		})
	}
	t.Run("basic client ID may equal audience", func(t *testing.T) {
		unifiedBearerEnvironment(t)
		t.Setenv("ACCOUNTSVC_SERVICE_CLIENTS", "AI_FOOD,LOCAL")
		t.Setenv("ACCOUNTSVC_SERVICE_CLIENT_LOCAL_ID", "accountsvc-service")
		t.Setenv("ACCOUNTSVC_SERVICE_CLIENT_LOCAL_AUTH_METHODS", "basic")
		t.Setenv("ACCOUNTSVC_SERVICE_CLIENT_LOCAL_PERMISSIONS", "purged_users.read")
		t.Setenv("ACCOUNTSVC_SERVICE_CLIENT_LOCAL_BASIC_SECRETS", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{12}, 32)))
		if _, err := LoadConfig("serve"); err != nil {
			t.Fatalf("only Bearer IDs reserve audiences: %v", err)
		}
	})
}

func TestConfigServiceClientMigrateIgnoresFields(t *testing.T) {
	for _, list := range []string{"", "invalid-alias,"} {
		t.Run(list, func(t *testing.T) {
			configEnvironment(t)
			t.Setenv("ACCOUNTSVC_SERVICE_CLIENTS", list)
			for _, field := range []string{"ID", "AUTH_METHODS", "PERMISSIONS", "BASIC_SECRETS", "TYPO"} {
				t.Setenv("ACCOUNTSVC_SERVICE_CLIENT_ORPHAN_"+field, "invalid-sensitive-value")
			}
			t.Setenv("ACCOUNTSVC_SERVICE_ISSUER", "http://unreachable.invalid")
			t.Setenv("ACCOUNTSVC_SERVICE_AUDIENCE", " invalid ")
			for _, name := range legacyServiceVariables {
				t.Setenv(name, "invalid-sensitive-old-value")
			}
			c, err := LoadConfig("migrate")
			if err != nil {
				t.Fatal(err)
			}
			if c.ServiceClients != nil || c.Service.Enabled() || c.Service.Issuer != "" {
				t.Fatal("migrate parsed service configuration")
			}
		})
	}
}

func TestConfigServiceClientEnvironmentIsolation(t *testing.T) {
	t.Setenv("ACCOUNTSVC_SERVICE_CLIENTS", "")
	t.Setenv("ACCOUNTSVC_SERVICE_CLIENT_ORPHAN_ID", "")
	serveEnvironment(t)
	c, err := LoadConfig("serve")
	if err != nil || c.ServiceClients == nil || !c.ServiceClients.HasPermission("tokens.introspect") {
		t.Fatalf("test fixture must unset presence-sensitive variables: %v", err)
	}
}

func TestConfigServiceClientIdentifierLimits(t *testing.T) {
	for _, id := range []string{"x", strings.Repeat("x", 256), strings.Repeat("中", 85) + "x"} {
		t.Run(id[:1], func(t *testing.T) {
			serveEnvironment(t)
			clearServiceClientEnvironment(t)
			alias := strings.Repeat("A", 32)
			t.Setenv("ACCOUNTSVC_SERVICE_CLIENTS", alias)
			t.Setenv("ACCOUNTSVC_SERVICE_CLIENT_"+alias+"_ID", id)
			t.Setenv("ACCOUNTSVC_SERVICE_CLIENT_"+alias+"_AUTH_METHODS", "basic")
			t.Setenv("ACCOUNTSVC_SERVICE_CLIENT_"+alias+"_PERMISSIONS", "tokens.introspect")
			t.Setenv("ACCOUNTSVC_SERVICE_CLIENT_"+alias+"_BASIC_SECRETS", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{11}, 32)))
			if _, err := LoadConfig("serve"); err != nil {
				t.Fatalf("valid identifier boundary rejected: %v", err)
			}
		})
	}
}
