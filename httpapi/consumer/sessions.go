package consumer

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bbxx111/accountkit/httpapi/apierror"
	"github.com/bbxx111/accountkit/ids"
)

type sessionList struct {
	Sessions []sessionResource `json:"sessions"`
}

// GET /users/me/sessions
func (h *Handler) listSessions(w http.ResponseWriter, r *http.Request) {
	p, ok := principal(w, r)
	if !ok {
		return
	}
	items, err := h.d.Users.ListSessions(r.Context(), p.UserID, p.SessionID)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	out := sessionList{Sessions: make([]sessionResource, 0, len(items))}
	for _, s := range items {
		out.Sessions = append(out.Sessions, newSessionResource(p.UserID, s))
	}
	apierror.WriteJSON(w, http.StatusOK, out)
}

// DELETE /users/me/sessions/{session}
func (h *Handler) deleteSession(w http.ResponseWriter, r *http.Request) {
	p, ok := principal(w, r)
	if !ok {
		return
	}
	sid := chi.URLParam(r, "session")
	if !ids.Valid(ids.Session, sid) {
		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, "INVALID_ID", "session id is malformed"))
		return
	}
	if err := h.d.Users.RevokeSession(r.Context(), p.UserID, sid, h.meta(r)); err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /users/me/sessions:revokeOthers
func (h *Handler) revokeOtherSessions(w http.ResponseWriter, r *http.Request) {
	p, ok := principal(w, r)
	if !ok {
		return
	}
	if err := h.d.Users.RevokeOtherSessions(r.Context(), p.UserID, p.SessionID, h.meta(r)); err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	apierror.WriteJSON(w, http.StatusOK, struct{}{})
}
