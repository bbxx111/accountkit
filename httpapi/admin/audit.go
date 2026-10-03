package admin

import (
	"net/http"

	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/httpapi/apierror"
)

// GET /users/{user}/auditEvents?page_size=&page_token=  —— 读取不产生审计事件（§4.3）。
func (h *Handler) listAuditEvents(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	size, e := parsePageSize(q.Get("page_size"))
	if e != nil {
		apierror.Write(w, e)
		return
	}
	t, id, hasCursor, e := decodeCursor(q.Get("page_token"))
	if e != nil {
		apierror.Write(w, e)
		return
	}
	if err := h.d.Users.UserExists(r.Context(), uid); err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	var after *audit.EventCursor
	if hasCursor {
		after = &audit.EventCursor{Time: t, ID: id}
	}
	rows, err := h.d.Audit.ListByUser(r.Context(), uid, after, size+1) // 多取一行判断下一页
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	out := auditEventList{AuditEvents: make([]auditEventResource, 0, min(len(rows), size))}
	for i, e := range rows {
		if i == size {
			last := rows[size-1]
			out.NextPageToken = encodeCursor(last.OccurTime, last.ID)
			break
		}
		out.AuditEvents = append(out.AuditEvents, newAuditEventResource(uid, e))
	}
	apierror.WriteJSON(w, http.StatusOK, out)
}
