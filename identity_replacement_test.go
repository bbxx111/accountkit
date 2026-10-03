package accountkit_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/enum"
)

type replacementLog struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *replacementLog) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *replacementLog) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

// Real PostgreSQL and controlled Redis exercise the public Auth/HTTP assembly.
// The independent accountsvc E2E verifies the same lifecycle against real Redis.
func TestIdentityReplacementEndToEndAgainstRealDB(t *testing.T) {
	dsn := dbDSN(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	schema := fmt.Sprintf("authtest_replace_%08x", rand.Uint32())
	pc, err := accountkit.PoolConfig(dsn, schema)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		t.Fatal(err)
	}
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	prefix := schema + ":"
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if _, err := pool.Exec(cleanupCtx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE"); err != nil {
			t.Error("cleanup owned schema failed")
		}
		rdb.Close()
		pool.Close()
	})
	captured := &captureSender{}
	logs := &replacementLog{}
	cfg := minimal()
	cfg.Schema, cfg.KeyPrefix = schema, prefix
	cfg.CodeCooldown, cfg.ReauthMaxAge = time.Second, 5*time.Second
	a, err := accountkit.New(cfg, accountkit.Deps{Pool: pool, Redis: rdb, SMSSender: captured, EmailSender: captured, Logger: slog.New(slog.NewTextHandler(logs, nil))})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	if err := a.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	a.Start(ctx)
	r := chi.NewRouter()
	r.Mount("/v1", a.ConsumerHandler())
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	client := srv.Client()
	client.Timeout = 10 * time.Second
	secrets := []string{"replacement-old@example.test", "replacement-new@example.test"}
	assertPrivate := func(raw string) {
		t.Helper()
		for _, secret := range secrets {
			if secret != "" && strings.Contains(raw, secret) {
				t.Fatal("response, logs or audit exposed credential/private identity")
			}
		}
	}
	call := func(method, path, bearer, device string, body any, want int) map[string]any {
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
		req.Header.Set("X-Request-Id", "replacement-e2e")
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != want {
			t.Fatalf("%s: expected %d got %d", path, want, resp.StatusCode)
		}
		if strings.Contains(path, "/identities") || want >= 400 {
			assertPrivate(string(raw))
		}
		if want == 401 && resp.Header.Get("WWW-Authenticate") == "" {
			t.Fatal("revoked access missing Bearer challenge")
		}
		out := map[string]any{}
		if len(raw) != 0 {
			if err := json.Unmarshal(raw, &out); err != nil {
				t.Fatal("invalid JSON response")
			}
		}
		return out
	}
	rememberPair := func(pair map[string]any) {
		secrets = append(secrets, pair["access_token"].(string), pair["refresh_token"].(string))
	}
	login := func(target, device string) map[string]any {
		t.Helper()
		call("POST", "/v1/users:sendSignInCode", "", device, map[string]string{"channel": "EMAIL", "target": target}, 200)
		c := captured.code(target)
		if c == "" {
			t.Fatal("sender did not deliver login code")
		}
		secrets = append(secrets, c)
		pair := call("POST", "/v1/users:signInWithCode", "", device, map[string]any{"email": map[string]string{"target": target, "code": c}}, 200)
		rememberPair(pair)
		return pair
	}
	oldTarget, newTarget := secrets[0], secrets[1]
	current := login(oldTarget, "replacement-current")
	uid, access, refresh := current["user_id"].(string), current["access_token"].(string), current["refresh_token"].(string)
	mr.FastForward(2 * time.Second)
	other := login(oldTarget, "replacement-other")
	if other["user_id"] != uid {
		t.Fatal("second device did not enter original account")
	}
	previousOther := other["refresh_token"].(string)
	rotatedOther := call("POST", "/v1/token", "", "", map[string]string{"grant_type": "refresh_token", "refresh_token": previousOther}, 200)
	rememberPair(rotatedOther)
	list := call("GET", "/v1/users/me/identities", access, "", nil, 200)
	identities := list["identities"].([]any)
	if len(identities) != 1 {
		t.Fatal("expected one original email")
	}
	oldName := identities[0].(map[string]any)["name"].(string)
	oldID := oldName[strings.LastIndex(oldName, "/")+1:]
	path := "/v1/users/me/identities/" + oldID + ":replace"
	call("POST", "/v1/users/me:sendBindCode", access, "", map[string]string{"channel": "EMAIL", "target": newTarget}, 200)
	bindCode := captured.code(newTarget)
	if bindCode == "" {
		t.Fatal("sender did not deliver BIND code")
	}
	secrets = append(secrets, bindCode)
	body := map[string]any{"email": map[string]string{"target": newTarget, "code": bindCode}}
	// Let the configured freshness window expire; the first attempt must not consume BIND proof.
	time.Sleep(6 * time.Second)
	rejected := call("POST", path, access, "", body, 400)
	if rejected["error"].(map[string]any)["reason"] != "REAUTHENTICATION_REQUIRED" {
		t.Fatal("expired auth was not rejected as reauthentication required")
	}
	mr.FastForward(2 * time.Second)
	call("POST", "/v1/users/me:sendReauthenticationCode", access, "", map[string]string{"channel": "EMAIL", "target": oldTarget}, 200)
	reauthCode := captured.code(oldTarget)
	secrets = append(secrets, reauthCode)
	reauth := call("POST", "/v1/users/me:reauthenticate", access, "", map[string]any{"email": map[string]string{"target": oldTarget, "code": reauthCode}}, 200)
	access = reauth["access_token"].(string)
	secrets = append(secrets, access)
	before, err := a.Users().Authenticate(ctx, access)
	if err != nil {
		t.Fatal(err)
	}
	newIdentity := call("POST", path, access, "", body, 200)
	newName, ok := newIdentity["name"].(string)
	if !ok || newName == oldName || newIdentity["kind"] != "EMAIL" || newIdentity["masked_subject"] == "" || newIdentity["create_time"] == nil || len(newIdentity) != 4 {
		t.Fatal("replacement did not return only the new masked identity resource")
	}
	call("POST", path, access, "", body, 404)
	list = call("GET", "/v1/users/me/identities", access, "", nil, 200)
	identities = list["identities"].([]any)
	if len(identities) != 1 || identities[0].(map[string]any)["name"] != newName {
		t.Fatal("identity list did not confirm replacement")
	}
	call("GET", "/v1/users/me", access, "", nil, 200)
	for _, token := range []string{other["access_token"].(string), rotatedOther["access_token"].(string)} {
		call("GET", "/v1/users/me", token, "", nil, 401)
	}
	for _, token := range []string{previousOther, rotatedOther["refresh_token"].(string)} {
		out := call("POST", "/v1/token", "", "", map[string]string{"grant_type": "refresh_token", "refresh_token": token}, 400)
		if out["error"] != "invalid_grant" {
			t.Fatal("other-device refresh was not revoked")
		}
	}
	retained := call("POST", "/v1/token", "", "", map[string]string{"grant_type": "refresh_token", "refresh_token": refresh}, 200)
	rememberPair(retained)
	access = retained["access_token"].(string)
	after, err := a.Users().Authenticate(ctx, access)
	if err != nil || after.UserID != uid || after.SessionID != before.SessionID || !after.AuthTime.Equal(before.AuthTime) {
		t.Fatal("replacement or refresh changed current session/auth_time")
	}
	call("GET", "/v1/users/me", access, "", nil, 200)
	newLogin := login(newTarget, "replacement-new-login")
	if newLogin["user_id"] != uid || newLogin["is_new_user"] != false {
		t.Fatal("new target did not enter original account")
	}
	// Miniredis cooldown TTL requires explicit advancement across REAUTH/login.
	mr.FastForward(2 * time.Second)
	oldLogin := login(oldTarget, "replacement-old-login")
	if oldLogin["user_id"] == uid || oldLogin["is_new_user"] != true {
		t.Fatal("released address entered original account")
	}
	a.Close() // Flush bounded asynchronous auditing before checking persisted evidence.
	var auditJSON string
	if err := pool.QueryRow(ctx, "SELECT COALESCE(json_agg(a)::text,'[]') FROM audit_event a").Scan(&auditJSON); err != nil {
		t.Fatal(err)
	}
	assertPrivate(auditJSON)
	assertPrivate(logs.String())
	var linked int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM audit_event WHERE user_id=$1 AND request_id=$2 AND reason='IDENTITY_REPLACED' AND event_type IN ($3,$4,$5)", uid, "replacement-e2e", enum.EventIdentityBound, enum.EventIdentityUnbound, enum.EventSessionRevoked).Scan(&linked); err != nil {
		t.Fatal(err)
	}
	if linked != 3 {
		t.Fatalf("expected correlated bind/unbind/session revoke audit; got %d", linked)
	}
}
