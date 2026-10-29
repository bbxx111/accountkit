package admin

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bbxx111/accountkit/httpapi/apierror"
	"github.com/bbxx111/accountkit/ids"
)

// GET /users/{user}/identities/{identity}:reveal（super-admin）：解密返回明文，写 IDENTITY_REVEALED。
func (h *Handler) revealIdentity(w http.ResponseWriter, r *http.Request) {
	// 该路由的所有响应（含错误）都不得被中间缓存保存：成功体是明文身份，错误体不依赖宿主或网关配置。
	w.Header().Set("Cache-Control", "no-store")
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
		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, "INVALID_ID", "identity id is malformed"))
		return
	}
	rv, err := h.d.Users.RevealIdentity(r.Context(), a, uid, iid, h.meta(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	apierror.WriteJSON(w, http.StatusOK, revealResource{Name: "users/" + uid + "/identities/" + rv.ID, Kind: rv.Kind.String(), Subject: rv.Subject})
}
