package consumer_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/code"
)

func meFixture() user.Me {
	return user.Me{ID: principal.UserID, State: enum.UserActive, DisplayName: "白博", CreateTime: testNow.Add(-24 * time.Hour)}
}

func TestGetMe(t *testing.T) {
	f := withAuth(&fakeService{getMe: func(_ context.Context, uid string) (user.Me, error) {
		if uid != principal.UserID {
			return user.Me{}, user.ErrNotFound
		}
		return meFixture(), nil
	}}, principal)
	h := newHandler(t, f)

	rec := do(t, h, call{method: "GET", path: "/users/me", bearer: "good"})
	var me map[string]any
	decode(t, rec, &me)
	if rec.Code != 200 || me["name"] != "users/"+principal.UserID || me["state"] != "ACTIVE" || me["display_name"] != "白博" || me["create_time"] != "2026-09-09T12:00:00Z" || me["delete_time"] != nil || me["purge_time"] != nil {
		t.Fatalf("me: %d %v", rec.Code, me)
	}
	for k := range me {
		switch k {
		case "name", "state", "display_name", "create_time", "delete_time", "purge_time":
		default:
			t.Fatalf("unexpected field on consumer DTO: %s", k)
		}
	}
	// 401 形态
	rec = do(t, h, call{method: "GET", path: "/users/me"})
	if status, _ := aipError(t, rec); rec.Code != 401 || status != "UNAUTHENTICATED/TOKEN_MISSING" || rec.Header().Get("WWW-Authenticate") != `Bearer realm="user"` {
		t.Fatalf("no bearer: %d %s %q", rec.Code, status, rec.Header().Get("WWW-Authenticate"))
	}
	rec = do(t, h, call{method: "GET", path: "/users/me", bearer: "expired"})
	if status, _ := aipError(t, rec); rec.Code != 401 || status != "UNAUTHENTICATED/TOKEN_INVALID" || rec.Header().Get("WWW-Authenticate") != `Bearer realm="user", error="invalid_token"` {
		t.Fatalf("bad bearer: %d %s", rec.Code, status)
	}
	// 任意 scope 可读
	bind := principal
	bind.Scope = "user:bind"
	h2 := newHandler(t, withAuth(&fakeService{getMe: f.getMe}, bind))
	if rec = do(t, h2, call{method: "GET", path: "/users/me", bearer: "good"}); rec.Code != 200 {
		t.Fatalf("user:bind must read me: %d", rec.Code)
	}
}

func TestUpdateMe(t *testing.T) {
	var gotName string
	f := withAuth(&fakeService{updateDisplayName: func(_ context.Context, uid, name string) (user.Me, error) {
		gotName = name
		if len([]rune(name)) > 32 {
			return user.Me{}, user.ErrInvalidArgument
		}
		m := meFixture()
		m.DisplayName = name
		return m, nil
	}}, principal)
	h := newHandler(t, f)

	rec := do(t, h, call{method: "PATCH", path: "/users/me", bearer: "good", body: map[string]string{"display_name": "新名字"}})
	var me map[string]any
	decode(t, rec, &me)
	if rec.Code != 200 || me["display_name"] != "新名字" || gotName != "新名字" {
		t.Fatalf("patch: %d %v", rec.Code, me)
	}
	// 清空
	rec = do(t, h, call{method: "PATCH", path: "/users/me", bearer: "good", body: map[string]string{"display_name": ""}})
	if rec.Code != 200 || gotName != "" {
		t.Fatal("empty string clears")
	}
	rec = do(t, h, call{method: "PATCH", path: "/users/me", bearer: "good", body: map[string]any{}})
	if status, _ := aipError(t, rec); rec.Code != 400 || status != "INVALID_ARGUMENT/NO_FIELDS" {
		t.Fatalf("no fields: %d %s", rec.Code, status)
	}
	rec = do(t, h, call{method: "PATCH", path: "/users/me", bearer: "good", body: map[string]any{"state": "FROZEN"}})
	if status, _ := aipError(t, rec); rec.Code != 400 || status != "INVALID_ARGUMENT/MALFORMED_BODY" {
		t.Fatalf("read-only field: %d %s", rec.Code, status)
	}
	rec = do(t, h, call{method: "PATCH", path: "/users/me", bearer: "good", body: map[string]string{"display_name": string(make([]rune, 33))}})
	if status, _ := aipError(t, rec); rec.Code != 400 || status != "INVALID_ARGUMENT/INVALID_ARGUMENT" {
		t.Fatalf("too long: %d %s", rec.Code, status)
	}
	// scope 不足 → 403
	bind := principal
	bind.Scope = "user:bind"
	h2 := newHandler(t, withAuth(&fakeService{}, bind))
	rec = do(t, h2, call{method: "PATCH", path: "/users/me", bearer: "good", body: map[string]string{"display_name": "x"}})
	if status, _ := aipError(t, rec); rec.Code != 403 || status != "PERMISSION_DENIED/INSUFFICIENT_SCOPE" {
		t.Fatalf("scope: %d %s", rec.Code, status)
	}
}

