package enduser

import (
	"io"
	"mime"
	"net/http"
	"net/url"

	"github.com/bbxx111/accountkit/httpapi/apierror"
)

type tokenRequest struct {
	GrantType    string `json:"grant_type"`
	RefreshToken string `json:"refresh_token"`
	// Scope 按 RFC 6749 §3.3 被忽略：签发 scope 始终由账号当前状态推导并在响应 scope 字段返回，
	// 携带与否结果相同。保留字段只是为了让严格 JSON 解码不把它当作未知字段拒绝。
	Scope string `json:"scope"`
}

// decodeOAuthBody 解析两个 OAuth 端点（/token、/revoke）的请求体。Content-Type 媒体类型为
// application/x-www-form-urlencoded（忽略 charset 等参数）时按 RFC 6749 §3.2 / RFC 7009 §2.1 解析表单，
// fields 把表单参数名映射到目标字段：与 JSON 同一大小上限，只读请求体（URL 查询串既不参与取值也不参与
// 校验，因此不用会合并并校验查询串的 Request.ParseForm），同名参数重复（无论是否识别）视为畸形，未识别
// 参数按 RFC 6749 §3.2 忽略。其他情况（包括缺失或无法解析的 Content-Type）沿用共用的严格 JSON 解码，与
// 新增表单支持之前完全一致：既有调用方不要求 Content-Type，JSON 路径继续拒绝未知字段。表单能力不扩散到
// 其他端点。
func (h *Handler) decodeOAuthBody(w http.ResponseWriter, r *http.Request, dst any, fields map[string]*string) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/x-www-form-urlencoded" {
		return h.decodeJSON(w, r, dst)
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, h.d.MaxBodyBytes))
	if err != nil {
		h.d.Logger.Debug("malformed form body", "request_id", requestIDFrom(r.Context()), "err", err)
		return errMalformedBody
	}
	values, err := url.ParseQuery(string(body))
	if err != nil {
		h.d.Logger.Debug("malformed form body", "request_id", requestIDFrom(r.Context()), "err", err)
		return errMalformedBody
	}
	for name, vs := range values {
		if len(vs) != 1 {
			return errMalformedBody
		}
		if field, ok := fields[name]; ok {
			*field = vs[0]
		}
	}
	return nil
}

// POST /token — RFC 6749 §6 刷新。请求体接受 JSON 或表单编码（见 decodeOAuthBody），响应与错误体为
// RFC 6749 §5.1 / §5.2 格式。
func (h *Handler) token(w http.ResponseWriter, r *http.Request) {
	var req tokenRequest
	// 表单映射不含 scope：未识别参数按 §3.2 忽略，效果与 JSON 路径一致。
	if err := h.decodeOAuthBody(w, r, &req, map[string]*string{
		"grant_type":    &req.GrantType,
		"refresh_token": &req.RefreshToken,
	}); err != nil {
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
	meta, err := h.authMeta(r)
	if err != nil {
		h.writeOAuthError(w, r, err)
		return
	}
	res, err := h.d.Users.Refresh(r.Context(), req.RefreshToken, meta)
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

// POST /revoke — RFC 7009。请求体接受 JSON 或表单编码；未知 token 也返回 200；hint 只作提示，服务端自行判定类型。
func (h *Handler) revoke(w http.ResponseWriter, r *http.Request) {
	var req revokeRequest
	if err := h.decodeOAuthBody(w, r, &req, map[string]*string{
		"token":           &req.Token,
		"token_type_hint": &req.TokenTypeHint,
	}); err != nil {
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
