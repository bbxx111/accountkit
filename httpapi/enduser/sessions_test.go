package enduser_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/user"
)

func TestSessions(t *testing.T) {
	const other = "s_0k3f9c2m1xq8a"
	var revoked, revokedOthersFor string
	f := withAuth(&fakeService{
		listSessions: func(_ context.Context, uid, sid string) ([]user.SessionInfo, error) {
			if uid != principal.UserID || sid != principal.SessionID {
				t.Fatalf("list args: %s %s", uid, sid)
			}
			return []user.SessionInfo{
				{ID: principal.SessionID, DeviceID: "d1", DeviceName: "Pixel 9", CreateTime: testNow.Add(-time.Hour), LastUsedTime: testNow, IsCurrent: true},
				{ID: other, DeviceID: "d2", DeviceName: "iPad", CreateTime: testNow.Add(-2 * time.Hour), LastUsedTime: testNow.Add(-time.Hour)},
			}, nil
		},
		revokeSession: func(_ context.Context, uid, sid string, meta user.Meta) error {
			if sid == "s_0k3f9c2m1xq9b" {
				return user.ErrNotFound
			}
			revoked = sid
			return nil
		},
		revokeOtherSessions: func(_ context.Context, uid, sid string, meta user.Meta) error {
			revokedOthersFor = uid + "/" + sid
			return nil
		},
	}, principal)
	h := newHandler(t, f)

	rec := do(t, h, call{method: "GET", path: "/users/me/sessions", bearer: "good"})
	var list struct {
		Sessions []map[string]any `json:"sessions"`
	}
	decode(t, rec, &list)
	if rec.Code != 200 || len(list.Sessions) != 2 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	s0 := list.Sessions[0]
	if s0["name"] != "users/"+principal.UserID+"/sessions/"+principal.SessionID || s0["device_id"] != "d1" || s0["device_name"] != "Pixel 9" || s0["is_current"] != true || s0["create_time"] != "2026-09-10T11:00:00Z" || s0["last_used_time"] != "2026-09-10T12:00:00Z" {
		t.Fatalf("session dto: %v", s0)
	}
	if list.Sessions[1]["is_current"] != false {
		t.Fatal("other session not current")
	}
	for k := range s0 {
		switch k {
		case "name", "device_id", "device_name", "create_time", "last_used_time", "is_current":
		default:
			t.Fatalf("unexpected field: %s", k)
		}
	}

	rec = do(t, h, call{method: "DELETE", path: "/users/me/sessions/" + other, bearer: "good"})
	if rec.Code != 204 || rec.Body.Len() != 0 || revoked != other {
		t.Fatalf("delete: %d %q %s", rec.Code, rec.Body.String(), revoked)
	}
	rec = do(t, h, call{method: "DELETE", path: "/users/me/sessions/s_0k3f9c2m1xq9b", bearer: "good"})
	if status, _ := aipError(t, rec); rec.Code != 404 || status != "NOT_FOUND/NOT_FOUND" {
		t.Fatalf("foreign: %d %s", rec.Code, status)
	}
	rec = do(t, h, call{method: "DELETE", path: "/users/me/sessions/not-an-id", bearer: "good"})
	if status, _ := aipError(t, rec); rec.Code != 400 || status != "INVALID_ARGUMENT/INVALID_ID" {
		t.Fatalf("malformed id: %d %s", rec.Code, status)
	}
	rec = do(t, h, call{method: "DELETE", path: "/users/me/sessions/u_0k3f9c2m1xq7z", bearer: "good"}) // 用户 id 前缀不是会话
	if status, _ := aipError(t, rec); rec.Code != 400 || status != "INVALID_ARGUMENT/INVALID_ID" {
		t.Fatalf("wrong prefix: %d %s", rec.Code, status)
	}

	rec = do(t, h, call{method: "POST", path: "/users/me/sessions:revokeOthers", bearer: "good"})
	if rec.Code != 200 || revokedOthersFor != principal.UserID+"/"+principal.SessionID {
		t.Fatalf("revokeOthers: %d %s", rec.Code, revokedOthersFor)
	}
	// 未认证
	if rec = do(t, h, call{method: "GET", path: "/users/me/sessions"}); rec.Code != 401 {
		t.Fatal("401")
	}
	// scope 不足
	bind := principal
	bind.Scope = "user:undelete"
	h2 := newHandler(t, withAuth(&fakeService{}, bind))
	if rec = do(t, h2, call{method: "GET", path: "/users/me/sessions", bearer: "good"}); rec.Code != 403 {
		t.Fatalf("scope: %d", rec.Code)
	}
}

// TestListSessionsNilIsEmptyArray：ListSessions 返回 nil, nil 时响应体必须是
// {"sessions":[]}，不能序列化成 {"sessions":null}。
func TestListSessionsNilIsEmptyArray(t *testing.T) {
	f := withAuth(&fakeService{
		listSessions: func(context.Context, string, string) ([]user.SessionInfo, error) { return nil, nil },
	}, principal)
	h := newHandler(t, f)
	rec := do(t, h, call{method: "GET", path: "/users/me/sessions", bearer: "good"})
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `{"sessions":[]}` {
		t.Fatalf("nil sessions: %d %s", rec.Code, rec.Body.String())
	}
}
