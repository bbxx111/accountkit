package enduser

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/bbxx111/accountkit/httpapi/apierror"
	"github.com/bbxx111/accountkit/httpapi/authn"
	"github.com/bbxx111/accountkit/internal/boundedjson"
	"github.com/bbxx111/accountkit/user"
)

// principal 取 ctx 中的 Principal；中间件保证存在，缺失视为编程错误 → 401 兜底。
func principal(w http.ResponseWriter, r *http.Request) (user.Principal, bool) {
	p, ok := authn.PrincipalFrom(r.Context())
	if !ok {
		authn.WriteUnauthenticated(w, authn.ReasonTokenMissing, "bearer token required")
	}
	return p, ok
}

// GET /users/me
func (h *Handler) getMe(w http.ResponseWriter, r *http.Request) {
	p, ok := principal(w, r)
	if !ok {
		return
	}
	me, err := h.d.Users.GetMe(r.Context(), p.UserID)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	apierror.WriteJSON(w, http.StatusOK, newMeResource(me))
}

type updateMeRequest struct {
	DisplayName *string         `json:"display_name"`
	AvatarURL   json.RawMessage `json:"avatar_url"`
}

// PATCH /users/me — 原子部分更新基础资料。
func (h *Handler) updateMe(w http.ResponseWriter, r *http.Request) {
	p, ok := principal(w, r)
	if !ok {
		return
	}
	var req updateMeRequest
	if err := h.decodeJSON(w, r, &req); err != nil {
		apierror.Write(w, malformed())
		return
	}
	// Preserve the raw value until Unicode validation; encoding/json otherwise
	// replaces invalid UTF-8 and lone surrogates before the domain can reject them.
	var avatarURL *string
	if len(req.AvatarURL) != 0 {
		raw := bytes.TrimSpace(req.AvatarURL)
		if len(raw) == 0 || raw[0] != '"' && !bytes.Equal(raw, []byte("null")) {
			apierror.Write(w, malformed())
			return
		}
		// U+2028/U+2029 use six canonical bytes for three input bytes.
		value, err := boundedjson.Decode(req.AvatarURL, boundedjson.Limits{MaxBytes: 2 * len(req.AvatarURL), MaxDepth: 1, MaxNodes: 1})
		if err != nil {
			apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, "INVALID_ARGUMENT", "invalid avatar_url"))
			return
		}
		if value != nil {
			text, ok := value.(string)
			if !ok {
				apierror.Write(w, malformed())
				return
			}
			avatarURL = &text
		}
	}
	if req.DisplayName == nil && avatarURL == nil {
		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, "NO_FIELDS", "no updatable fields present (display_name, avatar_url)"))
		return
	}
	var me user.Me
	var err error
	if updater, ok := h.d.Users.(ProfileUpdater); ok {
		me, err = updater.UpdateProfile(r.Context(), p.UserID, user.ProfilePatch{DisplayName: req.DisplayName, AvatarURL: avatarURL})
	} else if avatarURL != nil {
		apierror.Write(w, apierror.New(apierror.StatusUnavailable, "PROFILE_UPDATE_NOT_CONFIGURED", "profile update not configured"))
		return
	} else {
		me, err = h.d.Users.UpdateDisplayName(r.Context(), p.UserID, *req.DisplayName)
	}
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	apierror.WriteJSON(w, http.StatusOK, newMeResource(me))
}

// POST /users/me:sendReauthenticationCode
func (h *Handler) sendReauthenticationCode(w http.ResponseWriter, r *http.Request) {
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
		apierror.WriteInternal(w, h.d.Logger, requestIDFrom(r.Context()), errors.New("enduser: Deps.ClientIP returned empty"))
		return
	}
	challenge, err := h.d.Users.SendReauthenticationCode(r.Context(), p, ch, req.Target, meta)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	writeChallenge(w, challenge)
}

// POST /users/me:reauthenticate — 只接受 phone/email 凭证；响应不含 refresh_token。
func (h *Handler) reauthenticate(w http.ResponseWriter, r *http.Request) {
	p, ok := principal(w, r)
	if !ok {
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
	meta, err := h.authMeta(r)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	res, err := h.d.Users.Reauthenticate(r.Context(), p, user.CodeCredential{Channel: kind, Target: cc.Target, CodeID: cc.CodeID, Code: cc.Code}, meta)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	writeToken(w, res)
}

// DELETE /users/me — AIP-164 软删除；返回删除后的账号资源（state = PENDING_DELETION，含 delete_time / purge_time）。
// 请求体忽略；近期认证由路由上的 RequireRecentAuth 保证。
func (h *Handler) deleteMe(w http.ResponseWriter, r *http.Request) {
	p, ok := principal(w, r)
	if !ok {
		return
	}
	me, err := h.d.Users.DeleteMe(r.Context(), p, h.meta(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	apierror.WriteJSON(w, http.StatusOK, newMeResource(me))
}

// POST /users/me:undelete — 取消注销；返回恢复后的账号资源。请求体忽略（与 sessions:revokeOthers 一致）。
func (h *Handler) undelete(w http.ResponseWriter, r *http.Request) {
	p, ok := principal(w, r)
	if !ok {
		return
	}
	me, err := h.d.Users.Undelete(r.Context(), p, h.meta(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	apierror.WriteJSON(w, http.StatusOK, newMeResource(me))
}
