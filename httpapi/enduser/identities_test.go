package enduser_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/code"
	"github.com/bbxx111/accountkit/user/idp"
)

func identityFixture() []user.IdentityInfo {
	return []user.IdentityInfo{
		{ID: "i_0k3f9c2m1xq7a", Kind: enum.IdentityPhone, MaskedSubject: "+86 138****1234", CreateTime: testNow.Add(-2 * time.Hour)},
		{ID: "i_0k3f9c2m1xq7b", Kind: enum.IdentityWeChat, CreateTime: testNow.Add(-time.Hour)},
	}
}

func TestListIdentities(t *testing.T) {
	f := withAuth(&fakeService{listIdentities: func(_ context.Context, uid string) ([]user.IdentityInfo, error) {
		if uid != principal.UserID {
			t.Fatalf("uid %s", uid)
		}
		return identityFixture(), nil
	}}, principal)
	h := newHandler(t, f)
	rec := do(t, h, call{method: "GET", path: "/users/me/identities", bearer: "good"})
	var body struct {
		Identities []map[string]any `json:"identities"`
	}
	decode(t, rec, &body)
	if rec.Code != 200 || len(body.Identities) != 2 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	i0 := body.Identities[0]
	if i0["name"] != "users/"+principal.UserID+"/identities/i_0k3f9c2m1xq7a" || i0["kind"] != "PHONE" || i0["masked_subject"] != "+86 138****1234" || i0["create_time"] != "2026-09-10T10:00:00Z" {
		t.Fatalf("dto: %v", i0)
	}
	for k := range i0 {
		switch k {
		case "name", "kind", "masked_subject", "create_time":
		default:
			t.Fatalf("unexpected field: %s", k)
		}
	}
	if body.Identities[1]["masked_subject"] != "" {
		t.Fatal("wechat masked_subject must be empty")
	}
	// user:bind 可读；user:undelete 不可
	bind := principal
	bind.Scope = "user:bind"
	if rec = do(t, newHandler(t, withAuth(&fakeService{listIdentities: f.listIdentities}, bind)), call{method: "GET", path: "/users/me/identities", bearer: "good"}); rec.Code != 200 {
		t.Fatalf("user:bind must list: %d", rec.Code)
	}
	undel := principal
	undel.Scope = "user:undelete"
	if rec = do(t, newHandler(t, withAuth(&fakeService{}, undel)), call{method: "GET", path: "/users/me/identities", bearer: "good"}); rec.Code != 403 {
		t.Fatalf("user:undelete must be 403: %d", rec.Code)
	}
	if rec = do(t, h, call{method: "GET", path: "/users/me/identities"}); rec.Code != 401 {
		t.Fatal("401")
	}
}

func TestSendBindCode(t *testing.T) {
	var got struct {
		ch     enum.IdentityKind
		target string
	}
	f := withAuth(&fakeService{sendBindCode: func(_ context.Context, p user.Principal, ch enum.IdentityKind, target string, meta user.Meta) (user.CodeChallenge, error) {
		got.ch, got.target = ch, target
		if p.UserID != principal.UserID {
			t.Fatal("principal")
		}
		if target == "+8613800000000" {
			return user.CodeChallenge{}, &code.RateLimitedError{Dimension: "COOLDOWN", RetryAfter: 30 * time.Second}
		}
		return user.CodeChallenge{}, nil
	}}, principal)
	h := newHandler(t, f)
	rec := do(t, h, call{method: "POST", path: "/users/me:sendBindCode", bearer: "good", body: map[string]string{"channel": "EMAIL", "target": "a@b.co"}})
	if rec.Code != 200 || got.ch != enum.IdentityEmail || got.target != "a@b.co" {
		t.Fatalf("send: %d %+v", rec.Code, got)
	}
	rec = do(t, h, call{method: "POST", path: "/users/me:sendBindCode", bearer: "good", body: map[string]string{"channel": "PHONE", "target": "+8613800000000"}})
	if status, body := aipError(t, rec); rec.Code != 429 || status != "RESOURCE_EXHAUSTED/COOLDOWN" || body["retry_after_seconds"] != float64(30) {
		t.Fatalf("cooldown: %d %s", rec.Code, status)
	}
	rec = do(t, h, call{method: "POST", path: "/users/me:sendBindCode", bearer: "good", body: map[string]string{"channel": "WECHAT", "target": "x"}})
	if status, _ := aipError(t, rec); status != "INVALID_ARGUMENT/CHANNEL_INVALID" {
		t.Fatalf("channel: %s", status)
	}
	bind := principal
	bind.Scope = "user:bind"
	if rec = do(t, newHandler(t, withAuth(&fakeService{sendBindCode: f.sendBindCode}, bind)), call{method: "POST", path: "/users/me:sendBindCode", bearer: "good", body: map[string]string{"channel": "EMAIL", "target": "a@b.co"}}); rec.Code != 200 {
		t.Fatalf("user:bind may send bind code: %d", rec.Code)
	}
}