func TestReauthentication(t *testing.T) {
	var sent struct {
		p      user.Principal
		ch     enum.IdentityKind
		target string
	}
	f := withAuth(&fakeService{
		sendReauthenticationCode: func(_ context.Context, p user.Principal, ch enum.IdentityKind, target string, meta user.Meta) error {
			sent.p, sent.ch, sent.target = p, ch, target
			if target == "+8613900000009" {
				return user.ErrNotAnchor
			}
			return nil
		},
		reauthenticate: func(_ context.Context, p user.Principal, ch enum.IdentityKind, target, c string, meta user.Meta) (user.TokenResult, error) {
			switch c {
			case "000000":
				return user.TokenResult{}, code.ErrInvalid
			case "999999":
				return user.TokenResult{}, user.ErrInvalidToken // 会话已吊销
			case "888888":
				return user.TokenResult{}, errors.New("pg")
			}
			return user.TokenResult{AccessToken: "at3", ExpiresIn: 900, Scope: "user", UserID: p.UserID}, nil
		},
	}, principal)
	h := newHandler(t, f)

	rec := do(t, h, call{method: "POST", path: "/users/me:sendReauthenticationCode", bearer: "good", body: map[string]string{"channel": "PHONE", "target": "+8613812341234"}})
	if rec.Code != 200 || sent.p.UserID != principal.UserID || sent.p.SessionID != principal.SessionID || sent.ch != enum.IdentityPhone || sent.target != "+8613812341234" {
		t.Fatalf("send: %d %+v", rec.Code, sent)
	}
	rec = do(t, h, call{method: "POST", path: "/users/me:sendReauthenticationCode", bearer: "good", body: map[string]string{"channel": "PHONE", "target": "+8613900000009"}})
	if status, _ := aipError(t, rec); rec.Code != 400 || status != "FAILED_PRECONDITION/TARGET_NOT_ANCHOR" {
		t.Fatalf("not anchor: %d %s", rec.Code, status)
	}
	rec = do(t, h, call{method: "POST", path: "/users/me:sendReauthenticationCode", body: map[string]string{"channel": "PHONE", "target": "+8613812341234"}})
	if rec.Code != 401 {
		t.Fatalf("needs bearer: %d", rec.Code)
	}

	body := map[string]any{"phone": map[string]string{"target": "+8613812341234", "code": "123456"}}
	rec = do(t, h, call{method: "POST", path: "/users/me:reauthenticate", bearer: "good", body: body})
	var tok map[string]any
	decode(t, rec, &tok)
	if rec.Code != 200 || tok["access_token"] != "at3" || tok["expires_in"] != float64(900) {
		t.Fatalf("reauth: %d %v", rec.Code, tok)
	}
	if _, has := tok["refresh_token"]; has {
		t.Fatal("reauthenticate must not return refresh_token")
	}
	if _, has := tok["refresh_expires_in"]; has {
		t.Fatal("reauthenticate must not return refresh_expires_in")
	}
	rec = do(t, h, call{method: "POST", path: "/users/me:reauthenticate", bearer: "good", body: map[string]any{"phone": map[string]string{"target": "+8613812341234", "code": "000000"}}})
	if status, _ := aipError(t, rec); rec.Code != 400 || status != "INVALID_ARGUMENT/CODE_INVALID" {
		t.Fatalf("wrong code: %d %s", rec.Code, status)
	}
	rec = do(t, h, call{method: "POST", path: "/users/me:reauthenticate", bearer: "good", body: map[string]any{"phone": map[string]string{"target": "+8613812341234", "code": "999999"}}})
	if status, _ := aipError(t, rec); rec.Code != 401 || status != "UNAUTHENTICATED/TOKEN_INVALID" || rec.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("revoked session: %d %s", rec.Code, status)
	}
	rec = do(t, h, call{method: "POST", path: "/users/me:reauthenticate", bearer: "good", body: map[string]any{"wechat": map[string]string{"app_id": "wx", "code": "c"}}})
	if status, _ := aipError(t, rec); status != "INVALID_ARGUMENT/CREDENTIAL_KIND_NOT_ALLOWED" {
		t.Fatalf("reauth accepts only phone/email: %s", status)
	}
	rec = do(t, h, call{method: "POST", path: "/users/me:reauthenticate", bearer: "good", body: map[string]any{"phone": map[string]string{"target": "+8613812341234", "code": "888888"}}})
	if rec.Code != 500 || contains(rec.Body.String(), "pg") {
		t.Fatalf("500: %d %s", rec.Code, rec.Body.String())
	}
}

