package accountkit_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/anonymize"
	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/httpapi/authn"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/sender"
)

func testRedis(t *testing.T) redis.UniversalClient {
	t.Helper()
	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// lazyPool 返回一个未连接的池（New 不做 I/O，构造用它即可）。
func lazyPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig("postgres://u:p@127.0.0.1:1/db?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg) // 不 Ping，不连接
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func baseDeps(t *testing.T) accountkit.Deps {
	t.Helper()
	return accountkit.Deps{Pool: lazyPool(t), Redis: testRedis(t), SMSSender: sender.NewLog(nil), EmailSender: sender.NewLog(nil)}
}

func TestNewIsPureAndAppliesDefaults(t *testing.T) {
	a, err := accountkit.New(minimal(), baseDeps(t))
	if err != nil {
		t.Fatal(err)
	}
	if got := a.Config(); got.Schema != "auth" || got.KeyPrefix != "auth:" || got.AccessTokenTTL != 15*time.Minute {
		t.Fatalf("defaults not applied: %+v", got)
	}
}

func TestNewRejectsBadConfigAndMissingDeps(t *testing.T) {
	bad := minimal()
	bad.JWTIssuer = ""
	if _, err := accountkit.New(bad, baseDeps(t)); err == nil {
		t.Fatal("invalid config must fail")
	}
	if _, err := accountkit.New(minimal(), accountkit.Deps{Redis: testRedis(t), SMSSender: sender.NewLog(nil), EmailSender: sender.NewLog(nil)}); err == nil {
		t.Fatal("missing Pool must fail")
	}
	if _, err := accountkit.New(minimal(), accountkit.Deps{Pool: lazyPool(t), SMSSender: sender.NewLog(nil), EmailSender: sender.NewLog(nil)}); err == nil {
		t.Fatal("missing Redis must fail")
	}
	if _, err := accountkit.New(minimal(), accountkit.Deps{Pool: lazyPool(t), Redis: testRedis(t), EmailSender: sender.NewLog(nil)}); err == nil {
		t.Fatal("missing SMSSender must fail")
	}
	if _, err := accountkit.New(minimal(), accountkit.Deps{Pool: lazyPool(t), Redis: testRedis(t), SMSSender: sender.NewLog(nil)}); err == nil {
		t.Fatal("missing EmailSender must fail")
	}
}

func TestNewExposesUsersService(t *testing.T) {
	a, err := accountkit.New(minimal(), baseDeps(t))
	if err != nil {
		t.Fatal(err)
	}
	if a.Users() == nil {
		t.Fatal("Users() must be wired")
	}
}

func TestPoolConfigSetsSearchPath(t *testing.T) {
	cfg, err := accountkit.PoolConfig("postgres://u:p@localhost:5432/db?sslmode=disable", "auth_x")
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.ConnConfig.RuntimeParams["search_path"]; got != "auth_x,public" {
		t.Fatalf("search_path = %q", got)
	}
	if _, err := accountkit.PoolConfig("postgres://u:p@localhost/db", "Bad Schema"); err == nil {
		t.Fatal("invalid schema must fail")
	}
}

func TestCloseBeforeStartIsSafe(t *testing.T) {
	a, _ := accountkit.New(minimal(), baseDeps(t))
	a.Close()
	a.Close()
}

func TestNewDefaultsAuditToAsyncStoreUnlessInjected(t *testing.T) {
	a, err := accountkit.New(minimal(), baseDeps(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := a.AuditForTest().(*audit.Async); !ok {
		t.Fatalf("default Deps.Audit must be *audit.Async, got %T", a.AuditForTest())
	}
	a.Close() // 未 Start、队列为空：不得触碰数据库（lazyPool 不可连接）

	mem := &audit.Memory{}
	d := baseDeps(t)
	d.Audit = mem
	b, err := accountkit.New(minimal(), d)
	if err != nil {
		t.Fatal(err)
	}
	if b.AuditForTest() != audit.Recorder(mem) {
		t.Fatalf("injected recorder must be kept, got %T", b.AuditForTest())
	}
}

func TestDefaultRequestIDAndClientIP(t *testing.T) {
	a, _ := accountkit.New(minimal(), baseDeps(t))
	deps := a.DepsForTest()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "203.0.113.9:4567"
	if got := deps.ClientIP(r); got != "203.0.113.9" {
		t.Fatalf("ClientIP = %q", got)
	}
	r.Header.Set("X-Request-Id", "req-abc_123")
	if got := deps.RequestID(r); got != "req-abc_123" {
		t.Fatalf("RequestID = %q", got)
	}
	r.Header.Del("X-Request-Id")
	if got := deps.RequestID(r); len(got) != 32 {
		t.Fatalf("generated RequestID = %q, want 32 hex chars", got)
	}
	r.Header.Set("X-Request-Id", "bad id with spaces")
	if got := deps.RequestID(r); len(got) != 32 {
		t.Fatalf("invalid inbound id must be replaced, got %q", got)
	}
}

func TestEndUserHandlerAndMiddlewareWiring(t *testing.T) {
	a, err := accountkit.New(minimal(), baseDeps(t))
	if err != nil {
		t.Fatal(err)
	}
	// 1) EndUserHandler 可挂载、带 X-Request-Id、未知路由 AIP-193 404
	r := chi.NewRouter()
	r.Mount("/v1", a.EndUserHandler())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/nope", nil)
	req.Header.Set("X-Request-Id", "abc-123")
	r.ServeHTTP(rec, req)
	if rec.Code != 404 || rec.Header().Get("X-Request-Id") != "abc-123" || !strings.Contains(rec.Body.String(), `"status":"NOT_FOUND"`) {
		t.Fatalf("mounted 404: %d %q %s", rec.Code, rec.Header().Get("X-Request-Id"), rec.Body.String())
	}
	// 2) 已注册路径经挂载可达（无 bearer → 401 而非 404）
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/users/me", nil))
	if rec.Code != 401 || rec.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("users/me through mount: %d", rec.Code)
	}
	// 3) RequireScope 保护宿主路由：缺 bearer 401；伪造 token 401
	protected := chi.NewRouter()
	protected.With(a.RequireScope("user")).Get("/devices", func(w http.ResponseWriter, r *http.Request) {
		p, ok := accountkit.PrincipalFrom(r.Context())
		if !ok {
			t.Fatal("principal must be present after RequireScope")
		}
		_, _ = w.Write([]byte(p.UserID))
	})
	rec = httptest.NewRecorder()
	protected.ServeHTTP(rec, httptest.NewRequest("GET", "/devices", nil))
	if rec.Code != 401 {
		t.Fatalf("no bearer: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/devices", nil)
	req.Header.Set("Authorization", "Bearer forged.token.value")
	protected.ServeHTTP(rec, req)
	if rec.Code != 401 || !strings.Contains(rec.Body.String(), "TOKEN_INVALID") {
		t.Fatalf("forged: %d %s", rec.Code, rec.Body.String())
	}
	// 4) RequireRecentAuth 单独使用且无 Principal → 401（无论 SensitiveOpVerification 是否开启）；
	// SensitiveOpVerification=false 时只跳过新鲜度检查，已带 Principal 的请求放行。
	rec = httptest.NewRecorder()
	a.RequireRecentAuth()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })).ServeHTTP(rec, httptest.NewRequest("DELETE", "/x", nil))
	if rec.Code != 401 {
		t.Fatalf("recent-auth without principal: %d", rec.Code)
	}
	cfg := minimal()
	off := false
	cfg.SensitiveOpVerification = &off
	a2, err := accountkit.New(cfg, baseDeps(t))
	if err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	a2.RequireRecentAuth()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })).ServeHTTP(rec, httptest.NewRequest("DELETE", "/x", nil))
	if rec.Code != 401 {
		t.Fatalf("verification off without principal must still 401: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("DELETE", "/x", nil)
	req = req.WithContext(authn.WithPrincipal(req.Context(), user.Principal{UserID: "u_pre", Scope: "user"}))
	a2.RequireRecentAuth()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })).ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("verification off with principal must pass: %d", rec.Code)
	}
}

