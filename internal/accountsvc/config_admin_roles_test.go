package accountsvc

import (
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

func adminRoleEnvironment(t *testing.T) {
	t.Helper()
	serveEnvironment(t)
	t.Setenv(envPrefix+"ADMIN_ENABLED", "true")
	t.Setenv(envPrefix+"ADMIN_ISSUER", "https://admin.example.test")
	t.Setenv(envPrefix+"ADMIN_AUDIENCE", "admin-api")
}

func TestConfigAdminRoleBindings(t *testing.T) {
	adminRoleEnvironment(t)
	t.Setenv(envPrefix+"ADMIN_ROLE_BINDINGS", " CUSTOMER_SUPPORT ")
	t.Setenv(envPrefix+"ADMIN_ROLE_CUSTOMER_SUPPORT_NAME", "Support, Team")
	t.Setenv(envPrefix+"ADMIN_ROLE_CUSTOMER_SUPPORT_PERMISSIONS", " users.read, sessions.*,users.read ")
	c, err := LoadConfig("serve")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Admin.RoleBindings) != 1 || c.Admin.RoleBindings[0].Name != "Support, Team" || len(c.Admin.RoleBindings[0].Permissions) != 2 {
		t.Fatalf("bindings not preserved/deduplicated: %#v", c.Admin.RoleBindings)
	}
}

func TestConfigAdminRoleTemplate(t *testing.T) {
	adminRoleEnvironment(t)
	raw, err := os.ReadFile("../../deploy/accountsvc/.env.example")
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]bool{"ADMIN_ROLE_BINDINGS": false, "ADMIN_ROLE_OPERATOR_NAME": false, "ADMIN_ROLE_OPERATOR_PERMISSIONS": false, "ADMIN_ROLE_SUPER_ADMIN_NAME": false, "ADMIN_ROLE_SUPER_ADMIN_PERMISSIONS": false}
	for _, line := range strings.Split(string(raw), "\n") {
		name, value, ok := strings.Cut(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "#")), "=")
		name = strings.TrimPrefix(name, envPrefix)
		if found, wanted := fields[name]; ok && wanted && !found {
			t.Setenv(envPrefix+name, value)
			fields[name] = true
		}
	}
	for name, found := range fields {
		if !found {
			t.Fatalf("template missing %s", name)
		}
	}
	c, err := LoadConfig("serve")
	if err != nil || len(c.Admin.RoleBindings) != 2 {
		t.Fatalf("template policy: %v", err)
	}
}

