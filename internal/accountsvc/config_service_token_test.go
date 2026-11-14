package accountsvc

import (
	"strings"
	"testing"
)

func serviceTokenEnvironment(t *testing.T) {
	t.Helper()
	serveEnvironment(t)
	t.Setenv("ACCOUNTSVC_SERVICE_ISSUER", "https://idp.example.test/realms/svc")
	t.Setenv("ACCOUNTSVC_SERVICE_AUDIENCE", "accountsvc-service")
	t.Setenv("ACCOUNTSVC_SERVICE_CLIENTS", "TEST_CLIENT,INTRO,PURGE")
	for _, c := range []struct{ alias, id, permission string }{{"INTRO", "intro-client", "tokens.introspect"}, {"PURGE", "purge-client", "purged_users.read"}} {
		prefix := "ACCOUNTSVC_SERVICE_CLIENT_" + c.alias + "_"
		t.Setenv(prefix+"ID", c.id)
		t.Setenv(prefix+"AUTH_METHODS", "bearer")
		t.Setenv(prefix+"PERMISSIONS", c.permission)
	}
}

func TestConfigServiceTokens(t *testing.T) {
	serviceTokenEnvironment(t)
	c, err := LoadConfig("serve")
	if err != nil {
		t.Fatal(err)
	}
	if c.Service.Issuer != "https://idp.example.test/realms/svc" || c.Service.Audience != "accountsvc-service" || !c.Service.Enabled() || !c.ServiceClients.HasPermission("purged_users.read") {
		t.Fatalf("Service = %+v", c.Service)
	}
}

func TestConfigServiceTokensOptional(t *testing.T) {
	serveEnvironment(t)
	c, err := LoadConfig("serve")
	if err != nil {
		t.Fatal(err)
	}
	if c.Service.Enabled() {
		t.Fatal("service tokens enabled without configuration")
	}
}

func TestConfigIntrospectionWithOnlyTokens(t *testing.T) {
	serviceTokenEnvironment(t)
	clearServiceClientEnvironment(t)
	t.Setenv("ACCOUNTSVC_SERVICE_CLIENTS", "INTRO,PURGE")
	for _, c := range []struct{ alias, id, permission string }{{"INTRO", "intro-client", "tokens.introspect"}, {"PURGE", "purge-client", "purged_users.read"}} {
		prefix := "ACCOUNTSVC_SERVICE_CLIENT_" + c.alias + "_"
		t.Setenv(prefix+"ID", c.id)
		t.Setenv(prefix+"AUTH_METHODS", "bearer")
		t.Setenv(prefix+"PERMISSIONS", c.permission)
	}
	c, err := LoadConfig("serve")
	if err != nil {
		t.Fatal(err)
	}
	if !c.ServiceClients.BearerEnabled() || !c.ServiceClients.HasPermission("tokens.introspect") {
		t.Fatalf("config = %+v", c)
	}
}

// 内省与拉取一样可选：两种方式都未配置时 serve 照常启动，路由返回 503 introspection_not_configured。
func TestConfigIntrospectionOptional(t *testing.T) {
	for _, mode := range []string{"production", "development"} {
		t.Run(mode, func(t *testing.T) {
			serveEnvironment(t)
			clearServiceClientEnvironment(t)
			t.Setenv("ACCOUNTSVC_MODE", mode)
			c, err := LoadConfig("serve")
			if err != nil || c.ServiceClients == nil || c.ServiceClients.HasPermission("tokens.introspect") {
				t.Fatalf("serve without introspection callers: %v", err)
			}
		})
	}
}

