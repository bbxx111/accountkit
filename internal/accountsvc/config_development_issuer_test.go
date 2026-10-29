package accountsvc

import (
	"strings"
	"testing"
)

func developmentIssuerEnvironment(t *testing.T) {
	t.Helper()
	serviceTokenEnvironment(t)
	t.Setenv("ACCOUNTSVC_MODE", "development")
	t.Setenv("ACCOUNTSVC_SERVICE_ISSUER", "http://host.docker.internal:8180/realms/svc")
	t.Setenv("ACCOUNTSVC_ADMIN_ENABLED", "true")
	t.Setenv("ACCOUNTSVC_ADMIN_ISSUER", "http://localhost:8180/realms/admin")
	t.Setenv("ACCOUNTSVC_ADMIN_AUDIENCE", "admin-api")
}

func TestDevelopmentModeAcceptsPlainHTTPIssuers(t *testing.T) {
	developmentIssuerEnvironment(t)
	c, err := LoadConfig("serve")
	if err != nil {
		t.Fatal(err)
	}
	if !c.Service.AllowHTTP || c.Service.Issuer != "http://host.docker.internal:8180/realms/svc" {
		t.Fatalf("Service = %+v", c.Service)
	}
	if !c.Admin.AllowHTTP || c.Admin.Issuer != "http://localhost:8180/realms/admin" {
		t.Fatalf("Admin = %+v", c.Admin)
	}
	const secretish = "synthetic-secret.example.test"
	for _, tc := range []struct{ name, variable, value string }{
		{"service credentials", "ACCOUNTSVC_SERVICE_ISSUER", "http://user:pw@" + secretish},
		{"service query", "ACCOUNTSVC_SERVICE_ISSUER", "http://" + secretish + "?x=1"},
		{"service fragment", "ACCOUNTSVC_SERVICE_ISSUER", "http://" + secretish + "#f"},
		{"service scheme", "ACCOUNTSVC_SERVICE_ISSUER", "ftp://" + secretish},
		{"admin credentials", "ACCOUNTSVC_ADMIN_ISSUER", "http://user:pw@" + secretish},
		{"admin query", "ACCOUNTSVC_ADMIN_ISSUER", "http://" + secretish + "?x=1"},
		{"admin fragment", "ACCOUNTSVC_ADMIN_ISSUER", "http://" + secretish + "#f"},
		{"admin scheme", "ACCOUNTSVC_ADMIN_ISSUER", "ftp://" + secretish},
	} {
		t.Run(tc.name, func(t *testing.T) {
			developmentIssuerEnvironment(t)
			t.Setenv(tc.variable, tc.value)
			_, err := LoadConfig("serve")
			if err == nil || !strings.Contains(err.Error(), tc.variable) || strings.Contains(err.Error(), secretish) {
				t.Fatalf("development relaxed more than the scheme: %v", err)
			}
		})
	}
}

// Explicit production keeps the HTTPS requirement and the unchanged error wording; HTTPS
// issuers never flag AllowHTTP in production, while development flags it regardless of scheme.
func TestProductionModeKeepsHTTPSIssuers(t *testing.T) {
	developmentIssuerEnvironment(t)
	t.Setenv("ACCOUNTSVC_MODE", "production")
	_, err := LoadConfig("serve")
	if err == nil || !strings.Contains(err.Error(), "ACCOUNTSVC_ADMIN_ISSUER") || strings.Contains(err.Error(), "localhost:8180") {
		t.Fatalf("production accepted a plain HTTP admin issuer: %v", err)
	}
	t.Setenv("ACCOUNTSVC_ADMIN_ISSUER", "https://idp.example.test/realms/admin")
	_, err = LoadConfig("serve")
	if err == nil || !strings.Contains(err.Error(), "ACCOUNTSVC_SERVICE_ISSUER") || strings.Contains(err.Error(), "host.docker.internal") {
		t.Fatalf("production accepted a plain HTTP service issuer: %v", err)
	}
	t.Setenv("ACCOUNTSVC_SERVICE_ISSUER", "https://idp.example.test/realms/svc")
	c, err := LoadConfig("serve")
	if err != nil {
		t.Fatal(err)
	}
	if c.Service.AllowHTTP || c.Admin.AllowHTTP {
		t.Fatalf("production flagged AllowHTTP: service=%v admin=%v", c.Service.AllowHTTP, c.Admin.AllowHTTP)
	}
	t.Setenv("ACCOUNTSVC_MODE", "development")
	if c, err = LoadConfig("serve"); err != nil || !c.Service.AllowHTTP || !c.Admin.AllowHTTP {
		t.Fatalf("development must flag AllowHTTP regardless of the configured scheme: %v %+v", err, c.Service)
	}
}