func TestConfigAdminRoleRejections(t *testing.T) {
	cases := []struct{ name, list, role, permissions, extra, want string }{
		{"duplicate alias", "A,A", "support", "users.read", "", "ADMIN_ROLE_BINDINGS"},
		{"empty alias", "A,", "support", "users.read", "", "ADMIN_ROLE_BINDINGS"},
		{"lowercase alias", "a", "support", "users.read", "", "ADMIN_ROLE_BINDINGS"},
		{"long alias", strings.Repeat("A", 33), "support", "users.read", "", "ADMIN_ROLE_BINDINGS"},
		{"missing name", "A", "", "users.read", "", "ADMIN_ROLE_A_NAME"},
		{"name whitespace", "A", " support", "users.read", "", "ADMIN_ROLE_A_NAME"},
		{"name control", "A", "sup\nport", "users.read", "", "ADMIN_ROLE_A_NAME"},
		{"name utf8", "A", string([]byte{255}), "users.read", "", "ADMIN_ROLE_A_NAME"},
		{"name length", "A", strings.Repeat("x", 257), "users.read", "", "ADMIN_ROLE_A_NAME"},
		{"missing permissions", "A", "support", "", "", "ADMIN_ROLE_A_PERMISSIONS"},
		{"empty permission", "A", "support", "users.read,", "", "ADMIN_ROLE_A_PERMISSIONS"},
		{"scope syntax", "A", "support", "users.read users.freeze", "", "ADMIN_ROLE_A_PERMISSIONS"},
		{"unknown permission", "A", "support", "identities.resolve", "", "ADMIN_ROLE_A_PERMISSIONS"},
		{"invalid wildcard", "A", "support", "users.fr*", "", "ADMIN_ROLE_A_PERMISSIONS"},
		{"orphan empty", "", "", "", "ADMIN_ROLE_ORPHAN_NAME", "ADMIN_ROLE_ORPHAN_NAME"},
		{"unknown field empty", "A", "support", "users.read", "ADMIN_ROLE_A_PERMISSION", "ADMIN_ROLE_A_PERMISSION"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			adminRoleEnvironment(t)
			t.Setenv(envPrefix+"ADMIN_ROLE_BINDINGS", tc.list)
			if tc.list != "" {
				t.Setenv(envPrefix+"ADMIN_ROLE_A_NAME", tc.role)
				if !utf8.ValidString(tc.role) && utf8.ValidString(os.Getenv(envPrefix+"ADMIN_ROLE_A_NAME")) {
					t.Skip("OS environment normalizes invalid UTF-8; Linux covers this input")
				}
				t.Setenv(envPrefix+"ADMIN_ROLE_A_PERMISSIONS", tc.permissions)
			}
			if tc.extra != "" {
				t.Setenv(envPrefix+tc.extra, "")
			}
			_, err := LoadConfig("serve")
			if err == nil || !strings.Contains(err.Error(), envPrefix+tc.want) {
				t.Fatalf("want variable %s, got %v", tc.want, err)
			}
			if strings.Contains(err.Error(), tc.role) && len(tc.role) > 0 {
				t.Fatal("error exposed role value")
			}
		})
	}
	t.Run("duplicate exact role", func(t *testing.T) {
		adminRoleEnvironment(t)
		t.Setenv(envPrefix+"ADMIN_ROLE_BINDINGS", "A,B")
		for _, a := range []string{"A", "B"} {
			t.Setenv(envPrefix+"ADMIN_ROLE_"+a+"_NAME", "support")
			t.Setenv(envPrefix+"ADMIN_ROLE_"+a+"_PERMISSIONS", "users.read")
		}
		if _, err := LoadConfig("serve"); err == nil || !strings.Contains(err.Error(), "ADMIN_ROLE_B_NAME") {
			t.Fatalf("duplicate role: %v", err)
		}
	})
}

func TestConfigAdminRoleEmptyDisabledAndMigrate(t *testing.T) {
	for _, raw := range []string{"", "   "} {
		t.Run("empty"+raw, func(t *testing.T) {
			adminRoleEnvironment(t)
			t.Setenv(envPrefix+"ADMIN_ROLE_BINDINGS", raw)
			c, err := LoadConfig("serve")
			if err != nil || len(c.Admin.RoleBindings) != 0 {
				t.Fatalf("empty policy: %v", err)
			}
		})
	}
	for _, variable := range []string{"ADMIN_ROLE_BINDINGS", "ADMIN_ROLE_ORPHAN_NAME", "ADMIN_AUTHORIZATION_MODE"} {
		t.Run(variable, func(t *testing.T) {
			serveEnvironment(t)
			value := ""
			if variable != "ADMIN_ROLE_ORPHAN_NAME" {
				value = "unsupported"
			}
			t.Setenv(envPrefix+variable, value)
			if _, err := LoadConfig("serve"); err == nil || !strings.Contains(err.Error(), variable) {
				t.Fatalf("disabled residue accepted: %v", err)
			}
			if _, err := LoadConfig("migrate"); err != nil {
				t.Fatalf("migrate parsed admin: %v", err)
			}
		})
	}
	t.Run("unsupported mode enabled", func(t *testing.T) {
		adminRoleEnvironment(t)
		t.Setenv(envPrefix+"ADMIN_AUTHORIZATION_MODE", "builtin")
		if _, err := LoadConfig("serve"); err == nil || !strings.Contains(err.Error(), "ADMIN_AUTHORIZATION_MODE") {
			t.Fatalf("unsupported mode: %v", err)
		}
	})
	t.Run("blank placeholders disabled", func(t *testing.T) {
		serveEnvironment(t)
		t.Setenv(envPrefix+"ADMIN_ROLE_BINDINGS", " ")
		t.Setenv(envPrefix+"ADMIN_AUTHORIZATION_MODE", " ")
		if _, err := LoadConfig("serve"); err != nil {
			t.Fatal(err)
		}
	})
}
