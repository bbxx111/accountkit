// Package basicclients parses service client credential lists and checks HTTP Basic
// credentials against them. Introspection and the purged-user feed use separate lists.
package basicclients

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"unicode"
)

// Parse validates a JSON object mapping client IDs to base64 secrets (each at least 32 bytes)
// without silently overwriting duplicate IDs. Secrets remain base64 strings: callers use that
// exact string as the Basic password. The error names only the variable.
func Parse(raw, variable string) (map[string][]string, error) {
	invalid := errors.New("accountsvc: " + variable + " must contain unique nonempty client IDs and lists of base64 secrets of at least 32 bytes")
	d := json.NewDecoder(strings.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return nil, invalid
	}
	clients := make(map[string][]string)
	for d.More() {
		token, err = d.Token()
		if err != nil {
			return nil, invalid
		}
		id, ok := token.(string)
		if !ok {
			return nil, invalid
		}
		if _, exists := clients[id]; exists {
			return nil, invalid
		}
		var secrets []string
		if err = d.Decode(&secrets); err != nil {
			return nil, invalid
		}
		clients[id] = secrets
	}
	if token, err = d.Token(); err != nil || token != json.Delim('}') {
		return nil, invalid
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, invalid
	}
	if !Valid(clients) {
		return nil, invalid
	}
	return clients, nil
}

// Valid reports whether every client ID is a usable Basic username and every secret is
// canonical base64 of at least 32 bytes.
func Valid(clients map[string][]string) bool {
	if len(clients) == 0 {
		return false
	}
	for id, secrets := range clients {
		// A colon cannot be represented in a Basic username.
		if id == "" || strings.ContainsRune(id, ':') || strings.IndexFunc(id, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) >= 0 || len(secrets) == 0 {
			return false
		}
		for _, secret := range secrets {
			raw, err := base64.StdEncoding.Strict().DecodeString(secret)
			if err != nil || len(raw) < 32 || base64.StdEncoding.EncodeToString(raw) != secret {
				return false
			}
		}
	}
	return true
}

// Matcher holds hashed secrets per client ID.
type Matcher struct {
	clients map[string][][sha256.Size]byte
}

// NewMatcher snapshots validated clients; it never retains the plaintext secrets.
func NewMatcher(clients map[string][]string) (*Matcher, error) {
	if !Valid(clients) {
		return nil, errors.New("accountsvc: invalid service client configuration")
	}
	m := &Matcher{clients: make(map[string][][sha256.Size]byte, len(clients))}
	for id, secrets := range clients {
		for _, secret := range secrets {
			m.clients[id] = append(m.clients[id], sha256.Sum256([]byte(secret)))
		}
	}
	return m, nil
}

// Authorized requires exactly one Authorization header with Basic credentials matching a
// configured secret of that client ID. It compares fixed-size hashes and inspects every
// rotation entry rather than returning on the first match.
func (m *Matcher) Authorized(r *http.Request) bool {
	if len(r.Header.Values("Authorization")) != 1 {
		return false
	}
	id, password, ok := r.BasicAuth()
	if !ok {
		return false
	}
	digest := sha256.Sum256([]byte(password))
	match := 0
	for _, expected := range m.clients[id] {
		match |= subtle.ConstantTimeCompare(digest[:], expected[:])
	}
	return match == 1
}

// SharedSecret reports whether any secret appears in both lists, so one credential cannot
// grant access to two separately authorized interfaces.
func SharedSecret(a, b map[string][]string) bool {
	seen := map[string]bool{}
	for _, secrets := range a {
		for _, s := range secrets {
			seen[s] = true
		}
	}
	for _, secrets := range b {
		for _, s := range secrets {
			if seen[s] {
				return true
			}
		}
	}
	return false
}
