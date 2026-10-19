package admin_test

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bbxx111/accountkit/audit"
)

func TestAuditCursorRejectsOldVersionAndActionScope(t *testing.T) {
	for _, tc := range []struct {
		name    string
		version int
		actions []string
	}{
		{"old_uuid_v1", 1, nil},
		{"other_actions", 2, []string{"SIGN_IN"}},
		{"missing_actions", 2, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := auditRecordAt(t, 1, testNow)
			b, err := json.Marshal(map[string]any{"v": tc.version, "source": "test/source", "resource": audit.Resource{Type: "USER", Namespace: "test/ns", ID: uid}, "actions": tc.actions, "t": row.OccurTime, "id": row.ID})
			if err != nil {
				t.Fatal(err)
			}
			token := base64.RawURLEncoding.EncodeToString(b)
			rec := do(t, auditHandler(t, auditPageReader(audit.Page{}), nil), call{method: "GET", path: "/users/" + uid + "/auditEvents?page_token=" + token, headers: operator})
			if rec.Code != 400 || !strings.Contains(rec.Body.String(), "INVALID_PAGE_TOKEN") || !strings.Contains(rec.Body.String(), "restart from the first page") {
				t.Fatalf("cursor accepted or no restart guidance: %d %s", rec.Code, rec.Body)
			}
		})
	}
}
