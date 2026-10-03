package admin_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/httpapi/admin"
	"github.com/bbxx111/accountkit/user"
)

func TestRolesGateEveryRoute(t *testing.T) {
	h := newHandler(t, &fakeService{}, nil) // fake 未设置任何方法：只要放行就会 panic，所以 403 必须发生在 handler 之前
	routes := []struct {
		method, path, role string
	}{
		{"GET", "/users", admin.RoleOperator},
		{"GET", "/users/" + uid, admin.RoleOperator},
		{"POST", "/users/" + uid + ":freeze", admin.RoleOperator},
		{"POST", "/users/" + uid + ":unfreeze", admin.RoleOperator},
		{"GET", "/users/" + uid + "/sessions", admin.RoleOperator},
		{"DELETE", "/users/" + uid + "/sessions/" + sid, admin.RoleOperator},
		{"POST", "/users/" + uid + "/sessions:revokeAll", admin.RoleOperator},
		{"GET", "/users/" + uid + "/auditEvents", admin.RoleOperator},
		{"GET", "/users/" + uid + "/identities/" + iid + ":reveal", admin.RoleSuperAdmin},
		{"DELETE", "/users/" + uid, admin.RoleSuperAdmin},
		{"POST", "/users/" + uid + ":undelete", admin.RoleSuperAdmin},
	}
	for _, rt := range routes {
		// 无任何角色 → 403（fake verifier）
		rec := do(t, h, call{method: rt.method, path: rt.path, headers: map[string]string{"X-Test-Admin": adminHdr}})
		if status, _ := aipError(t, rec); rec.Code != 403 || status != "PERMISSION_DENIED/ROLE_REQUIRED" {
			t.Fatalf("%s %s without roles: %d %s", rt.method, rt.path, rec.Code, status)
		}
		if rt.role == admin.RoleSuperAdmin {
			rec = do(t, h, call{method: rt.method, path: rt.path, headers: operator})
			if rec.Code != 403 {
				t.Fatalf("%s %s must require super-admin: %d", rt.method, rt.path, rec.Code)
			}
		}
	}
	// 路由存在性：未知路径 404 ROUTE_NOT_FOUND；已知路径错误方法 405
	rec := do(t, h, call{method: "GET", path: "/nope", headers: superAdmin})
	if status, _ := aipError(t, rec); rec.Code != 404 || status != "NOT_FOUND/ROUTE_NOT_FOUND" {
		t.Fatalf("unknown route: %d %s", rec.Code, status)
	}
	if rec.Header().Get("X-Request-Id") != "req-admin" {
		t.Fatal("every response carries X-Request-Id")
	}
	if rec = do(t, h, call{method: "PATCH", path: "/users/" + uid, headers: superAdmin}); rec.Code != 405 {
		t.Fatalf("PATCH /users/{user}: %d", rec.Code)
	}
}

func TestMissingPrincipalIs401(t *testing.T) {
	h := newHandler(t, &fakeService{}, nil)
	rec := do(t, h, call{method: "POST", path: "/users/" + uid + ":freeze", body: map[string]string{"reason": "x"}, headers: map[string]string{"X-Test-Roles": "operator"}})
	if status, _ := aipError(t, rec); rec.Code != 401 || status != "UNAUTHENTICATED/TOKEN_MISSING" || rec.Header().Get("WWW-Authenticate") != `Bearer realm="admin"` {
		t.Fatalf("missing principal: %d %s %q", rec.Code, status, rec.Header().Get("WWW-Authenticate"))
	}
}

