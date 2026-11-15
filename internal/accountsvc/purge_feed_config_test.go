package accountsvc

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func TestConfigPurgeFeedClients(t *testing.T) {
	t.Run("unset", func(t *testing.T) {
		serveEnvironment(t)
		cfg, err := LoadConfig("serve")
		if err != nil || cfg.ServiceClients.HasPermission("purged_users.read") {
			t.Fatalf("unset feed: %v", err)
		}
	})
	t.Run("valid", func(t *testing.T) {
		unifiedClientEnvironment(t)
		cfg, err := LoadConfig("serve")
		if err != nil || !cfg.ServiceClients.HasPermission("purged_users.read") {
			t.Fatalf("valid feed: %v", err)
		}
	})
	t.Run("invalid secret", func(t *testing.T) {
		unifiedClientEnvironment(t)
		t.Setenv("ACCOUNTSVC_SERVICE_CLIENT_AI_FOOD_BASIC_SECRETS", "too-short-secret")
		_, err := LoadConfig("serve")
		if err == nil || !strings.Contains(err.Error(), "ACCOUNTSVC_SERVICE_CLIENT_AI_FOOD_BASIC_SECRETS") || strings.Contains(err.Error(), "too-short-secret") {
			t.Fatalf("safe error: %v", err)
		}
	})
	t.Run("shared secret", func(t *testing.T) {
		unifiedClientEnvironment(t)
		t.Setenv("ACCOUNTSVC_SERVICE_CLIENTS", "AI_FOOD,SECOND")
		t.Setenv("ACCOUNTSVC_SERVICE_CLIENT_SECOND_ID", "second")
		t.Setenv("ACCOUNTSVC_SERVICE_CLIENT_SECOND_AUTH_METHODS", "basic")
		t.Setenv("ACCOUNTSVC_SERVICE_CLIENT_SECOND_PERMISSIONS", "purged_users.read")
		t.Setenv("ACCOUNTSVC_SERVICE_CLIENT_SECOND_BASIC_SECRETS", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{11}, 32)))
		if _, err := LoadConfig("serve"); err == nil {
			t.Fatal("shared credential accepted")
		}
	})
	t.Run("migrate ignores", func(t *testing.T) {
		configEnvironment(t)
		t.Setenv("ACCOUNTSVC_PURGE_FEED_BASIC_CLIENTS", "not-json")
		if _, err := LoadConfig("migrate"); err != nil {
			t.Fatal(err)
		}
	})
}