// TestHostMountShape 复刻 README §6.3 的宿主挂载形状：EndUserHandler 挂在 "/"，
// 宿主自己的业务路由挂在同一 chi.Router 下的兄弟前缀（"/devices"）。证明
// Mount("/") 与相邻的 Mount("/devices") 能在 chi 下共存，互不吞掉对方的路由。
func TestHostMountShape(t *testing.T) {
	a, err := accountkit.New(minimal(), baseDeps(t))
	if err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	r.Route("/v1", func(r chi.Router) {
		r.Mount("/", a.EndUserHandler())
		r.Group(func(r chi.Router) {
			r.Use(a.RequireScope("user"))
			r.Mount("/devices", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
			r.With(a.RequireRecentAuth()).Post("/devices/{device}:unbind", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
		})
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/nope", nil))
	if rec.Code != 404 || !strings.Contains(rec.Body.String(), `"status":"NOT_FOUND"`) {
		t.Fatalf("unknown route must be the enduser handler's AIP-193 404: %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/users/me", nil))
	if rec.Code != 401 {
		t.Fatalf("enduser handler route through Mount(\"/\"): %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/devices", nil))
	if rec.Code != 401 {
		t.Fatalf("sibling Mount(\"/devices\") through RequireScope: %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/devices/d1:unbind", nil))
	if rec.Code != 401 {
		t.Fatalf("custom method route behind RequireScope+RequireRecentAuth: %d", rec.Code)
	}
}

func TestNewWiresIdPVerifiersOnlyWhenConfigured(t *testing.T) {
	// 未配置：signInWithIdp 走到领域层后以 IDP_APP_NOT_ALLOWED 拒绝（校验器为 nil），且 New 不做任何网络 I/O。
	a, err := accountkit.New(minimal(), baseDeps(t))
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/users:signInWithIdp", strings.NewReader(`{"wechat":{"app_id":"wx1","code":"c"}}`))
	req.Header.Set("X-Device-Id", "d1")
	a.EndUserHandler().ServeHTTP(rec, req)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "IDP_APP_NOT_ALLOWED") {
		t.Fatalf("disabled idp: %d %s", rec.Code, rec.Body.String())
	}
	// 配置了但 HTTPClient 指向不存在的地址：New 仍成功（懒加载），请求时 503 IDP_UNAVAILABLE
	cfg := minimal()
	cfg.WeChatApps = []accountkit.WeChatApp{{AppID: "wx1", Secret: "s"}}
	cfg.WeChatAPIBaseURL = "http://127.0.0.1:1"
	cfg.AppleBundleIDs = []string{"co.shifang.zavelo"}
	cfg.AppleJWKSURL = "http://127.0.0.1:1/keys"
	deps := baseDeps(t)
	deps.HTTPClient = &http.Client{Timeout: 300 * time.Millisecond}
	a2, err := accountkit.New(cfg, deps)
	if err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/users:signInWithIdp", strings.NewReader(`{"wechat":{"app_id":"wx1","code":"c"}}`))
	req.Header.Set("X-Device-Id", "d1")
	a2.EndUserHandler().ServeHTTP(rec, req)
	if rec.Code != 503 || !strings.Contains(rec.Body.String(), "IDP_UNAVAILABLE") || rec.Header().Get("Retry-After") != "1" {
		t.Fatalf("wechat unreachable: %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/users:signInWithIdp", strings.NewReader(`{"apple":{"id_token":"x.y.z","nonce":"n"}}`))
	req.Header.Set("X-Device-Id", "d1")
	a2.EndUserHandler().ServeHTTP(rec, req)
	// 垃圾 id_token 在拉 JWKS 前就解析失败 → 400；证明 Apple 校验器已装配（而非 IDP_APP_NOT_ALLOWED）
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "IDP_CREDENTIAL_INVALID") {
		t.Fatalf("apple wired: %d %s", rec.Code, rec.Body.String())
	}
}

type stubAnonymizer struct{ name string }

func (s stubAnonymizer) Name() string                                  { return s.name }
func (stubAnonymizer) Tables() []string                                { return nil }
func (stubAnonymizer) Anonymize(context.Context, pgx.Tx, string) error { return nil }

func TestNewRejectsBadAnonymizersAndAcceptsDistinctOnes(t *testing.T) {
	for name, list := range map[string][]anonymize.Anonymizer{
		"nil entry":  {nil},
		"empty name": {stubAnonymizer{}},
		"duplicate":  {stubAnonymizer{name: "a"}, stubAnonymizer{name: "a"}},
	} {
		d := baseDeps(t)
		d.Anonymizers = list
		if _, err := accountkit.New(minimal(), d); err == nil {
			t.Fatalf("%s must be rejected", name)
		}
	}
	d := baseDeps(t)
	d.Anonymizers = []anonymize.Anonymizer{stubAnonymizer{name: "a"}, stubAnonymizer{name: "b"}}
	if _, err := accountkit.New(minimal(), d); err != nil {
		t.Fatalf("distinct names must be accepted: %v", err)
	}
}

func TestLifecycleRoutesMounted(t *testing.T) {
	a, err := accountkit.New(minimal(), baseDeps(t))
	if err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	r.Mount("/v1", a.EndUserHandler())
	for _, c := range []struct{ method, path string }{{"DELETE", "/v1/users/me"}, {"POST", "/v1/users/me:undelete"}} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil))
		if rec.Code != 401 || rec.Header().Get("WWW-Authenticate") == "" {
			t.Fatalf("%s %s without bearer: %d (route must exist and demand a bearer)", c.method, c.path, rec.Code)
		}
	}
}

