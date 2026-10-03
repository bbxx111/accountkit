package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/audit"
	auditdb "github.com/bbxx111/accountkit/audit/db"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/httpapi/admin"
	"github.com/bbxx111/accountkit/user"
)

// fakeService 用函数字段实现 admin.Service；未设置的方法 panic，暴露测试遗漏。
type fakeService struct {
	listUsers              func(ctx context.Context, f user.UserFilter, after *user.PageCursor, limit int) (user.UserPage, error)
	getUserDetail          func(ctx context.Context, userID string) (user.AdminUserDetail, error)
	userExists             func(ctx context.Context, userID string) error
	freeze                 func(ctx context.Context, a user.Admin, userID, reason string, meta user.Meta) (user.AdminUser, error)
	unfreeze               func(ctx context.Context, a user.Admin, userID, reason string, meta user.Meta) (user.AdminUser, error)
	adminDeleteUser        func(ctx context.Context, a user.Admin, userID string, meta user.Meta) (user.AdminUser, error)
	adminUndeleteUser      func(ctx context.Context, a user.Admin, userID string, meta user.Meta) (user.AdminUser, error)
	revealIdentity         func(ctx context.Context, a user.Admin, userID, identityID string, meta user.Meta) (user.RevealedIdentity, error)
	adminListSessions      func(ctx context.Context, userID string) ([]user.SessionInfo, error)
	adminRevokeSession     func(ctx context.Context, a user.Admin, userID, sid string, meta user.Meta) error
	adminRevokeAllSessions func(ctx context.Context, a user.Admin, userID string, meta user.Meta) (int, error)
}

func (f *fakeService) ListUsers(ctx context.Context, fl user.UserFilter, after *user.PageCursor, limit int) (user.UserPage, error) {
	return f.listUsers(ctx, fl, after, limit)
}
func (f *fakeService) GetUserDetail(ctx context.Context, userID string) (user.AdminUserDetail, error) {
	return f.getUserDetail(ctx, userID)
}
func (f *fakeService) UserExists(ctx context.Context, userID string) error {
	return f.userExists(ctx, userID)
}
func (f *fakeService) Freeze(ctx context.Context, a user.Admin, userID, reason string, meta user.Meta) (user.AdminUser, error) {
	return f.freeze(ctx, a, userID, reason, meta)
}
func (f *fakeService) Unfreeze(ctx context.Context, a user.Admin, userID, reason string, meta user.Meta) (user.AdminUser, error) {
	return f.unfreeze(ctx, a, userID, reason, meta)
}
func (f *fakeService) AdminDeleteUser(ctx context.Context, a user.Admin, userID string, meta user.Meta) (user.AdminUser, error) {
	return f.adminDeleteUser(ctx, a, userID, meta)
}
func (f *fakeService) AdminUndeleteUser(ctx context.Context, a user.Admin, userID string, meta user.Meta) (user.AdminUser, error) {
	return f.adminUndeleteUser(ctx, a, userID, meta)
}
func (f *fakeService) RevealIdentity(ctx context.Context, a user.Admin, userID, identityID string, meta user.Meta) (user.RevealedIdentity, error) {
	return f.revealIdentity(ctx, a, userID, identityID, meta)
}
func (f *fakeService) AdminListSessions(ctx context.Context, userID string) ([]user.SessionInfo, error) {
	return f.adminListSessions(ctx, userID)
}
func (f *fakeService) AdminRevokeSession(ctx context.Context, a user.Admin, userID, sid string, meta user.Meta) error {
	return f.adminRevokeSession(ctx, a, userID, sid, meta)
}
func (f *fakeService) AdminRevokeAllSessions(ctx context.Context, a user.Admin, userID string, meta user.Meta) (int, error) {
	return f.adminRevokeAllSessions(ctx, a, userID, meta)
}

type fakeAudit struct {
	listByUser func(ctx context.Context, userID string, after *audit.EventCursor, limit int) ([]auditdb.AuditEvent, error)
}

func (f *fakeAudit) ListByUser(ctx context.Context, userID string, after *audit.EventCursor, limit int) ([]auditdb.AuditEvent, error) {
	return f.listByUser(ctx, userID, after, limit)
}

// fakeVerifier：角色来自请求头 X-Test-Roles（逗号分隔）；缺角色 → 403（与 oidc-verifier 同形的 AIP-193 体）。
type fakeVerifier struct{}

