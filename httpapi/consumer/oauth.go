package consumer

import (
	"net/http"

	"github.com/bbxx111/accountkit/httpapi/apierror"
)

type tokenRequest struct {
	GrantType    string `json:"grant_type"`
	RefreshToken string `json:"refresh_token"`
}

// POST /token — RFC 6749 §6 刷新（JSON body；不做 form 编码，见 AGENTS.md "标准的适用范围"）。
func (h *Handler) token(w http.ResponseWriter, r *http.Request) {
	var req tokenRequest
	if err := h.decodeJSON(w, r, &req); err != nil {
		h.writeOAuthError(w, r, err)
		return
	}
	if req.GrantType == "" || req.RefreshToken == "" {
		apierror.WriteOAuth(w, http.StatusBadRequest, "invalid_request", "grant_type and refresh_token are required", nil)
		return
	}
	if req.GrantType != "refresh_token" {
		apierror.WriteOAuth(w, http.StatusBadRequest, "unsupported_grant_type", "only grant_type=refresh_token is supported", nil)
		return
	}
	res, err := h.d.Users.Refresh(r.Context(), req.RefreshToken, h.meta(r))
	if err != nil {
		h.writeOAuthError(w, r, err)
		return
	}
	writeToken(w, res)
}

type revokeRequest struct {
	Token         string `json:"token"`
	TokenTypeHint string `json:"token_type_hint"`
}

// POST /revoke — RFC 7009。未知 token 也返回 200；hint 只作提示，服务端自行判定类型。
func (h *Handler) revoke(w http.ResponseWriter, r *http.Request) {
	var req revokeRequest
	if err := h.decodeJSON(w, r, &req); err != nil {
		h.writeOAuthError(w, r, err)
		return
	}
	if req.Token == "" {
		apierror.WriteOAuth(w, http.StatusBadRequest, "invalid_request", "token is required", nil)
		return
	}
	if err := h.d.Users.Revoke(r.Context(), req.Token, h.meta(r)); err != nil {
		h.writeOAuthError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
}
