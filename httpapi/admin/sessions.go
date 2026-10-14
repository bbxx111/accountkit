package admin

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bbxx111/accountkit/httpapi/apierror"
	"github.com/bbxx111/accountkit/ids"
)

// GET /users/{user}/sessions
func (h *Handler) listSessions(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	items, err := h.d.Users.AdminListSessions(r.Context(), uid)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	out := sessionList{Sessions: make([]sessionResource, 0, len(items))}
	for _, s := range items {
		out.Sessions = append(out.Sessions, sessionResource{Name: "users/" + uid + "/sessions/" + s.ID, DeviceID: s.DeviceID, DeviceName: s.DeviceName, CreateTime: fmtTime(s.CreateTime), LastUsedTime: fmtTime(s.LastUsedTime)})
	}
	apierror.WriteJSON(w, http.StatusOK, out)
}

// DELETE /users/{user}/sessions/{session} → 204
func (h *Handler) deleteSession(w http.ResponseWriter, r *http.Request) {
	a, ok := h.principal(w, r)
	if !ok {
		return
	}
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	sid := chi.URLParam(r, "session")
	if !ids.Valid(ids.Session, sid) {
		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, "INVALID_ID", "session id is malformed"))
		return
	}
	if err := h.d.Users.AdminRevokeSession(r.Context(), a, uid, sid, h.meta(r)); err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /users/{user}/sessions:revokeAll → 200 {revoked_count}
func (h *Handler) revokeAllSessions(w http.ResponseWriter, r *http.Request) {
	a, ok := h.principal(w, r)
	if !ok {
		return
	}
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	n, err := h.d.Users.AdminRevokeAllSessions(r.Context(), a, uid, h.meta(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	apierror.WriteJSON(w, http.StatusOK, revokeAllResponse{RevokedCount: n})
}
