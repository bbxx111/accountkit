package enduser

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/httpapi/apierror"
	"github.com/bbxx111/accountkit/user"
)

// SignInCompletionService 是可选登录补全能力；未实现的宿主仍可使用其他认证端点。
type SignInCompletionService interface {
	SendSignInCompletionCode(ctx context.Context, token string, channel enum.IdentityKind, target string, dev user.Device, meta user.Meta) (user.CodeChallenge, error)
	CompleteSignIn(ctx context.Context, token string, cred user.CodeCredential, dev user.Device, meta user.Meta) (user.SignInResult, error)
}

type sendSignInCompletionCodeRequest struct {
	SignInCompletionToken string `json:"sign_in_completion_token"`
	Channel               string `json:"channel"`
	Target                string `json:"target"`
}

type completeSignInRequest struct {
	SignInCompletionToken string          `json:"sign_in_completion_token"`
	Kind                  string          `json:"kind"`
	Credential            json.RawMessage `json:"credential"`
}

func completionNotConfigured(w http.ResponseWriter) {
	apierror.Write(w, apierror.New(apierror.StatusUnavailable, "SIGN_IN_COMPLETION_NOT_CONFIGURED", "sign-in completion is not configured"))
}

func (h *Handler) sendSignInCompletionCode(w http.ResponseWriter, r *http.Request) {
	var req sendSignInCompletionCodeRequest
	if err := h.decodeJSON(w, r, &req); err != nil {
		apierror.Write(w, malformed())
		return
	}
	if req.SignInCompletionToken == "" {
		h.writeServiceError(w, r, user.ErrSignInCompletionInvalid)
		return
	}
	ch, e := parseChannel(req.Channel)
	if e != nil {
		apierror.Write(w, e)
		return
	}
	dev, e := completionDeviceFrom(r)
	if e != nil {
		apierror.Write(w, e)
		return
	}
	svc, ok := h.d.Users.(SignInCompletionService)
	if !ok {
		completionNotConfigured(w)
		return
	}
	meta := h.meta(r)
	if meta.IP == "" {
		apierror.WriteInternal(w, h.d.Logger, requestIDFrom(r.Context()), errors.New("enduser: Deps.ClientIP returned empty"))
		return
	}
	challenge, err := svc.SendSignInCompletionCode(r.Context(), req.SignInCompletionToken, ch, req.Target, dev, meta)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	writeChallenge(w, challenge)
}

func (h *Handler) completeSignIn(w http.ResponseWriter, r *http.Request) {
	var req completeSignInRequest
	if err := h.decodeJSON(w, r, &req); err != nil {
		apierror.Write(w, malformed())
		return
	}
	if req.SignInCompletionToken == "" {
		h.writeServiceError(w, r, user.ErrSignInCompletionInvalid)
		return
	}
	kind, cc, e := (credential{Kind: req.Kind, Credential: req.Credential}).anchor()
	if e != nil {
		apierror.Write(w, e)
		return
	}
	dev, e := completionDeviceFrom(r)
	if e != nil {
		apierror.Write(w, e)
		return
	}
	svc, ok := h.d.Users.(SignInCompletionService)
	if !ok {
		completionNotConfigured(w)
		return
	}
	meta, err := h.authMeta(r)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	res, err := svc.CompleteSignIn(r.Context(), req.SignInCompletionToken, user.CodeCredential{Channel: kind, Target: cc.Target, CodeID: cc.CodeID, Code: cc.Code}, dev, meta)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeSignIn(w, r, res)
}
