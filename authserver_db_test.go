package authserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	authserver "github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/anonymize"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/maintenance"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/sender"
)

func dbDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("SERVER_TEST_DB_DSN")
	if dsn == "" {
		t.Skip("SERVER_TEST_DB_DSN not set")
	}
	return dsn
}

func TestMigrateStartCloseAgainstRealDB(t *testing.T) {
	dsn := dbDSN(t)
	schema := fmt.Sprintf("authtest_%08x", rand.Uint32())
	ctx := context.Background()
	poolCfg, err := authserver.PoolConfig(dsn, schema)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE") })

	cfg := minimal()
	cfg.Schema = schema
	cfg.MaintenanceInterval = time.Second
	a, err := authserver.New(cfg, authserver.Deps{Pool: pool, Redis: testRedis(t), SMSSender: sender.NewLog(nil), EmailSender: sender.NewLog(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_tables WHERE schemaname = $1 AND tablename = 'user_account'`, schema).Scan(&n); err != nil || n != 1 {
		t.Fatalf("user_account not created in %s: n=%d err=%v", schema, n, err)
	}
	// 非限定表名经 search_path 解析到本 schema
	if _, err := pool.Exec(ctx, `SELECT 1 FROM user_account LIMIT 1`); err != nil {
		t.Fatalf("unqualified table name must resolve via search_path: %v", err)
	}
	a.Start(ctx)
	time.Sleep(1500 * time.Millisecond) // 至少一轮维护（purge_users / cleanup_sessions 对空表为 no-op；验证不 panic、锁能拿到并释放）
	a.Close()
	// Close 后我们自己的 advisory lock 必须已释放：能重新拿到并释放它
	locker := maintenance.NewPGLocker(pool, schema)
	acquired, release, err := locker.TryLock(ctx)
	if err != nil {
		t.Fatalf("try lock after close: %v", err)
	}
	if !acquired {
		t.Fatal("advisory lock still held after Close")
	}
	release()

	// 领域层装配冒烟：通过 Auth.Users() 完成一次发码 + 登录（Log 发送器不返回码，只验证不报错的路径）
	// 此时 Auth 已 Close：产生的 CODE_SENT 事件会被审计记录器丢弃并计数（Warn 日志），不影响返回值。
	if err := a.Users().SendSignInCode(ctx, enum.IdentityPhone, "+8613812341234", user.Meta{IP: "127.0.0.1"}); err != nil {
		t.Fatalf("SendSignInCode via Auth: %v", err)
	}
}

func TestAuditEventsPersistedAndExpiredEndToEnd(t *testing.T) {
	dsn := dbDSN(t)
	ctx := context.Background()
	schema := fmt.Sprintf("authtest_%08x", rand.Uint32())
	poolCfg, err := authserver.PoolConfig(dsn, schema)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		pool.Close()
	})
	captured := &captureSender{}
	cfg := minimal()
	cfg.Schema = schema
	a, err := authserver.New(cfg, authserver.Deps{Pool: pool, Redis: testRedis(t), SMSSender: captured, EmailSender: captured})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	a.Start(ctx)
	r := chi.NewRouter()
	r.Mount("/v1", a.ConsumerHandler())
	srv := httptest.NewServer(r)
	defer srv.Close()

	post := func(path string, body any, hdr map[string]string) (*http.Response, map[string]any) {
		t.Helper()
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", srv.URL+path, bytes.NewReader(b))
		req.Header.Set("X-Request-Id", "e2e-audit-1")
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp, out
	}
	const phone = "+8613812340077"
	if resp, _ := post("/v1/users:sendSignInCode", map[string]string{"channel": "PHONE", "target": phone}, nil); resp.StatusCode != 200 {
		t.Fatalf("sendSignInCode: %d", resp.StatusCode)
	}
	resp, tok := post("/v1/users:signInWithCode", map[string]any{"phone": map[string]string{"target": phone, "code": captured.code(phone)}}, map[string]string{"X-Device-Id": "e2e-audit-dev"})
	if resp.StatusCode != 200 {
		t.Fatalf("signIn: %d %v", resp.StatusCode, tok)
	}
	// 错码一次 → SIGN_IN_FAILED
	if resp, _ := post("/v1/users:signInWithCode", map[string]any{"phone": map[string]string{"target": phone, "code": "000000"}}, map[string]string{"X-Device-Id": "e2e-audit-dev"}); resp.StatusCode != 400 {
		t.Fatalf("wrong code: %d", resp.StatusCode)
	}
	a.Close() // 刷出队列

	type row struct {
		typ     int16
		userID  *string
		reqID   *string
		ip      *string
		device  *string
		hintLen *int
	}
	rows, err := pool.Query(ctx, `SELECT event_type, user_id, request_id, host(ip), device_id, length(subject_hint) FROM audit_event ORDER BY occur_time, id`)
	if err != nil {
		t.Fatal(err)
	}
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.typ, &r.userID, &r.reqID, &r.ip, &r.device, &r.hintLen); err != nil {
			t.Fatal(err)
		}
		got = append(got, r)
	}
	rows.Close()
	want := map[int16]bool{int16(enum.EventCodeSent): false, int16(enum.EventSignIn): false, int16(enum.EventSignInFailed): false}
	for _, r := range got {
		if _, ok := want[r.typ]; ok {
			want[r.typ] = true
		}
		if r.reqID == nil || *r.reqID != "e2e-audit-1" || r.ip == nil || *r.ip != "127.0.0.1" {
			t.Fatalf("every event must carry request_id and ip: %+v", r)
		}
		if r.typ == int16(enum.EventSignIn) && (r.userID == nil || *r.userID != tok["user_id"]) {
			t.Fatalf("SIGN_IN must carry user_id: %+v", r)
		}
		if r.typ == int16(enum.EventSignIn) || r.typ == int16(enum.EventSignInFailed) {
			// 错码在找到账号之前就被拒绝，所以 SIGN_IN_FAILED 没有 user_id，但设备与 8 位 hint 都在
			if r.device == nil || *r.device != "e2e-audit-dev" || r.hintLen == nil || *r.hintLen != 8 {
				t.Fatalf("sign-in events must carry device_id and an 8-char hint: %+v", r)
			}
		}
	}
	for typ, seen := range want {
		if !seen {
			t.Fatalf("event type %d not persisted; got %+v", typ, got)
		}
	}
	// 保留期：把所有事件拨到 200 天前，跑一轮维护（AuditRetentionDays 默认 180）→ 全部删除
	if _, err := pool.Exec(ctx, `UPDATE audit_event SET occur_time = occur_time - interval '200 days'`); err != nil {
		t.Fatal(err)
	}
	if !a.RunMaintenanceOnce(ctx) {
		t.Fatal("maintenance round did not acquire the lock")
	}
	var left int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_event`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("expired events must be deleted: left=%d err=%v", left, err)
	}
}

// captureSender 记录最后一次发往每个 target 的验证码（仅测试）。
type captureSender struct {
	mu    sync.Mutex
	codes map[string]string
}

func (c *captureSender) SendSMS(_ context.Context, to string, m sender.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.codes == nil {
		c.codes = map[string]string{}
	}
	c.codes[to] = m.Code
	return nil
}
func (c *captureSender) SendEmail(ctx context.Context, to string, m sender.Message) error {
	return c.SendSMS(ctx, to, m)
}
func (c *captureSender) code(to string) string { c.mu.Lock(); defer c.mu.Unlock(); return c.codes[to] }

func TestConsumerEndToEndAgainstRealDB(t *testing.T) {
	dsn := dbDSN(t)
	schema := fmt.Sprintf("authtest_%08x", rand.Uint32())
	ctx := context.Background()
	poolCfg, err := authserver.PoolConfig(dsn, schema)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE") })

	captured := &captureSender{}
	cfg := minimal()
	cfg.Schema = schema
	a, err := authserver.New(cfg, authserver.Deps{Pool: pool, Redis: testRedis(t), SMSSender: captured, EmailSender: captured})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	r.Mount("/v1", a.ConsumerHandler())
	srv := httptest.NewServer(r)
	defer srv.Close()

	post := func(path, bearer string, body any, hdr map[string]string) (*http.Response, map[string]any) {
		t.Helper()
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		req, _ := http.NewRequest("POST", srv.URL+path, rd)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp, out
	}
	dev := map[string]string{"X-Device-Id": "e2e-device-1", "X-Device-Name": "E2E"}
	const phone = "+8613812341234"

	resp, _ := post("/v1/users:sendSignInCode", "", map[string]string{"channel": "PHONE", "target": phone}, nil)
	if resp.StatusCode != 200 || captured.code(phone) == "" {
		t.Fatalf("sendSignInCode: %d", resp.StatusCode)
	}
	resp, tok := post("/v1/users:signInWithCode", "", map[string]any{"phone": map[string]string{"target": phone, "code": captured.code(phone)}}, dev)
	if resp.StatusCode != 200 || tok["is_new_user"] != true || tok["scope"] != "user" {
		t.Fatalf("signIn: %d %v", resp.StatusCode, tok)
	}
	access, refresh := tok["access_token"].(string), tok["refresh_token"].(string)

	get := func(path, bearer string) (*http.Response, map[string]any) {
		req, _ := http.NewRequest("GET", srv.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+bearer)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp, out
	}
	resp, me := get("/v1/users/me", access)
	if resp.StatusCode != 200 || me["state"] != "ACTIVE" || me["name"] != "users/"+tok["user_id"].(string) {
		t.Fatalf("me: %d %v", resp.StatusCode, me)
	}
	resp, sessions := get("/v1/users/me/sessions", access)
	if resp.StatusCode != 200 || len(sessions["sessions"].([]any)) != 1 {
		t.Fatalf("sessions: %d %v", resp.StatusCode, sessions)
	}
	resp, tok2 := post("/v1/token", "", map[string]string{"grant_type": "refresh_token", "refresh_token": refresh}, nil)
	if resp.StatusCode != 200 || tok2["refresh_token"] == refresh || tok2["access_token"] == "" {
		t.Fatalf("refresh: %d %v", resp.StatusCode, tok2)
	}
	// 宽限内重放旧 refresh（RefreshGrace 默认 30s）→ 返回同一 pair
	resp, again := post("/v1/token", "", map[string]string{"grant_type": "refresh_token", "refresh_token": refresh}, nil)
	if resp.StatusCode != 200 || again["access_token"] != tok2["access_token"] {
		t.Fatalf("grace replay must return the same pair: %d", resp.StatusCode)
	}
	resp, _ = post("/v1/revoke", "", map[string]string{"token": tok2["refresh_token"].(string)}, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("revoke: %d", resp.StatusCode)
	}
	// 吊销后：新 access 立即 401；旧 refresh 也拒绝
	resp, body := get("/v1/users/me", tok2["access_token"].(string))
	if resp.StatusCode != 401 || resp.Header.Get("WWW-Authenticate") == "" {
		t.Fatalf("revoked access must 401: %d %v", resp.StatusCode, body)
	}
	resp, oerr := post("/v1/token", "", map[string]string{"grant_type": "refresh_token", "refresh_token": tok2["refresh_token"].(string)}, nil)
	if resp.StatusCode != 400 || oerr["error"] != "invalid_grant" {
		t.Fatalf("revoked refresh: %d %v", resp.StatusCode, oerr)
	}
	// 错码是 400，不是 401
	resp, aip := post("/v1/users:signInWithCode", "", map[string]any{"phone": map[string]string{"target": phone, "code": "000000"}}, dev)
	if resp.StatusCode != 400 || aip["error"].(map[string]any)["reason"] != "CODE_EXPIRED" { // 码已被消费 → 无有效码
		t.Fatalf("wrong code after consume: %d %v", resp.StatusCode, aip)
	}
}

func TestMigrateRejectsPoolWithoutSchemaOnSearchPath(t *testing.T) {
	dsn := dbDSN(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn) // 默认 search_path = "$user", public
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	cfg := minimal()
	cfg.Schema = "auth_nowhere"
	a, _ := authserver.New(cfg, authserver.Deps{Pool: pool, Redis: testRedis(t), SMSSender: sender.NewLog(nil), EmailSender: sender.NewLog(nil)})
	err = a.Migrate(ctx)
	if !errors.Is(err, authserver.ErrSearchPath) {
		t.Fatalf("err = %v, want ErrSearchPath", err)
	}
}

func TestWeChatSignInEndToEndAgainstRealDB(t *testing.T) {
	dsn := dbDSN(t)
	ctx := context.Background()
	schema := fmt.Sprintf("authtest_%08x", rand.Uint32())
	poolCfg, err := authserver.PoolConfig(dsn, schema)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		pool.Close()
	})
	// 假微信：任何 appid、code "ok-<unionid>" → 成功；其它 → 40029
	wx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		w.Header().Set("Content-Type", "application/json")
		if u, ok := strings.CutPrefix(code, "ok-"); ok {
			fmt.Fprintf(w, `{"access_token":"AT","openid":"o-%s","unionid":"%s"}`, r.URL.Query().Get("appid"), u)
			return
		}
		_, _ = w.Write([]byte(`{"errcode":40029,"errmsg":"invalid code"}`))
	}))
	defer wx.Close()

	cfg := minimal()
	cfg.Schema = schema
	cfg.WeChatApps = []authserver.WeChatApp{{AppID: "wx1", Secret: "s1"}, {AppID: "wx2", Secret: "s2"}}
	cfg.WeChatAPIBaseURL = wx.URL
	a, err := authserver.New(cfg, authserver.Deps{Pool: pool, Redis: testRedis(t), SMSSender: sender.NewLog(nil), EmailSender: sender.NewLog(nil), HTTPClient: wx.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	r.Mount("/v1", a.ConsumerHandler())
	srv := httptest.NewServer(r)
	defer srv.Close()

	signIn := func(appID, code, device string) (*http.Response, map[string]any) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"wechat": map[string]string{"app_id": appID, "code": code}})
		req, _ := http.NewRequest("POST", srv.URL+"/v1/users:signInWithIdp", bytes.NewReader(body))
		req.Header.Set("X-Device-Id", device)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp, out
	}
	resp, tok := signIn("wx1", "ok-UNION-1", "dev-a")
	if resp.StatusCode != 200 || tok["is_new_user"] != true || tok["scope"] != "user:bind" {
		t.Fatalf("first: %d %v", resp.StatusCode, tok)
	}
	if _, has := tok["hint_email"]; has {
		t.Fatal("wechat has no hint_email")
	}
	// user:bind 只能读 users/me，不能改
	get := func(path, bearer string) int {
		req, _ := http.NewRequest("GET", srv.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+bearer)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if get("/v1/users/me", tok["access_token"].(string)) != 200 {
		t.Fatal("user:bind can read me")
	}
	if get("/v1/users/me/sessions", tok["access_token"].(string)) != 403 {
		t.Fatal("user:bind must not list sessions")
	}
	// 同 UnionID 从 wx2 登录 → 同一用户、非新用户
	resp, tok2 := signIn("wx2", "ok-UNION-1", "dev-b")
	if resp.StatusCode != 200 || tok2["is_new_user"] != false || tok2["user_id"] != tok["user_id"] {
		t.Fatalf("second app: %d %v", resp.StatusCode, tok2)
	}
	// 错误 code → 400 IDP_CREDENTIAL_INVALID
	resp, aip := signIn("wx1", "bad", "dev-a")
	if resp.StatusCode != 400 || aip["error"].(map[string]any)["reason"] != "IDP_CREDENTIAL_INVALID" {
		t.Fatalf("bad code: %d %v", resp.StatusCode, aip)
	}
	// 未配置的 app → 400 IDP_APP_NOT_ALLOWED
	resp, aip = signIn("wx9", "ok-UNION-1", "dev-a")
	if resp.StatusCode != 400 || aip["error"].(map[string]any)["reason"] != "IDP_APP_NOT_ALLOWED" {
		t.Fatalf("unknown app: %d %v", resp.StatusCode, aip)
	}
}