// TestSendBindCodeEmptyClientIPIs500 覆盖 Deps.ClientIP 返回空字符串的情形：
// 这是服务端配置错误，不是客户端错误，必须 500 而不是把空 IP 传给领域层。
func TestSendBindCodeEmptyClientIPIs500(t *testing.T) {
	f := withAuth(&fakeService{}, principal)
	h := newHandlerWithDeps(t, f, func(d *endUserDeps) { d.ClientIP = func(*http.Request) string { return "" } })
	rec := do(t, h, call{method: "POST", path: "/users/me:sendBindCode", bearer: "good", body: map[string]string{"channel": "PHONE", "target": "+8613812341234"}})
	if rec.Code != 500 {
		t.Fatalf("empty client ip must 500: %d %s", rec.Code, rec.Body.String())
	}
}

func TestBindIdentity(t *testing.T) {
	f := withAuth(&fakeService{
		bindWithCode: func(_ context.Context, p user.Principal, cred user.CodeCredential, meta user.Meta) (user.IdentityInfo, bool, error) {
			ch, c := cred.Channel, cred.Code

			switch c {
			case "000000":
				return user.IdentityInfo{}, false, code.ErrInvalid
			case "111111":
				return user.IdentityInfo{}, false, user.ErrIdentityConflict
			case "222222":
				return user.IdentityInfo{}, false, user.ErrIdentityKindLimit
			case "333333":
				return identityFixture()[0], false, nil // 幂等
			}
			return user.IdentityInfo{ID: "i_0k3f9c2m1xq7c", Kind: ch, MaskedSubject: "ba***@b.co", CreateTime: testNow}, true, nil
		},
		bindWithIdp: func(_ context.Context, p user.Principal, cred user.IdpCredential, meta user.Meta) (user.IdentityInfo, bool, error) {
			if cred.Kind == enum.IdentityApple && cred.Nonce == "replay" {
				return user.IdentityInfo{}, false, idp.ErrNonceReplayed
			}
			return user.IdentityInfo{ID: "i_0k3f9c2m1xq7d", Kind: cred.Kind, CreateTime: testNow}, true, nil
		},
	}, principal)
	h := newHandler(t, f)

	rec := do(t, h, call{method: "POST", path: "/users/me/identities", bearer: "good", body: map[string]any{"email": map[string]string{"code_id": "0123456789abcdef0123456789abcdef", "target": "ba@b.co", "code": "123456"}}})
	var res map[string]any
	decode(t, rec, &res)
	if rec.Code != 201 || res["name"] != "users/"+principal.UserID+"/identities/i_0k3f9c2m1xq7c" || res["kind"] != "EMAIL" || res["masked_subject"] != "ba***@b.co" {
		t.Fatalf("bind email: %d %v", rec.Code, res)
	}
	rec = do(t, h, call{method: "POST", path: "/users/me/identities", bearer: "good", body: map[string]any{"phone": map[string]string{"code_id": "0123456789abcdef0123456789abcdef", "target": "+86138", "code": "333333"}}})
	if rec.Code != 200 {
		t.Fatalf("idempotent bind must be 200: %d", rec.Code)
	}
	rec = do(t, h, call{method: "POST", path: "/users/me/identities", bearer: "good", body: map[string]any{"wechat": map[string]string{"app_id": "wx1", "code": "c"}}})
	decode(t, rec, &res)
	if rec.Code != 201 || res["kind"] != "WECHAT" || res["masked_subject"] != "" {
		t.Fatalf("bind wechat: %d %v", rec.Code, res)
	}
	for name, c := range map[string]struct {
		body any
		want string
	}{
		"wrong code": {map[string]any{"phone": map[string]string{"code_id": "0123456789abcdef0123456789abcdef", "target": "+86138", "code": "000000"}}, "400 INVALID_ARGUMENT/CODE_INVALID"},
		"conflict":   {map[string]any{"phone": map[string]string{"code_id": "0123456789abcdef0123456789abcdef", "target": "+86138", "code": "111111"}}, "409 ALREADY_EXISTS/IDENTITY_ALREADY_BOUND"},
		"limit":      {map[string]any{"phone": map[string]string{"code_id": "0123456789abcdef0123456789abcdef", "target": "+86138", "code": "222222"}}, "409 ALREADY_EXISTS/IDENTITY_KIND_LIMIT"},
		"replay":     {map[string]any{"apple": map[string]string{"id_token": "t", "nonce": "replay"}}, "400 INVALID_ARGUMENT/IDP_NONCE_REPLAYED"},
		"none":       {map[string]any{}, "400 INVALID_ARGUMENT/CREDENTIAL_ONEOF"},
		"two":        {map[string]any{"phone": map[string]string{"code_id": "0123456789abcdef0123456789abcdef", "target": "a", "code": "b"}, "wechat": map[string]string{"app_id": "a", "code": "b"}}, "400 INVALID_ARGUMENT/CREDENTIAL_ONEOF"},
		"incomplete": {map[string]any{"email": map[string]string{"code_id": "0123456789abcdef0123456789abcdef", "target": "a@b.co"}}, "400 INVALID_ARGUMENT/CREDENTIAL_INCOMPLETE"},
		"malformed":  {"{", "400 INVALID_ARGUMENT/MALFORMED_BODY"},
	} {
		rec = do(t, h, call{method: "POST", path: "/users/me/identities", bearer: "good", body: c.body})
		status, _ := aipError(t, rec)
		if got := itoa(rec.Code) + " " + status; got != c.want {
			t.Errorf("%s: got %q want %q", name, got, c.want)
		}
		if rec.Header().Get("WWW-Authenticate") != "" {
			t.Errorf("%s: never 401", name)
		}
	}
	// user:bind 可以绑；user:undelete 不行
	bind := principal
	bind.Scope = "user:bind"
	if rec = do(t, newHandler(t, withAuth(&fakeService{bindWithCode: f.bindWithCode}, bind)), call{method: "POST", path: "/users/me/identities", bearer: "good", body: map[string]any{"email": map[string]string{"code_id": "0123456789abcdef0123456789abcdef", "target": "ba@b.co", "code": "123456"}}}); rec.Code != 201 {
		t.Fatalf("user:bind may bind: %d", rec.Code)
	}
}