// stubVerifier 模拟宿主的 oidc-verifier：Middleware 从 X-Test-Admin（"issuer|subject|username|role1,role2"）注入主体；
// RequireRole 检查主体角色。accountkit 只见到 RequireRole 与 principalFrom。
type stubVerifier struct{}

type stubPrincipal struct {
	issuer, subject, username string
	roles                     []string
}

type stubKey struct{}

func (stubVerifier) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			parts := strings.Split(r.Header.Get("X-Test-Admin"), "|")
			if len(parts) != 4 {
				w.WriteHeader(401)
				return
			}
			p := stubPrincipal{parts[0], parts[1], parts[2], strings.Split(parts[3], ",")}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), stubKey{}, p)))
		})
	}
}

func (stubVerifier) RequireRole(role string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, ok := r.Context().Value(stubKey{}).(stubPrincipal)
			if !ok {
				w.WriteHeader(401)
				return
			}
			for _, have := range p.roles {
				if have == role {
					next.ServeHTTP(w, r)
					return
				}
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(403)
			_, _ = w.Write([]byte(`{"error":{"code":403,"message":"role required","status":"PERMISSION_DENIED","reason":"ROLE_REQUIRED"}}`))
		})
	}
}

func stubPrincipalFrom(ctx context.Context) (accountkit.AdminPrincipal, bool) {
	p, ok := ctx.Value(stubKey{}).(stubPrincipal)
	return accountkit.AdminPrincipal{Issuer: p.issuer, Subject: p.subject, Username: p.username}, ok
}