func TestIdentityBindingEndToEndAgainstRealDB(t *testing.T) {
	dsn := dbDSN(t)
	ctx := context.Background()
	schema := fmt.Sprintf("authtest_%08x", rand.Uint32())
	poolCfg, err := authserver.PoolConfig(dsn, schema)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		pool.Close()
	})
	wx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		w.Header().Set("Content-Type", "application/json")
		if u, ok := strings.CutPrefix(code, "ok-"); ok {
			fmt.Fprintf(w, `{"access_token":"AT","openid":"o-%s","unionid":"%s"}`, r.URL.Query().Get("appid"), u)
			return
		}
		_, _ = w.Write([]byte(`{"errcode":40029,"errmsg":"invalid code"}`))
	}))
	defer wx.Close()
	captured := &captureSender{}
	cfg := minimal()
	cfg.Schema = schema
	cfg.WeChatApps = []authserver.WeChatApp{{AppID: "wx1", Secret: "s1"}}
	cfg.WeChatAPIBaseURL = wx.URL
	cfg.ReauthMaxAge = time.Minute
	cfg.CodeCooldown = time.Second // 端到端需对同一 target 二次发码（见第 7 步）
	// 直接构造 miniredis 而非用 testRedis(t)：miniredis 的 TTL 是虚拟倒计时，只有
	// FastForward 会推进它，真实 time.Sleep 不会让 Redis 里的 cooldown key 过期。
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	a, err := authserver.New(cfg, authserver.Deps{Pool: pool, Redis: rdb, SMSSender: captured, EmailSender: captured, HTTPClient: wx.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	r.Mount("/v1", a.ConsumerHandler())
	srv := httptest.NewServer(r)
	defer srv.Close()

	call := func(method, path, bearer string, body any, hdr map[string]string) (*http.Response, map[string]any) {
		t.Helper()
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, srv.URL+path, rd)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp, out
	}
	dev := map[string]string{"X-Device-Id": "e2e-bind-1"}
	const phone = "+8613812340001"
	const mail = "bind@shifang.co"

	// 1. 微信登录 → user:bind
	resp, tok := call("POST", "/v1/users:signInWithIdp", "", map[string]any{"wechat": map[string]string{"app_id": "wx1", "code": "ok-UNION-B"}}, dev)
	if resp.StatusCode != 200 || tok["scope"] != "user:bind" {
		t.Fatalf("wechat sign-in: %d %v", resp.StatusCode, tok)
	}
	access, refresh := tok["access_token"].(string), tok["refresh_token"].(string)
	// user:bind 不能列会话，但能列身份
	if resp, _ = call("GET", "/v1/users/me/sessions", access, nil, nil); resp.StatusCode != 403 {
		t.Fatalf("sessions with user:bind: %d", resp.StatusCode)
	}
	resp, list := call("GET", "/v1/users/me/identities", access, nil, nil)
	if resp.StatusCode != 200 || len(list["identities"].([]any)) != 1 {
		t.Fatalf("identities: %d %v", resp.StatusCode, list)
	}
	// 2. 发绑定码并绑定手机 → 201
	if resp, _ = call("POST", "/v1/users/me:sendBindCode", access, map[string]string{"channel": "PHONE", "target": phone}, nil); resp.StatusCode != 200 || captured.code(phone) == "" {
		t.Fatalf("sendBindCode: %d", resp.StatusCode)
	}
	resp, ident := call("POST", "/v1/users/me/identities", access, map[string]any{"phone": map[string]string{"target": phone, "code": captured.code(phone)}}, nil)
	if resp.StatusCode != 201 || ident["kind"] != "PHONE" || ident["masked_subject"] != "+86 138****0001" {
		t.Fatalf("bind phone: %d %v", resp.StatusCode, ident)
	}
	phoneName := ident["name"].(string)
	// 3. 刷新后 scope 变为 user
	resp, tok2 := call("POST", "/v1/token", "", map[string]string{"grant_type": "refresh_token", "refresh_token": refresh}, nil)
	if resp.StatusCode != 200 || tok2["scope"] != "user" {
		t.Fatalf("refresh after bind: %d %v", resp.StatusCode, tok2)
	}
	access = tok2["access_token"].(string)
	// 4. 绑邮箱（用户已 user）→ 201；再绑同一邮箱 → 200 幂等
	if resp, _ = call("POST", "/v1/users/me:sendBindCode", access, map[string]string{"channel": "EMAIL", "target": mail}, nil); resp.StatusCode != 200 {
		t.Fatalf("sendBindCode email: %d", resp.StatusCode)
	}
	if resp, _ = call("POST", "/v1/users/me/identities", access, map[string]any{"email": map[string]string{"target": mail, "code": captured.code(mail)}}, nil); resp.StatusCode != 201 {
		t.Fatalf("bind email: %d", resp.StatusCode)
	}
	// 冷却期内再发同一邮箱会被限流；这里直接用第二个手机号验证 kind 上限
	if resp, _ = call("POST", "/v1/users/me:sendBindCode", access, map[string]string{"channel": "PHONE", "target": "+8613812340002"}, nil); resp.StatusCode != 200 {
		t.Fatalf("sendBindCode phone2: %d", resp.StatusCode)
	}
	resp, aip := call("POST", "/v1/users/me/identities", access, map[string]any{"phone": map[string]string{"target": "+8613812340002", "code": captured.code("+8613812340002")}}, nil)
	if resp.StatusCode != 409 || aip["error"].(map[string]any)["reason"] != "IDENTITY_KIND_LIMIT" {
		t.Fatalf("kind limit: %d %v", resp.StatusCode, aip)
	}
	// 5. 解绑手机（邮箱仍是锚点）→ 204；近期认证在 ReauthMaxAge=1m 内
	phoneID := phoneName[strings.LastIndex(phoneName, "/")+1:]
	if resp, _ = call("DELETE", "/v1/users/me/identities/"+phoneID, access, nil, nil); resp.StatusCode != 204 {
		t.Fatalf("unbind phone: %d", resp.StatusCode)
	}
	// 6. 解绑邮箱 → 最后锚点 → 400 FAILED_PRECONDITION
	resp, list = call("GET", "/v1/users/me/identities", access, nil, nil)
	var emailID string
	for _, it := range list["identities"].([]any) {
		m := it.(map[string]any)
		if m["kind"] == "EMAIL" {
			n := m["name"].(string)
			emailID = n[strings.LastIndex(n, "/")+1:]
		}
	}
	resp, aip = call("DELETE", "/v1/users/me/identities/"+emailID, access, nil, nil)
	if resp.StatusCode != 400 || aip["error"].(map[string]any)["reason"] != "LAST_ANCHOR_IDENTITY" {
		t.Fatalf("last anchor: %d %v", resp.StatusCode, aip)
	}
	// 7. 解绑后的手机号可被另一账号绑定；随后原账号再绑同一号 → 409（属于他人）。
	//    同一 target 二次发码要跨过 CodeCooldown（本测试设为 1s）；miniredis 的 TTL 是虚拟
	//    倒计时、不随真实时钟流逝而过期，所以用 FastForward 推进它，而不是 time.Sleep。
	resp, tokC := call("POST", "/v1/users:signInWithIdp", "", map[string]any{"wechat": map[string]string{"app_id": "wx1", "code": "ok-UNION-C"}}, map[string]string{"X-Device-Id": "e2e-bind-2"})
	if resp.StatusCode != 200 {
		t.Fatalf("second account: %d", resp.StatusCode)
	}
	accessC := tokC["access_token"].(string)
	mr.FastForward(2 * time.Second)
	if resp, _ = call("POST", "/v1/users/me:sendBindCode", accessC, map[string]string{"channel": "PHONE", "target": phone}, nil); resp.StatusCode != 200 {
		t.Fatalf("C sendBindCode: %d", resp.StatusCode)
	}
	if resp, _ = call("POST", "/v1/users/me/identities", accessC, map[string]any{"phone": map[string]string{"target": phone, "code": captured.code(phone)}}, nil); resp.StatusCode != 201 {
		t.Fatalf("C binds the released phone: %d", resp.StatusCode)
	}
	mr.FastForward(2 * time.Second)
	if resp, _ = call("POST", "/v1/users/me:sendBindCode", access, map[string]string{"channel": "PHONE", "target": phone}, nil); resp.StatusCode != 200 {
		t.Fatalf("A sendBindCode: %d", resp.StatusCode)
	}
	resp, aip = call("POST", "/v1/users/me/identities", access, map[string]any{"phone": map[string]string{"target": phone, "code": captured.code(phone)}}, nil)
	if resp.StatusCode != 409 || aip["error"].(map[string]any)["reason"] != "IDENTITY_ALREADY_BOUND" {
		t.Fatalf("conflict: %d %v", resp.StatusCode, aip)
	}
}