// TestSendReauthenticationCodeEmptyClientIPIs500 覆盖 Deps.ClientIP 返回空字符串的情形：
// 这是服务端配置错误，不是客户端错误，必须 500 而不是把空 IP 传给领域层。
func TestSendReauthenticationCodeEmptyClientIPIs500(t *testing.T) {
	f := withAuth(&fakeService{}, principal)
	hh, err := newHandlerErr(t, func(d *consumerDeps) {
		d.Users = f
		d.ClientIP = func(*http.Request) string { return "" }
	})
	if err != nil {
		t.Fatal(err)
	}
	h := hh.Router()
	rec := do(t, h, call{method: "POST", path: "/users/me:sendReauthenticationCode", bearer: "good", body: map[string]string{"channel": "PHONE", "target": "+8613812341234"}})
	if rec.Code != 500 {
		t.Fatalf("empty client ip must 500: %d %s", rec.Code, rec.Body.String())
	}
}

func TestDeleteMe(t *testing.T) {
	var got user.Principal
	var gotMeta user.Meta
	pendingMe := func(context.Context, user.Principal, user.Meta) (user.Me, error) {
		m := meFixture()
		m.State = enum.UserPendingDeletion
		dt, pt := testNow, testNow.Add(360*time.Hour)
		m.DeleteTime, m.PurgeTime = &dt, &pt
		return m, nil
	}
	f := withAuth(&fakeService{deleteMe: func(ctx context.Context, p user.Principal, meta user.Meta) (user.Me, error) {
		got, gotMeta = p, meta
		return pendingMe(ctx, p, meta)
	}}, principal)
	h := newHandler(t, f)

	rec := do(t, h, call{method: "DELETE", path: "/users/me", bearer: "good"})
	var me map[string]any
	decode(t, rec, &me)
	if rec.Code != 200 || me["name"] != "users/"+principal.UserID || me["state"] != "PENDING_DELETION" || me["delete_time"] != "2026-09-10T12:00:00Z" || me["purge_time"] != "2026-09-25T12:00:00Z" {
		t.Fatalf("delete: %d %v", rec.Code, me)
	}
	if got != principal || gotMeta.IP != "203.0.113.9" || gotMeta.RequestID != "req-test" {
		t.Fatalf("principal/meta passed through: %+v %+v", got, gotMeta)
	}
	// 近期认证过期 → 400 REAUTHENTICATION_REQUIRED，且不调用服务
	stale := principal
	stale.AuthTime = testNow.Add(-6 * time.Minute)
	called := false
	h2 := newHandler(t, withAuth(&fakeService{deleteMe: func(ctx context.Context, p user.Principal, meta user.Meta) (user.Me, error) {
		called = true
		return pendingMe(ctx, p, meta)
	}}, stale))
	rec = do(t, h2, call{method: "DELETE", path: "/users/me", bearer: "good"})
	if status, _ := aipError(t, rec); rec.Code != 400 || status != "FAILED_PRECONDITION/REAUTHENTICATION_REQUIRED" || called {
		t.Fatalf("stale auth_time: %d %s called=%v", rec.Code, status, called)
	}
	// SensitiveOpVerification=false 放行
	h3 := newHandlerWithDeps(t, withAuth(&fakeService{deleteMe: pendingMe}, stale), func(d *consumerDeps) { d.SensitiveOpVerification = false })
	if rec = do(t, h3, call{method: "DELETE", path: "/users/me", bearer: "good"}); rec.Code != 200 {
		t.Fatalf("verification off: %d %s", rec.Code, rec.Body.String())
	}
	// scope 不足：user:bind / user:undelete → 403
	for _, scope := range []string{"user:bind", "user:undelete"} {
		p := principal
		p.Scope = scope
		rec = do(t, newHandler(t, withAuth(&fakeService{}, p)), call{method: "DELETE", path: "/users/me", bearer: "good"})
		if status, _ := aipError(t, rec); rec.Code != 403 || status != "PERMISSION_DENIED/INSUFFICIENT_SCOPE" {
			t.Fatalf("scope %s: %d %s", scope, rec.Code, status)
		}
	}
	// 无 bearer → 401
	rec = do(t, h, call{method: "DELETE", path: "/users/me"})
	if status, _ := aipError(t, rec); rec.Code != 401 || status != "UNAUTHENTICATED/TOKEN_MISSING" {
		t.Fatalf("no bearer: %d %s", rec.Code, status)
	}
	// 领域错误映射
	for _, c := range []struct {
		err    error
		code   int
		status string
	}{
		{user.ErrInvalidState, 400, "FAILED_PRECONDITION/INVALID_ACCOUNT_STATE"},
		{user.ErrUserFrozen, 403, "PERMISSION_DENIED/USER_FROZEN"},
		{user.ErrInvalidToken, 401, "UNAUTHENTICATED/TOKEN_INVALID"},
	} {
		e := c.err
		hh := newHandler(t, withAuth(&fakeService{deleteMe: func(context.Context, user.Principal, user.Meta) (user.Me, error) { return user.Me{}, e }}, principal))
		rec = do(t, hh, call{method: "DELETE", path: "/users/me", bearer: "good"})
		if status, _ := aipError(t, rec); rec.Code != c.code || status != c.status {
			t.Fatalf("%v: %d %s", c.err, rec.Code, status)
		}
	}
}

