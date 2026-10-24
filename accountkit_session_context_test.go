package accountkit_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/internal/accountsvc/introspection"
	"github.com/bbxx111/accountkit/user"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func TestCurrentSessionHTTPAgainstRealDB(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	cfg := minimal()
	cfg.CodeCooldown = time.Second
	a, _, sent := integrationInstance(t, cfg, rdb)
	if e := a.Migrate(context.Background()); e != nil {
		t.Fatal(e)
	}
	h := a.EndUserHandler()
	request := func(method, path, token string, body any, headers map[string]string, want int) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.RemoteAddr = "203.0.113.9:42"
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s: %d want %d %s", path, w.Code, want, w.Body.String())
		}
		if strings.HasSuffix(path, "/current") && (w.Header().Get("Cache-Control") != "no-store" || want == 401 && w.Header().Get("WWW-Authenticate") == "") {
			t.Fatal("cache/challenge boundary")
		}
		var out map[string]any
		if w.Body.Len() > 0 {
			if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil {
				t.Fatal(e)
			}
		}
		sent.rememberChallenge(body, out)
		return out
	}
	const email = "http-context@example.test"
	const phone = "+8613812349988"
	request("POST", "/users:sendSignInCode", "", map[string]string{"channel": "EMAIL", "target": email}, nil, 200)
	cred := map[string]any{"kind": "EMAIL", "credential": map[string]string{"code_id": sent.codeID(email), "target": email, "code": sent.code(email)}}
	request("POST", "/users:signInWithCode", "", cred, map[string]string{"X-Device-Id": "http-context", "X-Client-Type": "INVALID"}, 400)
	pair := request("POST", "/users:signInWithCode", "", cred, map[string]string{"X-Device-Id": "http-context", "X-Device-Name": "Original Device", "X-Client-Type": "WEB", "X-Client-OS-Name": "Initial OS", "X-Client-Browser-Name": "Firefox"}, 200)
	access := pair["access_token"].(string)
	initial := request("GET", "/users/me/sessions/current", access, nil, nil, 200)
	if _, exists := initial["authentication_identities_known"]; exists {
		t.Fatal("removed known flag exposed")
	}
	if _, exists := initial["initial_client_info"]; exists {
		t.Fatal("removed initial snapshot exposed")
	}
	if len(initial["authentication_identities"].([]any)) != 1 || initial["client_info"].(map[string]any)["os"].(map[string]any)["name"] != "Initial OS" {
		t.Fatal("missing initial source or snapshot")
	}
	if _, e := time.Parse(time.RFC3339, initial["auth_time"].(string)); e != nil {
		t.Fatal(e)
	}
	request("POST", "/users/me:sendBindCode", access, map[string]string{"channel": "PHONE", "target": phone}, nil, 200)
	bound := request("POST", "/users/me/identities", access, map[string]any{"kind": "PHONE", "credential": map[string]string{"code_id": sent.codeID(phone), "target": phone, "code": sent.code(phone)}}, map[string]string{"X-Client-Type": "INVALID"}, 201)
	mr.FastForward(2 * time.Second)
	request("POST", "/users/me:sendReauthenticationCode", access, map[string]string{"channel": "PHONE", "target": phone}, nil, 200)
	time.Sleep(1100 * time.Millisecond)
	request("POST", "/users/me:reauthenticate", access, map[string]any{"kind": "PHONE", "credential": map[string]string{"code_id": sent.codeID(phone), "target": phone, "code": sent.code(phone)}}, map[string]string{"X-Client-Type": "NATIVE", "X-Client-App-Name": "Context App"}, 200)
	current := request("GET", "/users/me/sessions/current", access, nil, nil, 200) // The original JWT remains valid and sees the new database proof.
	sources := current["authentication_identities"].([]any)
	if len(sources) != 1 || sources[0].(map[string]any)["name"] != bound["name"] || sources[0].(map[string]any)["kind"] != "PHONE" || current["auth_time"] == initial["auth_time"] {
		t.Fatal("current proof not replaced")
	}
	if current["device_name"] != "Original Device" || current["client_info"].(map[string]any)["type"] != "NATIVE" {
		t.Fatal("client information/device changed incorrectly")
	}
	request("DELETE", "/users/me/identities/"+strings.Split(bound["name"].(string), "/")[3], access, nil, nil, 204)
	request("GET", "/users/me/sessions/current", access, nil, nil, 401)
}

type sessionQueryCounter struct{ count atomic.Int64 }