func TestKeyRotationBackfillEndToEnd(t *testing.T) {
	dsn := dbDSN(t)
	ctx := context.Background()
	schema := fmt.Sprintf("authtest_%08x", rand.Uint32())
	poolCfg, err := authserver.PoolConfig(dsn, schema)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		pool.Close()
	})
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	captured := &captureSender{}
	const phone = "+8613812340055"

	// build 用给定密钥版本构造并迁移一个 Auth（同一 schema）；返回 Auth 与一个登录助手。
	build := func(hmacKeys, cipherKeys map[uint16][]byte, active uint16) (*authserver.Auth, func(dev string) map[string]any, error) {
		cfg := minimal()
		cfg.Schema = schema
		cfg.CodeCooldown = time.Second
		cfg.SubjectHMACKeys, cfg.SubjectHMACActiveKey = hmacKeys, active
		cfg.SubjectCipherKeys, cfg.SubjectCipherActiveKey = cipherKeys, active
		a, err := authserver.New(cfg, authserver.Deps{Pool: pool, Redis: rdb, SMSSender: captured, EmailSender: captured})
		if err != nil {
			t.Fatal(err)
		}
		merr := a.Migrate(ctx)
		r := chi.NewRouter()
		r.Mount("/v1", a.ConsumerHandler())
		srv := httptest.NewServer(r)
		t.Cleanup(srv.Close)
		signIn := func(dev string) map[string]any {
			t.Helper()
			mr.FastForward(2 * time.Second)
			post := func(path string, body any, hdr map[string]string) (*http.Response, map[string]any) {
				b, _ := json.Marshal(body)
				req, _ := http.NewRequest("POST", srv.URL+path, bytes.NewReader(b))
				for k, v := range hdr {
					req.Header.Set(k, v)
				}
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				var out map[string]any
				_ = json.NewDecoder(resp.Body).Decode(&out)
				return resp, out
			}
			if resp, _ := post("/v1/users:sendSignInCode", map[string]string{"channel": "PHONE", "target": phone}, nil); resp.StatusCode != 200 {
				t.Fatalf("sendSignInCode: %d", resp.StatusCode)
			}
			resp, tok := post("/v1/users:signInWithCode", map[string]any{"phone": map[string]string{"target": phone, "code": captured.code(phone)}}, map[string]string{"X-Device-Id": dev})
			if resp.StatusCode != 200 {
				t.Fatalf("signInWithCode: %d %v", resp.StatusCode, tok)
			}
			return tok
		}
		return a, signIn, merr
	}
	versions := func() (dv, cv []int16) {
		rows, err := pool.Query(ctx, `SELECT digest_key_version, cipher_key_version FROM identity WHERE delete_time IS NULL AND subject_digest IS NOT NULL`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var d, c int16
			if err := rows.Scan(&d, &c); err != nil {
				t.Fatal(err)
			}
			dv, cv = append(dv, d), append(cv, c)
		}
		return dv, cv
	}

	// 1. 只有 1 号密钥：注册
	a1, signIn1, err := build(map[uint16][]byte{1: k(2)}, map[uint16][]byte{1: k(3)}, 1)
	if err != nil {
		t.Fatalf("migrate v1: %v", err)
	}
	tok1 := signIn1("rot-dev-1")
	a1.Close()
	if dv, cv := versions(); len(dv) != 1 || dv[0] != 1 || cv[0] != 1 {
		t.Fatalf("after first sign-in: digest=%v cipher=%v", dv, cv)
	}
	// 2. 加入 2 号并切 active：Migrate 通过；旧行仍可登录到同一账号；一轮维护完成回填
	a2, signIn2, err := build(map[uint16][]byte{1: k(2), 2: k(4)}, map[uint16][]byte{1: k(3), 2: k(5)}, 2)
	if err != nil {
		t.Fatalf("migrate v1+v2: %v", err)
	}
	if tok := signIn2("rot-dev-2"); tok["user_id"] != tok1["user_id"] || tok["is_new_user"] != false {
		t.Fatalf("login with old digest must hit the same account: %v", tok)
	}
	if !a2.RunMaintenanceOnce(ctx) {
		t.Fatal("maintenance round did not acquire the lock")
	}
	if dv, cv := versions(); len(dv) != 1 || dv[0] != 2 || cv[0] != 2 {
		t.Fatalf("after backfill: digest=%v cipher=%v", dv, cv)
	}
	if tok := signIn2("rot-dev-3"); tok["user_id"] != tok1["user_id"] {
		t.Fatalf("login after backfill: %v", tok)
	}
	a2.Close()
	// 3. 移除 1 号：Migrate 通过（表里已无 v1）
	a3, _, err := build(map[uint16][]byte{2: k(4)}, map[uint16][]byte{2: k(5)}, 2)
	if err != nil {
		t.Fatalf("migrate v2 only after backfill: %v", err)
	}
	a3.Close()
	// 4. 过早移除：表里回到 v1，只配 v2 → Migrate 以 ErrUnknownKeyVersion 失败
	if _, err := pool.Exec(ctx, `UPDATE identity SET digest_key_version = 1 WHERE delete_time IS NULL AND subject_digest IS NOT NULL`); err != nil {
		t.Fatal(err)
	}
	a4, _, err := build(map[uint16][]byte{2: k(4)}, map[uint16][]byte{2: k(5)}, 2)
	if !errors.Is(err, user.ErrUnknownKeyVersion) || !strings.Contains(err.Error(), "digest_key_version") {
		t.Fatalf("premature key removal must fail Migrate: %v", err)
	}
	a4.Close()
}

