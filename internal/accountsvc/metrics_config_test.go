package accountsvc

import (
	"os"
	"strings"
	"testing"
)

func TestConfigMetrics(t *testing.T) {
	for _, tc := range []struct {
		name, enabled, instance, field string
		wantEnabled                    bool
	}{
		{name: "unset"},
		{name: "empty", enabled: ""},
		{name: "false ignores instance", enabled: "false", instance: "invalid/secret"},
		{name: "enabled", enabled: "true", instance: "Accountsvc.prod_1-2", wantEnabled: true},
		{name: "maximum length", enabled: "true", instance: strings.Repeat("a", 64), wantEnabled: true},
		{name: "invalid bool", enabled: "synthetic-secret", field: "ACCOUNTSVC_METRICS_ENABLED"},
		{name: "missing instance", enabled: "true", field: "ACCOUNTSVC_METRICS_INSTANCE"},
		{name: "too long", enabled: "true", instance: strings.Repeat("a", 65), field: "ACCOUNTSVC_METRICS_INSTANCE"},
		{name: "invalid first", enabled: "true", instance: "_synthetic-secret", field: "ACCOUNTSVC_METRICS_INSTANCE"},
		{name: "invalid character", enabled: "true", instance: "synthetic-secret/value", field: "ACCOUNTSVC_METRICS_INSTANCE"},
		{name: "unicode", enabled: "true", instance: "实例", field: "ACCOUNTSVC_METRICS_INSTANCE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			serveEnvironment(t)
			t.Setenv("ACCOUNTSVC_METRICS_ENABLED", tc.enabled)
			t.Setenv("ACCOUNTSVC_METRICS_INSTANCE", tc.instance)
			if tc.name == "unset" {
				if err := os.Unsetenv("ACCOUNTSVC_METRICS_ENABLED"); err != nil {
					t.Fatal(err)
				}
			}
			cfg, err := LoadConfig("serve")
			if tc.field != "" {
				if err == nil || err.Error() != "accountsvc: invalid or missing "+tc.field {
					t.Fatalf("invalid metric setting must return only its field name, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			wantInstance := ""
			if tc.wantEnabled {
				wantInstance = tc.instance
			}
			if cfg.MetricsEnabled != tc.wantEnabled || cfg.MetricsInstance != wantInstance {
				t.Fatalf("metrics enabled=%v instance=%q", cfg.MetricsEnabled, cfg.MetricsInstance)
			}
		})
	}
	t.Run("migrate ignores metrics", func(t *testing.T) {
		configEnvironment(t)
		t.Setenv("ACCOUNTSVC_METRICS_ENABLED", "synthetic-secret-invalid")
		t.Setenv("ACCOUNTSVC_METRICS_INSTANCE", "synthetic-secret/invalid")
		if _, err := LoadConfig("migrate"); err != nil {
			t.Fatal(err)
		}
	})
}