func TestListUsersParsesQueryAndEncodesNextPage(t *testing.T) {
	var got struct {
		f     user.UserFilter
		after *user.PageCursor
		limit int
	}
	f := &fakeService{listUsers: func(_ context.Context, fl user.UserFilter, after *user.PageCursor, limit int) (user.UserPage, error) {
		got.f, got.after, got.limit = fl, after, limit
		u := adminUserFixture()
		return user.UserPage{Users: []user.AdminUser{u}, NextCursor: &user.PageCursor{Time: u.CreateTime, ID: u.ID}}, nil
	}}
	h := newHandler(t, f, nil)
	rec := do(t, h, call{method: "GET", path: `/users?filter=state%20%3D%20FROZEN%20AND%20identity.phone_suffix%20%3D%201234&show_deleted=true&page_size=5`, headers: operator})
	var out map[string]any
	decode(t, rec, &out)
	if rec.Code != 200 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	if got.f.State == nil || *got.f.State != enum.UserFrozen || got.f.IdentityKind != enum.IdentityPhone || got.f.HintSuffix == nil || *got.f.HintSuffix != "1234" || !got.f.IncludeDeleted || got.limit != 5 || got.after != nil {
		t.Fatalf("filter passed to service: %+v limit=%d after=%v", got.f, got.limit, got.after)
	}
	users := out["users"].([]any)
	u := users[0].(map[string]any)
	assertKeys(t, u, "name", "state", "display_name", "create_time", "update_time", "delete_time", "purge_time", "freeze")
	if u["name"] != "users/"+uid || u["state"] != "ACTIVE" || u["freeze"] != nil || u["delete_time"] != nil {
		t.Fatalf("user resource: %v", u)
	}
	token, _ := out["next_page_token"].(string)
	if token == "" {
		t.Fatal("next_page_token must be set when the service reports a cursor")
	}
	// 第二页：token 解码后作为 after 传给服务；page_size 缺省 20；超过上限按 100
	rec = do(t, h, call{method: "GET", path: "/users?page_token=" + token, headers: operator})
	if rec.Code != 200 || got.after == nil || got.after.ID != uid || !got.after.Time.Equal(adminUserFixture().CreateTime) || got.limit != 20 {
		t.Fatalf("page 2: %d after=%+v limit=%d", rec.Code, got.after, got.limit)
	}
	if rec = do(t, h, call{method: "GET", path: "/users?page_size=500", headers: operator}); rec.Code != 200 || got.limit != 100 {
		t.Fatalf("page_size clamp: %d limit=%d", rec.Code, got.limit)
	}
	// 无下一页 → 空串
	f.listUsers = func(context.Context, user.UserFilter, *user.PageCursor, int) (user.UserPage, error) {
		return user.UserPage{Users: []user.AdminUser{}}, nil
	}
	rec = do(t, h, call{method: "GET", path: "/users", headers: operator})
	decode(t, rec, &out)
	if out["next_page_token"] != "" || len(out["users"].([]any)) != 0 {
		t.Fatalf("empty page: %v", out)
	}
	// 参数错误
	for path, want := range map[string]string{
		"/users?filter=nope%20%3D%201": "INVALID_ARGUMENT/INVALID_FILTER",
		"/users?page_size=-1":          "INVALID_ARGUMENT/INVALID_PAGE_SIZE",
		"/users?page_token=%21%21":     "INVALID_ARGUMENT/INVALID_PAGE_TOKEN",
		"/users?show_deleted=yes":      "INVALID_ARGUMENT/INVALID_SHOW_DELETED",
	} {
		rec = do(t, h, call{method: "GET", path: path, headers: operator})
		if status, _ := aipError(t, rec); rec.Code != 400 || status != want {
			t.Fatalf("%s: %d %s", path, rec.Code, status)
		}
	}
	// 领域层归一化失败（identity.phone 值非法）→ 400 INVALID_FILTER，不回显值
	f.listUsers = func(context.Context, user.UserFilter, *user.PageCursor, int) (user.UserPage, error) {
		return user.UserPage{}, user.ErrInvalidTarget
	}
	rec = do(t, h, call{method: "GET", path: "/users?filter=identity.phone%20%3D%20abc", headers: operator})
	if status, body := aipError(t, rec); rec.Code != 400 || status != "INVALID_ARGUMENT/INVALID_FILTER" || strings.Contains(body["message"].(string), "abc") {
		t.Fatalf("invalid target: %d %s %v", rec.Code, status, body)
	}
}