// hostNoteAnonymizer 模拟宿主业务域：在 purge 的同一事务里把 host_note 中该用户的行匿名化。
type hostNoteAnonymizer struct{}

func (hostNoteAnonymizer) Name() string     { return "host_note" }
func (hostNoteAnonymizer) Tables() []string { return []string{"host_note"} }
func (hostNoteAnonymizer) Anonymize(ctx context.Context, tx pgx.Tx, userID string) error {
	_, err := tx.Exec(ctx, `UPDATE host_note SET user_id = 'purged', body = NULL WHERE user_id = $1`, userID)
	return err
}

func TestAccountLifecycleEndToEndAgainstRealDB(t *testing.T) {
	dsn := dbDSN(t)
	ctx := context.Background()
	schema := fmt.Sprintf("authtest_%08x", rand.Uint32())
	poolCfg, err := authserver.PoolConfig(dsn, schema)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		pool.Close()
	})
	captured := &captureSender{}
	cfg := minimal()
	cfg.Schema = schema
	cfg.ReauthMaxAge = time.Minute
	cfg.CodeCooldown = time.Second // 同一手机号要登录四次；miniredis 的 TTL 只随 FastForward 推进
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	a, err := authserver.New(cfg, authserver.Deps{Pool: pool, Redis: rdb, SMSSender: captured, EmailSender: captured, Anonymizers: []anonymize.Anonymizer{hostNoteAnonymizer{}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	// 宿主业务表与库表同库（这里为省事放同一 schema；宿主通常在 public）
	if _, err := pool.Exec(ctx, `CREATE TABLE host_note (user_id TEXT NOT NULL, body TEXT)`); err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	r.Mount("/v1", a.ConsumerHandler())
	srv := httptest.NewServer(r)
	defer srv.Close()

	call := func(method, path, bearer string, body any, hdr map[string]string) (*http.Response, map[string]any) {
		t.Helper()
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, srv.URL+path, rd)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp, out
	}
	reason := func(m map[string]any) string {
		e, _ := m["error"].(map[string]any)
		s, _ := e["reason"].(string)
		return s
	}
	const phone = "+8613812349999"
	devA := map[string]string{"X-Device-Id": "e2e-life-a"}
	devB := map[string]string{"X-Device-Id": "e2e-life-b"}
	signIn := func(dev map[string]string) map[string]any {
		t.Helper()
		mr.FastForward(2 * time.Second) // 跨过 CodeCooldown
		if resp, _ := call("POST", "/v1/users:sendSignInCode", "", map[string]string{"channel": "PHONE", "target": phone}, nil); resp.StatusCode != 200 {
			t.Fatalf("sendSignInCode: %d", resp.StatusCode)
		}
		resp, tok := call("POST", "/v1/users:signInWithCode", "", map[string]any{"phone": map[string]string{"target": phone, "code": captured.code(phone)}}, dev)
		if resp.StatusCode != 200 {
			t.Fatalf("signInWithCode: %d %v", resp.StatusCode, tok)
		}
		return tok
	}

	// 1. 两台设备登录；宿主写一行业务数据
	tokA, tokB := signIn(devA), signIn(devB)
	uid := tokA["user_id"].(string)
	if _, err := pool.Exec(ctx, `INSERT INTO host_note VALUES ($1, 'secret')`, uid); err != nil {
		t.Fatal(err)
	}
	// 2. 软删除（刚登录，auth_time 新鲜）→ 200 PENDING_DELETION
	resp, me := call("DELETE", "/v1/users/me", tokA["access_token"].(string), nil, nil)
	if resp.StatusCode != 200 || me["state"] != "PENDING_DELETION" || me["delete_time"] == nil || me["purge_time"] == nil {
		t.Fatalf("delete: %d %v", resp.StatusCode, me)
	}
	// 3. 两台设备的会话都失效：access 401，refresh 400 invalid_grant
	for _, tok := range []map[string]any{tokA, tokB} {
		if resp, _ = call("GET", "/v1/users/me", tok["access_token"].(string), nil, nil); resp.StatusCode != 401 {
			t.Fatalf("revoked access must be 401: %d", resp.StatusCode)
		}
		resp, body := call("POST", "/v1/token", "", map[string]string{"grant_type": "refresh_token", "refresh_token": tok["refresh_token"].(string)}, nil)
		if resp.StatusCode != 400 || body["error"] != "invalid_grant" {
			t.Fatalf("refresh after delete: %d %v", resp.StatusCode, body)
		}
	}
	// 4. 冷静期内锚点登录 → 同一账号、user:undelete；能读 me，不能改
	tokP := signIn(devA)
	if tokP["scope"] != "user:undelete" || tokP["user_id"] != uid || tokP["is_new_user"] != false {
		t.Fatalf("pending-deletion login: %v", tokP)
	}
	accessP := tokP["access_token"].(string)
	if resp, me = call("GET", "/v1/users/me", accessP, nil, nil); resp.StatusCode != 200 || me["state"] != "PENDING_DELETION" {
		t.Fatalf("me while pending: %d %v", resp.StatusCode, me)
	}
	resp, aip := call("PATCH", "/v1/users/me", accessP, map[string]string{"display_name": "x"}, nil)
	if resp.StatusCode != 403 || reason(aip) != "INSUFFICIENT_SCOPE" {
		t.Fatalf("patch with user:undelete: %d %v", resp.StatusCode, aip)
	}
	// 5. undelete → 200 ACTIVE；刷新 → user；ACTIVE 账号持 user 凭证调 :undelete → 403
	//    INSUFFICIENT_SCOPE（scope 只给 user:undelete）
	if resp, me = call("POST", "/v1/users/me:undelete", accessP, nil, nil); resp.StatusCode != 200 || me["state"] != "ACTIVE" || me["delete_time"] != nil || me["purge_time"] != nil {
		t.Fatalf("undelete: %d %v", resp.StatusCode, me)
	}
	resp, tok2 := call("POST", "/v1/token", "", map[string]string{"grant_type": "refresh_token", "refresh_token": tokP["refresh_token"].(string)}, nil)
	if resp.StatusCode != 200 || tok2["scope"] != "user" {
		t.Fatalf("refresh after undelete: %d %v", resp.StatusCode, tok2)
	}
	access2 := tok2["access_token"].(string)
	if resp, aip = call("POST", "/v1/users/me:undelete", access2, nil, nil); resp.StatusCode != 403 || reason(aip) != "INSUFFICIENT_SCOPE" {
		t.Fatalf("undelete an active account: %d %v", resp.StatusCode, aip)
	}
	// 6. 再次软删除（auth_time 仍在 1 分钟内）；把 purge_time 拨到过去；跑一轮维护 → 匿名化
	if resp, _ = call("DELETE", "/v1/users/me", access2, nil, nil); resp.StatusCode != 200 {
		t.Fatalf("second delete: %d", resp.StatusCode)
	}
	if _, err := pool.Exec(ctx, `UPDATE user_account SET purge_time = now() - interval '1 second' WHERE id = $1`, uid); err != nil {
		t.Fatal(err)
	}
	if !a.RunMaintenanceOnce(ctx) {
		t.Fatal("maintenance round did not acquire the lock")
	}
	var state int16
	var displayName *string
	if err := pool.QueryRow(ctx, `SELECT state, display_name FROM user_account WHERE id = $1`, uid).Scan(&state, &displayName); err != nil || state != 4 || displayName != nil {
		t.Fatalf("user row after purge: state=%d name=%v err=%v", state, displayName, err)
	}
	var leftovers, activeSessions int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM identity WHERE user_id = $1 AND (delete_time IS NULL OR subject_ciphertext IS NOT NULL OR hint_suffix IS NOT NULL OR provider_meta IS NOT NULL)`, uid).Scan(&leftovers); err != nil || leftovers != 0 {
		t.Fatalf("identity pii left after purge: %d %v", leftovers, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM session WHERE user_id = $1 AND revoke_time IS NULL`, uid).Scan(&activeSessions); err != nil || activeSessions != 0 {
		t.Fatalf("active sessions after purge: %d %v", activeSessions, err)
	}
	var hostUser string
	var hostBody *string
	if err := pool.QueryRow(ctx, `SELECT user_id, body FROM host_note`).Scan(&hostUser, &hostBody); err != nil || hostUser != "purged" || hostBody != nil {
		t.Fatalf("host table after purge: user=%q body=%v err=%v", hostUser, hostBody, err)
	}
	// 7. 同一手机号重新登录 → 新账号（原 subject 已释放）
	tokN := signIn(devA)
	if tokN["is_new_user"] != true || tokN["user_id"] == uid || tokN["scope"] != "user" {
		t.Fatalf("re-register after purge: %v", tokN)
	}
}

