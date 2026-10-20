package admin_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
)

func TestAdminSessionClientInfoDTO(t *testing.T) {
	f := &fakeService{adminListSessions: func(context.Context, string) ([]user.SessionInfo, error) {
		return []user.SessionInfo{{ID: sid, ClientInfo: &user.ClientInfo{ClientType: enum.ClientNative, AppName: "Account", AppVersion: "2", AppBuild: "200", BrowserName: "Chrome", DeviceType: enum.DevicePhone, DeviceManufacturer: "Example", Extensions: map[string]json.RawMessage{"engine": json.RawMessage(`{"channel":"stable"}`)}}, ClientInfoUpdateTime: &testNow}, {}}, nil
	}}
	rec := do(t, newHandler(t, f, nil), call{method: "GET", path: "/users/" + uid + "/sessions", headers: operator})
	var out map[string]any
	decode(t, rec, &out)
	rows := out["sessions"].([]any)
	first := rows[0].(map[string]any)
	assertKeys(t, first, "name", "device_id", "device_name", "create_time", "last_used_time", "client_info", "client_info_update_time")
	ci := first["client_info"].(map[string]any)
	assertKeys(t, ci, "type", "app", "browser", "device", "extensions")
	if ci["type"] != "NATIVE" || ci["app"].(map[string]any)["name"] != "Account" || ci["device"].(map[string]any)["type"] != "PHONE" {
		t.Fatal(ci)
	}
	if ci["app"].(map[string]any)["build"] != "200" || ci["browser"].(map[string]any)["name"] != "Chrome" || ci["device"].(map[string]any)["manufacturer"] != "Example" || ci["extensions"].(map[string]any)["engine"].(map[string]any)["channel"] != "stable" {
		t.Fatal(ci)
	}
	if _, e := rows[1].(map[string]any)["client_info"]; e {
		t.Fatal("missing must be omitted")
	}
	if _, e := rows[1].(map[string]any)["client_info_update_time"]; e {
		t.Fatal("missing time must be omitted")
	}
}