func (c *sessionQueryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	c.count.Add(1)
	return ctx
}
func (c *sessionQueryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestSessionQueriesStayBoundedAndAuthenticationHasNoDB(t *testing.T) {
	ctx := context.Background()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	cfg := minimal()
	cfg.CodeCooldown = time.Second
	base, pool, sent := integrationInstance(t, cfg, rdb)
	if e := base.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	pc := pool.Config()
	counter := &sessionQueryCounter{}
	pc.ConnConfig.Tracer = counter
	traced, e := pgxpool.NewWithConfig(ctx, pc)
	if e != nil {
		t.Fatal(e)
	}
	defer traced.Close()
	// New performs no I/O; retain the same owned schema and instance settings.
	cfg.Schema = strings.Split(pc.ConnConfig.RuntimeParams["search_path"], ",")[0]
	cfg = externalAuditConfig(cfg)
	auth, e := accountkit.New(cfg, accountkit.Deps{Pool: traced, Redis: rdb, SMSSender: sent, EmailSender: sent, Audit: audit.Noop{}, ClientInfo: func(*http.Request) (*user.ClientInfo, error) { return &user.ClientInfo{OSName: " Host OS "}, nil }})
	if e != nil {
		t.Fatal(e)
	}
	defer auth.Close()
	var pair user.TokenResult
	for i := 0; i < 4; i++ {
		challenge, e := auth.Users().SendSignInCode(ctx, enum.IdentityEmail, "bounded@example.test", user.Meta{IP: "203.0.113.9"})
		if e != nil {
			t.Fatal(e)
		}
		// Exercise root Deps.ClientInfo wiring through the real HTTP adapter.
		raw, _ := json.Marshal(map[string]any{"kind": "EMAIL", "credential": map[string]string{"target": "bounded@example.test", "code_id": challenge.CodeID, "code": sent.code("bounded@example.test")}})
		req := httptest.NewRequest("POST", "/users:signInWithCode", bytes.NewReader(raw))
		req.Header.Set("X-Device-Id", fmt.Sprintf("batch-%d", i))
		w := httptest.NewRecorder()
		auth.EndUserHandler().ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatalf("login %d", w.Code)
		}
		var out map[string]any
		json.Unmarshal(w.Body.Bytes(), &out)
		pair = user.TokenResult{AccessToken: out["access_token"].(string), UserID: out["user_id"].(string)}
		p, e := auth.Users().Authenticate(ctx, pair.AccessToken)
		if e != nil {
			t.Fatal(e)
		}
		counter.count.Store(0)
		sessions, e := auth.Users().ListSessions(ctx, p.UserID, p.SessionID)
		if e != nil || len(sessions) != i+1 || counter.count.Load() != 2 {
			t.Fatalf("list: n=%d queries=%d err=%v", len(sessions), counter.count.Load(), e)
		}
		for _, s := range sessions {
			if s.ClientInfo == nil || s.ClientInfo.OSName != "Host OS" {
				t.Fatal("root callback was not normalized/wired")
			}
		}
		counter.count.Store(0)
		admin, e := auth.Users().AdminListSessions(ctx, p.UserID)
		if e != nil || len(admin) != i+1 || counter.count.Load() != 3 {
			t.Fatalf("admin list: queries=%d err=%v", counter.count.Load(), e)
		}
		mr.FastForward(2 * time.Second)
	}
	counter.count.Store(0)
	if _, e := auth.Users().Authenticate(ctx, pair.AccessToken); e != nil {
		t.Fatal(e)
	}
	secret := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	ih, e := introspection.New(map[string][]string{"business": {secret}}, auth.Users().Authenticate)
	if e != nil {
		t.Fatal(e)
	}
	req := httptest.NewRequest("POST", "/v1/introspect", strings.NewReader("token="+pair.AccessToken))
	req.SetBasicAuth("business", secret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Client-Type", "INVALID")
	w := httptest.NewRecorder()
	ih.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"active":true`) || counter.count.Load() != 0 {
		t.Fatalf("hot path: status=%d queries=%d", w.Code, counter.count.Load())
	}
	// A closed pool proves the hot path does not merely hide ignored SQL failures.
	traced.Close()
	if _, e := auth.Users().Authenticate(ctx, pair.AccessToken); e != nil {
		t.Fatal(e)
	}
	w = httptest.NewRecorder()
	req.Body = io.NopCloser(strings.NewReader("token=" + pair.AccessToken))
	ih.ServeHTTP(w, req.Clone(ctx))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"active":true`) {
		t.Fatalf("introspection with closed DB: %d", w.Code)
	}
	req = httptest.NewRequest("GET", "/users/me/sessions/current", nil)
	req.Header.Set("Authorization", "Bearer "+pair.AccessToken)
	w = httptest.NewRecorder()
	auth.EndUserHandler().ServeHTTP(w, req)
	if w.Code != 503 {
		t.Fatalf("database failure must be 503, got %d %s", w.Code, w.Body.String())
	}
}