func TestAdminSurfaceEndToEndAgainstRealDB(t *testing.T) {
	dsn := dbDSN(t)
	ctx := context.Background()
	schema := fmt.Sprintf("authtest_%08x", rand.Uint32())
	poolCfg, err := authserver.PoolConfig(dsn, schema)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		pool.Close()
	})
	captured := &captureSender{}
	cfg := minimal()
	cfg.Schema = schema
	cfg.CodeCooldown = time.Second
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	a, err := authserver.New(cfg, authserver.Deps{Pool: pool, Redis: rdb, SMSSender: captured, EmailSender: captured, AdminVerifier: stubVerifier{}, AdminPrincipal: stubPrincipalFrom})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	a.Start(ctx)
	r := chi.NewRouter()
	r.Mount("/v1", a.ConsumerHandler())
	r.Route("/admin/v1", func(r chi.Router) {
		r.Use(stubVerifier{}.Middleware())
		r.Mount("/", a.AdminHandler())
	})
	srv := httptest.NewServer(r)
	defer srv.Close()

	call := func(method, path, adminHdr string, body any, hdr map[string]string) (*http.Response, map[string]any) {
		t.Helper()
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, srv.URL+path, rd)
		if adminHdr != "" {
			req.Header.Set("X-Test-Admin", adminHdr)
		}
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp, out
	}
	reason := func(m map[string]any) string {
		e, _ := m["error"].(map[string]any)
		s, _ := e["reason"].(string)
		return s
	}
	const (
		phone    = "+8613812340099"
		operator = "https://kc/realms/shifang-admin|adm-1|ops|operator"
		super    = "https://kc/realms/shifang-admin|adm-2|root|operator,super-admin"
	)
	signIn := func(dev string) map[string]any {
		t.Helper()
		mr.FastForward(2 * time.Second)
		if resp, _ := call("POST", "/v1/users:sendSignInCode", "", map[string]string{"channel": "PHONE", "target": phone}, nil); resp.StatusCode != 200 {
			t.Fatalf("sendSignInCode: %d", resp.StatusCode)
		}
		resp, tok := call("POST", "/v1/users:signInWithCode", "", map[string]any{"phone": map[string]string{"target": phone, "code": captured.code(phone)}}, map[string]string{"X-Device-Id": dev})
		if resp.StatusCode != 200 {
			t.Fatalf("signInWithCode: %d %v", resp.StatusCode, tok)
		}
		return tok
	}

	// 1. C 端建号 + 两台设备
	tokA, tokB := signIn("adm-e2e-a"), signIn("adm-e2e-b")
	uid := tokA["user_id"].(string)
	// 2. 列表：按手机号过滤命中；无凭证 401；operator 可读
	resp, out := call("GET", "/admin/v1/users?filter=identity.phone%20%3D%20%22"+phone+"%22", operator, nil, nil)
	if resp.StatusCode != 200 || len(out["users"].([]any)) != 1 || out["users"].([]any)[0].(map[string]any)["name"] != "users/"+uid {
		t.Fatalf("list by phone: %d %v", resp.StatusCode, out)
	}
	if resp, _ = call("GET", "/admin/v1/users", "", nil, nil); resp.StatusCode != 401 {
		t.Fatalf("no credential: %d", resp.StatusCode)
	}
	// 3. 详情：掩码身份 + 2 个活跃会话
	resp, d := call("GET", "/admin/v1/users/"+uid, operator, nil, nil)
	if resp.StatusCode != 200 || d["active_session_count"] != float64(2) || d["identities"].([]any)[0].(map[string]any)["masked_subject"] != "+86 138****0099" || d["freeze"] != nil {
		t.Fatalf("detail: %d %v", resp.StatusCode, d)
	}
	// 4. 冻结：C 端 access 401、refresh 400 invalid_grant（会话已被冻结吊销）、登录 403
	resp, u := call("POST", "/admin/v1/users/"+uid+":freeze", operator, map[string]string{"reason": "abuse"}, nil)
	if resp.StatusCode != 200 || u["state"] != "FROZEN" || u["freeze"].(map[string]any)["actor_username"] != "ops" {
		t.Fatalf("freeze: %d %v", resp.StatusCode, u)
	}
	if resp, _ = call("GET", "/v1/users/me", "", nil, map[string]string{"Authorization": "Bearer " + tokA["access_token"].(string)}); resp.StatusCode != 401 {
		t.Fatalf("frozen user's access: %d", resp.StatusCode)
	}
	resp, body := call("POST", "/v1/token", "", map[string]string{"grant_type": "refresh_token", "refresh_token": tokB["refresh_token"].(string)}, nil)
	if resp.StatusCode != 400 || body["error"] != "invalid_grant" { // 会话已被冻结吊销 → invalid_grant（3a：吊销先于冻结判定）
		t.Fatalf("refresh after freeze: %d %v", resp.StatusCode, body)
	}
	mr.FastForward(2 * time.Second)
	_, _ = call("POST", "/v1/users:sendSignInCode", "", map[string]string{"channel": "PHONE", "target": phone}, nil)
	resp, aip := call("POST", "/v1/users:signInWithCode", "", map[string]any{"phone": map[string]string{"target": phone, "code": captured.code(phone)}}, map[string]string{"X-Device-Id": "adm-e2e-a"})
	if resp.StatusCode != 403 || reason(aip) != "USER_FROZEN" {
		t.Fatalf("login while frozen: %d %v", resp.StatusCode, aip)
	}
	// 5. 解冻 → 登录恢复；踢出单个会话 → 204；revokeAll → 计数
	if resp, u = call("POST", "/admin/v1/users/"+uid+":unfreeze", operator, map[string]any{}, nil); resp.StatusCode != 200 || u["state"] != "ACTIVE" || u["freeze"] != nil {
		t.Fatalf("unfreeze: %d %v", resp.StatusCode, u)
	}
	tokC := signIn("adm-e2e-c")
	_ = signIn("adm-e2e-d")
	resp, sessions := call("GET", "/admin/v1/users/"+uid+"/sessions", operator, nil, nil)
	if resp.StatusCode != 200 || len(sessions["sessions"].([]any)) != 2 {
		t.Fatalf("sessions: %d %v", resp.StatusCode, sessions)
	}
	firstName := sessions["sessions"].([]any)[0].(map[string]any)["name"].(string)
	if resp, _ = call("DELETE", "/admin/v1/"+firstName, operator, nil, nil); resp.StatusCode != 204 {
		t.Fatalf("revoke one: %d", resp.StatusCode)
	}
	resp, ra := call("POST", "/admin/v1/users/"+uid+"/sessions:revokeAll", operator, nil, nil)
	if resp.StatusCode != 200 || ra["revoked_count"] != float64(1) {
		t.Fatalf("revoke all: %d %v", resp.StatusCode, ra)
	}
	if resp, _ = call("GET", "/v1/users/me", "", nil, map[string]string{"Authorization": "Bearer " + tokC["access_token"].(string)}); resp.StatusCode != 401 {
		t.Fatalf("revoked session's access: %d", resp.StatusCode)
	}
	// 6. :reveal：operator 403；super-admin 得到明文
	identName := d["identities"].([]any)[0].(map[string]any)["name"].(string)
	if resp, _ = call("GET", "/admin/v1/"+identName+":reveal", operator, nil, nil); resp.StatusCode != 403 {
		t.Fatalf("operator reveal: %d", resp.StatusCode)
	}
	resp, rv := call("GET", "/admin/v1/"+identName+":reveal", super, nil, nil)
	if resp.StatusCode != 200 || rv["subject"] != phone || rv["kind"] != "PHONE" {
		t.Fatalf("reveal: %d %v", resp.StatusCode, rv)
	}
	// 7. 代办删除 / 恢复（super-admin）；operator 403；show_deleted 控制列表可见性
	if resp, _ = call("DELETE", "/admin/v1/users/"+uid, operator, nil, nil); resp.StatusCode != 403 {
		t.Fatalf("operator delete: %d", resp.StatusCode)
	}
	if resp, u = call("DELETE", "/admin/v1/users/"+uid, super, nil, nil); resp.StatusCode != 200 || u["state"] != "PENDING_DELETION" {
		t.Fatalf("admin delete: %d %v", resp.StatusCode, u)
	}
	if resp, out = call("GET", "/admin/v1/users", operator, nil, nil); resp.StatusCode != 200 || len(out["users"].([]any)) != 0 {
		t.Fatalf("pending user hidden by default: %v", out)
	}
	if resp, out = call("GET", "/admin/v1/users?show_deleted=true", operator, nil, nil); resp.StatusCode != 200 || len(out["users"].([]any)) != 1 {
		t.Fatalf("show_deleted: %v", out)
	}
	if resp, u = call("POST", "/admin/v1/users/"+uid+":undelete", super, nil, nil); resp.StatusCode != 200 || u["state"] != "ACTIVE" {
		t.Fatalf("admin undelete: %d %v", resp.StatusCode, u)
	}
	if resp, aip = call("POST", "/admin/v1/users/"+uid+":undelete", super, nil, nil); resp.StatusCode != 400 || reason(aip) != "INVALID_ACCOUNT_STATE" {
		t.Fatalf("undelete active: %d %v", resp.StatusCode, aip)
	}
	// 8. 审计：Close 刷出后分页读取，ADMIN 事件带管理员快照，USER 事件带设备
	a.Close()
	var names []string
	seen := map[string]int{}
	token := ""
	for {
		resp, page := call("GET", "/admin/v1/users/"+uid+"/auditEvents?page_size=3&page_token="+token, operator, nil, nil)
		if resp.StatusCode != 200 {
			t.Fatalf("auditEvents: %d %v", resp.StatusCode, page)
		}
		for _, it := range page["audit_events"].([]any) {
			m := it.(map[string]any)
			names = append(names, m["name"].(string))
			seen[m["event_type"].(string)]++
			if m["actor_kind"] == "ADMIN" && (m["admin_username"] == nil || m["admin_issuer"] == nil) {
				t.Fatalf("admin event must carry the admin snapshot: %v", m)
			}
			if m["event_type"] == "SIGN_IN" && (m["device_id"] == nil || m["ip"] == nil) {
				t.Fatalf("sign-in event must carry device and ip: %v", m)
			}
		}
		token, _ = page["next_page_token"].(string)
		if token == "" {
			break
		}
	}
	uniq := map[string]bool{}
	for _, n := range names {
		uniq[n] = true
	}
	if len(uniq) != len(names) || len(names) < 8 {
		t.Fatalf("pagination must not repeat or skip: %d names, %d unique", len(names), len(uniq))
	}
	for _, typ := range []string{"SIGN_IN", "USER_FROZEN", "USER_UNFROZEN", "SESSION_REVOKED", "IDENTITY_REVEALED", "USER_DELETED", "USER_UNDELETED"} {
		if seen[typ] == 0 {
			t.Fatalf("event %s missing; saw %v", typ, seen)
		}
	}
}