func TestGetUserDetail(t *testing.T) {
	f := &fakeService{getUserDetail: func(_ context.Context, id string) (user.AdminUserDetail, error) {
		if id != uid {
			return user.AdminUserDetail{}, user.ErrNotFound
		}
		u := adminUserFixture()
		u.State = enum.UserFrozen
		u.Freeze = &user.FreezeInfo{Time: testNow, Reason: "abuse", ActorSubject: "adm-1", ActorUsername: "ops"}
		return user.AdminUserDetail{AdminUser: u, ActiveSessionCount: 2, Identities: []user.IdentityInfo{
			{ID: iid, Kind: enum.IdentityPhone, MaskedSubject: "+86 138****1234", CreateTime: testNow},
			{ID: "i_0k3f9c2m1xq70", Kind: enum.IdentityWeChat, MaskedSubject: "", CreateTime: testNow},
		}}, nil
	}}
	h := newHandler(t, f, nil)
	rec := do(t, h, call{method: "GET", path: "/users/" + uid, headers: operator})
	var d map[string]any
	decode(t, rec, &d)
	if rec.Code != 200 {
		t.Fatalf("detail: %d %s", rec.Code, rec.Body.String())
	}
	assertKeys(t, d, "name", "state", "display_name", "create_time", "update_time", "delete_time", "purge_time", "freeze", "identities", "active_session_count")
	fr := d["freeze"].(map[string]any)
	assertKeys(t, fr, "freeze_time", "reason", "actor_subject", "actor_username")
	if fr["reason"] != "abuse" || fr["actor_username"] != "ops" || d["active_session_count"] != float64(2) || d["state"] != "FROZEN" {
		t.Fatalf("detail fields: %v", d)
	}
	idents := d["identities"].([]any)
	first := idents[0].(map[string]any)
	assertKeys(t, first, "name", "kind", "masked_subject", "create_time")
	if first["name"] != "users/"+uid+"/identities/"+iid || first["masked_subject"] != "+86 138****1234" || idents[1].(map[string]any)["masked_subject"] != "" {
		t.Fatalf("identities: %v", idents)
	}
	// 时间格式：RFC 3339（UTC，纳秒精度按需）
	if _, err := time.Parse(time.RFC3339Nano, d["create_time"].(string)); err != nil {
		t.Fatalf("create_time format: %v", d["create_time"])
	}
	rec = do(t, h, call{method: "GET", path: "/users/u_0000000000000", headers: operator})
	if status, _ := aipError(t, rec); rec.Code != 404 || status != "NOT_FOUND/NOT_FOUND" {
		t.Fatalf("unknown user: %d %s", rec.Code, status)
	}
	rec = do(t, h, call{method: "GET", path: "/users/not-an-id", headers: operator})
	if status, _ := aipError(t, rec); rec.Code != 400 || status != "INVALID_ARGUMENT/INVALID_ID" {
		t.Fatalf("malformed id: %d %s", rec.Code, status)
	}
}