func (fakeVerifier) RequireRole(role string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for _, have := range strings.Split(r.Header.Get("X-Test-Roles"), ",") {
				if strings.TrimSpace(have) == role {
					next.ServeHTTP(w, r)
					return
				}
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"code":403,"message":"role ` + role + ` required","status":"PERMISSION_DENIED","reason":"ROLE_REQUIRED"}}`))
		})
	}
}

// principalFromHeader：X-Test-Admin = "issuer|subject|username"；缺头 → 无主体（模拟宿主漏挂 Middleware）。
func principalFromHeader(ctx context.Context) (admin.Principal, bool) {
	raw, _ := ctx.Value(adminHeaderKey{}).(string)
	parts := strings.Split(raw, "|")
	if raw == "" || len(parts) != 3 {
		return admin.Principal{}, false
	}
	return admin.Principal{Issuer: parts[0], Subject: parts[1], Username: parts[2]}, true
}

type adminHeaderKey struct{}

// withAdminHeader 把 X-Test-Admin 头搬进 ctx（真实宿主由 verifier Middleware 注入 Principal）。
func withAdminHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), adminHeaderKey{}, r.Header.Get("X-Test-Admin"))))
	})
}

var (
	testNow    = time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	uid        = "u_0k3f9c2m1xq7z"
	iid        = "i_0k3f9c2m1xq7z"
	sid        = "s_0k3f9c2m1xq7z"
	adminHdr   = "https://kc.example/realms/shifang-admin|adm-1|ops"
	wantAdmin  = user.Admin{Issuer: "https://kc.example/realms/shifang-admin", Subject: "adm-1", Username: "ops"}
	operator   = map[string]string{"X-Test-Roles": "operator", "X-Test-Admin": adminHdr}
	superAdmin = map[string]string{"X-Test-Roles": "operator,super-admin", "X-Test-Admin": adminHdr}
)

func newHandler(t *testing.T, f *fakeService, fa *fakeAudit) http.Handler {
	t.Helper()
	if fa == nil {
		fa = &fakeAudit{}
	}
	h, err := admin.New(admin.Deps{
		Users: f, Audit: fa, Verifier: fakeVerifier{}, Principal: principalFromHeader,
		ClientIP:  func(*http.Request) string { return "198.51.100.7" },
		RequestID: func(*http.Request) string { return "req-admin" },
	})
	if err != nil {
		t.Fatal(err)
	}
	return withAdminHeader(h.Router())
}

type call struct {
	method, path string
	body         any // string 原样；其他 json 编码；nil 无 body
	headers      map[string]string
}

func do(t *testing.T, h http.Handler, c call) *httptest.ResponseRecorder {
	t.Helper()
	var rd io.Reader
	switch b := c.body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	default:
		buf, _ := json.Marshal(b)
		rd = bytes.NewReader(buf)
	}
	req := httptest.NewRequest(c.method, c.path, rd)
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// aipError 解析 AIP-193 错误体，返回 "STATUS/REASON" 与附加字段。
func aipError(t *testing.T, rec *httptest.ResponseRecorder) (string, map[string]any) {
	t.Helper()
	var b struct {
		Error map[string]any `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil || b.Error == nil {
		t.Fatalf("not an AIP-193 body (%d): %s", rec.Code, rec.Body.String())
	}
	if int(b.Error["code"].(float64)) != rec.Code {
		t.Fatalf("error.code %v != http %d", b.Error["code"], rec.Code)
	}
	reason, _ := b.Error["reason"].(string)
	return b.Error["status"].(string) + "/" + reason, b.Error
}

func decode(t *testing.T, rec *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode (%d): %s", rec.Code, rec.Body.String())
	}
}

func adminUserFixture() user.AdminUser {
	return user.AdminUser{ID: uid, State: enum.UserActive, DisplayName: "白博", CreateTime: testNow.Add(-48 * time.Hour), UpdateTime: testNow.Add(-time.Hour)}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func assertKeys(t *testing.T, m map[string]any, want ...string) {
	t.Helper()
	if len(m) != len(want) {
		t.Fatalf("field set: got %v want %v", keysOf(m), want)
	}
	for _, k := range want {
		if _, ok := m[k]; !ok {
			t.Fatalf("missing field %q in %v", k, keysOf(m))
		}
	}
}
