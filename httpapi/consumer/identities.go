package consumer

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bbxx111/accountkit/httpapi/apierror"
	"github.com/bbxx111/accountkit/ids"
	"github.com/bbxx111/accountkit/user"
)

// POST /users/me:sendBindCode
func (h *Handler) sendBindCode(w http.ResponseWriter, r *http.Request) {
	p, ok := principal(w, r)
	if !ok {
		return
	}
	var req sendCodeRequest
	if err := h.decodeJSON(w, r, &req); err != nil {
		apierror.Write(w, malformed())
		return
	}
	ch, e := parseChannel(req.Channel)
	if e != nil {
		apierror.Write(w, e)
		return
	}
	meta := h.meta(r)
	if meta.IP == "" {
		apierror.WriteInternal(w, h.d.Logger, requestIDFrom(r.Context()), errors.New("consumer: Deps.ClientIP returned empty"))
		return
	}
	if err := h.d.Users.SendBindCode(r.Context(), p, ch, req.Target, meta); err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	apierror.WriteJSON(w, http.StatusOK, struct{}{})
}

// GET /users/me/identities
func (h *Handler) listIdentities(w http.ResponseWriter, r *http.Request) {
	p, ok := principal(w, r)
	if !ok {
		return
	}
	items, err := h.d.Users.ListIdentities(r.Context(), p.UserID)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	out := identityList{Identities: make([]identityResource, 0, len(items))}
	for _, i := range items {
		out.Identities = append(out.Identities, newIdentityResource(p.UserID, i))
	}
	apierror.WriteJSON(w, http.StatusOK, out)
}

// POST /users/me/identities — body 为凭证 oneof；新建 201，幂等命中 200。
func (h *Handler) bindIdentity(w http.ResponseWriter, r *http.Request) {
	p, ok := principal(w, r)
	if !ok {
		return
	}
	var cred credential
	if err := h.decodeJSON(w, r, &cred); err != nil {
		apierror.Write(w, malformed())
		return
	}
	if cred.present() != 1 {
		apierror.Write(w, oneofError())
		return
	}
	var (
		info    user.IdentityInfo
		created bool
		err     error
	)
	if cred.Phone != nil || cred.Email != nil {
		kind, cc, e := cred.anchor()
		if e != nil {
			apierror.Write(w, e)
			return
		}
		info, created, err = h.d.Users.BindWithCode(r.Context(), p, kind, cc.Target, cc.Code, h.meta(r))
	} else {
		ic, e := cred.idp()
		if e != nil {
			apierror.Write(w, e)
			return
		}
		info, created, err = h.d.Users.BindWithIdp(r.Context(), p, ic, h.meta(r))
	}
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	apierror.WriteJSON(w, status, newIdentityResource(p.UserID, info))
}

// DELETE /users/me/identities/{identity} — 路由已挂 RequireScope(user) + RequireRecentAuth。
func (h *Handler) unbindIdentity(w http.ResponseWriter, r *http.Request) {
	p, ok := principal(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "identity")
	if !ids.Valid(ids.Identity, id) {
		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, "INVALID_ID", "identity id is malformed"))
		return
	}
	if err := h.d.Users.UnbindIdentity(r.Context(), p, id, h.meta(r)); err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
