package accountsvc

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func configEnvironment(t *testing.T) {
	t.Helper()
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "ACCOUNTKIT_") || strings.HasPrefix(name, "ACCOUNTSVC_") {
			t.Setenv(name, "")
		}
	}
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	for _, name := range []string{"JWT", "SUBJECT_HMAC", "SUBJECT_CIPHER"} {
		t.Setenv("ACCOUNTKIT_"+name+"_KEYS", "1:"+key)
		t.Setenv("ACCOUNTKIT_"+name+"_ACTIVE_KEY", "1")
	}
	t.Setenv("ACCOUNTKIT_JWT_ISSUER", "test-issuer")
	t.Setenv("ACCOUNTKIT_JWT_AUDIENCE", "consumer")
	t.Setenv("ACCOUNTSVC_DATABASE_URL", "postgres://user:synthetic-dsn-password@127.0.0.1:5432/test?sslmode=disable")
	t.Setenv("ACCOUNTSVC_REDIS_URL", "redis://:synthetic-redis-password@127.0.0.1:6379/2")
}

func serveEnvironment(t *testing.T) {
	t.Helper()
	configEnvironment(t)
	cert, key := certificateFiles(t)
	t.Setenv("ACCOUNTSVC_TLS_CERT_FILE", cert)
	t.Setenv("ACCOUNTSVC_TLS_KEY_FILE", key)
	t.Setenv("ACCOUNTSVC_SMTP_HOST", "smtp.example.org")
	t.Setenv("ACCOUNTSVC_SMTP_PORT", "465")
	t.Setenv("ACCOUNTSVC_SMTP_FROM", "sender@example.org")
	t.Setenv("ACCOUNTSVC_SMTP_USERNAME", "smtp-user")
	t.Setenv("ACCOUNTSVC_SMTP_PASSWORD", "synthetic-smtp-password")
	t.Setenv("ACCOUNTSVC_INTROSPECTION_CLIENTS", fmt.Sprintf(`{"test-client":["%s"]}`, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{8}, 32))))
}

func certificateFiles(t *testing.T) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), DNSNames: []string{"localhost"}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	priv, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certfile := filepath.Join(dir, "cert.pem")
	keyfile := filepath.Join(dir, "key.pem")
	if err = os.WriteFile(certfile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(keyfile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: priv}), 0600); err != nil {
		t.Fatal(err)
	}
	return certfile, keyfile
}

