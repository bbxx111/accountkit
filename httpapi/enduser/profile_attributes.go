package enduser

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/bbxx111/accountkit/httpapi/apierror"
	"github.com/bbxx111/accountkit/user"
)

var _ ProfileAttributesService = (*user.Service)(nil)

// ProfileAttributesService 是可选的本人资料能力；原 Service 适配器无需增加方法。
type ProfileAttributesService interface {
	GetProfileAttributes(context.Context, string) (user.ProfileAttributesView, error)
	PatchProfileAttributes(context.Context, string, user.ProfileAttributesPatch, user.Meta) (user.ProfileAttributesView, error)
}

type profileResource struct {
	Name              string          `json:"name"`
	ProfileAttributes json.RawMessage `json:"profile_attributes"`
}

func (h *Handler) getProfile(w http.ResponseWriter, r *http.Request) {
	p, ok := principal(w, r)
	if !ok {
		return
	}
	svc, ok := h.d.Users.(ProfileAttributesService)
	if !ok {
		h.writeProfileError(w, r, user.ErrProfileAttributesNotConfigured)
		return
	}
	view, err := svc.GetProfileAttributes(r.Context(), p.UserID)
	if err != nil {
		h.writeProfileError(w, r, err)
		return
	}
	apierror.WriteJSON(w, http.StatusOK, profileResource{Name: "users/" + p.UserID + "/profile", ProfileAttributes: view.Data})
}

func (h *Handler) patchProfile(w http.ResponseWriter, r *http.Request) {
	p, ok := principal(w, r)
	if !ok {
		return
	}
	svc, ok := h.d.Users.(ProfileAttributesService)
	if !ok {
		h.writeProfileError(w, r, user.ErrProfileAttributesNotConfigured)
		return
	}
	document, err := h.decodeProfilePatch(w, r)
	if err != nil {
		h.d.Logger.Debug("profile request rejected", "request_id", requestIDFrom(r.Context()), "reason", "MALFORMED_BODY")
		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, "MALFORMED_BODY", "request body must contain only profile_attributes and fit the configured size limit"))
		return
	}
	view, err := svc.PatchProfileAttributes(r.Context(), p.UserID, user.ProfileAttributesPatch{Document: document}, h.meta(r))
	if err != nil {
		h.writeProfileError(w, r, err)
		return
	}
	apierror.WriteJSON(w, http.StatusOK, profileResource{Name: "users/" + p.UserID + "/profile", ProfileAttributes: view.Data})
}

// decodeProfilePatch validates only the envelope and JSON syntax. The original
// inner bytes, including whitespace, remain subject to the domain's own budget
// and strict duplicate-key, Unicode, object and Schema validation.
func (h *Handler) decodeProfilePatch(w http.ResponseWriter, r *http.Request) (json.RawMessage, error) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, h.d.MaxBodyBytes))
	if err != nil || !json.Valid(raw) {
		return nil, errMalformedBody
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	tok, err := d.Token()
	if err != nil || tok != json.Delim('{') || !d.More() {
		return nil, errMalformedBody
	}
	key, err := d.Token()
	if err != nil || key != "profile_attributes" {
		return nil, errMalformedBody
	}
	start := int(d.InputOffset())
	for start < len(raw) && jsonWhitespace(raw[start]) {
		start++
	}
	if start >= len(raw) || raw[start] != ':' {
		return nil, errMalformedBody
	}
	start++
	var document json.RawMessage
	if err := d.Decode(&document); err != nil {
		return nil, errMalformedBody
	}
	end := int(d.InputOffset())
	for end < len(raw) && jsonWhitespace(raw[end]) {
		end++
	}
	if d.More() {
		return nil, errMalformedBody
	}
	if tok, err := d.Token(); err != nil || tok != json.Delim('}') {
		return nil, errMalformedBody
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, errMalformedBody
	}
	return json.RawMessage(raw[start:end]), nil
}

func jsonWhitespace(b byte) bool {
	return b == ' ' || b == '\n' || b == '\r' || b == '\t'
}

// Profile errors may wrap private values or validator details. Log only fixed
// classifications, including for unknown storage and uncertain commit errors.
func (h *Handler) writeProfileError(w http.ResponseWriter, r *http.Request, err error) {
	var out *apierror.Error
	switch {
	case errors.Is(err, user.ErrProfileAttributesNotConfigured):
		out = apierror.New(apierror.StatusUnavailable, "PROFILE_ATTRIBUTES_NOT_CONFIGURED", "profile attributes are not configured")
	case errors.Is(err, user.ErrInvalidArgument):
		out = apierror.New(apierror.StatusInvalidArgument, "INVALID_PROFILE_ATTRIBUTES", "invalid profile attributes")
	case errors.Is(err, user.ErrInvalidState):
		out = apierror.New(apierror.StatusFailedPrecondition, "INVALID_ACCOUNT_STATE", "operation not allowed in the account's current state")
	case errors.Is(err, user.ErrNotFound):
		out = apierror.New(apierror.StatusNotFound, "NOT_FOUND", "resource not found")
	case errors.Is(err, user.ErrUnavailable):
		out = &apierror.Error{Status: apierror.StatusUnavailable, Reason: "DEPENDENCY_UNAVAILABLE", Message: "service temporarily unavailable, retry later", RetryAfterSeconds: 1}
	default:
		apierror.WriteInternal(w, h.d.Logger, requestIDFrom(r.Context()), errors.New("enduser: profile operation failed"))
		return
	}
	h.d.Logger.Debug("profile request rejected", "request_id", requestIDFrom(r.Context()), "reason", out.Reason)
	apierror.Write(w, out)
}
