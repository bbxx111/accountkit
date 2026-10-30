package httpflows_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
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
	"github.com/bbxx111/accountkit/internal/testkeys"
	"github.com/bbxx111/accountkit/user/sender"
)

// codeCooldown 与 Config.CodeCooldown 默认值一致；同一目标重复发码前用 miniredis 快进越过冷却，
// 不修改生产默认值，也不真实等待。
const codeCooldown = 60 * time.Second

// capture 记录每个目标最近一次验证码与 code_id；真实客户端从短信/邮件取得验证码。
type capture struct {
	mu      sync.Mutex
	codes   map[string]string
	codeIDs map[string]string
}

func (c *capture) SendSMS(_ context.Context, to string, m sender.CodeMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.codes == nil {
		c.codes = map[string]string{}
	}
	c.codes[to] = m.Code
	return nil
}

func (c *capture) SendEmail(ctx context.Context, to string, m sender.CodeMessage) error {
	return c.SendSMS(ctx, to, m)
}

func (c *capture) code(to string) string { c.mu.Lock(); defer c.mu.Unlock(); return c.codes[to] }

func (c *capture) remember(target, id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.codeIDs == nil {
		c.codeIDs = map[string]string{}
	}
	c.codeIDs[target] = id
}

func (c *capture) codeID(target string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.codeIDs[target]
}

type env struct {
	t    *testing.T
	srv  *httptest.Server
	mr   *miniredis.Miniredis
	sent *capture
	// sentTo 记录已发过码的目标，再次发码前快进越过冷却。
	sentTo map[string]bool
}

type reply struct {
	status int
	header http.Header
	body   map[string]any
}

