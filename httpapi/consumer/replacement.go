package consumer

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/httpapi/apierror"
	"github.com/bbxx111/accountkit/ids"
	"github.com/bbxx111/accountkit/user"
)

// IdentityReplacer 是可选的同类手机/邮箱换绑能力；*user.Service 满足它。
// 旧 Service 实现无需增加此方法；未提供能力时换绑端点返回 503。
type IdentityReplacer interface {
	ReplaceIdentity(ctx context.Context, p user.Principal, identityID string, channel enum.IdentityKind, target, plainCode string, meta user.Meta) (user.IdentityInfo, error)
}

// POST /users/me/identities/{identity}:replace
func (h *Handler) replaceIdentity(w http.ResponseWriter, r *http.Request) {
	p, ok := principal(w, r)
	if !ok {
		return
	}
	replacer, ok := h.d.Users.(IdentityReplacer)
	if !ok {
		apierror.Write(w, apierror.New(apierror.StatusUnavailable, "IDENTITY_REPLACEMENT_NOT_CONFIGURED", "identity replacement is not configured"))
		return
	}
	id := chi.URLParam(r, "identity")
	if !ids.Valid(ids.Identity, id) {
		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, "INVALID_ID", "identity id is malformed"))
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
	info, err := replacer.ReplaceIdentity(r.Context(), p, id, kind, cc.Target, cc.Code, h.meta(r))
	if err != nil {
		h.writeReplacementError(w, r, err)
		return
	}
	apierror.WriteJSON(w, http.StatusOK, newIdentityResource(p.UserID, info))
}

// 新能力的错误局限在换绑路由；原绑定冲突的消息保持兼容。
func (h *Handler) writeReplacementError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, user.ErrIdentityUnchanged):
		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, "IDENTITY_UNCHANGED", "new target must differ from the current identity"))
	case errors.Is(err, user.ErrInsufficientScope):
		apierror.Write(w, apierror.New(apierror.StatusPermissionDenied, "INSUFFICIENT_SCOPE", "token scope does not permit this operation"))
	case errors.Is(err, user.ErrReauthenticationRequired):
		apierror.Write(w, apierror.New(apierror.StatusFailedPrecondition, "REAUTHENTICATION_REQUIRED", "recent authentication required for this operation"))
	case errors.Is(err, user.ErrIdentityConflict):
		apierror.Write(w, apierror.New(apierror.StatusAlreadyExists, "IDENTITY_ALREADY_BOUND", "this identity is already bound"))
	default:
		h.writeServiceError(w, r, err)
	}
}
