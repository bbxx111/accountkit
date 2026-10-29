package adminauth

import (
	"errors"
	"strings"

	"github.com/bbxx111/accountkit/internal/accountsvc/oidcjwt"
)

// Config identifies an external provider and the separate administrator API audience.
// Claim locations are JSON Pointers, defaulting to /roles and /preferred_username.
// AllowHTTP additionally accepts a plain http:// issuer, discovery document and jwks_uri;
// accountsvc sets it only in development mode.
type Config struct {
	Issuer, Audience, RolesClaim, UsernameClaim string
	AllowHTTP                                   bool
}

// Validate rejects unsafe provider URLs and malformed claim pointers before any I/O.
func (c Config) Validate() error {
	valid := oidcjwt.ValidIssuer
	if c.AllowHTTP {
		valid = oidcjwt.ValidPlainIssuer
	}
	if !valid(c.Issuer) {
		return errors.New("adminauth: issuer must be an absolute HTTPS URL without credentials, query or fragment")
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
