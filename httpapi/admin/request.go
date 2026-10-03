package admin

import (
	"net/http"

	"github.com/bbxx111/accountkit/httpapi/jsonbody"
	"github.com/bbxx111/accountkit/httpapi/reqid"
)

// reasonRequest 是 :freeze / :unfreeze 的请求体。
type reasonRequest struct {
	Reason string `json:"reason"`
}

// decodeJSON 与 C 端同一套严格解码规则；失败细节只进 Debug 日志。
func (h *Handler) decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := jsonbody.Decode(w, r, dst, h.d.MaxBodyBytes); err != nil {
		h.d.Logger.Debug("admin: malformed request body", "request_id", reqid.From(r.Context()), "err", err)
		return false
	}
	return true
}
