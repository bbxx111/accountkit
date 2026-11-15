package accountsvc

import (
	"os"
	"sort"
	"strings"

	"github.com/bbxx111/accountkit/internal/accountsvc/adminauth"
)

const (
	adminRolePrefix           = envPrefix + "ADMIN_ROLE_"
	adminRoleBindingsVariable = adminRolePrefix + "BINDINGS"
	adminRoleNameField        = "NAME"
	adminRolePermissionsField = "PERMISSIONS"
)

func adminRoleVariable(alias, field string) string {
	return adminRolePrefix + alias + "_" + field
}

// loadAdminRoles validates the complete expected environment-name set. In
// particular, an empty orphan field is still configuration, not absence.
func (c *Config) loadAdminRoles() error {
	if env("ADMIN_AUTHORIZATION_MODE") != "" {
		return invalidConfig(envPrefix + "ADMIN_AUTHORIZATION_MODE (unsupported)")
	}
	const list = adminRoleBindingsVariable
	raw := os.Getenv(list)
	var aliases []string
	if strings.TrimSpace(raw) != "" {
		if !c.AdminEnabled {
			return invalidConfig(list + " (ADMIN_ENABLED is false)")
		}
		var err error
		aliases, err = serviceClientCSV(raw, list)
		if err != nil {
			return err
		}
	}
	expected := map[string]bool{list: true}
	for _, alias := range aliases {
		if !serviceClientAliasPattern.MatchString(alias) {
			return invalidConfig(list)
		}
		expected[adminRoleVariable(alias, adminRoleNameField)] = true
		expected[adminRoleVariable(alias, adminRolePermissionsField)] = true
	}
	names := []string{}
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, adminRolePrefix) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		if name != list && (!c.AdminEnabled || !expected[name]) {
			return invalidConfig(name + " (undeclared role, unknown field or ADMIN_ENABLED is false)")
		}
	}
	bindings := make([]adminauth.RoleBinding, 0, len(aliases))
	seen := map[string]bool{}
	for _, alias := range aliases {
		nameVariable := adminRoleVariable(alias, adminRoleNameField)
		permissionsVariable := adminRoleVariable(alias, adminRolePermissionsField)
		name := os.Getenv(nameVariable)
		if !adminauth.ValidRoleName(name) || seen[name] {
			return invalidConfig(nameVariable)
		}
		seen[name] = true
		values := strings.Split(os.Getenv(permissionsVariable), ",")
		permissions := make([]string, 0, len(values))
		unique := map[string]bool{}
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value == "" {
				return invalidConfig(permissionsVariable)
			}
			if !unique[value] {
				permissions = append(permissions, value)
				unique[value] = true
			}
		}
		binding := adminauth.RoleBinding{Name: name, Permissions: permissions}
		check := c.Admin
		check.RoleBindings = []adminauth.RoleBinding{binding}
		if err := check.Validate(); err != nil {
			return invalidConfig(permissionsVariable)
		}
		bindings = append(bindings, binding)
	}
	c.Admin.RoleBindings = bindings
	return nil
}
