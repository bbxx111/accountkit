package enduser_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/httpapi/enduser"
	"github.com/bbxx111/accountkit/user"
)

// fakeService 用函数字段实现 enduser.Service；未设置的方法 panic，暴露测试遗漏。
type fakeService struct {
	sendSignInCode           func(ctx context.Context, ch enum.IdentityKind, target string, meta user.Meta) error
	signInWithCode           func(ctx context.Context, ch enum.IdentityKind, target, code string, dev user.Device, meta user.Meta) (user.TokenResult, error)
	signInWithIdp            func(ctx context.Context, cred user.IdpCredential, dev user.Device, meta user.Meta) (user.TokenResult, error)
	refresh                  func(ctx context.Context, rt string, meta user.Meta) (user.TokenResult, error)
	revoke                   func(ctx context.Context, rt string, meta user.Meta) error
	authenticate             func(ctx context.Context, raw string) (user.Principal, error)
	getMe                    func(ctx context.Context, uid string) (user.Me, error)
	updateDisplayName        func(ctx context.Context, uid, name string) (user.Me, error)
	sendReauthenticationCode func(ctx context.Context, p user.Principal, ch enum.IdentityKind, target string, meta user.Meta) error
	reauthenticate           func(ctx context.Context, p user.Principal, ch enum.IdentityKind, target, code string, meta user.Meta) (user.TokenResult, error)
	listSessions             func(ctx context.Context, uid, sid string) ([]user.SessionInfo, error)
	revokeSession            func(ctx context.Context, uid, sid string, meta user.Meta) error
	revokeOtherSessions      func(ctx context.Context, uid, sid string, meta user.Meta) error
	listIdentities           func(ctx context.Context, uid string) ([]user.IdentityInfo, error)
	sendBindCode             func(ctx context.Context, p user.Principal, ch enum.IdentityKind, target string, meta user.Meta) error
	bindWithCode             func(ctx context.Context, p user.Principal, ch enum.IdentityKind, target, code string, meta user.Meta) (user.IdentityInfo, bool, error)
	bindWithIdp              func(ctx context.Context, p user.Principal, cred user.IdpCredential, meta user.Meta) (user.IdentityInfo, bool, error)
	unbindIdentity           func(ctx context.Context, p user.Principal, id string, meta user.Meta) error
	deleteMe                 func(ctx context.Context, p user.Principal, meta user.Meta) (user.Me, error)
	undelete                 func(ctx context.Context, p user.Principal, meta user.Meta) (user.Me, error)
}

func (f *fakeService) SendSignInCode(ctx context.Context, ch enum.IdentityKind, target string, meta user.Meta) error {
	return f.sendSignInCode(ctx, ch, target, meta)
}
func (f *fakeService) SignInWithCode(ctx context.Context, ch enum.IdentityKind, target, code string, dev user.Device, meta user.Meta) (user.TokenResult, error) {
	return f.signInWithCode(ctx, ch, target, code, dev, meta)
}
func (f *fakeService) SignInWithIdp(ctx context.Context, cred user.IdpCredential, dev user.Device, meta user.Meta) (user.TokenResult, error) {
	return f.signInWithIdp(ctx, cred, dev, meta)
}
func (f *fakeService) Refresh(ctx context.Context, rt string, meta user.Meta) (user.TokenResult, error) {
	return f.refresh(ctx, rt, meta)
}
func (f *fakeService) Revoke(ctx context.Context, rt string, meta user.Meta) error {
	return f.revoke(ctx, rt, meta)
}
func (f *fakeService) Authenticate(ctx context.Context, raw string) (user.Principal, error) {
	return f.authenticate(ctx, raw)
}
func (f *fakeService) GetMe(ctx context.Context, uid string) (user.Me, error) {
	return f.getMe(ctx, uid)
}
func (f *fakeService) UpdateDisplayName(ctx context.Context, uid, name string) (user.Me, error) {
	return f.updateDisplayName(ctx, uid, name)
}
func (f *fakeService) SendReauthenticationCode(ctx context.Context, p user.Principal, ch enum.IdentityKind, target string, meta user.Meta) error {
	return f.sendReauthenticationCode(ctx, p, ch, target, meta)
}
func (f *fakeService) Reauthenticate(ctx context.Context, p user.Principal, ch enum.IdentityKind, target, code string, meta user.Meta) (user.TokenResult, error) {
	return f.reauthenticate(ctx, p, ch, target, code, meta)
}
func (f *fakeService) ListSessions(ctx context.Context, uid, sid string) ([]user.SessionInfo, error) {
	return f.listSessions(ctx, uid, sid)
}
func (f *fakeService) RevokeSession(ctx context.Context, uid, sid string, meta user.Meta) error {
	return f.revokeSession(ctx, uid, sid, meta)
}
func (f *fakeService) RevokeOtherSessions(ctx context.Context, uid, sid string, meta user.Meta) error {
	return f.revokeOtherSessions(ctx, uid, sid, meta)
}
func (f *fakeService) ListIdentities(ctx context.Context, uid string) ([]user.IdentityInfo, error) {
	return f.listIdentities(ctx, uid)
}
func (f *fakeService) SendBindCode(ctx context.Context, p user.Principal, ch enum.IdentityKind, target string, meta user.Meta) error {
	return f.sendBindCode(ctx, p, ch, target, meta)
}
func (f *fakeService) BindWithCode(ctx context.Context, p user.Principal, ch enum.IdentityKind, target, code string, meta user.Meta) (user.IdentityInfo, bool, error) {
	return f.bindWithCode(ctx, p, ch, target, code, meta)
}
func (f *fakeService) BindWithIdp(ctx context.Context, p user.Principal, cred user.IdpCredential, meta user.Meta) (user.IdentityInfo, bool, error) {
	return f.bindWithIdp(ctx, p, cred, meta)
}
func (f *fakeService) UnbindIdentity(ctx context.Context, p user.Principal, id string, meta user.Meta) error {
	return f.unbindIdentity(ctx, p, id, meta)
}
func (f *fakeService) DeleteMe(ctx context.Context, p user.Principal, meta user.Meta) (user.Me, error) {
	return f.deleteMe(ctx, p, meta)
}
func (f *fakeService) Undelete(ctx context.Context, p user.Principal, meta user.Meta) (user.Me, error) {
	return f.undelete(ctx, p, meta)
}