func TestConfigProductionDefaultsAndMigrate(t *testing.T) {
	t.Run("production", func(t *testing.T) {
		serveEnvironment(t)
		c, err := LoadConfig("serve")
		if err != nil {
			t.Fatal(err)
		}
		if c.Mode != "production" || c.HTTPAddr != "127.0.0.1:8080" || c.InternalAddr != "127.0.0.1:8081" || c.StartupTimeout != time.Minute || c.ShutdownTimeout != 30*time.Second || c.SMTP.TLSMode != "implicit" || c.SMTP.Timeout != 10*time.Second || c.AdminEnabled {
			t.Fatal("unsafe service defaults")
		}
		if c.Library.Schema != "auth" || c.Library.KeyPrefix != "auth:" || len(c.IntrospectionClients) != 1 {
			t.Fatal("library defaults/clients not loaded")
		}
	})
	t.Run("migrate ignores service-only config", func(t *testing.T) {
		configEnvironment(t)
		for _, key := range []string{"MODE", "SMTP_PORT", "ADMIN_ENABLED", "INTROSPECTION_CLIENTS", "HTTP_ADDR", "TLS_CERT_FILE", "SHUTDOWN_TIMEOUT"} {
			t.Setenv("ACCOUNTSVC_"+key, "malformed-service-only")
		}
		if _, err := LoadConfig("migrate"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("explicit development", func(t *testing.T) {
		serveEnvironment(t)
		t.Setenv("ACCOUNTSVC_MODE", "development")
		t.Setenv("ACCOUNTSVC_TLS_CERT_FILE", "")
		t.Setenv("ACCOUNTSVC_TLS_KEY_FILE", "")
		t.Setenv("ACCOUNTSVC_SMTP_TLS_MODE", "none")
		t.Setenv("ACCOUNTSVC_SMTP_USERNAME", "")
		t.Setenv("ACCOUNTSVC_SMTP_PASSWORD", "")
		if _, err := LoadConfig("serve"); err != nil {
			t.Fatal(err)
		}
	})
}

func TestConfigRejectsUnsafeValuesWithoutLeaks(t *testing.T) {
	for _, tc := range []struct{ key, value, want string }{
		{"ACCOUNTSVC_MODE", "synthetic-invalid-mode", "MODE"},
		{"ACCOUNTSVC_DATABASE_URL", "postgres://user:synthetic-secret@%xx/db", "DATABASE_URL"},
		{"ACCOUNTSVC_DATABASE_URL", "http://user:synthetic-secret@example.org/db", "DATABASE_URL"},
		{"ACCOUNTSVC_REDIS_URL", "redis://:synthetic-secret@%xx/0", "REDIS_URL"},
		{"ACCOUNTSVC_REDIS_URL", "https://synthetic-secret.example.org", "REDIS_URL"},
		{"ACCOUNTSVC_HTTP_ADDR", "synthetic-secret", "HTTP_ADDR"},
		{"ACCOUNTSVC_INTERNAL_ADDR", "127.0.0.1:8080", "INTERNAL_ADDR"},
		{"ACCOUNTSVC_STARTUP_TIMEOUT", "synthetic-secret", "STARTUP_TIMEOUT"},
		{"ACCOUNTSVC_STARTUP_TIMEOUT", "0s", "STARTUP_TIMEOUT"},
		{"ACCOUNTSVC_SHUTDOWN_TIMEOUT", "15s", "SHUTDOWN_TIMEOUT"},
		{"ACCOUNTSVC_TLS_CERT_FILE", "", "TLS_CERT_FILE"},
		{"ACCOUNTSVC_TLS_KEY_FILE", "missing-synthetic-secret.key", "TLS_KEY_FILE"},
		{"ACCOUNTSVC_SMTP_TLS_MODE", "none", "SMTP_TLS_MODE"},
		{"ACCOUNTSVC_SMTP_USERNAME", "", "SMTP_USERNAME"},
		{"ACCOUNTSVC_SMTP_PASSWORD", "", "SMTP_PASSWORD"},
		{"ACCOUNTSVC_SMTP_PORT", "synthetic-secret", "SMTP_PORT"},
		{"ACCOUNTSVC_SMTP_TIMEOUT", "0s", "SMTP_TIMEOUT"},
		{"ACCOUNTSVC_SMTP_FROM", "sender@example.org\r\nBcc: synthetic-secret@example.org", "SMTP_FROM"},
		{"ACCOUNTSVC_INTROSPECTION_CLIENTS", `{"id":["synthetic-secret"]}`, "INTROSPECTION_CLIENTS"},
		{"ACCOUNTSVC_TRUSTED_PROXY_CIDRS", "synthetic-secret", "TRUSTED_PROXY_CIDRS"},
		{"ACCOUNTSVC_ADMIN_ENABLED", "synthetic-secret", "ADMIN_ENABLED"},
		{"ACCOUNTSVC_ADMIN_ISSUER", "https://synthetic-secret.example.org", "ADMIN_ISSUER"},
		{"ACCOUNTKIT_ACCESS_TOKEN_TTL", "synthetic-secret", "ACCESS_TOKEN_TTL"},
		{"ACCOUNTKIT_AUTH_SCHEMA", "synthetic-secret", "AUTH_SCHEMA"},
		{"ACCOUNTKIT_DEFAULT_REGION", "synthetic-secret", "DEFAULT_REGION"},
	} {
		t.Run(tc.key+tc.want, func(t *testing.T) {
			serveEnvironment(t)
			t.Setenv(tc.key, tc.value)
			_, err := LoadConfig("serve")
			if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "synthetic-secret") || strings.Contains(err.Error(), "synthetic-invalid-mode") {
				t.Fatalf("unsafe or missing config error for %s", tc.key)
			}
		})
	}
}

func TestConfigAdministratorAudienceSeparation(t *testing.T) {
	serveEnvironment(t)
	t.Setenv("ACCOUNTSVC_ADMIN_ENABLED", "true")
	t.Setenv("ACCOUNTSVC_ADMIN_ISSUER", "https://admin.example.org")
	t.Setenv("ACCOUNTSVC_ADMIN_AUDIENCE", "admin-api")
	t.Setenv("ACCOUNTSVC_TRUSTED_PROXY_CIDRS", "10.0.0.0/8,2001:db8::/32")
	c, err := LoadConfig("serve")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.TrustedProxyCIDRs) != 2 || c.Admin.RolesClaim != "/roles" || c.Admin.UsernameClaim != "/preferred_username" {
		t.Fatal("admin/proxy defaults lost")
	}
	t.Setenv("ACCOUNTSVC_ADMIN_AUDIENCE", "consumer")
	if _, err = LoadConfig("serve"); err == nil {
		t.Fatal("admin audience equals consumer audience")
	}
	t.Setenv("ACCOUNTSVC_ADMIN_AUDIENCE", "apple-app")
	t.Setenv("ACCOUNTKIT_APPLE_BUNDLE_IDS", "apple-app")
	if _, err = LoadConfig("serve"); err == nil {
		t.Fatal("ID token audience accepted for admin API")
	}
}
