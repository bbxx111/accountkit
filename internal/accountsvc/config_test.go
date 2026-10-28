package accountsvc

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
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

	"github.com/bbxx111/accountkit/internal/testkeys"
	"github.com/bbxx111/accountkit/user/sender/smtp"
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
		t.Setenv("ACCOUNTSVC_"+name+"_KEYS", "1:"+key)
		t.Setenv("ACCOUNTSVC_"+name+"_ACTIVE_KEY", "1")
	}
	t.Setenv("ACCOUNTSVC_JWT_KEYS", "1:"+testkeys.JWTBase64(1))
	t.Setenv("ACCOUNTSVC_JWT_ISSUER", "test-issuer")
	t.Setenv("ACCOUNTSVC_JWT_AUDIENCE", "consumer")
	t.Setenv("ACCOUNTSVC_DATABASE_URL", "postgres://user:synthetic-dsn-password@127.0.0.1:5432/test?sslmode=disable")
	t.Setenv("ACCOUNTSVC_REDIS_URL", "redis://:synthetic-redis-password@127.0.0.1:6379/2")
}

func TestConfigAlipay(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	private := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	public := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PUBLIC KEY", Bytes: x509.MarshalPKCS1PublicKey(&key.PublicKey)}))
	setAlipay := func(t *testing.T) {
		t.Setenv("ACCOUNTSVC_ALIPAY_APP_IDS", "2026000000000001")
		t.Setenv("ACCOUNTSVC_ALIPAY_2026000000000001_PRIVATE_KEY", private)
		t.Setenv("ACCOUNTSVC_ALIPAY_2026000000000001_PUBLIC_KEY", public)
	}
	t.Run("pure Alipay without SMTP", func(t *testing.T) {
		serveEnvironment(t)
		setAlipay(t)
		t.Setenv("ACCOUNTSVC_ENABLED_IDENTITY_KINDS", "ALIPAY")
		t.Setenv("ACCOUNTSVC_REQUIRED_IDENTITY_KINDS", "[]")
		for _, name := range []string{"HOST", "PORT", "FROM", "USERNAME", "PASSWORD"} {
			t.Setenv("ACCOUNTSVC_SMTP_"+name, "")
		}
		c, err := LoadConfig("serve")
		if err != nil || len(c.Library.AlipayApps) != 1 || c.SMTP.Host != "" {
			t.Fatalf("pure Alipay service: %v", err)
		}
	})
	t.Run("enabled requires provider", func(t *testing.T) {
		serveEnvironment(t)
		t.Setenv("ACCOUNTSVC_ENABLED_IDENTITY_KINDS", "ALIPAY")
		t.Setenv("ACCOUNTSVC_REQUIRED_IDENTITY_KINDS", "[]")
		if _, err := LoadConfig("serve"); err == nil || !strings.Contains(err.Error(), "ACCOUNTSVC_ALIPAY_APP_IDS") {
			t.Fatalf("missing provider: %v", err)
		}
	})
	t.Run("migrate no runtime or provider required", func(t *testing.T) {
		configEnvironment(t)
		t.Setenv("ACCOUNTSVC_ENABLED_IDENTITY_KINDS", "ALIPAY")
		t.Setenv("ACCOUNTSVC_REQUIRED_IDENTITY_KINDS", "[]")
		if _, err := LoadConfig("migrate"); err != nil {
			t.Fatal(err)
		}
	})
	for _, field := range []string{"PRIVATE_KEY", "PUBLIC_KEY"} {
		t.Run("safe dynamic "+field, func(t *testing.T) {
			configEnvironment(t)
			setAlipay(t)
			name := "ACCOUNTSVC_ALIPAY_2026000000000001_" + field
			t.Setenv(name, "synthetic-secret-that-must-not-appear")
			if _, err := LoadConfig("migrate"); err == nil || !strings.Contains(err.Error(), name) || strings.Contains(err.Error(), "synthetic-secret") {
				t.Fatalf("safe dynamic error name: %v", err)
			}
		})
	}
	for _, field := range []string{"SUBJECT_MODE", "GATEWAY_URL"} {
		t.Run("safe static "+field, func(t *testing.T) {
			configEnvironment(t)
			t.Setenv("ACCOUNTSVC_ALIPAY_"+field, "invalid-sensitive-value")
			if _, err := LoadConfig("migrate"); err == nil || !strings.Contains(err.Error(), "ACCOUNTSVC_ALIPAY_"+field) || strings.Contains(err.Error(), "sensitive") {
				t.Fatalf("safe static error: %v", err)
			}
		})
	}
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
	t.Setenv("ACCOUNTSVC_INTROSPECTION_BASIC_CLIENTS", fmt.Sprintf(`{"test-client":["%s"]}`, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{8}, 32))))
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
		if c.Mode != "production" || c.HTTPAddr != "127.0.0.1:8080" || c.StartupTimeout != time.Minute || c.ShutdownTimeout != 30*time.Second || c.SMTP.TLSMode != "implicit" || c.SMTP.Timeout != 10*time.Second || c.AdminEnabled {
			t.Fatal("unsafe service defaults")
		}
		if c.Library.Schema != "account" || c.Library.KeyPrefix != "auth:" || len(c.IntrospectionClients) != 1 {
			t.Fatal("library defaults/clients not loaded")
		}
	})
	t.Run("migrate ignores service-only config", func(t *testing.T) {
		configEnvironment(t)
		for _, key := range []string{"MODE", "SMTP_PORT", "ADMIN_ENABLED", "INTROSPECTION_BASIC_CLIENTS", "HTTP_ADDR", "INTERNAL_ADDR", "TLS_ENABLED", "TLS_CERT_FILE", "TLS_KEY_FILE", "SHUTDOWN_TIMEOUT"} {
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

func TestConfigIdentityPolicySMTP(t *testing.T) {
	for _, provider := range []string{"WECHAT", "APPLE"} {
		t.Run(provider+" ignores disabled SMTP", func(t *testing.T) {
			serveEnvironment(t)
			t.Setenv("ACCOUNTSVC_ENABLED_IDENTITY_KINDS", provider)
			t.Setenv("ACCOUNTSVC_REQUIRED_IDENTITY_KINDS", "[]")
			t.Setenv("ACCOUNTSVC_APPLE_BUNDLE_IDS", "com.example.accountsvc")
			t.Setenv("ACCOUNTSVC_WECHAT_APPS", "test-app:synthetic-provider-secret")
			for _, key := range []string{"HOST", "PORT", "FROM", "USERNAME", "PASSWORD", "TLS_MODE", "TIMEOUT"} {
				t.Setenv("ACCOUNTSVC_SMTP_"+key, "malformed-disabled-smtp")
			}
			cfg, err := LoadConfig("serve")
			if err != nil {
				t.Fatalf("disabled EMAIL must ignore SMTP: %v", err)
			}
			if cfg.SMTP != (smtp.Config{}) {
				t.Fatal("disabled EMAIL parsed SMTP settings")
			}
		})
	}
	t.Run("explicit PHONE requires SMS support", func(t *testing.T) {
		serveEnvironment(t)
		t.Setenv("ACCOUNTSVC_ENABLED_IDENTITY_KINDS", "PHONE,EMAIL")
		_, err := LoadConfig("serve")
		if err == nil || !strings.Contains(err.Error(), "PHONE") || !strings.Contains(err.Error(), "SMS") {
			t.Fatalf("expected missing SMS support, got %v", err)
		}
	})
	t.Run("migrate does not require provider capabilities", func(t *testing.T) {
		configEnvironment(t)
		t.Setenv("ACCOUNTSVC_ENABLED_IDENTITY_KINDS", "PHONE,WECHAT")
		t.Setenv("ACCOUNTSVC_REQUIRED_IDENTITY_KINDS", "PHONE")
		if _, err := LoadConfig("migrate"); err != nil {
			t.Fatalf("migrate must allow static valid policy: %v", err)
		}
	})
}

func TestConfigExplicitIdentityProviderDependencies(t *testing.T) {
	for _, provider := range []struct{ kind, key, value string }{
		{"WECHAT", "ACCOUNTSVC_WECHAT_APPS", "test-app:synthetic-provider-secret"},
		{"APPLE", "ACCOUNTSVC_APPLE_BUNDLE_IDS", "com.example.synthetic-app"},
		{"GOOGLE", "ACCOUNTSVC_GOOGLE_CLIENT_IDS", "synthetic-client.apps.googleusercontent.com"},
	} {
		for _, configured := range []bool{false, true} {
			for _, command := range []string{"serve", "migrate"} {
				t.Run(fmt.Sprintf("%s/%s/configured=%v", provider.kind, command, configured), func(t *testing.T) {
					serveEnvironment(t)
					t.Setenv("ACCOUNTSVC_ENABLED_IDENTITY_KINDS", provider.kind)
					t.Setenv("ACCOUNTSVC_REQUIRED_IDENTITY_KINDS", "[]")
					if configured {
						t.Setenv(provider.key, provider.value)
					}
					for _, key := range []string{"HOST", "PORT", "FROM", "USERNAME", "PASSWORD", "TLS_MODE", "TIMEOUT"} {
						t.Setenv("ACCOUNTSVC_SMTP_"+key, "")
					}
					_, err := LoadConfig(command)
					if command == "serve" && !configured {
						if err == nil || !strings.Contains(err.Error(), provider.key) {
							t.Fatalf("missing provider dependency must name %s: %v", provider.key, err)
						}
						for _, secret := range []string{"synthetic-provider-secret", "synthetic-dsn-password", "synthetic-redis-password"} {
							if strings.Contains(err.Error(), secret) {
								t.Fatal("provider configuration error leaked secret")
							}
						}
					} else if err != nil {
						t.Fatalf("valid provider policy rejected: %v", err)
					}
				})
			}
		}
	}
	t.Run("default policy preserves optional providers", func(t *testing.T) {
		serveEnvironment(t)
		if _, err := LoadConfig("serve"); err != nil {
			t.Fatalf("default policy requires optional provider: %v", err)
		}
	})
}

func TestLoadConfigSchema(t *testing.T) {
	for _, command := range []string{"serve", "migrate"} {
		for _, tc := range []struct{ name, value, want string }{
			{"unset", "", "account"},
			{"empty", "", "account"},
			{"account", "account", "account"},
			{"auth", "auth", "auth"},
			{"custom", "custom_account", "custom_account"},
		} {
			t.Run(command+"/"+tc.name, func(t *testing.T) {
				serveEnvironment(t)
				t.Setenv("ACCOUNTSVC_AUTH_SCHEMA", tc.value)
				if tc.name == "unset" {
					if err := os.Unsetenv("ACCOUNTSVC_AUTH_SCHEMA"); err != nil {
						t.Fatal(err)
					}
				}
				c, err := LoadConfig(command)
				if err != nil {
					t.Fatal(err)
				}
				if c.Library.Schema != tc.want || c.Library.KeyPrefix != "auth:" {
					t.Fatalf("schema=%q prefix=%q, want schema=%q prefix=auth:", c.Library.Schema, c.Library.KeyPrefix, tc.want)
				}
			})
		}
	}
}

func TestConfigTLSMode(t *testing.T) {
	for _, mode := range []string{"production", "development"} {
		for _, setting := range []string{"unset", "", "true", "false", "synthetic-secret-invalid"} {
			for _, pair := range []string{"empty", "valid", "cert only", "key only", "invalid cert", "invalid key"} {
				t.Run(mode+"/"+setting+"/"+pair, func(t *testing.T) {
					serveEnvironment(t)
					t.Setenv("ACCOUNTSVC_MODE", mode)
					if setting == "unset" {
						t.Setenv("ACCOUNTSVC_TLS_ENABLED", "")
						if err := os.Unsetenv("ACCOUNTSVC_TLS_ENABLED"); err != nil {
							t.Fatal(err)
						}
					} else {
						t.Setenv("ACCOUNTSVC_TLS_ENABLED", setting)
					}
					cert, key := os.Getenv("ACCOUNTSVC_TLS_CERT_FILE"), os.Getenv("ACCOUNTSVC_TLS_KEY_FILE")
					switch pair {
					case "empty":
						cert, key = "", ""
					case "cert only":
						key = ""
					case "key only":
						cert = ""
					case "invalid cert", "invalid key":
						invalid := filepath.Join(t.TempDir(), "invalid.pem")
						if err := os.WriteFile(invalid, []byte("synthetic-secret-invalid-pem"), 0600); err != nil {
							t.Fatal(err)
						}
						if pair == "invalid cert" {
							cert = invalid
						} else {
							key = invalid
						}
					}
					t.Setenv("ACCOUNTSVC_TLS_CERT_FILE", cert)
					t.Setenv("ACCOUNTSVC_TLS_KEY_FILE", key)
					wantTLS := setting == "true" || ((setting == "unset" || setting == "") && (mode == "production" || pair != "empty"))
					wantErr := setting == "synthetic-secret-invalid" || (setting == "false" && pair != "empty") || (wantTLS && pair != "valid")
					cfg, err := LoadConfig("serve")
					if (err != nil) != wantErr {
						t.Fatalf("error=%v want failure=%v", err, wantErr)
					}
					if err != nil {
						if !strings.Contains(err.Error(), "ACCOUNTSVC_TLS_") || strings.Contains(err.Error(), "synthetic-secret") || strings.Contains(err.Error(), "invalid.pem") {
							t.Fatal("TLS configuration error leaked input or omitted field")
						}
						return
					}
					if cfg.TLSEnabled != wantTLS || (cfg.tlsConfig != nil) != wantTLS {
						t.Fatalf("effective TLS=%v want %v", cfg.tlsConfig != nil, wantTLS)
					}
					if wantTLS && (cfg.tlsConfig.MinVersion < tls.VersionTLS12 || len(cfg.tlsConfig.Certificates) != 1) {
						t.Fatal("TLS version/certificate requirements lost")
					}
				})
			}
		}
	}
	for _, tc := range []struct{ key, value string }{{"SMTP_TLS_MODE", "none"}, {"SMTP_USERNAME", ""}, {"SMTP_PASSWORD", ""}, {"INTROSPECTION_BASIC_CLIENTS", ""}, {"ADMIN_ISSUER", "http://synthetic-secret.example.test"}} {
		t.Run("production HTTP retains "+tc.key, func(t *testing.T) {
			serveEnvironment(t)
			t.Setenv("ACCOUNTSVC_TLS_ENABLED", "false")
			t.Setenv("ACCOUNTSVC_TLS_CERT_FILE", "")
			t.Setenv("ACCOUNTSVC_TLS_KEY_FILE", "")
			if tc.key == "ADMIN_ISSUER" {
				t.Setenv("ACCOUNTSVC_ADMIN_ENABLED", "true")
				t.Setenv("ACCOUNTSVC_ADMIN_AUDIENCE", "admin-api")
			}
			t.Setenv("ACCOUNTSVC_"+tc.key, tc.value)
			_, err := LoadConfig("serve")
			if err == nil || !strings.Contains(err.Error(), "ACCOUNTSVC_"+tc.key) || strings.Contains(err.Error(), "synthetic-secret") {
				t.Fatalf("HTTP weakened %s requirement: %v", tc.key, err)
			}
		})
	}
}

func TestConfigRejectsInternalAddress(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:8081", "0.0.0.0:9000", "synthetic-secret-invalid", "   "} {
		t.Run(addr, func(t *testing.T) {
			serveEnvironment(t)
			t.Setenv("ACCOUNTSVC_INTERNAL_ADDR", addr)
			_, err := LoadConfig("serve")
			if err == nil || !strings.Contains(err.Error(), "ACCOUNTSVC_INTERNAL_ADDR") || !strings.Contains(err.Error(), "remove") || (strings.TrimSpace(addr) != "" && strings.Contains(err.Error(), addr)) {
				t.Fatalf("old listener configuration not safely rejected: %v", err)
			}
		})
	}
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
		{"ACCOUNTSVC_INTROSPECTION_BASIC_CLIENTS", `{"id":["synthetic-secret"]}`, "INTROSPECTION_BASIC_CLIENTS"},
		{"ACCOUNTSVC_TRUSTED_PROXY_CIDRS", "synthetic-secret", "TRUSTED_PROXY_CIDRS"},
		{"ACCOUNTSVC_ADMIN_ENABLED", "synthetic-secret", "ADMIN_ENABLED"},
		{"ACCOUNTSVC_ADMIN_ISSUER", "https://synthetic-secret.example.org", "ADMIN_ISSUER"},
		{"ACCOUNTSVC_ACCESS_TOKEN_TTL", "synthetic-secret", "ACCESS_TOKEN_TTL"},
		{"ACCOUNTSVC_AUTH_SCHEMA", "synthetic-secret", "AUTH_SCHEMA"},
		{"ACCOUNTSVC_ENABLED_IDENTITY_KINDS", "[]", "ENABLED_IDENTITY_KINDS"},
		{"ACCOUNTSVC_REQUIRED_IDENTITY_KINDS", "WECHAT", "REQUIRED_IDENTITY_KINDS"},
		{"ACCOUNTSVC_DEFAULT_REGION", "synthetic-secret", "DEFAULT_REGION"},
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
	t.Setenv("ACCOUNTSVC_APPLE_BUNDLE_IDS", "apple-app")
	if _, err = LoadConfig("serve"); err == nil {
		t.Fatal("ID token audience accepted for admin API")
	}
}

func TestConfigOIDCProviders(t *testing.T) {
	oidcEnv := func(t *testing.T) {
		t.Setenv("ACCOUNTSVC_OIDC_PROVIDERS", "CORP_SSO")
		t.Setenv("ACCOUNTSVC_OIDC_CORP_SSO_ISSUER", "https://sso.corp.example")
		t.Setenv("ACCOUNTSVC_OIDC_CORP_SSO_JWKS_URL", "https://sso.corp.example/jwks")
		t.Setenv("ACCOUNTSVC_OIDC_CORP_SSO_CLIENT_IDS", "synthetic-corp-client")
	}
	noSMTP := func(t *testing.T) {
		for _, key := range []string{"HOST", "PORT", "FROM", "USERNAME", "PASSWORD", "TLS_MODE", "TIMEOUT"} {
			t.Setenv("ACCOUNTSVC_SMTP_"+key, "")
		}
	}
	t.Run("OIDC only without SMTP", func(t *testing.T) {
		serveEnvironment(t)
		oidcEnv(t)
		noSMTP(t)
		t.Setenv("ACCOUNTSVC_ENABLED_IDENTITY_KINDS", "CORP_SSO")
		t.Setenv("ACCOUNTSVC_REQUIRED_IDENTITY_KINDS", "[]")
		c, err := LoadConfig("serve")
		if err != nil || len(c.Library.OIDCProviders) != 1 || c.Library.OIDCProviders[0].Kind != "CORP_SSO" {
			t.Fatalf("OIDC-only service: %+v %v", c.Library.OIDCProviders, err)
		}
	})
	t.Run("missing issuer names the variable", func(t *testing.T) {
		serveEnvironment(t)
		oidcEnv(t)
		t.Setenv("ACCOUNTSVC_OIDC_CORP_SSO_ISSUER", "synthetic-secret-host")
		_, err := LoadConfig("serve")
		if err == nil || !strings.Contains(err.Error(), "ACCOUNTSVC_OIDC_CORP_SSO_ISSUER") || strings.Contains(err.Error(), "synthetic-secret-host") {
			t.Fatalf("issuer error: %v", err)
		}
	})
	t.Run("enabled kind without OIDC configuration", func(t *testing.T) {
		serveEnvironment(t)
		t.Setenv("ACCOUNTSVC_ENABLED_IDENTITY_KINDS", "EMAIL,CORP_SSO")
		t.Setenv("ACCOUNTSVC_REQUIRED_IDENTITY_KINDS", "EMAIL")
		_, err := LoadConfig("serve")
		if err == nil || !strings.Contains(err.Error(), "ACCOUNTSVC_ENABLED_IDENTITY_KINDS") {
			t.Fatalf("unconfigured enabled kind: %v", err)
		}
	})
	t.Run("migrate does not need OIDC variables", func(t *testing.T) {
		configEnvironment(t)
		if _, err := LoadConfig("migrate"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("migrate rejects incomplete OIDC configuration", func(t *testing.T) {
		configEnvironment(t)
		t.Setenv("ACCOUNTSVC_OIDC_PROVIDERS", "CORP_SSO")
		_, err := LoadConfig("migrate")
		if err == nil || !strings.Contains(err.Error(), "ACCOUNTSVC_OIDC_CORP_SSO_ISSUER") {
			t.Fatalf("incomplete OIDC during migrate: %v", err)
		}
	})
}

func TestConfigQQ(t *testing.T) {
	t.Run("pure QQ without SMTP", func(t *testing.T) {
		serveEnvironment(t)
		t.Setenv("ACCOUNTSVC_QQ_APP_IDS", "1100000001,1100000002")
		t.Setenv("ACCOUNTSVC_ENABLED_IDENTITY_KINDS", "QQ")
		t.Setenv("ACCOUNTSVC_REQUIRED_IDENTITY_KINDS", "[]")
		for _, name := range []string{"HOST", "PORT", "FROM", "USERNAME", "PASSWORD"} {
			t.Setenv("ACCOUNTSVC_SMTP_"+name, "")
		}
		c, err := LoadConfig("serve")
		if err != nil || len(c.Library.QQAppIDs) != 2 || c.SMTP.Host != "" {
			t.Fatalf("pure QQ service: %v", err)
		}
	})
	t.Run("enabled without apps", func(t *testing.T) {
		serveEnvironment(t)
		t.Setenv("ACCOUNTSVC_ENABLED_IDENTITY_KINDS", "QQ")
		t.Setenv("ACCOUNTSVC_REQUIRED_IDENTITY_KINDS", "[]")
		if _, err := LoadConfig("serve"); err == nil || !strings.Contains(err.Error(), "ACCOUNTSVC_QQ_APP_IDS") {
			t.Fatalf("missing apps: %v", err)
		}
	})
	for name, env := range map[string][2]string{
		"ACCOUNTSVC_QQ_APP_IDS":      {"ACCOUNTSVC_QQ_APP_IDS", "1100000001,sensitive-app"},
		"ACCOUNTSVC_QQ_SUBJECT_MODE": {"ACCOUNTSVC_QQ_SUBJECT_MODE", "sensitive-mode"},
		"ACCOUNTSVC_QQ_API_BASE_URL": {"ACCOUNTSVC_QQ_API_BASE_URL", "http://sensitive.example.test"},
	} {
		for _, mode := range []string{"serve", "migrate"} {
			t.Run(mode+"_"+name, func(t *testing.T) {
				serveEnvironment(t)
				t.Setenv("ACCOUNTSVC_QQ_APP_IDS", "1100000001")
				t.Setenv(env[0], env[1])
				_, err := LoadConfig(mode)
				if err == nil || !strings.Contains(err.Error(), name) || strings.Contains(err.Error(), "sensitive") {
					t.Fatalf("%s: %v", name, err)
				}
			})
		}
	}
	t.Run("migrate without QQ", func(t *testing.T) {
		configEnvironment(t)
		if _, err := LoadConfig("migrate"); err != nil {
			t.Fatalf("migrate must not require QQ: %v", err)
		}
	})
}

func TestConfigWeibo(t *testing.T) {
	t.Run("pure Weibo without SMTP", func(t *testing.T) {
		serveEnvironment(t)
		t.Setenv("ACCOUNTSVC_WEIBO_APP_IDS", "1234567890,1234567891")
		t.Setenv("ACCOUNTSVC_ENABLED_IDENTITY_KINDS", "WEIBO")
		t.Setenv("ACCOUNTSVC_REQUIRED_IDENTITY_KINDS", "[]")
		for _, name := range []string{"HOST", "PORT", "FROM", "USERNAME", "PASSWORD"} {
			t.Setenv("ACCOUNTSVC_SMTP_"+name, "")
		}
		c, err := LoadConfig("serve")
		if err != nil || len(c.Library.WeiboAppIDs) != 2 || c.SMTP.Host != "" {
			t.Fatalf("pure Weibo service: %v", err)
		}
	})
	t.Run("enabled without apps", func(t *testing.T) {
		serveEnvironment(t)
		t.Setenv("ACCOUNTSVC_ENABLED_IDENTITY_KINDS", "WEIBO")
		t.Setenv("ACCOUNTSVC_REQUIRED_IDENTITY_KINDS", "[]")
		if _, err := LoadConfig("serve"); err == nil || !strings.Contains(err.Error(), "ACCOUNTSVC_WEIBO_APP_IDS") {
			t.Fatalf("missing apps: %v", err)
		}
	})
	for name, env := range map[string][2]string{
		"ACCOUNTSVC_WEIBO_APP_IDS":      {"ACCOUNTSVC_WEIBO_APP_IDS", "1234567890,sensitive-app"},
		"ACCOUNTSVC_WEIBO_API_BASE_URL": {"ACCOUNTSVC_WEIBO_API_BASE_URL", "http://sensitive.example.test"},
	} {
		for _, mode := range []string{"serve", "migrate"} {
			t.Run(mode+"_"+name, func(t *testing.T) {
				serveEnvironment(t)
				t.Setenv("ACCOUNTSVC_WEIBO_APP_IDS", "1234567890")
				t.Setenv(env[0], env[1])
				_, err := LoadConfig(mode)
				if err == nil || !strings.Contains(err.Error(), name) || strings.Contains(err.Error(), "sensitive") {
					t.Fatalf("%s: %v", name, err)
				}
			})
		}
	}
	t.Run("migrate without Weibo", func(t *testing.T) {
		configEnvironment(t)
		if _, err := LoadConfig("migrate"); err != nil {
			t.Fatalf("migrate must not require Weibo: %v", err)
		}
	})
}

func TestConfigWeChatMiniProgram(t *testing.T) {
	serveEnvironment(t)
	t.Setenv("ACCOUNTSVC_ENABLED_IDENTITY_KINDS", "WECHAT")
	t.Setenv("ACCOUNTSVC_REQUIRED_IDENTITY_KINDS", "[]")
	t.Setenv("ACCOUNTSVC_WECHAT_MINI_PROGRAM_APPS", "mini:synthetic-mini-secret")
	t.Setenv("ACCOUNTSVC_SMTP_PORT", "invalid-unused")
	if _, err := LoadConfig("serve"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ACCOUNTSVC_ADMIN_ENABLED", "true")
	t.Setenv("ACCOUNTSVC_ADMIN_ISSUER", "https://admin.example.org")
	t.Setenv("ACCOUNTSVC_ADMIN_AUDIENCE", "mini")
	if _, err := LoadConfig("serve"); err == nil || !strings.Contains(err.Error(), "ACCOUNTSVC_ADMIN_AUDIENCE") {
		t.Fatalf("audience must be isolated: %v", err)
	}
	t.Setenv("ACCOUNTSVC_ADMIN_ENABLED", "false")
	t.Setenv("ACCOUNTSVC_ADMIN_ISSUER", "")
	t.Setenv("ACCOUNTSVC_ADMIN_AUDIENCE", "")
	for _, tc := range []struct{ key, value string }{{"WECHAT_SUBJECT_MODE", "synthetic-mini-secret"}, {"WECHAT_MINI_PROGRAM_APPS", "invalid-synthetic-mini-secret"}} {
		t.Run(tc.key, func(t *testing.T) {
			t.Setenv("ACCOUNTSVC_"+tc.key, tc.value)
			_, err := LoadConfig("serve")
			if err == nil || !strings.Contains(err.Error(), "ACCOUNTSVC_"+tc.key) || strings.Contains(err.Error(), "synthetic-mini-secret") {
				t.Fatalf("unsafe config error: %v", err)
			}
		})
	}
}

func TestConfigProviderProfileBackfill(t *testing.T) {
	serveEnvironment(t)
	t.Setenv("ACCOUNTSVC_PROVIDER_PROFILE_BACKFILL", "false")
	c, err := LoadConfig("serve")
	if err != nil || c.Library.ProviderProfileBackfill == nil || *c.Library.ProviderProfileBackfill {
		t.Fatalf("backfill switch not loaded: %v", err)
	}
	for _, mode := range []string{"serve", "migrate"} {
		t.Run(mode, func(t *testing.T) {
			serveEnvironment(t)
			t.Setenv("ACCOUNTSVC_PROVIDER_PROFILE_BACKFILL", "sensitive-bool")
			_, err := LoadConfig(mode)
			if err == nil || !strings.Contains(err.Error(), "ACCOUNTSVC_PROVIDER_PROFILE_BACKFILL") || strings.Contains(err.Error(), "sensitive") {
				t.Fatalf("invalid switch: %v", err)
			}
		})
	}
}
