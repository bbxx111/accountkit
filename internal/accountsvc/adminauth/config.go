package adminauth

import (
	"errors"
	"net/url"
	"strings"
)

// Config identifies an external provider and the separate administrator API audience.
// Claim locations are JSON Pointers, defaulting to /roles and /preferred_username.
type Config struct{ Issuer, Audience, RolesClaim, UsernameClaim string }

// Validate rejects unsafe provider URLs and malformed claim pointers before any I/O.
func (c Config) Validate() error {
	if err := validateHTTPS(c.Issuer); err != nil {
		return errors.New("adminauth: issuer must be an absolute HTTPS URL without credentials, query or fragment")
	}
	u, _ := url.Parse(c.Issuer)
	if u.RawQuery != "" || u.ForceQuery {
		return errors.New("adminauth: issuer must not contain a query")
	}
	if strings.TrimSpace(c.Audience) == "" || strings.TrimSpace(c.Audience) != c.Audience {
		return errors.New("adminauth: audience is required and must not have surrounding whitespace")
	}
	for _, pointer := range []string{c.RolesClaim, c.UsernameClaim} {
		if pointer != "" && !validPointer(pointer) {
			return errors.New("adminauth: claim location must be a valid JSON Pointer")
		}
	}
	return nil
}

func validateHTTPS(value string) error {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || strings.Contains(value, "#") || u.Opaque != "" || strings.TrimSpace(value) != value {
		return errors.New("invalid HTTPS URL")
	}
	return nil
}

func validPointer(pointer string) bool {
	if !strings.HasPrefix(pointer, "/") {
		return false
	}
	for i := 0; i < len(pointer); i++ {
		if pointer[i] == '~' {
			if i+1 == len(pointer) || (pointer[i+1] != '0' && pointer[i+1] != '1') {
				return false
			}
			i++
		}
	}
	return true
}
