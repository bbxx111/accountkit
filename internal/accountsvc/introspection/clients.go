package introspection

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode"
)

var errClients = errors.New("accountsvc: ACCOUNTSVC_INTROSPECTION_CLIENTS must contain unique nonempty client IDs and lists of base64 secrets of at least 32 bytes")

// ParseClients validates the JSON object without silently overwriting duplicate IDs.
// Secrets remain base64 strings: callers use that exact string as the Basic password.
func ParseClients(raw string) (map[string][]string, error) {
	d := json.NewDecoder(strings.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return nil, errClients
	}
	clients := make(map[string][]string)
	for d.More() {
		token, err = d.Token()
		if err != nil {
			return nil, errClients
		}
		id, ok := token.(string)
		if !ok {
			return nil, errClients
		}
		if _, exists := clients[id]; exists {
			return nil, errClients
		}
		var secrets []string
		if err = d.Decode(&secrets); err != nil {
			return nil, errClients
		}
		clients[id] = secrets
	}
	if token, err = d.Token(); err != nil || token != json.Delim('}') {
		return nil, errClients
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, errClients
	}
	if err = validateClients(clients); err != nil {
		return nil, err
	}
	return clients, nil
}

func validateClients(clients map[string][]string) error {
	if len(clients) == 0 {
		return errClients
	}
	for id, secrets := range clients {
		// A colon cannot be represented in a Basic username.
		if id == "" || strings.ContainsRune(id, ':') || strings.IndexFunc(id, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) >= 0 || len(secrets) == 0 {
			return errClients
		}
		for _, secret := range secrets {
			raw, err := base64.StdEncoding.Strict().DecodeString(secret)
			if err != nil || len(raw) < 32 || base64.StdEncoding.EncodeToString(raw) != secret {
				return errClients
			}
		}
	}
	return nil
}