func TestConfigServiceTokenRejections(t *testing.T) {
	const secretish = "synthetic-config-value"
	for _, tc := range []struct {
		name     string
		set      map[string]string
		variable string
	}{
		{"issuer not HTTPS", map[string]string{"ACCOUNTSVC_SERVICE_ISSUER": "http://" + secretish}, "ACCOUNTSVC_SERVICE_ISSUER"},
		{"issuer equals consumer issuer", map[string]string{"ACCOUNTSVC_SERVICE_ISSUER": "https://consumer.example.test", "ACCOUNTSVC_JWT_ISSUER": "https://consumer.example.test"}, "ACCOUNTSVC_SERVICE_ISSUER"},
		{"issuer equals consumer issuer up to a trailing slash", map[string]string{"ACCOUNTSVC_SERVICE_ISSUER": "https://consumer.example.test/", "ACCOUNTSVC_JWT_ISSUER": "https://consumer.example.test"}, "ACCOUNTSVC_SERVICE_ISSUER"},
		{"audience equals consumer audience", map[string]string{"ACCOUNTSVC_SERVICE_AUDIENCE": "consumer"}, "ACCOUNTSVC_SERVICE_AUDIENCE"},
		{"audience equals an allowed azp", map[string]string{"ACCOUNTSVC_SERVICE_AUDIENCE": "purge-client"}, "ACCOUNTSVC_SERVICE_AUDIENCE"},
		{"audience equals Google client ID", map[string]string{"ACCOUNTSVC_GOOGLE_CLIENT_IDS": "web.apps.googleusercontent.com", "ACCOUNTSVC_SERVICE_AUDIENCE": "web.apps.googleusercontent.com"}, "ACCOUNTSVC_SERVICE_AUDIENCE"},
		{"audience equals Apple bundle ID", map[string]string{"ACCOUNTSVC_APPLE_BUNDLE_IDS": "com.example.app", "ACCOUNTSVC_SERVICE_AUDIENCE": "com.example.app"}, "ACCOUNTSVC_SERVICE_AUDIENCE"},
		{"audience equals QQ app ID", map[string]string{"ACCOUNTSVC_QQ_APP_IDS": "1100000001", "ACCOUNTSVC_SERVICE_AUDIENCE": "1100000001"}, "ACCOUNTSVC_SERVICE_AUDIENCE"},
		{"audience with whitespace", map[string]string{"ACCOUNTSVC_SERVICE_AUDIENCE": " padded"}, "ACCOUNTSVC_SERVICE_AUDIENCE"},
		{"clients without issuer", map[string]string{"ACCOUNTSVC_SERVICE_ISSUER": ""}, "ACCOUNTSVC_SERVICE_ISSUER"},
		{"clients without audience", map[string]string{"ACCOUNTSVC_SERVICE_AUDIENCE": ""}, "ACCOUNTSVC_SERVICE_AUDIENCE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			serviceTokenEnvironment(t)
			for k, v := range tc.set {
				t.Setenv(k, v)
			}
			_, err := LoadConfig("serve")
			if err == nil || !strings.Contains(err.Error(), tc.variable) || strings.Contains(err.Error(), secretish) {
				t.Fatalf("err = %v, want mention of %s without values", err, tc.variable)
			}
		})
	}
}

func TestConfigServiceTokenAudienceAgainstAdmin(t *testing.T) {
	serviceTokenEnvironment(t)
	t.Setenv("ACCOUNTSVC_ADMIN_ENABLED", "true")
	t.Setenv("ACCOUNTSVC_ADMIN_ISSUER", "https://idp.example.test/realms/svc")
	t.Setenv("ACCOUNTSVC_ADMIN_AUDIENCE", "admin-api")
	if _, err := LoadConfig("serve"); err != nil {
		t.Fatalf("shared issuer with distinct audiences must be allowed: %v", err)
	}
	t.Setenv("ACCOUNTSVC_SERVICE_AUDIENCE", "admin-api")
	if _, err := LoadConfig("serve"); err == nil || !strings.Contains(err.Error(), "ACCOUNTSVC_SERVICE_AUDIENCE") {
		t.Fatalf("service audience equal to admin audience: %v", err)
	}
}

func TestMigrateIgnoresServiceTokenSettings(t *testing.T) {
	configEnvironment(t)
	t.Setenv("ACCOUNTSVC_SERVICE_ISSUER", "http://invalid")
	t.Setenv("ACCOUNTSVC_PURGE_FEED_BEARER_CLIENTS", "not-json")
	if _, err := LoadConfig("migrate"); err != nil {
		t.Fatalf("migrate parsed service token settings: %v", err)
	}
}