var (
	testNow   = time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	principal = user.Principal{UserID: "u_0k3f9c2m1xq7z", SessionID: "s_0k3f9c2m1xq7z", Scope: "user", AuthTime: testNow.Add(-time.Minute)}
)

// withAuth 让 fake 接受 "Bearer good" → principal，其余 → ErrInvalidToken。
func withAuth(f *fakeService, p user.Principal) *fakeService {
	f.authenticate = func(_ context.Context, raw string) (user.Principal, error) {
		if raw == "good" {
			return p, nil
		}
		return user.Principal{}, user.ErrInvalidToken
	}
	return f
}

func newHandler(t *testing.T, f *fakeService) http.Handler {
	t.Helper()
	h, err := enduser.New(enduser.Deps{
		Users:                   f,
		ClientIP:                func(r *http.Request) string { return "203.0.113.9" },
		RequestID:               func(r *http.Request) string { return "req-test" },
		Now:                     func() time.Time { return testNow },
		ReauthMaxAge:            5 * time.Minute,
		SensitiveOpVerification: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return h.Router()
}

// newHandlerWithDeps 与 newHandler 相同，但允许调整 Deps。
func newHandlerWithDeps(t *testing.T, f *fakeService, mutate func(*endUserDeps)) http.Handler {
	t.Helper()
	d := endUserDeps{
		Users:                   f,
		ClientIP:                func(r *http.Request) string { return "203.0.113.9" },
		RequestID:               func(r *http.Request) string { return "req-test" },
		Now:                     func() time.Time { return testNow },
		ReauthMaxAge:            5 * time.Minute,
		SensitiveOpVerification: true,
	}
	mutate(&d)
	h, err := enduser.New(d)
	if err != nil {
		t.Fatal(err)
	}
	return h.Router()
}

type call struct {
	method, path string
	body         any // string 原样发送；其他 json 编码；nil 无 body
	headers      map[string]string
	bearer       string
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
	req.RemoteAddr = "203.0.113.9:4321"
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
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

func devHeaders() map[string]string {
	return map[string]string{"X-Device-Id": "3fa85f64-5717-4562-b3fc-2c963f66afa6", "X-Device-Name": "Pixel 9"}
}

type endUserDeps = enduser.Deps

func newHandlerErr(t *testing.T, mutate func(*endUserDeps)) (*enduser.Handler, error) {
	t.Helper()
	d := endUserDeps{
		Users:        &fakeService{},
		ClientIP:     func(r *http.Request) string { return "1.1.1.1" },
		RequestID:    func(r *http.Request) string { return "r" },
		ReauthMaxAge: time.Minute,
	}
	mutate(&d)
	return enduser.New(d)
}

func itoa(i int) string { return strconv.Itoa(i) }

func contains(s, sub string) bool { return strings.Contains(s, sub) }
