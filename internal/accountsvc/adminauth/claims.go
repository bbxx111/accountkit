package adminauth

import (
	"strconv"
	"strings"

	"github.com/bbxx111/accountkit"
	"github.com/golang-jwt/jwt/v5"
)

func (v *Verifier) extractClaims(claims jwt.MapClaims) (accountkit.AdminPrincipal, map[string]bool, error) {
	subject, ok := claims["sub"].(string)
	if !ok || strings.TrimSpace(subject) == "" {
		return accountkit.AdminPrincipal{}, nil, errInvalid
	}
	principal := accountkit.AdminPrincipal{Issuer: v.cfg.Issuer, Subject: subject}
	if username, ok := pointerValue(map[string]any(claims), v.cfg.UsernameClaim); ok {
		principal.Username, _ = username.(string)
	}
	roles := make(map[string]bool)
	if value, ok := pointerValue(map[string]any(claims), v.cfg.RolesClaim); ok {
		array, ok := value.([]any)
		if !ok {
			return accountkit.AdminPrincipal{}, nil, errInvalid
		}
		for _, value := range array {
			role, ok := value.(string)
			if !ok {
				return accountkit.AdminPrincipal{}, nil, errInvalid
			}
			roles[role] = true
		}
	}
	if roles["super-admin"] {
		roles["operator"] = true
	}
	return principal, roles, nil
}

func pointerValue(value any, pointer string) (any, bool) {
	for _, part := range strings.Split(pointer[1:], "/") {
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		switch current := value.(type) {
		case map[string]any:
			var ok bool
			value, ok = current[part]
			if !ok {
				return nil, false
			}
		case []any:
			if part == "" || (len(part) > 1 && part[0] == '0') {
				return nil, false
			}
			for _, char := range part {
				if char < '0' || char > '9' {
					return nil, false
				}
			}
			index, err := strconv.Atoi(part)
			if err != nil || index >= len(current) {
				return nil, false
			}
			value = current[index]
		default:
			return nil, false
		}
	}
	return value, true
}