func key(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func newEnv(t *testing.T, dsn string, grace time.Duration) *env {
	t.Helper()
	ctx := context.Background()
	schema := fmt.Sprintf("aktest_%08x", rand.Uint32())
	pc, err := accountkit.PoolConfig(dsn, schema)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		defer pool.Close()
		if _, err := pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	// 合成微信提供方：任意 code 即为稳定的 unionid。
	wechat := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"access_token":"synthetic","openid":"open-%s","unionid":"%s"}`, r.URL.Query().Get("code"), r.URL.Query().Get("code"))
	}))
	t.Cleanup(wechat.Close)
	cfg := accountkit.Config{
		Schema:                 schema,
		JWTKeys:                map[uint16]*ecdsa.PrivateKey{1: testkeys.JWT(1)},
		JWTActiveKey:           1,
		JWTIssuer:              "flows",
		JWTAudience:            "app",
		SubjectHMACKeys:        map[uint16][]byte{1: key(2)},
		SubjectHMACActiveKey:   1,
		SubjectCipherKeys:      map[uint16][]byte{1: key(3)},
		SubjectCipherActiveKey: 1,
		EnabledIdentityKinds:   []enum.IdentityKind{enum.IdentityEmail, enum.IdentityWeChat},
		RequiredIdentityKinds:  []enum.IdentityKind{enum.IdentityEmail},
		// 解绑流程需要先绑定第二个邮箱，再解绑当前会话的登录来源。
		MaxIdentitiesPerKind: 2,
		WeChatApps:           []accountkit.WeChatApp{{AppID: "flows-wechat", Secret: "synthetic"}},
		WeChatAPIBaseURL:     wechat.URL,
	}
	if grace > 0 {
		cfg.RefreshGrace = grace
	}
	sent := &capture{}
	a, err := accountkit.New(cfg, accountkit.Deps{Pool: pool, Redis: rdb, SMSSender: sent, EmailSender: sent, HTTPClient: wechat.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	r := chi.NewRouter()
	r.Mount("/v1", a.EndUserHandler())
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return &env{t: t, srv: srv, mr: mr, sent: sent, sentTo: map[string]bool{}}
}

func (e *env) call(method, path, bearer, device string, body any) reply {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			e.t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, e.srv.URL+"/v1"+path, rd)
	if err != nil {
		e.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if device != "" {
		req.Header.Set("X-Device-Id", device)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return reply{resp.StatusCode, resp.Header, out}
}

// sendCode 调用任一发码端点并记录 code_id。同一目标再次发码前快进越过冷却（冷却按渠道与目标计，
// 不区分用途），对应真实客户端等待 retry_after_seconds 后重发。
func (e *env) sendCode(path, bearer string, body map[string]any) {
	e.t.Helper()
	target, _ := body["target"].(string)
	if e.sentTo[target] {
		e.mr.FastForward(codeCooldown + time.Second)
	}
	r := e.call("POST", path, bearer, "", body)
	if r.status != 200 || str(r.body, "code_id") == "" || str(r.body, "expire_time") == "" {
		e.t.Fatalf("%s %d %v", path, r.status, r.body)
	}
	e.sentTo[target] = true
	e.sent.remember(target, str(r.body, "code_id"))
}

func (e *env) credential(target string) map[string]any {
	return map[string]any{"target": target, "code_id": e.sent.codeID(target), "code": e.sent.code(target)}
}

// signIn 走完验证码登录并返回 token pair。
func (e *env) signIn(email, device string) map[string]any {
	e.t.Helper()
	e.sendCode("/users:sendSignInCode", "", map[string]any{"channel": "EMAIL", "target": email})
	r := e.call("POST", "/users:signInWithCode", "", device, map[string]any{"kind": "EMAIL", "credential": e.credential(email)})
	if r.status != 200 || str(r.body, "access_token") == "" || r.header.Get("Cache-Control") != "no-store" {
		e.t.Fatalf("signInWithCode %d %v", r.status, r.body)
	}
	return r.body
}

func str(m map[string]any, k string) string { s, _ := m[k].(string); return s }

// lastSegment 取资源名（如 users/u_…/sessions/s_…）末段，作为路径参数。
func lastSegment(t *testing.T, name, collection string) string {
	t.Helper()
	i := strings.LastIndex(name, "/"+collection+"/")
	if i < 0 {
		t.Fatalf("resource name %q is not in %s", name, collection)
	}
	return name[i+len(collection)+2:]
}

func TestHTTPFlows(t *testing.T) {
	// 在顶层跳过：缺少数据库时整个必需测试显示为 SKIP，不会以全部子测试跳过的 PASS 通过门禁。
	dsn := os.Getenv("SERVER_TEST_DB_DSN")
	if dsn == "" {
		t.Skip("SERVER_TEST_DB_DSN not set")
	}
	t.Run("first_sign_in", func(t *testing.T) {
		e := newEnv(t, dsn, 0)
		pair := e.signIn("first@example.test", "device-a")
		if pair["is_new_user"] != true || str(pair, "scope") != "user" || str(pair, "token_type") != "Bearer" || str(pair, "refresh_token") == "" {
			t.Fatalf("pair %v", pair)
		}
		me := e.call("GET", "/users/me", str(pair, "access_token"), "", nil)
		if me.status != 200 || str(me.body, "state") != "ACTIVE" || str(me.body, "name") != "users/"+str(pair, "user_id") {
			t.Fatalf("me %d %v", me.status, me.body)
		}
	})

	t.Run("pending_completion", func(t *testing.T) {
		e := newEnv(t, dsn, 0)
		pending := e.call("POST", "/users:signInWithIdp", "", "device-a", map[string]any{"kind": "WECHAT", "credential": map[string]any{"app_id": "flows-wechat", "code": "union-1"}})
		if pending.status != 200 || pending.body["access_token"] != nil || str(pending.body, "next_action") != "COMPLETE_IDENTITY" || pending.header.Get("Cache-Control") != "no-store" {
			t.Fatalf("pending %d %v", pending.status, pending.body)
		}
		ticket := str(pending.body, "sign_in_completion_token")
		if ticket == "" || str(pending.body, "expire_time") == "" {
			t.Fatalf("pending ticket %v", pending.body)
		}
		e.sendCode("/users:sendSignInCompletionCode", "", map[string]any{"sign_in_completion_token": ticket, "channel": "EMAIL", "target": "pending@example.test"})
		done := e.call("POST", "/users:completeSignIn", "", "device-a", map[string]any{"sign_in_completion_token": ticket, "kind": "EMAIL", "credential": e.credential("pending@example.test")})
		if done.status != 200 || str(done.body, "access_token") == "" || str(done.body, "scope") != "user" {
			t.Fatalf("complete %d %v", done.status, done.body)
		}
		// 票据只能完成一次登录：原期限内重放相同的完成请求（例如响应丢失后重试）只读回同一 pair，
		// 不新建会话；该 pair 的 refresh 一经轮换，再重放即为 400 SIGN_IN_ALREADY_COMPLETED。
		complete := map[string]any{"sign_in_completion_token": ticket, "kind": "EMAIL", "credential": e.credential("pending@example.test")}
		again := e.call("POST", "/users:completeSignIn", "", "device-a", complete)
		if again.status != 200 || str(again.body, "access_token") != str(done.body, "access_token") || str(again.body, "refresh_token") != str(done.body, "refresh_token") {
			t.Fatalf("identical completion replay must return the original pair: %d", again.status)
		}
		if r := e.call("POST", "/token", "", "", map[string]any{"grant_type": "refresh_token", "refresh_token": str(done.body, "refresh_token")}); r.status != 200 {
			t.Fatalf("refresh after completion %d %v", r.status, r.body)
		}
		replay := e.call("POST", "/users:completeSignIn", "", "device-a", complete)
		if errBody, _ := replay.body["error"].(map[string]any); replay.status != 400 || str(errBody, "reason") != "SIGN_IN_ALREADY_COMPLETED" {
			t.Fatalf("completion replay after refresh rotation %d %v", replay.status, replay.body)
		}
	})

	t.Run("refresh_coordination", func(t *testing.T) {
		e := newEnv(t, dsn, 0)
		pair := e.signIn("refresh@example.test", "device-a")
		old := str(pair, "refresh_token")
		// 多个请求同时发现 access 过期：所有并发刷新都拿到同一 pair。
		var wg sync.WaitGroup
		results := make([]reply, 4)
		for i := range results {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				results[i] = e.call("POST", "/token", "", "", map[string]any{"grant_type": "refresh_token", "refresh_token": old})
			}(i)
		}
		wg.Wait()
		for i, r := range results {
			if r.status != 200 || str(r.body, "access_token") == "" || str(r.body, "access_token") != str(results[0].body, "access_token") || str(r.body, "refresh_token") != str(results[0].body, "refresh_token") {
				t.Fatalf("concurrent refresh %d: %d %v", i, r.status, r.body)
			}
			if r.header.Get("Cache-Control") != "no-store" {
				t.Fatalf("token response %d must be no-store", i)
			}
		}
		if str(results[0].body, "refresh_token") == old {
			t.Fatal("refresh must rotate the refresh token")
		}
	})

	t.Run("refresh_replay_outside_grace", func(t *testing.T) {
		const grace = time.Second
		e := newEnv(t, dsn, grace)
		pair := e.signIn("replay@example.test", "device-a")
		old := str(pair, "refresh_token")
		fresh := e.call("POST", "/token", "", "", map[string]any{"grant_type": "refresh_token", "refresh_token": old})
		if fresh.status != 200 || str(fresh.body, "access_token") == "" {
			t.Fatalf("refresh %d %v", fresh.status, fresh.body)
		}
		// 宽限按应用时钟与会话轮换时间判断，需要真实越过宽限。
		time.Sleep(grace + 300*time.Millisecond)
		replay := e.call("POST", "/token", "", "", map[string]any{"grant_type": "refresh_token", "refresh_token": old})
		if replay.status != 400 || str(replay.body, "error") != "invalid_grant" {
			t.Fatalf("replay outside grace %d %v", replay.status, replay.body)
		}
		// 重放视为泄露信号：整个会话被吊销，刚换到的 pair 也失效，客户端必须退出登录。
		me := e.call("GET", "/users/me", str(fresh.body, "access_token"), "", nil)
		if me.status != 401 || me.header.Get("WWW-Authenticate") == "" {
			t.Fatalf("session must be revoked after replay: %d", me.status)
		}
		if r := e.call("POST", "/token", "", "", map[string]any{"grant_type": "refresh_token", "refresh_token": str(fresh.body, "refresh_token")}); r.status != 400 || str(r.body, "error") != "invalid_grant" {
			t.Fatalf("new refresh must be rejected after replay: %d %v", r.status, r.body)
		}
	})

	t.Run("reauthentication_keeps_refresh", func(t *testing.T) {
		e := newEnv(t, dsn, 0)
		const email = "reauth@example.test"
		pair := e.signIn(email, "device-a")
		access, refresh := str(pair, "access_token"), str(pair, "refresh_token")
		e.sendCode("/users/me:sendReauthenticationCode", access, map[string]any{"channel": "EMAIL", "target": email})
		re := e.call("POST", "/users/me:reauthenticate", access, "", map[string]any{"kind": "EMAIL", "credential": e.credential(email)})
		if re.status != 200 || str(re.body, "access_token") == "" || re.body["refresh_token"] != nil {
			t.Fatalf("reauthenticate must return a new access only: %d %v", re.status, re.body)
		}
		if r := e.call("GET", "/users/me", str(re.body, "access_token"), "", nil); r.status != 200 {
			t.Fatalf("new access must work: %d %v", r.status, r.body)
		}
		// 客户端保存新 access、保留原 refresh；原 refresh 仍可刷新。
		if r := e.call("POST", "/token", "", "", map[string]any{"grant_type": "refresh_token", "refresh_token": refresh}); r.status != 200 || str(r.body, "refresh_token") == "" {
			t.Fatalf("original refresh must still work: %d %v", r.status, r.body)
		}
	})

	t.Run("revoke_others_and_delete_session", func(t *testing.T) {
		e := newEnv(t, dsn, 0)
		const email = "sessions@example.test"
		a := e.signIn(email, "device-a")
		b := e.signIn(email, "device-b")
		c := e.signIn(email, "device-c")
		list := e.call("GET", "/users/me/sessions", str(a, "access_token"), "", nil)
		sessions, _ := list.body["sessions"].([]any)
		if list.status != 200 || len(sessions) != 3 {
			t.Fatalf("sessions %d %v", list.status, list.body)
		}
		var otherName string
		for _, s := range sessions {
			m := s.(map[string]any)
			if m["is_current"] != true {
				otherName = str(m, "name")
				break
			}
		}
		// 单个撤销：按 name 末段 s_ ID 调用。
		id := lastSegment(t, otherName, "sessions")
		if r := e.call("DELETE", "/users/me/sessions/"+id, str(a, "access_token"), "", nil); r.status != 204 {
			t.Fatalf("delete session %d %v", r.status, r.body)
		}
		if r := e.call("POST", "/users/me/sessions:revokeOthers", str(a, "access_token"), "", nil); r.status != 200 {
			t.Fatalf("revokeOthers %d %v", r.status, r.body)
		}
		for _, other := range []map[string]any{b, c} {
			if r := e.call("GET", "/users/me", str(other, "access_token"), "", nil); r.status != 401 || r.header.Get("WWW-Authenticate") == "" {
				t.Fatalf("other device must be signed out: %d", r.status)
			}
			if r := e.call("POST", "/token", "", "", map[string]any{"grant_type": "refresh_token", "refresh_token": str(other, "refresh_token")}); r.status != 400 || str(r.body, "error") != "invalid_grant" {
				t.Fatalf("other device refresh must fail: %d %v", r.status, r.body)
			}
		}
		if r := e.call("GET", "/users/me", str(a, "access_token"), "", nil); r.status != 200 {
			t.Fatalf("current device must survive revokeOthers: %d", r.status)
		}
	})

	t.Run("unbind_source_logs_out", func(t *testing.T) {
		e := newEnv(t, dsn, 0)
		const first, second = "source@example.test", "second@example.test"
		pair := e.signIn(first, "device-a")
		access := str(pair, "access_token")
		e.sendCode("/users/me:sendBindCode", access, map[string]any{"channel": "EMAIL", "target": second})
		if r := e.call("POST", "/users/me/identities", access, "", map[string]any{"kind": "EMAIL", "credential": e.credential(second)}); r.status != 201 {
			t.Fatalf("bind %d %v", r.status, r.body)
		}
		current := e.call("GET", "/users/me/sessions/current", access, "", nil)
		sources, _ := current.body["authentication_identities"].([]any)
		if current.status != 200 || len(sources) != 1 {
			t.Fatalf("current session %d %v", current.status, current.body)
		}
		id := lastSegment(t, str(sources[0].(map[string]any), "name"), "identities")
		if r := e.call("DELETE", "/users/me/identities/"+id, access, "", nil); r.status != 204 {
			t.Fatalf("unbind %d %v", r.status, r.body)
		}
		// 解绑了当前会话的登录来源：成功即退出，客户端清理凭据并重新登录。
		if r := e.call("GET", "/users/me", access, "", nil); r.status != 401 || r.header.Get("WWW-Authenticate") == "" {
			t.Fatalf("session must be revoked after unbinding its source: %d", r.status)
		}
		if r := e.call("POST", "/token", "", "", map[string]any{"grant_type": "refresh_token", "refresh_token": str(pair, "refresh_token")}); r.status != 400 || str(r.body, "error") != "invalid_grant" {
			t.Fatalf("refresh must fail after unbinding the source: %d %v", r.status, r.body)
		}
	})
}