func TestUndelete(t *testing.T) {
	undel := principal
	undel.Scope = "user:undelete"
	var got user.Principal
	f := withAuth(&fakeService{undelete: func(_ context.Context, p user.Principal, meta user.Meta) (user.Me, error) {
		got = p
		return meFixture(), nil
	}}, undel)
	h := newHandler(t, f)

	rec := do(t, h, call{method: "POST", path: "/users/me:undelete", bearer: "good"})
	var me map[string]any
	decode(t, rec, &me)
	if rec.Code != 200 || me["state"] != "ACTIVE" || me["delete_time"] != nil || me["purge_time"] != nil || got.UserID != principal.UserID || got.Scope != "user:undelete" {
		t.Fatalf("undelete: %d %v (%+v)", rec.Code, me, got)
	}
	// 请求体被忽略（与 sessions:revokeOthers 一致）
	if rec = do(t, h, call{method: "POST", path: "/users/me:undelete", bearer: "good", body: `{"whatever":1}`}); rec.Code != 200 {
		t.Fatalf("body must be ignored: %d", rec.Code)
	}
	// user scope 不再能到达领域层：:undelete 只放行 user:undelete（§2.4），user 凭证 → 403，不调用领域层
	called := false
	full := newHandler(t, withAuth(&fakeService{undelete: func(context.Context, user.Principal, user.Meta) (user.Me, error) {
		called = true
		return user.Me{}, user.ErrInvalidState
	}}, principal))
	rec = do(t, full, call{method: "POST", path: "/users/me:undelete", bearer: "good"})
	if status, _ := aipError(t, rec); rec.Code != 403 || status != "PERMISSION_DENIED/INSUFFICIENT_SCOPE" || called {
		t.Fatalf("user scope: %d %s (called=%v)", rec.Code, status, called)
	}
	// user:undelete + 领域层按状态判 400
	invalidState := newHandler(t, withAuth(&fakeService{undelete: func(context.Context, user.Principal, user.Meta) (user.Me, error) {
		return user.Me{}, user.ErrInvalidState
	}}, undel))
	rec = do(t, invalidState, call{method: "POST", path: "/users/me:undelete", bearer: "good"})
	if status, body := aipError(t, rec); rec.Code != 400 || status != "FAILED_PRECONDITION/INVALID_ACCOUNT_STATE" || contains(body["message"].(string), "ACTIVE") {
		t.Fatalf("invalid state: %d %s %v", rec.Code, status, body)
	}
	// user:bind → 403
	bind := principal
	bind.Scope = "user:bind"
	rec = do(t, newHandler(t, withAuth(&fakeService{}, bind)), call{method: "POST", path: "/users/me:undelete", bearer: "good"})
	if status, _ := aipError(t, rec); rec.Code != 403 || status != "PERMISSION_DENIED/INSUFFICIENT_SCOPE" {
		t.Fatalf("bind scope: %d %s", rec.Code, status)
	}
	// 冻结 → 403 USER_FROZEN；无 bearer → 401；GET → 405
	frozen := newHandler(t, withAuth(&fakeService{undelete: func(context.Context, user.Principal, user.Meta) (user.Me, error) {
		return user.Me{}, user.ErrUserFrozen
	}}, undel))
	rec = do(t, frozen, call{method: "POST", path: "/users/me:undelete", bearer: "good"})
	if status, _ := aipError(t, rec); rec.Code != 403 || status != "PERMISSION_DENIED/USER_FROZEN" {
		t.Fatalf("frozen: %d %s", rec.Code, status)
	}
	if rec = do(t, h, call{method: "POST", path: "/users/me:undelete"}); rec.Code != 401 {
		t.Fatalf("no bearer: %d", rec.Code)
	}
	if rec = do(t, h, call{method: "GET", path: "/users/me:undelete", bearer: "good"}); rec.Code != 405 {
		t.Fatalf("GET: %d", rec.Code)
	}
}