func TestNewRequiresBothAdminDepsOrNeither(t *testing.T) {
	d := baseDeps(t)
	d.AdminVerifier = stubVerifier{}
	if _, err := accountkit.New(minimal(), d); err == nil {
		t.Fatal("AdminVerifier without AdminPrincipal must be rejected")
	}
	d = baseDeps(t)
	d.AdminPrincipal = stubPrincipalFrom
	if _, err := accountkit.New(minimal(), d); err == nil {
		t.Fatal("AdminPrincipal without AdminVerifier must be rejected")
	}
	d = baseDeps(t)
	d.AdminVerifier, d.AdminPrincipal = stubVerifier{}, stubPrincipalFrom
	if _, err := accountkit.New(minimal(), d); err != nil {
		t.Fatalf("both set: %v", err)
	}
}

func TestAdminHandlerUnconfiguredIs503(t *testing.T) {
	a, err := accountkit.New(minimal(), baseDeps(t))
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/users", nil)
	req.Header.Set("X-Request-Id", "rid-unconf")
	a.AdminHandler().ServeHTTP(rec, req)
	if rec.Code != 503 || !strings.Contains(rec.Body.String(), `"reason":"ADMIN_NOT_CONFIGURED"`) || rec.Header().Get("X-Request-Id") != "rid-unconf" {
		t.Fatalf("unconfigured admin surface: %d %s %q", rec.Code, rec.Body.String(), rec.Header().Get("X-Request-Id"))
	}
}

func TestAdminHandlerMountsBehindHostVerifier(t *testing.T) {
	d := baseDeps(t)
	d.AdminVerifier, d.AdminPrincipal = stubVerifier{}, stubPrincipalFrom
	a, err := accountkit.New(minimal(), d)
	if err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	r.Route("/admin/v1", func(r chi.Router) {
		r.Use(stubVerifier{}.Middleware())
		r.Mount("/", a.AdminHandler())
	})
	// 无凭证 → 宿主 Middleware 401；operator 调 super-admin 路由 → 403；角色够 → 到达 handler（400 INVALID_ID 证明已进入库内路由）
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/admin/v1/users/u_0k3f9c2m1xq7z", nil))
	if rec.Code != 401 {
		t.Fatalf("no credential: %d", rec.Code)
	}
	req := httptest.NewRequest("DELETE", "/admin/v1/users/u_0k3f9c2m1xq7z", nil)
	req.Header.Set("X-Test-Admin", "iss|sub|ops|operator")
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("operator on super-admin route: %d", rec.Code)
	}
	req = httptest.NewRequest("GET", "/admin/v1/users/not-an-id", nil)
	req.Header.Set("X-Test-Admin", "iss|sub|ops|operator")
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), `"reason":"INVALID_ID"`) {
		t.Fatalf("operator reaching handler: %d %s", rec.Code, rec.Body.String())
	}
}

func TestRecordAdminForbidden(t *testing.T) {
	mem := &audit.Memory{}
	d := baseDeps(t)
	d.Audit = mem
	a, err := accountkit.New(minimal(), d)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/admin/v1/users", nil)
	req.Header.Set("X-Request-Id", "req-forbidden")
	req.RemoteAddr = "198.51.100.9:1234"
	a.RecordAdminForbidden(req, accountkit.AdminPrincipal{Issuer: "iss", Subject: "sub", Username: "ops"})
	evs := mem.Events()
	if len(evs) != 1 {
		t.Fatalf("events: %+v", evs)
	}
	e := evs[0]
	if e.Type != enum.EventAdminForbidden || e.Actor != enum.ActorAdmin || e.Result != enum.ResultFailure || e.Reason != "ROLE_REQUIRED" || e.AdminIssuer != "iss" || e.AdminSubject != "sub" || e.AdminUsername != "ops" || e.IP != "198.51.100.9" || e.RequestID != "req-forbidden" || e.OccurTime.IsZero() {
		t.Fatalf("ADMIN_FORBIDDEN event: %+v", e)
	}
}
