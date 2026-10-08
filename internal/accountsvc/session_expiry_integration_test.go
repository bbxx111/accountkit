package accountsvc_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
)

// Run the actual service binary with PostgreSQL, Redis, SMTP and OIDC fixtures.
func TestServiceIntegrationSessionExpiry(t *testing.T) {
	f := newIntegration(t)
	f.oidc()
	p := f.start(nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	const target = "service-expiry@example.test"
	login := func(device string) map[string]any {
		t.Helper()
		f.call("POST", p.baseURL+"/v1/users:sendSignInCode", "", map[string]string{"channel": "EMAIL", "target": target}, 200)
		m := f.smtp.mail(t)
		if m.target != target || !strings.Contains(m.body, "登录") {
			t.Fatal("wrong sign-in delivery")
		}
		pair := replacementDeviceSignIn(t, f, p, target, m.code, device)
		f.secrets = append(f.secrets, target, m.code, pair["access_token"].(string), pair["refresh_token"].(string))
		return pair
	}
	expired := login("expiry-old")
	if len(expired) != 8 || expired["token_type"] != "Bearer" || expired["expires_in"] != float64(900) {
		t.Fatal("sign-in token DTO/default access TTL changed")
	}
	time.Sleep(1100 * time.Millisecond) // The real Redis code cooldown, not a session TTL.
	live := login("expiry-live")
	access, uid := expired["access_token"].(string), expired["user_id"].(string)
	sid := replacementClaims(t, access)["sid"].(string)
	liveSID := replacementClaims(t, live["access_token"].(string))["sid"].(string)
	if _, err := f.pool.Exec(ctx, "UPDATE session SET refresh_expire_time=$2 WHERE id=$1", sid, time.Now().UTC().Add(-time.Second).Truncate(time.Microsecond)); err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		t.Helper()
		var out string
		if err := f.pool.QueryRow(ctx, "SELECT row_to_json(s)::text FROM session s WHERE id=$1", sid).Scan(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	before := snapshot()
	for _, tc := range []struct {
		path, bearer string
		fields       int
	}{
		{"/v1/users/me/sessions", access, 6},
		{"/admin/v1/users/" + uid + "/sessions", f.env["FIXTURE_operator"], 5},
	} {
		list := f.call("GET", p.baseURL+tc.path, tc.bearer, nil, 200)
		rows, ok := list["sessions"].([]any)
		if !ok || len(rows) != 1 {
			t.Errorf("%s: sessions=%d want=1", tc.path, len(rows))
			continue
		}
		row := rows[0].(map[string]any)
		if len(row) != tc.fields || row["name"] != "users/"+uid+"/sessions/"+liveSID {
			t.Error("active membership or session DTO changed")
		}
		for _, field := range []string{"name", "device_id", "device_name", "create_time", "last_used_time"} {
			if _, ok := row[field]; !ok {
				t.Errorf("missing session field %s", field)
			}
		}
		if tc.fields == 6 && row["is_current"] != false {
			t.Error("expired caller was marked current")
		}
		f.assertSafe(mustExpiryJSON(t, list))
	}
	detail := f.call("GET", p.baseURL+"/admin/v1/users/"+uid, f.env["FIXTURE_operator"], nil, 200)
	if detail["active_session_count"] != float64(1) || len(detail) != 10 {
		t.Error("detail count or DTO changed")
	}
	f.assertSafe(mustExpiryJSON(t, detail))
	if snapshot() != before {
		t.Fatal("listing modified expired row")
	}
	failed := f.call("POST", p.baseURL+"/v1/token", "", map[string]string{"grant_type": "refresh_token", "refresh_token": expired["refresh_token"].(string)}, 400)
	if failed["error"] != "invalid_grant" {
		t.Error("refresh expiry error changed")
	}
	time.Sleep(1100 * time.Millisecond) // Sign-in and REAUTH share the real Redis cooldown.
	f.call("POST", p.baseURL+"/v1/users/me:sendReauthenticationCode", access, map[string]string{"channel": "EMAIL", "target": target}, 200)
	m := f.smtp.mail(t)
	f.secrets = append(f.secrets, m.target, m.code)
	if m.target != target || !strings.Contains(m.body, "重新认证") {
		t.Fatal("wrong reauthentication delivery")
	}
	// Preserve the WWW-Authenticate header in addition to asserting the error body.
	raw := mustExpiryJSON(t, map[string]any{"email": map[string]string{"code_id": f.codeID(target), "target": target, "code": m.code}})
	req, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/v1/users/me:reauthenticate", bytes.NewBufferString(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("X-Request-Id", "svc-integration")
	resp, err := f.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	challenge := resp.Header.Get("WWW-Authenticate")
	if resp.StatusCode != 401 || !strings.HasPrefix(challenge, "Bearer ") || !strings.Contains(challenge, `error="invalid_token"`) {
		t.Fatalf("expired reauth: status=%d, Bearer challenge=%v", resp.StatusCode, resp.Header.Get("WWW-Authenticate") != "")
	}
	var rejected map[string]any
	if err := json.Unmarshal(body, &rejected); err != nil {
		t.Fatal(err)
	}
	if rejected["error"].(map[string]any)["reason"] != "TOKEN_INVALID" {
		t.Error("reauth expiry reason changed")
	}
	f.assertSafe(string(body))
	if snapshot() != before {
		t.Fatal("expiry rejection mutated stored session")
	}
	if n, err := f.redis.Exists(ctx, f.prefix+"revoked:"+sid).Result(); err != nil || n != 0 {
		t.Fatalf("expiry created revocation key: n=%d err=%v", n, err)
	}
	f.introspect(p, access, true)
	me := f.call("GET", p.baseURL+"/v1/users/me", access, nil, 200)
	if me["state"] != "ACTIVE" {
		t.Error("expiry changed user state")
	}
	f.call("DELETE", p.baseURL+"/admin/v1/users/"+uid+"/sessions/"+sid, f.env["FIXTURE_operator"], nil, 204)
	f.introspect(p, access, false)
	f.call("GET", p.baseURL+"/v1/users/me", access, nil, 401)
	p.stop()
	f.assertAuditPrivate()
	for _, typ := range []enum.EventType{enum.EventRefreshRejected, enum.EventReauthenticationFailed} {
		var count int
		if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM audit_event WHERE session_id=$1 AND event_type=$2 AND reason='SESSION_EXPIRED'", sid, typ).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Errorf("SESSION_EXPIRED audit %s count=%d want=1", typ, count)
		}
	}
	var revoked int
	if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM audit_event WHERE session_id=$1 AND event_type=$2", sid, enum.EventSessionRevoked).Scan(&revoked); err != nil {
		t.Fatal(err)
	}
	if revoked != 1 {
		t.Errorf("expiry rejection added revocation audit: count=%d want=1 explicit admin revoke", revoked)
	}
}

func mustExpiryJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
