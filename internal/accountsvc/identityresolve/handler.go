package identityresolve

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/httpapi/apierror"
	"github.com/bbxx111/accountkit/internal/accountsvc/serviceauth"
	"github.com/bbxx111/accountkit/internal/boundedjson"
	"github.com/bbxx111/accountkit/user"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"
)

const Realm = "accountsvc-identity-resolution"

type Resolver func(context.Context, user.ResolveIdentityInput) (user.ResolvedIdentity, error)
type handler struct {
	auth      *serviceauth.Authenticator
	resolve   Resolver
	namespace string
	requestID func(*http.Request) string
}

func NewWithAuthenticator(auth *serviceauth.Authenticator, resolve Resolver, namespace string, requestID func(*http.Request) string) (http.Handler, error) {
	if auth == nil || resolve == nil || namespace == "" || requestID == nil {
		return nil, errors.New("accountsvc: identity resolution dependencies are required")
	}
	return &handler{auth, resolve, namespace, requestID}, nil
}
func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	fail := func(status string, reason, message string) { apierror.Write(w, apierror.New(status, reason, message)) }
	if !h.auth.Enabled() {
		fail(apierror.StatusUnavailable, "IDENTITY_RESOLUTION_NOT_CONFIGURED", "identity resolution is not configured")
		return
	}
	res := h.auth.Authenticate(r)
	switch res.Outcome {
	case serviceauth.Unauthenticated:
		serviceauth.WriteChallenges(w, h.auth.Challenges(res))
		fail(apierror.StatusUnauthenticated, "INVALID_CLIENT", "valid service credentials are required")
		return
	case serviceauth.Forbidden:
		serviceauth.WriteChallenges(w, h.auth.Challenges(res))
		fail(apierror.StatusPermissionDenied, "CLIENT_NOT_AUTHORIZED", "service client is not authorized")
		return
	case serviceauth.Unavailable:
		fail(apierror.StatusUnavailable, "DEPENDENCY_UNAVAILABLE", "service token verification temporarily unavailable")
		return
	}
	if r.Method != "POST" {
		w.Header().Set("Allow", "POST")
		w.WriteHeader(405)
		return
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		w.WriteHeader(415)
		return
	}
	invalid := func() {
		fail(apierror.StatusInvalidArgument, "INVALID_ARGUMENT", "invalid identity resolution request")
	}
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		invalid()
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 4097))
	if len(raw) > 4096 {
		w.WriteHeader(413)
		return
	}
	if err != nil {
		invalid()
		return
	}
	value, err := boundedjson.Decode(raw, boundedjson.Limits{MaxBytes: 4096, MaxDepth: 4, MaxNodes: 8})
	if err != nil {
		invalid()
		return
	}
	obj, ok := value.(map[string]any)
	if !ok || len(obj) != 2 {
		invalid()
		return
	}
	kind, ok := obj["kind"].(string)
	if !ok || (kind != "PHONE" && kind != "EMAIL") {
		invalid()
		return
	}
	subject, ok := obj["subject"].(string)
	if !ok || strings.TrimSpace(subject) == "" {
		invalid()
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	out, err := h.resolve(ctx, user.ResolveIdentityInput{Kind: enum.IdentityKind(kind), Subject: subject, Actor: audit.Actor{Type: audit.ActorService, Namespace: h.namespace, ID: res.ClientID}, Meta: user.Meta{RequestID: h.requestID(r)}})
	switch {
	case errors.Is(err, user.ErrInvalidArgument), errors.Is(err, user.ErrInvalidTarget):
		invalid()
		return
	case errors.Is(err, user.ErrNotFound):
		fail(apierror.StatusNotFound, "NOT_FOUND", "identity not found")
		return
	case errors.Is(err, user.ErrUnavailable), errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		fail(apierror.StatusUnavailable, "DEPENDENCY_UNAVAILABLE", "identity resolution temporarily unavailable")
		return
	case err != nil:
		fail(apierror.StatusInternal, "INTERNAL", "internal server error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		UserID      string `json:"user_id"`
		State       string `json:"state"`
		DisplayName string `json:"display_name"`
		AvatarURL   string `json:"avatar_url"`
	}{out.UserID, out.State.String(), out.DisplayName, out.AvatarURL})
}
