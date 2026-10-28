package accountsvc

import (
	"reflect"
	"strings"
	"testing"
)

func serviceTokenEnvironment(t *testing.T) {
	t.Helper()
	serveEnvironment(t)
	t.Setenv("ACCOUNTSVC_SERVICE_ISSUER", "https://idp.example.test/realms/svc")
	t.Setenv("ACCOUNTSVC_SERVICE_AUDIENCE", "accountsvc-service")
	t.Setenv("ACCOUNTSVC_INTROSPECTION_BEARER_CLIENTS", `["intro-client"]`)
	t.Setenv("ACCOUNTSVC_PURGE_FEED_BEARER_CLIENTS", `["purge-client"]`)
}

func TestConfigServiceTokens(t *testing.T) {
	serviceTokenEnvironment(t)
	c, err := LoadConfig("serve")
	if err != nil {
		t.Fatal(err)
	}
	want := ServiceTokenConfig{Issuer: "https://idp.example.test/realms/svc", Audience: "accountsvc-service", IntrospectionClients: []string{"intro-client"}, PurgeFeedClients: []string{"purge-client"}}
	if !reflect.DeepEqual(c.ServiceToken, want) || !c.ServiceToken.Enabled() {
		t.Fatalf("ServiceToken = %+v", c.ServiceToken)
	}
}

func TestConfigServiceTokensOptional(t *testing.T) {
	serveEnvironment(t)
	c, err := LoadConfig("serve")
	if err != nil {
		t.Fatal(err)
	}
	if c.ServiceToken.Enabled() {
		t.Fatal("service tokens enabled without configuration")
	}
	t.Setenv("ACCOUNTSVC_INTROSPECTION_BEARER_CLIENTS", "[]")
	t.Setenv("ACCOUNTSVC_PURGE_FEED_BEARER_CLIENTS", "[]")
	if c, err = LoadConfig("serve"); err != nil || c.ServiceToken.Enabled() {
		t.Fatalf("empty arrays must mean unconfigured: %v %+v", err, c.ServiceToken)
	}
}

func TestConfigIntrospectionWithOnlyTokens(t *testing.T) {
	serviceTokenEnvironment(t)
	t.Setenv("ACCOUNTSVC_INTROSPECTION_BASIC_CLIENTS", "")
	c, err := LoadConfig("serve")
	if err != nil {
		t.Fatal(err)
	}
	if c.IntrospectionClients != nil || len(c.ServiceToken.IntrospectionClients) != 1 {
		t.Fatalf("config = %+v", c)
	}
	t.Setenv("ACCOUNTSVC_INTROSPECTION_BEARER_CLIENTS", "")
	_, err = LoadConfig("serve")
	if err == nil || !strings.Contains(err.Error(), "ACCOUNTSVC_INTROSPECTION_BASIC_CLIENTS") || !strings.Contains(err.Error(), "ACCOUNTSVC_INTROSPECTION_BEARER_CLIENTS") {
		t.Fatalf("introspection without any caller authentication: %v", err)
	}
}

func TestConfigServiceTokenRejections(t *testing.T) {
	const secretish = "synthetic-config-value"
	for _, tc := range []struct {
		name     string
		set      map[string]string
		variable string
	}{
		{"lists intersect", map[string]string{"ACCOUNTSVC_PURGE_FEED_BEARER_CLIENTS": `["purge-client","intro-client"]`}, "ACCOUNTSVC_PURGE_FEED_BEARER_CLIENTS"},
		{"duplicate element", map[string]string{"ACCOUNTSVC_INTROSPECTION_BEARER_CLIENTS": `["intro-client","intro-client"]`}, "ACCOUNTSVC_INTROSPECTION_BEARER_CLIENTS"},
		{"empty element", map[string]string{"ACCOUNTSVC_INTROSPECTION_BEARER_CLIENTS": `[""]`}, "ACCOUNTSVC_INTROSPECTION_BEARER_CLIENTS"},
		{"whitespace element", map[string]string{"ACCOUNTSVC_INTROSPECTION_BEARER_CLIENTS": `["intro client"]`}, "ACCOUNTSVC_INTROSPECTION_BEARER_CLIENTS"},
		{"not an array", map[string]string{"ACCOUNTSVC_PURGE_FEED_BEARER_CLIENTS": `{"` + secretish + `":1}`}, "ACCOUNTSVC_PURGE_FEED_BEARER_CLIENTS"},
		{"null", map[string]string{"ACCOUNTSVC_PURGE_FEED_BEARER_CLIENTS": `null`}, "ACCOUNTSVC_PURGE_FEED_BEARER_CLIENTS"},
		{"non-string element", map[string]string{"ACCOUNTSVC_PURGE_FEED_BEARER_CLIENTS": `[1]`}, "ACCOUNTSVC_PURGE_FEED_BEARER_CLIENTS"},
		{"trailing data", map[string]string{"ACCOUNTSVC_PURGE_FEED_BEARER_CLIENTS": `["purge-client"] []`}, "ACCOUNTSVC_PURGE_FEED_BEARER_CLIENTS"},
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
		{"issuer and audience without clients", map[string]string{"ACCOUNTSVC_INTROSPECTION_BEARER_CLIENTS": "", "ACCOUNTSVC_PURGE_FEED_BEARER_CLIENTS": ""}, "ACCOUNTSVC_SERVICE_ISSUER"},
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
