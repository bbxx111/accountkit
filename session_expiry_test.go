package accountkit_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// Expiry fixtures only change rows in this test's schema. JWT TTL remains real.
func TestSessionExpiryEndToEndAgainstRealDB(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	schema := fmt.Sprintf("authtest_expiry_%08x", rand.Uint32())
	pc, err := accountkit.PoolConfig(dbDSN(t), schema)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		t.Fatal(err)
	}
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() {
		clean, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		if _, err := pool.Exec(clean, "DROP SCHEMA IF EXISTS "+schema+" CASCADE"); err != nil {
			t.Error("cleanup owned schema:", err)
		}
		rdb.Close()
		pool.Close()
	})
	sent, events := &captureSender{}, &audit.Memory{}
	cfg := minimal()
	cfg.Schema, cfg.KeyPrefix, cfg.CodeCooldown = schema, schema+":", time.Second
	a, err := accountkit.New(cfg, accountkit.Deps{Pool: pool, Redis: rdb, SMSSender: sent, EmailSender: sent, Audit: events, AdminVerifier: stubVerifier{}, AdminPrincipal: stubPrincipalFrom})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	if err := a.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	r.Mount("/v1", a.EndUserHandler())
	r.Route("/admin/v1", func(r chi.Router) { r.Use(stubVerifier{}.Middleware()); r.Mount("/", a.AdminHandler()) })
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	client := srv.Client()
	client.Timeout = 5 * time.Second
	call := func(method, path, token, device string, body any, want int) map[string]any {
		t.Helper()
		var rd io.Reader
		if body != nil {
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			rd = bytes.NewReader(raw)
		}
		req, err := http.NewRequestWithContext(ctx, method, srv.URL+path, rd)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Device-Id", device)
		req.Header.Set("X-Request-Id", "expiry-e2e")
		if strings.HasPrefix(path, "/admin/") {
			req.Header.Set("X-Test-Admin", "https://fixture.test|expiry-admin|operator|operator,super-admin")
		} else if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("%s: status=%d want=%d", path, resp.StatusCode, want)
		}
		challenge := resp.Header.Get("WWW-Authenticate")
		if want == 401 && (!strings.HasPrefix(challenge, "Bearer ") || !strings.Contains(challenge, `error="invalid_token"`)) {
			t.Fatal("missing Bearer invalid_token challenge")
		}
		out := map[string]any{}
		if want != 204 {
			if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
				t.Fatal(err)
			}
		}
		return out
	}
	const target = "expiry@example.test"
	login := func(device string) map[string]any {
		t.Helper()
		mr.FastForward(2 * time.Second)
		call("POST", "/v1/users:sendSignInCode", "", device, map[string]string{"channel": "EMAIL", "target": target}, 200)
		return call("POST", "/v1/users:signInWithCode", "", device, map[string]any{"email": map[string]string{"target": target, "code": sent.code(target)}}, 200)
	}
	expired := login("expired")
	live := login("live")
	if len(expired) != 8 || expired["token_type"] != "Bearer" || expired["expires_in"] != float64(900) {
		t.Fatal("sign-in token DTO/default access TTL changed")
	}
	access := expired["access_token"].(string)
	principal, err := a.Users().Authenticate(ctx, access)
	if err != nil {
		t.Fatal(err)
	}
	livePrincipal, err := a.Users().Authenticate(ctx, live["access_token"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "UPDATE session SET refresh_expire_time=$2 WHERE id=$1", principal.SessionID, time.Now().UTC().Add(-time.Second).Truncate(time.Microsecond)); err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		t.Helper()
		var out string
		if err := pool.QueryRow(ctx, "SELECT row_to_json(s)::text FROM session s WHERE id=$1", principal.SessionID).Scan(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	before, auditBefore := snapshot(), len(events.Events())
	for _, tc := range []struct {
		path   string
		fields []string
	}{
		{"/v1/users/me/sessions", []string{"name", "device_id", "device_name", "create_time", "last_used_time", "is_current"}},
		{"/admin/v1/users/" + principal.UserID + "/sessions", []string{"name", "device_id", "device_name", "create_time", "last_used_time"}},
	} {
		out := call("GET", tc.path, access, "", nil, 200)
		rows, ok := out["sessions"].([]any)
		if !ok || len(rows) != 1 {
			t.Errorf("%s: active session count=%d want=1", tc.path, len(rows))
			continue
		}
		item := rows[0].(map[string]any)
		if item["name"] != "users/"+principal.UserID+"/sessions/"+livePrincipal.SessionID || len(item) != len(tc.fields) {
			t.Error("session DTO or active membership changed")
		}
		for _, key := range tc.fields {
			if _, ok := item[key]; !ok {
				t.Errorf("missing session field %s", key)
			}
		}
		if len(tc.fields) == 6 && item["is_current"] != false {
			t.Error("expired caller was marked current")
		}
	}
	detail := call("GET", "/admin/v1/users/"+principal.UserID, "", "", nil, 200)
	if detail["active_session_count"] != float64(1) || len(detail) != 10 {
		t.Error("admin detail must count only live sessions and retain DTO")
	}
	if snapshot() != before || len(events.Events()) != auditBefore {
		t.Fatal("list/detail changed session or audit")
	}
	// An account with only expired stored sessions keeps the existing [] DTO.
	var liveExpiry time.Time
	if err := pool.QueryRow(ctx, "SELECT refresh_expire_time FROM session WHERE id=$1", livePrincipal.SessionID).Scan(&liveExpiry); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "UPDATE session SET refresh_expire_time=$2 WHERE id=$1", livePrincipal.SessionID, time.Now().UTC().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/users/me/sessions", "/admin/v1/users/" + principal.UserID + "/sessions"} {
		out := call("GET", path, access, "", nil, 200)
		rows, ok := out["sessions"].([]any)
		if !ok || len(rows) != 0 || len(out) != 1 {
			t.Errorf("%s did not preserve empty sessions array DTO", path)
		}
	}
	if out := call("GET", "/admin/v1/users/"+principal.UserID, "", "", nil, 200); out["active_session_count"] != float64(0) {
		t.Error("all-expired account count must be zero")
	}
	if _, err := pool.Exec(ctx, "UPDATE session SET refresh_expire_time=$2 WHERE id=$1", livePrincipal.SessionID, liveExpiry); err != nil {
		t.Fatal(err)
	}
	refresh := call("POST", "/v1/token", "", "", map[string]string{"grant_type": "refresh_token", "refresh_token": expired["refresh_token"].(string)}, 400)
	if refresh["error"] != "invalid_grant" {
		t.Error("expiry did not return OAuth invalid_grant")
	}
	mr.FastForward(2 * time.Second)
	call("POST", "/v1/users/me:sendReauthenticationCode", access, "", map[string]string{"channel": "EMAIL", "target": target}, 200)
	reauth := call("POST", "/v1/users/me:reauthenticate", access, "", map[string]any{"email": map[string]string{"target": target, "code": sent.code(target)}}, 401)
	if reauth["error"].(map[string]any)["reason"] != "TOKEN_INVALID" {
		t.Error("reauth expiry error changed")
	}
	if snapshot() != before || mr.Exists(schema+":revoked:"+principal.SessionID) {
		t.Fatal("expiry rejection modified session/revocation")
	}
	for _, e := range events.Events()[auditBefore:] {
		if e.Type == enum.EventSessionRevoked {
			t.Error("natural expiry or expiry rejection created revocation audit")
		}
	}
	if _, err := a.Users().Authenticate(ctx, access); err != nil {
		t.Fatal("natural expiry invalidated JWT:", err)
	}
	me := call("GET", "/v1/users/me", access, "", nil, 200)
	if me["state"] != "ACTIVE" {
		t.Error("expiry changed account state")
	}
	call("DELETE", "/v1/users/me/sessions/"+principal.SessionID, live["access_token"].(string), "", nil, 204)
	call("GET", "/v1/users/me", access, "", nil, 401)
	if snapshot() == before {
		t.Fatal("explicit revoke did not change stored session")
	}
	for _, typ := range []enum.EventType{enum.EventRefreshRejected, enum.EventReauthenticationFailed} {
		found := false
		for _, e := range events.Events() {
			if e.Type == typ && e.Reason == "SESSION_EXPIRED" && e.SessionID == principal.SessionID {
				found = true
			}
		}
		if !found {
			t.Errorf("missing SESSION_EXPIRED audit for %s", typ)
		}
	}
}
