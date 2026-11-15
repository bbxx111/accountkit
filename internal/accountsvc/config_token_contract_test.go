package accountsvc

import (
	"strings"
	"testing"
)

func TestConfigTokenProfilesAndBearerSubjects(t *testing.T) {
	for _, field := range []string{"ADMIN_TOKEN_PROFILE", "SERVICE_TOKEN_PROFILE"} {
		t.Run("orphan "+field, func(t *testing.T) {
			serveEnvironment(t)
			t.Setenv(envPrefix+field, "rfc9068")
			_, err := LoadConfig("serve")
			if err == nil || !strings.Contains(err.Error(), field) {
				t.Fatalf("orphan profile accepted: %v", err)
			}
		})
	}
	for _, value := range []string{"", `[]`, `null`, `[null]`, `[1]`, `[""]`, `["   "]`, `["x","x"]`, `["a\u0001"]`, `["` + strings.Repeat("x", 257) + `"]`, `"machine"`} {
		t.Run("subject "+value, func(t *testing.T) {
			unifiedBearerEnvironment(t)
			t.Setenv("ACCOUNTSVC_SERVICE_CLIENT_AI_FOOD_BEARER_SUBJECTS", value)
			_, err := LoadConfig("serve")
			if err == nil || !strings.Contains(err.Error(), "BEARER_SUBJECTS") {
				t.Fatalf("invalid subjects accepted: %v", err)
			}
		})
	}
}

func TestConfigTokenProfilesSelectAndReject(t *testing.T) {
	for _, profile := range []string{"default", "rfc9068"} {
		t.Run(profile, func(t *testing.T) {
			unifiedBearerEnvironment(t)
			t.Setenv(envPrefix+"ADMIN_ENABLED", "true")
			t.Setenv(envPrefix+"ADMIN_ISSUER", "https://idp.example.test")
			t.Setenv(envPrefix+"ADMIN_AUDIENCE", "admin-api")
			t.Setenv(envPrefix+"ADMIN_TOKEN_PROFILE", profile)
			t.Setenv(envPrefix+"SERVICE_TOKEN_PROFILE", profile)
			c, err := LoadConfig("serve")
			if err != nil {
				t.Fatal(err)
			}
			if c.Admin.TokenProfile != profile || c.Service.TokenProfile != profile {
				t.Fatal("profile not selected")
			}
		})
	}
	for _, field := range []string{"ADMIN_TOKEN_PROFILE", "SERVICE_TOKEN_PROFILE"} {
		for _, value := range []string{"azp", "unknown", "AZP", "DEFAULT", " default ", " "} {
			t.Run(field+value, func(t *testing.T) {
				unifiedBearerEnvironment(t)
				t.Setenv(envPrefix+"ADMIN_ENABLED", "true")
				t.Setenv(envPrefix+"ADMIN_ISSUER", "https://idp.example.test")
				t.Setenv(envPrefix+"ADMIN_AUDIENCE", "admin-api")
				t.Setenv(envPrefix+field, value)
				_, err := LoadConfig("serve")
				if err == nil || !strings.Contains(err.Error(), field) || strings.Contains(err.Error(), "unknown") {
					t.Fatalf("profile accepted or leaked: %v", err)
				}
			})
		}
	}
	t.Run("migrate ignores profiles and subjects", func(t *testing.T) {
		configEnvironment(t)
		t.Setenv(envPrefix+"ADMIN_TOKEN_PROFILE", "unknown")
		t.Setenv(envPrefix+"SERVICE_TOKEN_PROFILE", "unknown")
		t.Setenv(envPrefix+"SERVICE_CLIENT_ORPHAN_BEARER_SUBJECTS", "bad-json")
		if _, err := LoadConfig("migrate"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("defaults", func(t *testing.T) {
		unifiedBearerEnvironment(t)
		t.Setenv(envPrefix+"ADMIN_ENABLED", "true")
		t.Setenv(envPrefix+"ADMIN_ISSUER", "https://idp.example.test")
		t.Setenv(envPrefix+"ADMIN_AUDIENCE", "admin-api")
		c, err := LoadConfig("serve")
		if err != nil {
			t.Fatal(err)
		}
		if c.Admin.TokenProfile != "default" || c.Service.TokenProfile != "default" {
			t.Fatal("wrong defaults")
		}
	})
}
