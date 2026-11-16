package admin

import (
	"net/http"

	"github.com/bbxx111/accountkit/httpapi/apierror"
	"github.com/bbxx111/accountkit/httpapi/internal/resourcename"
	"github.com/bbxx111/accountkit/ids"
	"github.com/bbxx111/accountkit/internal/httpcontract"
	"github.com/go-chi/chi/v5"
)

// GET /users/{user}/identities/{identity}:reveal：要求 identities.reveal，旧宿主为 super-admin。
func (h *Handler) revealIdentity(w http.ResponseWriter, r *http.Request) {
	// 路由包装在授权前为所有响应设置 no-store；这里解密返回明文并写 IDENTITY_REVEALED。
	a, ok := h.principal(w, r)
	if !ok {
		return
	}
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	iid := chi.URLParam(r, "identity")
	if !ids.Valid(ids.Identity, iid) {
		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, httpcontract.ReasonInvalidID, "identity id is malformed"))
		return
	}
	rv, err := h.d.Users.RevealIdentity(r.Context(), a, uid, iid, h.meta(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	apierror.WriteJSON(w, http.StatusOK, revealResource{Name: resourcename.Identity(uid, rv.ID), Kind: rv.Kind.String(), Subject: rv.Subject})
}

// noStore runs before authorization so sensitive-route failures cannot be cached.
func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpcontract.SetNoStore(w)
		next.ServeHTTP(w, r)
	})
}
