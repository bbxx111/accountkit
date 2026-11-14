package accountsvc

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/internal/accountsvc/serviceauth"
	"github.com/bbxx111/accountkit/internal/testkeys"
)

// Read the runnable commented examples, so a typo in the distributed template
// fails configuration loading instead of being hidden by a separate Go fixture.
func serviceClientTemplateExample(t *testing.T, name string) map[string]string {
	t.Helper()
	data, err := os.ReadFile("../../deploy/accountsvc/.env.example")
	if err != nil {
		t.Fatal(err)
	}
	_, block, ok := strings.Cut(strings.ReplaceAll(string(data), "\r\n", "\n"), "# BEGIN SERVICE CLIENT EXAMPLE "+name+"\n")
	if !ok {
		t.Fatalf("missing service-client template example %s", name)
	}
	block, _, ok = strings.Cut(block, "# END SERVICE CLIENT EXAMPLE "+name)
	if !ok {
		t.Fatalf("unterminated template example %s", name)
	}
	values := map[string]string{}
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "#"))
		if !strings.HasPrefix(line, "ACCOUNTSVC_") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatal("invalid example assignment")
		}
		value = strings.ReplaceAll(value, "<basic-secret-old>", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{31}, 32)))
		value = strings.ReplaceAll(value, "<basic-secret-new>", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{32}, 32)))
		values[key] = value
	}
	return values
}

func checkServiceClientExample(t *testing.T, values map[string]string, bearer, purge bool, secretCount int) {
	t.Helper()
	configEnvironment(t)
	t.Setenv("ACCOUNTSVC_MODE", "development")
	t.Setenv("ACCOUNTSVC_TLS_ENABLED", "false")
	t.Setenv("ACCOUNTSVC_SMTP_HOST", "mailpit")
	t.Setenv("ACCOUNTSVC_SMTP_PORT", "1025")
	t.Setenv("ACCOUNTSVC_SMTP_FROM", "accounts@example.test")
	t.Setenv("ACCOUNTSVC_SMTP_TLS_MODE", "none")
	for key, value := range values {
		t.Setenv(key, value)
	}
	cfg, err := LoadConfig("serve")
	if err != nil {
		t.Fatalf("distributed example failed to load: %v", err)
	}
	if cfg.ServiceClients == nil || !cfg.ServiceClients.HasPermission("tokens.introspect") || cfg.ServiceClients.HasPermission("purged_users.read") != purge || cfg.Service.Enabled() != bearer {
		t.Fatal("distributed example selected the wrong permissions or trust")
	}
	if secretCount == 0 {
		return
	}
	auth, _, _, err := serviceAuthenticators(cfg, slog.Default(), nil)
	if err != nil {
		t.Fatal(err)
	}
	alias := values["ACCOUNTSVC_SERVICE_CLIENTS"]
	secrets := strings.Split(values["ACCOUNTSVC_SERVICE_CLIENT_"+alias+"_BASIC_SECRETS"], ",")
	if len(secrets) != secretCount {
		t.Fatal("wrong number of example rotation secrets")
	}
	for _, secret := range secrets {
		r := httptest.NewRequest("POST", "/v1/introspect", nil)
		r.SetBasicAuth("business-service", secret)
		if auth.Authenticate(r).Outcome != serviceauth.OK {
			t.Fatal("example Basic credential was rejected")
		}
	}
}

func TestConfigServiceClientTemplateExamples(t *testing.T) {
	t.Run("DEFAULT_EMPTY", func(t *testing.T) {
		configEnvironment(t)
		t.Setenv("ACCOUNTSVC_MODE", "development")
		t.Setenv("ACCOUNTSVC_TLS_ENABLED", "false")
		t.Setenv("ACCOUNTSVC_SMTP_HOST", "mailpit")
		t.Setenv("ACCOUNTSVC_SMTP_PORT", "1025")
		t.Setenv("ACCOUNTSVC_SMTP_FROM", "accounts@example.test")
		t.Setenv("ACCOUNTSVC_SMTP_TLS_MODE", "none")
		data, err := os.ReadFile("../../deploy/accountsvc/.env.example")
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
			if ok && (strings.HasPrefix(key, "ACCOUNTSVC_SERVICE_") || key == "ACCOUNTSVC_SERVICE_CLIENTS") {
				t.Setenv(key, value)
			}
		}
		cfg, err := LoadConfig("serve")
		if err != nil || cfg.ServiceClients == nil || cfg.ServiceClients.HasPermission("tokens.introspect") || cfg.ServiceClients.HasPermission("purged_users.read") || cfg.Service.Enabled() {
			t.Fatal("default template must produce a non-nil empty registry")
		}
	})
	for _, tc := range []struct {
		name          string
		bearer, purge bool
		secrets       int
	}{
		{"BASIC_ONLY", false, false, 1},
		{"BEARER_ONLY", true, false, 0},
		{"BOTH_ROTATION", true, true, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkServiceClientExample(t, serviceClientTemplateExample(t, tc.name), tc.bearer, tc.purge, tc.secrets)
		})
	}
}

