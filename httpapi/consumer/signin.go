package consumer

import (
	"errors"
	"net/http"

	"github.com/bbxx111/accountkit/httpapi/apierror"
)

type sendCodeRequest struct {
	Channel string `json:"channel"`
	Target  string `json:"target"`
}

// POST /users:sendSignInCode
func (h *Handler) sendSignInCode(w http.ResponseWriter, r *http.Request) {
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
	if err := h.d.Users.SendSignInCode(r.Context(), ch, req.Target, meta); err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	apierror.WriteJSON(w, http.StatusOK, struct{}{})
}

// POST /users:signInWithCode
func (h *Handler) signInWithCode(w http.ResponseWriter, r *http.Request) {
	dev, e := deviceFrom(r)
	if e != nil {
		apierror.Write(w, e)
		return
	}
	var cred credential
	if err := h.decodeJSON(w, r, &cred); err != nil {
		apierror.Write(w, malformed())
		return
	}
	kind, cc, e := cred.anchor()
	if e != nil {
		apierror.Write(w, e)
		return
	}
	res, err := h.d.Users.SignInWithCode(r.Context(), kind, cc.Target, cc.Code, dev, h.meta(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	writeToken(w, res)
}

// POST /users:signInWithIdp — 微信 / Apple 登录即注册。
func (h *Handler) signInWithIdp(w http.ResponseWriter, r *http.Request) {
	dev, e := deviceFrom(r)
	if e != nil {
		apierror.Write(w, e)
		return
	}
	var cred credential
	if err := h.decodeJSON(w, r, &cred); err != nil {
		apierror.Write(w, malformed())
		return
	}
	ic, e := cred.idp()
	if e != nil {
		apierror.Write(w, e)
		return
	}
	res, err := h.d.Users.SignInWithIdp(r.Context(), ic, dev, h.meta(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	writeToken(w, res)
}
