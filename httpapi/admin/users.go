package admin

import (
	"context"
	"net/http"

	"github.com/bbxx111/accountkit/httpapi/apierror"
	"github.com/bbxx111/accountkit/httpapi/jsonbody"
	"github.com/bbxx111/accountkit/user"
)

// GET /users?filter=&show_deleted=&page_size=&page_token=
func (h *Handler) listUsers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f, e := parseFilter(q.Get("filter"))
	if e != nil {
		apierror.Write(w, e)
		return
	}
	show, e := parseShowDeleted(q.Get("show_deleted"))
	if e != nil {
		apierror.Write(w, e)
		return
	}
	f.IncludeDeleted = show
	size, e := parsePageSize(q.Get("page_size"))
	if e != nil {
		apierror.Write(w, e)
		return
	}
	t, id, ok, e := decodeCursor(q.Get("page_token"))
	if e != nil {
		apierror.Write(w, e)
		return
	}
	var after *user.PageCursor
	if ok {
		after = &user.PageCursor{Time: t, ID: id}
	}
	page, err := h.d.Users.ListUsers(r.Context(), f, after, size)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	out := userList{Users: make([]userResource, 0, len(page.Users))}
	for _, u := range page.Users {
		out.Users = append(out.Users, newUserResource(u))
	}
	if page.NextCursor != nil {
		out.NextPageToken = encodeCursor(page.NextCursor.Time, page.NextCursor.ID)
	}
	apierror.WriteJSON(w, http.StatusOK, out)
}

// GET /users/{user}
func (h *Handler) getUser(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	d, err := h.d.Users.GetUserDetail(r.Context(), uid)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	apierror.WriteJSON(w, http.StatusOK, newUserDetailResource(d))
}

// POST /users/{user}:freeze  body {reason}
func (h *Handler) freeze(w http.ResponseWriter, r *http.Request) {
	h.stateChange(w, r, true, h.d.Users.Freeze)
}

// POST /users/{user}:unfreeze  body {reason?}
func (h *Handler) unfreeze(w http.ResponseWriter, r *http.Request) {
	h.stateChange(w, r, true, h.d.Users.Unfreeze)
}

// DELETE /users/{user}（super-admin 代办软删除）
func (h *Handler) deleteUser(w http.ResponseWriter, r *http.Request) {
	h.stateChange(w, r, false, func(ctx context.Context, a user.Admin, uid, _ string, meta user.Meta) (user.AdminUser, error) {
		return h.d.Users.AdminDeleteUser(ctx, a, uid, meta)
	})
}

// POST /users/{user}:undelete（super-admin 代办恢复）
func (h *Handler) undeleteUser(w http.ResponseWriter, r *http.Request) {
	h.stateChange(w, r, false, func(ctx context.Context, a user.Admin, uid, _ string, meta user.Meta) (user.AdminUser, error) {
		return h.d.Users.AdminUndeleteUser(ctx, a, uid, meta)
	})
}

// stateChange 是四个状态迁移端点的公共外壳：主体 → 路径 id → （可选）reason 体 → 领域调用 → 200 用户资源。
func (h *Handler) stateChange(w http.ResponseWriter, r *http.Request, withBody bool, op func(context.Context, user.Admin, string, string, user.Meta) (user.AdminUser, error)) {
	a, ok := h.principal(w, r)
	if !ok {
		return
	}
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	var req reasonRequest
	if withBody && !h.decodeJSON(w, r, &req) {
		apierror.Write(w, jsonbody.Malformed())
		return
	}
	u, err := op(r.Context(), a, uid, req.Reason, h.meta(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	apierror.WriteJSON(w, http.StatusOK, newUserResource(u))
}