func TestConfigServiceClientComposeExamples(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("Docker Compose is required to expand deployment examples")
	}
	names := []string{"DEFAULT_ABSENT", "DEFAULT_EMPTY", "DEFAULT_WHITESPACE", "LEGACY_WHITESPACE", "BASIC_ONLY", "BEARER_ONLY", "BOTH_ROTATION", "DYNAMIC_ALIAS"}
	legacyKeys := []string{"INTROSPECTION_BASIC_CLIENTS", "PURGE_FEED_BASIC_CLIENTS", "INTROSPECTION_BEARER_CLIENTS", "PURGE_FEED_BEARER_CLIENTS"}
	for _, key := range legacyKeys {
		names = append(names, "REJECT_"+key, "REJECT_MIXED_"+key)
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			data, err := os.ReadFile("../../deploy/accountsvc/compose.yaml")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), data, 0600); err != nil {
				t.Fatal(err)
			}
			values := map[string]string{
				"POSTGRES_PASSWORD":  "synthetic-development-password",
				"JWT_KEY":            testkeys.JWTBase64(1),
				"SUBJECT_HMAC_KEY":   base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{33}, 32)),
				"SUBJECT_CIPHER_KEY": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{34}, 32)),
			}
			legacyKey := ""
			if strings.HasPrefix(name, "REJECT_") {
				legacyKey = "ACCOUNTSVC_" + strings.TrimPrefix(strings.TrimPrefix(name, "REJECT_"), "MIXED_")
				values[legacyKey] = "[]"
				if strings.Contains(legacyKey, "_BASIC_") {
					values[legacyKey] = "{}"
				}
			}
			switch name {
			case "DEFAULT_EMPTY":
				values["ACCOUNTSVC_SERVICE_CLIENTS"] = ""
			case "DEFAULT_WHITESPACE":
				values["ACCOUNTSVC_SERVICE_CLIENTS"] = "   "
			case "LEGACY_WHITESPACE":
				for _, key := range legacyKeys {
					values["ACCOUNTSVC_"+key] = "   "
				}
			}
			if strings.Contains(name, "ONLY") || name == "BOTH_ROTATION" || name == "DYNAMIC_ALIAS" || strings.HasPrefix(name, "REJECT_MIXED_") {
				example := name
				if name == "DYNAMIC_ALIAS" || strings.HasPrefix(name, "REJECT_MIXED_") {
					example = "BASIC_ONLY"
				}
				for key, value := range serviceClientTemplateExample(t, example) {
					if name == "DYNAMIC_ALIAS" {
						key = strings.ReplaceAll(key, "_BUSINESS_", "_DYNAMIC_CLIENT_")
						if key == "ACCOUNTSVC_SERVICE_CLIENTS" {
							value = "DYNAMIC_CLIENT"
						}
					}
					values[key] = value
				}
			}
			var file strings.Builder
			for key, value := range values {
				file.WriteString(key + "='" + value + "'\n")
			}
			if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(file.String()), 0600); err != nil {
				t.Fatal(err)
			}
			args := []string{"compose", "--env-file", filepath.Join(dir, ".env"), "-f", filepath.Join(dir, "compose.yaml")}
			args = append(args, "config", "--format", "json")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "docker", args...)
			for _, entry := range os.Environ() {
				key, _, _ := strings.Cut(entry, "=")
				if !strings.HasPrefix(key, "ACCOUNTSVC_") && !strings.HasPrefix(key, "ACCOUNTKIT_") {
					if _, owned := values[key]; !owned {
						cmd.Env = append(cmd.Env, entry)
					}
				}
			}
			data, err = cmd.Output()
			if err != nil {
				t.Fatal("Docker Compose example expansion failed (output withheld to protect credentials)")
			}
			var expanded struct {
				Services map[string]struct{ Environment map[string]string }
			}
			if err := json.Unmarshal(data, &expanded); err != nil {
				t.Fatal("invalid Compose JSON")
			}
			env := expanded.Services["accountsvc"].Environment
			for key, want := range values {
				if strings.HasPrefix(key, "ACCOUNTSVC_") {
					if got, present := env[key]; !present || got != want {
						t.Fatalf("Compose lost environment field %s", key)
					}
				}
			}
			for _, key := range legacyKeys {
				fullKey := "ACCOUNTSVC_" + key
				if _, supplied := values[fullKey]; !supplied {
					if _, injected := env[fullKey]; injected {
						t.Fatalf("Compose injected removed setting %s", fullKey)
					}
				}
			}
			if legacyKey != "" || strings.HasPrefix(name, "DEFAULT_") || name == "LEGACY_WHITESPACE" {
				configEnvironment(t)
				for key, value := range env {
					if strings.HasPrefix(key, "ACCOUNTSVC_") {
						t.Setenv(key, value)
					}
				}
				cfg, err := LoadConfig("serve")
				if legacyKey != "" {
					if err == nil || !strings.Contains(err.Error(), legacyKey) || strings.Contains(err.Error(), values[legacyKey]) {
						t.Fatal("expanded deployment must safely reject its removed setting")
					}
				} else if err != nil || cfg.ServiceClients == nil || cfg.ServiceClients.HasPermission("tokens.introspect") || cfg.ServiceClients.HasPermission("purged_users.read") || cfg.Service.Enabled() {
					t.Fatal("expanded default deployment must produce a non-nil empty registry")
				}
			} else {
				checkServiceClientExample(t, env, name == "BEARER_ONLY" || name == "BOTH_ROTATION", name == "BOTH_ROTATION", map[string]int{"BASIC_ONLY": 1, "BEARER_ONLY": 0, "BOTH_ROTATION": 2, "DYNAMIC_ALIAS": 1}[name])
			}
		})
	}
}
