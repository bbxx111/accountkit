// Package basicclients validates and matches HTTP Basic service credentials.
package basicclients

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"unicode"
)

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

// Authorized reports whether Match succeeds.
func (m *Matcher) Authorized(r *http.Request) bool {
	_, ok := m.Match(r)
	return ok
}

// Match requires exactly one Authorization header with Basic credentials matching a
// configured secret of that client ID, and returns the client ID. It compares fixed-size
// hashes and inspects every rotation entry rather than returning on the first match.
func (m *Matcher) Match(r *http.Request) (string, bool) {
	if len(r.Header.Values("Authorization")) != 1 {
		return "", false
	}
	id, password, ok := r.BasicAuth()
	if !ok {
		return "", false
	}
	digest := sha256.Sum256([]byte(password))
	match := 0
	for _, expected := range m.clients[id] {
		match |= subtle.ConstantTimeCompare(digest[:], expected[:])
	}
	if match != 1 {
		return "", false
	}
	return id, true
}
