// Package introspection exposes the service-only consumer token introspection adapter.
package introspection

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bbxx111/accountkit/internal/accountsvc/basicclients"
	"github.com/bbxx111/accountkit/user"
)

const maxBody = 64 * 1024

// lookupTimeout bounds token validation (including the session lookup), so a hung
// database yields 503 instead of holding the request until the caller gives up.
const lookupTimeout = 3 * time.Second

type handler struct {
	clients      *basicclients.Matcher
	authenticate func(context.Context, string) (user.Principal, error)
}

// New snapshots validated client credentials and delegates token validation to the
// consumer service's strict authenticator, which checks the committed session row
// (revocation is immediate, dependency failures map to 503). It never parses JWT
// claims itself and does not query account state.
func New(clients map[string][]string, authenticate func(context.Context, string) (user.Principal, error)) (http.Handler, error) {
	matcher, err := basicclients.NewMatcher(clients)
	if err != nil {
		return nil, errors.New("accountsvc: ACCOUNTSVC_INTROSPECTION_CLIENTS must contain unique nonempty client IDs and lists of base64 secrets of at least 32 bytes")
	}
	if authenticate == nil {
		return nil, errors.New("accountsvc: introspection authenticator is required")
	}
	return &handler{clients: matcher, authenticate: authenticate}, nil
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	if !h.clients.Authorized(r) {
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
	ctx, cancel := context.WithTimeout(r.Context(), lookupTimeout)
	defer cancel()
	principal, err := h.authenticate(ctx, form.Get("token"))
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

func oauthError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, struct {
		Error string `json:"error"`
	}{code})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
