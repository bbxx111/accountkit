package accountsvc

import (
	"strings"
	"testing"
)

func TestConfigFacebook(t *testing.T) {
	noSMTP := func(t *testing.T) {
		for _, key := range []string{"HOST", "PORT", "FROM", "USERNAME", "PASSWORD", "TLS_MODE", "TIMEOUT"} {
			t.Setenv("ACCOUNTSVC_SMTP_"+key, "")
		}
	}
	t.Run("missing app secret", func(t *testing.T) {
		serveEnvironment(t)
		t.Setenv("ACCOUNTSVC_FACEBOOK_APP_ID", "synthetic-app-id-123")
		_, err := LoadConfig("serve")
		if err == nil || !strings.Contains(err.Error(), "ACCOUNTSVC_FACEBOOK_APP_SECRET") {
			t.Fatalf("missing secret must name variable: %v", err)
		}
		for _, leaked := range []string{"synthetic-app-id-123", "synthetic-dsn-password", "synthetic-redis-password", "synthetic-smtp-password"} {
			if strings.Contains(err.Error(), leaked) {
				t.Fatalf("error leaked %q", leaked)
			}
		}
	})
	t.Run("invalid optional values name variable without leaking", func(t *testing.T) {
		for _, tc := range []struct{ key, value string }{
			{"ACCOUNTSVC_FACEBOOK_GRAPH_API_VERSION", "bad-version"},
			{"ACCOUNTSVC_FACEBOOK_NONCE_TTL", "1s"},
			{"ACCOUNTSVC_FACEBOOK_GRAPH_API_BASE_URL", "ftp://synthetic-host"},
			{"ACCOUNTSVC_FACEBOOK_JWKS_URL", "not-a-url"},
		} {
			serveEnvironment(t)
			t.Setenv("ACCOUNTSVC_FACEBOOK_APP_ID", "synthetic-app-id")
			t.Setenv("ACCOUNTSVC_FACEBOOK_APP_SECRET", "synthetic-facebook-secret")
			t.Setenv(tc.key, tc.value)
			_, err := LoadConfig("serve")
			if err == nil || !strings.Contains(err.Error(), tc.key) {
				t.Fatalf("%s: want variable name, got %v", tc.key, err)
			}
			for _, leaked := range []string{"synthetic-facebook-secret", "synthetic-app-id", tc.value} {
				if strings.Contains(err.Error(), leaked) {
					t.Fatalf("%s: error leaked %q", tc.key, leaked)
				}
			}
		}
	})
	t.Run("enabled without app", func(t *testing.T) {
		serveEnvironment(t)
		t.Setenv("ACCOUNTSVC_ENABLED_IDENTITY_KINDS", "FACEBOOK")
		t.Setenv("ACCOUNTSVC_REQUIRED_IDENTITY_KINDS", "[]")
		noSMTP(t)
		_, err := LoadConfig("serve")
		if err == nil || !strings.Contains(err.Error(), "ACCOUNTSVC_FACEBOOK_APP_ID") {
			t.Fatalf("want app id variable, got %v", err)
		}
	})
	t.Run("facebook-only without SMTP", func(t *testing.T) {
		serveEnvironment(t)
		t.Setenv("ACCOUNTSVC_ENABLED_IDENTITY_KINDS", "FACEBOOK")
		t.Setenv("ACCOUNTSVC_REQUIRED_IDENTITY_KINDS", "[]")
		t.Setenv("ACCOUNTSVC_FACEBOOK_APP_ID", "synthetic-app-id")
		t.Setenv("ACCOUNTSVC_FACEBOOK_APP_SECRET", "synthetic-facebook-secret")
		noSMTP(t)
		c, err := LoadConfig("serve")
		if err != nil {
			t.Fatalf("facebook-only service: %v", err)
		}
		if c.Library.FacebookAppID != "synthetic-app-id" {
			t.Fatal("facebook app not loaded")
		}
	})
	t.Run("migrate ignores facebook", func(t *testing.T) {
		configEnvironment(t)
		if _, err := LoadConfig("migrate"); err != nil {
			t.Fatal(err)
		}
		t.Setenv("ACCOUNTSVC_ENABLED_IDENTITY_KINDS", "FACEBOOK")
		t.Setenv("ACCOUNTSVC_REQUIRED_IDENTITY_KINDS", "[]")
		if _, err := LoadConfig("migrate"); err != nil {
			t.Fatalf("migrate must not require facebook vars: %v", err)
		}
	})
}
