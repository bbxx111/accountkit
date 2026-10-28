package accountsvc

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
)

func TestConfigPurgeFeedClients(t *testing.T) {
	feedSecret := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32))
	t.Run("unset", func(t *testing.T) {
		serveEnvironment(t)
		cfg, err := LoadConfig("serve")
		if err != nil || cfg.PurgeFeedBasicClients != nil {
			t.Fatalf("unset feed clients: %v %v", cfg.PurgeFeedBasicClients, err)
		}
	})
	t.Run("valid", func(t *testing.T) {
		serveEnvironment(t)
		t.Setenv("ACCOUNTSVC_PURGE_FEED_BASIC_CLIENTS", fmt.Sprintf(`{"ai-food":["%s"]}`, feedSecret))
		cfg, err := LoadConfig("serve")
		if err != nil || len(cfg.PurgeFeedBasicClients["ai-food"]) != 1 {
			t.Fatalf("valid: %v %v", cfg.PurgeFeedBasicClients, err)
		}
	})
	t.Run("invalid", func(t *testing.T) {
		serveEnvironment(t)
		t.Setenv("ACCOUNTSVC_PURGE_FEED_BASIC_CLIENTS", `{"ai-food":["too-short-secret"]}`)
		_, err := LoadConfig("serve")
		if err == nil || !strings.Contains(err.Error(), "ACCOUNTSVC_PURGE_FEED_BASIC_CLIENTS") || strings.Contains(err.Error(), "too-short-secret") {
			t.Fatalf("invalid: %v", err)
		}
	})
	t.Run("secret shared with introspection", func(t *testing.T) {
		serveEnvironment(t)
		introspectionSecret := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{8}, 32))
		t.Setenv("ACCOUNTSVC_PURGE_FEED_BASIC_CLIENTS", fmt.Sprintf(`{"ai-food":["%s"]}`, introspectionSecret))
		_, err := LoadConfig("serve")
		if err == nil || !strings.Contains(err.Error(), "ACCOUNTSVC_PURGE_FEED_BASIC_CLIENTS") || strings.Contains(err.Error(), introspectionSecret) {
			t.Fatalf("shared secret: %v", err)
		}
	})
	t.Run("migrate ignores feed clients", func(t *testing.T) {
		configEnvironment(t)
		t.Setenv("ACCOUNTSVC_PURGE_FEED_BASIC_CLIENTS", "not json")
		if _, err := LoadConfig("migrate"); err != nil {
			t.Fatalf("migrate must not parse feed clients: %v", err)
		}
	})
}
