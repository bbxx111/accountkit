// Package httpcredentials parses a single HTTP Authorization credential without authenticating it.
package httpcredentials

import (
	"net/http"
	"strings"
)

// MaxBearerLength is the maximum accepted Bearer token length in bytes.
const MaxBearerLength = 16 * 1024

// Supported schemes are returned in lowercase by Parse.
const (
	SchemeBearer = "bearer"
	SchemeBasic  = "basic"
)

// Parse requires exactly one Authorization header and an ASCII-space separator.
// It returns the lowercase scheme even for malformed credentials, so callers can
// preserve their protocol-specific challenges. Duplicate headers return no scheme.
func Parse(r *http.Request) (scheme, credential string, ok bool) {
	headers := r.Header.Values("Authorization")
	if len(headers) != 1 {
		return "", "", false
	}
	name, rest, found := strings.Cut(headers[0], " ")
	scheme = strings.ToLower(name)
	if !found || (scheme != SchemeBearer && scheme != SchemeBasic) {
		return scheme, "", false
	}
	credential = strings.TrimLeft(rest, " ")
	if credential == "" || (scheme == SchemeBearer && len(credential) > MaxBearerLength) {
		return scheme, "", false
	}
	// Both supported schemes carry token68 credentials. In particular, commas,
	// whitespace and control bytes must never combine or hide another credential.
	padding := false
	for i := 0; i < len(credential); i++ {
		c := credential[i]
		if c == '=' {
			padding = true
			continue
		}
		if padding || !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-._~+/", rune(c))) {
			return scheme, "", false
		}
	}
	return scheme, credential, true
}
