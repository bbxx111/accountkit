// Package introspection exposes the service-only consumer token introspection adapter.
package introspection

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"github.com/bbxx111/accountkit/user"
)

const maxBody = 64 * 1024

type handler struct {
	clients      map[string][][sha256.Size]byte
	authenticate func(context.Context, string) (user.Principal, error)
}

// New snapshots validated client credentials and delegates token validation to the
// existing consumer service. It neither parses JWT claims nor queries account state.
func New(clients map[string][]string, authenticate func(context.Context, string) (user.Principal, error)) (http.Handler, error) {
	if err := validateClients(clients); err != nil {
		return nil, err
	}
	if authenticate == nil {
		return nil, errors.New("accountsvc: introspection authenticator is required")
	}
	h := &handler{clients: make(map[string][][sha256.Size]byte, len(clients)), authenticate: authenticate}
	for id, secrets := range clients {
		for _, secret := range secrets {
			h.clients[id] = append(h.clients[id], sha256.Sum256([]byte(secret)))
		}
	}
	return h, nil
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	if !h.authorized(r) {
		w.Header().Set("WWW-Authenticate", `Basic realm="accountsvc-introspection"`)
		oauthError(w, http.StatusUnauthorized, "invalid_client")
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		oauthError(w, http.StatusMethodNotAllowed, "invalid_request")
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/x-www-form-urlencoded" {
		oauthError(w, http.StatusUnsupportedMediaType, "invalid_request")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			oauthError(w, http.StatusRequestEntityTooLarge, "invalid_request")
		} else {
			oauthError(w, http.StatusBadRequest, "invalid_request")
		}
		return
	}
	// Parse only the bounded body. Request.ParseForm would merge query parameters.
	form, err := url.ParseQuery(string(body))
	if err != nil || len(form["token"]) != 1 || strings.TrimSpace(form.Get("token")) == "" {
		oauthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	principal, err := h.authenticate(r.Context(), form.Get("token"))
	if errors.Is(err, user.ErrInvalidToken) {
		writeJSON(w, http.StatusOK, struct {
			Active bool `json:"active"`
		}{false})
		return
	}
	if err != nil {
		oauthError(w, http.StatusServiceUnavailable, "temporarily_unavailable")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Active   bool   `json:"active"`
		Sub      string `json:"sub"`
		Scope    string `json:"scope"`
		SID      string `json:"sid"`
		AuthTime int64  `json:"auth_time"`
	}{true, principal.UserID, principal.Scope, principal.SessionID, principal.AuthTime.Unix()})
}

func (h *handler) authorized(r *http.Request) bool {
	if len(r.Header.Values("Authorization")) != 1 {
		return false
	}
	id, password, ok := r.BasicAuth()
	if !ok {
		return false
	}
	digest := sha256.Sum256([]byte(password))
	match := 0
	// Compare fixed-size hashes and inspect every rotation entry, never return on
	// the first match. The public client ID selects its permitted secrets.
	for _, expected := range h.clients[id] {
		match |= subtle.ConstantTimeCompare(digest[:], expected[:])
	}
	return match == 1
}

func oauthError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, struct {
		Error string `json:"error"`
	}{code})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