func TestFreezeUnfreezeDeleteUndelete(t *testing.T) {
	type callRec struct {
		a      user.Admin
		id     string
		reason string
		meta   user.Meta
	}
	var last callRec
	frozen := adminUserFixture()
	frozen.State = enum.UserFrozen
	frozen.Freeze = &user.FreezeInfo{Time: testNow, Reason: "abuse", ActorSubject: "adm-1", ActorUsername: "ops"}
	f := &fakeService{
		freeze: func(_ context.Context, a user.Admin, id, reason string, meta user.Meta) (user.AdminUser, error) {
			last = callRec{a, id, reason, meta}
			if reason == "" {
				return user.AdminUser{}, user.ErrInvalidArgument
			}
			return frozen, nil
		},
		unfreeze: func(_ context.Context, a user.Admin, id, reason string, meta user.Meta) (user.AdminUser, error) {
			last = callRec{a, id, reason, meta}
			return adminUserFixture(), nil
		},
		adminDeleteUser: func(_ context.Context, a user.Admin, id string, meta user.Meta) (user.AdminUser, error) {
			last = callRec{a: a, id: id, meta: meta}
			u := adminUserFixture()
			u.State = enum.UserPendingDeletion
			dt, pt := testNow, testNow.Add(360*time.Hour)
			u.DeleteTime, u.PurgeTime = &dt, &pt
			return u, nil
		},
		adminUndeleteUser: func(_ context.Context, a user.Admin, id string, meta user.Meta) (user.AdminUser, error) {
			last = callRec{a: a, id: id, meta: meta}
			return adminUserFixture(), nil
		},
	}
	h := newHandler(t, f, nil)

	rec := do(t, h, call{method: "POST", path: "/users/" + uid + ":freeze", body: map[string]string{"reason": "abuse"}, headers: operator})
	var u map[string]any
	decode(t, rec, &u)
	if rec.Code != 200 || u["state"] != "FROZEN" || u["freeze"].(map[string]any)["reason"] != "abuse" {
		t.Fatalf("freeze: %d %v", rec.Code, u)
	}
	if last.a != wantAdmin || last.id != uid || last.reason != "abuse" || last.meta.IP != "198.51.100.7" || last.meta.RequestID != "req-admin" {
		t.Fatalf("freeze call: %+v", last)
	}
	// reason 由领域层校验（空 → ErrInvalidArgument → 400）；body 非对象 / 未知字段 → 400 MALFORMED_BODY
	rec = do(t, h, call{method: "POST", path: "/users/" + uid + ":freeze", body: map[string]string{"reason": ""}, headers: operator})
	if status, _ := aipError(t, rec); rec.Code != 400 || status != "INVALID_ARGUMENT/INVALID_ARGUMENT" {
		t.Fatalf("empty reason: %d %s", rec.Code, status)
	}
	for _, body := range []any{`[]`, map[string]string{"why": "x"}, nil} {
		rec = do(t, h, call{method: "POST", path: "/users/" + uid + ":freeze", body: body, headers: operator})
		if status, _ := aipError(t, rec); rec.Code != 400 || status != "INVALID_ARGUMENT/MALFORMED_BODY" {
			t.Fatalf("malformed body %v: %d %s", body, rec.Code, status)
		}
	}
	// unfreeze：reason 可省（{}）
	rec = do(t, h, call{method: "POST", path: "/users/" + uid + ":unfreeze", body: map[string]any{}, headers: operator})
	if rec.Code != 200 || last.reason != "" {
		t.Fatalf("unfreeze: %d %+v", rec.Code, last)
	}
	// delete / undelete（super-admin）；operator → 403（fake verifier）
	rec = do(t, h, call{method: "DELETE", path: "/users/" + uid, headers: superAdmin})
	decode(t, rec, &u)
	if rec.Code != 200 || u["state"] != "PENDING_DELETION" || u["delete_time"] == nil || u["purge_time"] == nil || last.a != wantAdmin {
		t.Fatalf("admin delete: %d %v", rec.Code, u)
	}
	rec = do(t, h, call{method: "POST", path: "/users/" + uid + ":undelete", headers: superAdmin})
	decode(t, rec, &u)
	if rec.Code != 200 || u["state"] != "ACTIVE" {
		t.Fatalf("admin undelete: %d %v", rec.Code, u)
	}
	// 领域错误映射：状态不符 400 INVALID_ACCOUNT_STATE；冻结账号 400 USER_FROZEN（不是 403）；未知 404
	for _, c := range []struct {
		err    error
		code   int
		status string
	}{
		{user.ErrInvalidState, 400, "FAILED_PRECONDITION/INVALID_ACCOUNT_STATE"},
		{user.ErrUserFrozen, 400, "FAILED_PRECONDITION/USER_FROZEN"},
		{user.ErrNotFound, 404, "NOT_FOUND/NOT_FOUND"},
		{errors.New("db down"), 500, "INTERNAL/"},
	} {
		e := c.err
		hh := newHandler(t, &fakeService{adminDeleteUser: func(context.Context, user.Admin, string, user.Meta) (user.AdminUser, error) {
			return user.AdminUser{}, e
		}}, nil)
		rec = do(t, hh, call{method: "DELETE", path: "/users/" + uid, headers: superAdmin})
		if status, body := aipError(t, rec); rec.Code != c.code || status != c.status || (c.code == 500 && body["message"] != "internal error") {
			t.Fatalf("%v: %d %s %v", c.err, rec.Code, status, body)
		}
	}
	// 非法路径 id
	rec = do(t, h, call{method: "POST", path: "/users/bad:freeze", body: map[string]string{"reason": "x"}, headers: operator})
	if status, _ := aipError(t, rec); rec.Code != 400 || status != "INVALID_ARGUMENT/INVALID_ID" {
		t.Fatalf("bad id: %d %s", rec.Code, status)
	}
}
