package introspection

import "github.com/bbxx111/accountkit/internal/accountsvc/basicclients"

// ParseClients validates ACCOUNTSVC_INTROSPECTION_BASIC_CLIENTS without silently overwriting duplicate IDs.
// Secrets remain base64 strings: callers use that exact string as the Basic password.
func ParseClients(raw string) (map[string][]string, error) {
	return basicclients.Parse(raw, "ACCOUNTSVC_INTROSPECTION_BASIC_CLIENTS")
}
