package admin_test

import (
	"context"
	"testing"

	"github.com/bbxx111/accountkit/user"
)

func TestSessionsListRevokeAndRevokeAll(t *testing.T) {
	var revoked []string
	var allFor string
	f := &fakeService{
		adminListSessions: func(_ context.Context, id string) ([]user.SessionInfo, error) {
			if id != uid {
				return nil, user.ErrNotFound
			}
			return []user.SessionInfo{{ID: sid, DeviceID: "dev-1", DeviceName: "iPhone", CreateTime: testNow, LastUsedTime: testNow}}, nil
		},
		adminRevokeSession: func(_ context.Context, a user.Admin, id, s string, _ user.Meta) error {
			if s != sid {
				return user.ErrNotFound
			}
			revoked = append(revoked, s)
			return nil
		},
		adminRevokeAllSessions: func(_ context.Context, a user.Admin, id string, _ user.Meta) (int, error) {
			if id != uid {
				return 0, user.ErrNotFound
			}
			allFor = id
			return 3, nil
		},
	}
	h := newHandler(t, f, nil)
	rec := do(t, h, call{method: "GET", path: "/users/" + uid + "/sessions", headers: operator})
	var out map[string]any
	decode(t, rec, &out)
	s := out["sessions"].([]any)[0].(map[string]any)
	assertKeys(t, s, "name", "device_id", "device_name", "create_time", "last_used_time")
	if rec.Code != 200 || s["name"] != "users/"+uid+"/sessions/"+sid {
		t.Fatalf("list: %d %v", rec.Code, out)
	}
	if rec = do(t, h, call{method: "GET", path: "/users/u_0000000000000/sessions", headers: operator}); rec.Code != 404 {
		t.Fatalf("list unknown user: %d", rec.Code)
	}
	if rec = do(t, h, call{method: "DELETE", path: "/users/" + uid + "/sessions/" + sid, headers: operator}); rec.Code != 204 || len(revoked) != 1 {
		t.Fatalf("revoke: %d %v", rec.Code, revoked)
	}
	if rec = do(t, h, call{method: "DELETE", path: "/users/" + uid + "/sessions/s_0000000000000", headers: operator}); rec.Code != 404 {
		t.Fatalf("revoke unknown: %d", rec.Code)
	}
	rec = do(t, h, call{method: "DELETE", path: "/users/" + uid + "/sessions/nope", headers: operator})
	if status, _ := aipError(t, rec); rec.Code != 400 || status != "INVALID_ARGUMENT/INVALID_ID" {
		t.Fatalf("bad sid: %d %s", rec.Code, status)
	}
	rec = do(t, h, call{method: "POST", path: "/users/" + uid + "/sessions:revokeAll", headers: operator})
	var ra map[string]any
	decode(t, rec, &ra)
	if rec.Code != 200 || ra["revoked_count"] != float64(3) || allFor != uid {
		t.Fatalf("revoke all: %d %v", rec.Code, ra)
	}
	assertKeys(t, ra, "revoked_count")
}