func TestUnbindIdentityRequiresRecentAuth(t *testing.T) {
	var gotID string
	f := &fakeService{unbindIdentity: func(_ context.Context, p user.Principal, id string, meta user.Meta) error {
		gotID = id
		switch id {
		case "i_0k3f9c2m1xq7z":
			return user.ErrNotFound
		case "i_0k3f9c2m1xq7y":
			return user.ErrLastAnchor
		}
		return nil
	}}
	fresh := principal // AuthTime = testNow-1m，在 5m 内
	stale := principal
	stale.AuthTime = testNow.Add(-10 * time.Minute)
	f.authenticate = func(_ context.Context, raw string) (user.Principal, error) {
		switch raw {
		case "good":
			return fresh, nil
		case "stale":
			return stale, nil
		}
		return user.Principal{}, user.ErrInvalidToken
	}
	h := newHandler(t, f)
	rec := do(t, h, call{method: "DELETE", path: "/users/me/identities/i_0k3f9c2m1xq7a", bearer: "good"})
	if rec.Code != 204 || rec.Body.Len() != 0 || gotID != "i_0k3f9c2m1xq7a" {
		t.Fatalf("delete: %d %q", rec.Code, rec.Body.String())
	}
	rec = do(t, h, call{method: "DELETE", path: "/users/me/identities/i_0k3f9c2m1xq7a", bearer: "stale"})
	if status, _ := aipError(t, rec); rec.Code != 400 || status != "FAILED_PRECONDITION/REAUTHENTICATION_REQUIRED" {
		t.Fatalf("stale auth_time: %d %s", rec.Code, status)
	}
	rec = do(t, h, call{method: "DELETE", path: "/users/me/identities/i_0k3f9c2m1xq7z", bearer: "good"})
	if status, _ := aipError(t, rec); rec.Code != 404 || status != "NOT_FOUND/NOT_FOUND" {
		t.Fatalf("foreign: %d %s", rec.Code, status)
	}
	rec = do(t, h, call{method: "DELETE", path: "/users/me/identities/i_0k3f9c2m1xq7y", bearer: "good"})
	if status, _ := aipError(t, rec); rec.Code != 400 || status != "FAILED_PRECONDITION/LAST_ANCHOR_IDENTITY" {
		t.Fatalf("last anchor: %d %s", rec.Code, status)
	}
	rec = do(t, h, call{method: "DELETE", path: "/users/me/identities/s_0k3f9c2m1xq7a", bearer: "good"})
	if status, _ := aipError(t, rec); rec.Code != 400 || status != "INVALID_ARGUMENT/INVALID_ID" {
		t.Fatalf("wrong prefix: %d %s", rec.Code, status)
	}
	// user:bind 不能解绑（需要 user）
	bind := principal
	bind.Scope = "user:bind"
	h2 := newHandler(t, withAuth(&fakeService{}, bind))
	if rec = do(t, h2, call{method: "DELETE", path: "/users/me/identities/i_0k3f9c2m1xq7a", bearer: "good"}); rec.Code != 403 {
		t.Fatalf("user:bind must not unbind: %d", rec.Code)
	}
	// SensitiveOpVerification=false 时 stale 也放行
	h3 := newHandlerWithDeps(t, f, func(d *endUserDeps) { d.SensitiveOpVerification = false })
	if rec = do(t, h3, call{method: "DELETE", path: "/users/me/identities/i_0k3f9c2m1xq7a", bearer: "stale"}); rec.Code != 204 {
		t.Fatalf("verification off: %d", rec.Code)
	}
}
