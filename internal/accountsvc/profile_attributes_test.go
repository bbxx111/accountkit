package accountsvc

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const serviceProfileSchema = `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"bio":{"type":"string"}},"additionalProperties":false}`

func profileSchemaFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "synthetic-private-schema.json")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertProfileSchemaError(t *testing.T, err error, private ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("invalid profile Schema configuration was accepted")
	}
	if !strings.Contains(err.Error(), "ACCOUNTSVC_PROFILE_ATTRIBUTES_SCHEMA_FILE") {
		t.Fatalf("configuration name missing from error: %v", err)
	}
	for _, value := range private {
		if value != "" && strings.Contains(err.Error(), value) {
			t.Fatal("profile Schema error disclosed a private path or content")
		}
	}
}

func TestLoadConfigProfileAttributes(t *testing.T) {
	t.Run("unset remains disabled", func(t *testing.T) {
		serveEnvironment(t)
		cfg, err := LoadConfig("serve")
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Library.ProfileAttributes != nil {
			t.Fatal("unset profile Schema enabled profile attributes")
		}
	})
	t.Run("valid snapshot and reload", func(t *testing.T) {
		serveEnvironment(t)
		path := profileSchemaFile(t, serviceProfileSchema)
		t.Setenv("ACCOUNTSVC_PROFILE_ATTRIBUTES_SCHEMA_FILE", path)
		cfg, err := LoadConfig("serve")
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Library.ProfileAttributes == nil || string(cfg.Library.ProfileAttributes.Schema) != serviceProfileSchema {
			t.Fatal("serve did not load profile Schema bytes")
		}
		next := strings.Replace(serviceProfileSchema, `"bio"`, `"biography"`, 1)
		if err := os.WriteFile(path, []byte(next), 0600); err != nil {
			t.Fatal(err)
		}
		if string(cfg.Library.ProfileAttributes.Schema) != serviceProfileSchema {
			t.Fatal("loaded profile Schema changed with the file")
		}
		reloaded, err := LoadConfig("serve")
		if err != nil {
			t.Fatal(err)
		}
		if reloaded.Library.ProfileAttributes == nil || string(reloaded.Library.ProfileAttributes.Schema) != next {
			t.Fatal("a new configuration did not load changed Schema bytes")
		}
	})
	t.Run("exact 64 KiB accepted", func(t *testing.T) {
		serveEnvironment(t)
		content := serviceProfileSchema + strings.Repeat(" ", 64*1024-len(serviceProfileSchema))
		t.Setenv("ACCOUNTSVC_PROFILE_ATTRIBUTES_SCHEMA_FILE", profileSchemaFile(t, content))
		cfg, err := LoadConfig("serve")
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Library.ProfileAttributes == nil || !bytes.Equal(cfg.Library.ProfileAttributes.Schema, []byte(content)) {
			t.Fatal("maximum-size Schema was not loaded intact")
		}
	})
	for _, tc := range []struct{ name, content string }{
		{"empty", ""},
		{"whitespace", " \n\t"},
		{"invalid JSON", `{"synthetic_private_schema_secret":`},
		{"invalid Schema", `{"type":"object","properties":{"synthetic_private_schema_secret":{"type":"string"}}}`},
		{"over limit valid prefix", serviceProfileSchema + strings.Repeat(" ", 64*1024+1-len(serviceProfileSchema))},
		{"over limit invalid suffix", serviceProfileSchema + strings.Repeat(" ", 64*1024-len(serviceProfileSchema)) + "!"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			serveEnvironment(t)
			path := profileSchemaFile(t, tc.content)
			t.Setenv("ACCOUNTSVC_PROFILE_ATTRIBUTES_SCHEMA_FILE", path)
			_, err := LoadConfig("serve")
			assertProfileSchemaError(t, err, path, filepath.Base(path), "synthetic_private_schema_secret", tc.content)
		})
	}
	for _, kind := range []string{"missing", "directory", "URL"} {
		t.Run(kind, func(t *testing.T) {
			serveEnvironment(t)
			path := t.TempDir()
			if kind == "missing" {
				path = filepath.Join(path, "synthetic-private-missing.json")
			} else if kind == "URL" {
				path = "https://synthetic-private-schema.invalid/profile.json"
			}
			t.Setenv("ACCOUNTSVC_PROFILE_ATTRIBUTES_SCHEMA_FILE", path)
			_, err := LoadConfig("serve")
			assertProfileSchemaError(t, err, path)
		})
	}
	t.Run("migrate ignores missing and invalid files", func(t *testing.T) {
		configEnvironment(t)
		for _, path := range []string{filepath.Join(t.TempDir(), "missing.json"), profileSchemaFile(t, "synthetic_private_schema_secret"), t.TempDir()} {
			t.Setenv("ACCOUNTSVC_PROFILE_ATTRIBUTES_SCHEMA_FILE", path)
			cfg, err := LoadConfig("migrate")
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Library.ProfileAttributes != nil {
				t.Fatal("migrate loaded service-only profile Schema")
			}
		}
	})
}
