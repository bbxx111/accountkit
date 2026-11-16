package adminauth

import (
	"errors"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/bbxx111/accountkit/httpapi/admin"
)

// RoleBinding grants catalog permissions to one exact external role name.
type RoleBinding struct {
	Name        string
	Permissions []string
}
type permissionPolicy map[string]map[admin.Permission]bool

// ValidRoleName checks a configured role scalar without normalizing its identity.
func ValidRoleName(name string) bool {
	return name != "" && len(name) <= MaxRoleNameBytes && utf8.ValidString(name) && strings.TrimSpace(name) == name && !strings.ContainsFunc(name, unicode.IsControl)
}

func compilePermissions(bindings []RoleBinding, catalog []admin.Permission) (permissionPolicy, error) {
	known := make(map[string]admin.Permission, len(catalog))
	groups := make(map[string][]admin.Permission)
	for _, p := range catalog {
		known[string(p)] = p
		group, _, _ := strings.Cut(string(p), ".")
		groups[group] = append(groups[group], p)
	}
	policy := make(permissionPolicy, len(bindings))
	for _, binding := range bindings {
		name := binding.Name
		if !ValidRoleName(name) {
			return nil, errors.New("adminauth: invalid role name")
		}
		if _, exists := policy[name]; exists {
			return nil, errors.New("adminauth: duplicate role name")
		}
		if len(binding.Permissions) == 0 {
			return nil, errors.New("adminauth: role permissions are required")
		}
		granted := make(map[admin.Permission]bool)
		for _, value := range binding.Permissions {
			if p, ok := known[value]; ok {
				granted[p] = true
				continue
			}
			group, pattern, ok := strings.Cut(value, ".")
			if !ok || pattern != "*" || len(groups[group]) == 0 {
				return nil, errors.New("adminauth: invalid administrator permission")
			}
			for _, p := range groups[group] {
				granted[p] = true
			}
		}
		policy[name] = granted
	}
	return policy, nil
}
func (p permissionPolicy) forRoles(roles map[string]bool) map[admin.Permission]bool {
	granted := make(map[admin.Permission]bool)
	for role, present := range roles {
		if present {
			for permission := range p[role] {
				granted[permission] = true
			}
		}
	}
	return granted
}

// RequirePermission authorizes only the trusted context established by this verifier.
func (v *Verifier) RequirePermission(permission admin.Permission) func(http.Handler) http.Handler {
	return v.requireAuthorization(func(a authenticated) bool { return a.permissions[permission] }, "administrator permission is required")
}
