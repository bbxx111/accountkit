package enduser_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bbxx111/accountkit/user"
)

func TestTokenEndpoint(t *testing.T) {
	var gotRT string
	f := &fakeService{refresh: func(_ context.Context, rt string, meta user.Meta) (user.TokenResult, error) {
		gotRT = rt
		switch rt {
		case "stale":
			return user.TokenResult{}, user.ErrInvalidGrant
		case "frozen":
			return user.TokenResult{}, user.ErrUserFrozen
		case "down":
			return user.TokenResult{}, user.ErrUnavailable
		case "boom":
			return user.TokenResult{}, errors.New("pg")
		}
		return user.TokenResult{AccessToken: "at2", RefreshToken: "rt2", ExpiresIn: 900, RefreshExpiresIn: 2592000, Scope: "user", UserID: "u_1"}, nil
	}}
	h := newHandler(t, f)

	rec := do(t, h, call{method: "POST", path: "/token", body: map[string]string{"grant_type": "refresh_token", "refresh_token": "rt1"}})
	var tok map[string]any
	decode(t, rec, &tok)
	if rec.Code != 200 || tok["access_token"] != "at2" || tok["refresh_token"] != "rt2" || tok["is_new_user"] != false || rec.Header().Get("Cache-Control") != "no-store" || gotRT != "rt1" {
		t.Fatalf("ok: %d %v", rec.Code, tok)
	}

	type oerr struct {
		Error       string `json:"error"`
		Description string `json:"error_description"`
		Reason      string `json:"reason"`
	}
	check := func(body any, wantCode int, wantErr, wantReason string) {
		t.Helper()
		rec := do(t, h, call{method: "POST", path: "/token", body: body})
		var e oerr
		decode(t, rec, &e)
		if rec.Code != wantCode || e.Error != wantErr || e.Reason != wantReason || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%v: got %d %+v", body, rec.Code, e)
		}
		if rec.Header().Get("WWW-Authenticate") != "" {
			t.Fatalf("%v: /token never challenges", body)
		}
	}
	check(map[string]string{"grant_type": "refresh_token", "refresh_token": "stale"}, 400, "invalid_grant", "")
	check(map[string]string{"grant_type": "refresh_token", "refresh_token": "frozen"}, 403, "invalid_grant", "USER_FROZEN")
	check(map[string]string{"grant_type": "refresh_token", "refresh_token": "down"}, 503, "temporarily_unavailable", "")
	check(map[string]string{"grant_type": "refresh_token", "refresh_token": "boom"}, 500, "server_error", "")
	check(map[string]string{"grant_type": "password", "refresh_token": "x"}, 400, "unsupported_grant_type", "")
	check(map[string]string{"grant_type": "refresh_token"}, 400, "invalid_request", "")
	check(map[string]string{"refresh_token": "x"}, 400, "invalid_request", "")
	check("{", 400, "invalid_request", "")
	check(`{"grant_type":"refresh_token","refresh_token":"x","client_id":"app"}`, 400, "invalid_request", "") // JSON 仍拒绝未知字段
	// 500 不泄露
	rec = do(t, h, call{method: "POST", path: "/token", body: map[string]string{"grant_type": "refresh_token", "refresh_token": "boom"}})
	if s := rec.Body.String(); len(s) > 0 && (contains(s, "pg")) {
		t.Fatalf("leak: %s", s)
	}
}

func TestRevokeEndpoint(t *testing.T) {
	var gotRT string
	f := &fakeService{revoke: func(_ context.Context, rt string, meta user.Meta) error {
		gotRT = rt
		if rt == "down" {
			return user.ErrUnavailable
		}
		return nil // 未知 token 也是 nil（领域层 RFC 7009 语义）
	}}
	h := newHandler(t, f)
	rec := do(t, h, call{method: "POST", path: "/revoke", body: map[string]string{"token": "rt1", "token_type_hint": "refresh_token"}})
	if rec.Code != 200 || rec.Body.Len() != 0 || gotRT != "rt1" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("ok: %d %q", rec.Code, rec.Body.String())
	}
	rec = do(t, h, call{method: "POST", path: "/revoke", body: map[string]string{"token": "rt1"}})
	if rec.Code != 200 {
		t.Fatal("hint optional")
	}
	rec = do(t, h, call{method: "POST", path: "/revoke", body: map[string]string{"token": "rt1", "token_type_hint": "access_token"}})
	if rec.Code != 200 {
		t.Fatal("unknown hint is ignored (RFC 7009 §2.1)")
	}
	rec = do(t, h, call{method: "POST", path: "/revoke", body: map[string]string{"token_type_hint": "refresh_token"}})
	if rec.Code != 400 || !contains(rec.Body.String(), `"invalid_request"`) {
		t.Fatalf("missing token: %d %s", rec.Code, rec.Body.String())
	}
	rec = do(t, h, call{method: "POST", path: "/revoke", body: map[string]string{"token": "down"}})
	if rec.Code != 503 || !contains(rec.Body.String(), `"temporarily_unavailable"`) {
		t.Fatalf("unavailable: %d %s", rec.Code, rec.Body.String())
	}
}

const formType = "application/x-www-form-urlencoded"

// oauthErr 解析 RFC 6749 §5.2 错误体。
func oauthErr(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var e struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil || e.Error == "" {
		t.Fatalf("not an OAuth error body (%d): %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("OAuth error must be no-store: %v", rec.Header())
	}
	return e.Error
}

func TestTokenEndpointForm(t *testing.T) {
	calls := 0
	var gotRT string
	f := &fakeService{refresh: func(_ context.Context, rt string, meta user.Meta) (user.TokenResult, error) {
		calls++
		gotRT = rt
		return user.TokenResult{AccessToken: "at2", RefreshToken: "rt2", ExpiresIn: 900, RefreshExpiresIn: 2592000, Scope: "user", UserID: "u_1"}, nil
	}}
	h := newHandler(t, f)

	ok := func(name, path, body, contentType string) {
		t.Helper()
		calls, gotRT = 0, ""
		rec := do(t, h, call{method: "POST", path: path, body: body, headers: map[string]string{"Content-Type": contentType}})
		var tok map[string]any
		decode(t, rec, &tok)
		if rec.Code != 200 || tok["access_token"] != "at2" || tok["refresh_token"] != "rt2" || rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Pragma") != "no-cache" || gotRT != "rt1" || calls != 1 {
			t.Fatalf("%s: got %d %v calls=%d rt=%q", name, rec.Code, tok, calls, gotRT)
		}
	}
	ok("form", "/token", "grant_type=refresh_token&refresh_token=rt1", formType)
	ok("form with charset", "/token", "refresh_token=rt1&grant_type=refresh_token", formType+"; charset=UTF-8")
	ok("unknown form params ignored (RFC 6749 §3.2)", "/token", "grant_type=refresh_token&refresh_token=rt1&client_id=app&client_secret=", formType)
	ok("percent-encoded value", "/token", "grant_type=refresh_token&refresh_token=rt%31", formType)
	ok("malformed query string is ignored, only the body counts", "/token?%zz&a=1;b=2", "grant_type=refresh_token&refresh_token=rt1", formType)

	bad := func(name, path, body, contentType, wantErr string) {
		t.Helper()
		calls = 0
		headers := map[string]string{}
		if contentType != "" {
			headers["Content-Type"] = contentType
		}
		rec := do(t, h, call{method: "POST", path: path, body: body, headers: headers})
		if got := oauthErr(t, rec); rec.Code != 400 || got != wantErr || calls != 0 {
			t.Fatalf("%s: got %d %q calls=%d body=%s", name, rec.Code, got, calls, rec.Body.String())
		}
	}
	bad("duplicate refresh_token", "/token", "grant_type=refresh_token&refresh_token=rt1&refresh_token=rt1", formType, "invalid_request")
	bad("duplicate grant_type", "/token", "grant_type=refresh_token&grant_type=refresh_token&refresh_token=rt1", formType, "invalid_request")
	bad("duplicate unrecognized parameter (RFC 6749 §3.2: no parameter more than once)", "/token", "grant_type=refresh_token&refresh_token=rt1&client_id=a&client_id=b", formType, "invalid_request")
	bad("semicolon separator in body", "/token", "grant_type=refresh_token;refresh_token=rt1", formType, "invalid_request")
	bad("query string is not the body", "/token?grant_type=refresh_token&refresh_token=rt1", "", formType, "invalid_request")
	bad("query string does not complete a partial body", "/token?refresh_token=rt1", "grant_type=refresh_token", formType, "invalid_request")
	bad("missing refresh_token", "/token", "grant_type=refresh_token", formType, "invalid_request")
	bad("wrong grant_type", "/token", "grant_type=password&refresh_token=rt1", formType, "unsupported_grant_type")
	bad("bad percent encoding", "/token", "grant_type=refresh_token&refresh_token=%zz", formType, "invalid_request")
	bad("no content type", "/token", "grant_type=refresh_token&refresh_token=rt1", "", "invalid_request")
	bad("text/plain", "/token", "grant_type=refresh_token&refresh_token=rt1", "text/plain", "invalid_request")
	bad("json body labelled as form", "/token", `{"grant_type":"refresh_token","refresh_token":"rt1"}`, formType, "invalid_request")
	bad("form body labelled as json", "/token", "grant_type=refresh_token&refresh_token=rt1", "application/json", "invalid_request")
	bad("multipart", "/token", "grant_type=refresh_token&refresh_token=rt1", "multipart/form-data; boundary=x", "invalid_request")

	// 超过 MaxBodyBytes：与 JSON 路径同一上限，归为 invalid_request 而不是 500。
	small := newHandlerWithDeps(t, f, func(d *endUserDeps) { d.MaxBodyBytes = 48 })
	calls = 0
	rec := do(t, small, call{method: "POST", path: "/token", body: "grant_type=refresh_token&refresh_token=" + strings.Repeat("r", 64), headers: map[string]string{"Content-Type": formType}})
	if got := oauthErr(t, rec); rec.Code != 400 || got != "invalid_request" || calls != 0 {
		t.Fatalf("oversize form: got %d %q calls=%d", rec.Code, got, calls)
	}
	rec = do(t, small, call{method: "POST", path: "/token", body: "grant_type=refresh_token&refresh_token=rt1", headers: map[string]string{"Content-Type": formType}})
	if rec.Code != 200 {
		t.Fatalf("form within limit must still work: %d %s", rec.Code, rec.Body.String())
	}
}

func TestTokenEndpointScope(t *testing.T) {
	calls := 0
	f := &fakeService{refresh: func(_ context.Context, rt string, meta user.Meta) (user.TokenResult, error) {
		calls++
		return user.TokenResult{AccessToken: "at2", RefreshToken: "rt2", ExpiresIn: 900, RefreshExpiresIn: 2592000, Scope: "user", UserID: "u_1"}, nil
	}}
	h := newHandler(t, f)
	// RFC 6749 §3.3：服务端可忽略请求 scope，签发 scope 以响应为准。携带任何 scope（已知、未知、多值、空、
	// 任一编码）都与不传一致：200、领域层刷新一次、响应 scope 为推导值。
	for name, c := range map[string]call{
		"form same":    {method: "POST", path: "/token", body: "grant_type=refresh_token&refresh_token=rt1&scope=user", headers: map[string]string{"Content-Type": formType}},
		"form other":   {method: "POST", path: "/token", body: "grant_type=refresh_token&refresh_token=rt1&scope=user%3Abind", headers: map[string]string{"Content-Type": formType}},
		"form unknown": {method: "POST", path: "/token", body: "grant_type=refresh_token&refresh_token=rt1&scope=admin+openid", headers: map[string]string{"Content-Type": formType}},
		"form empty":   {method: "POST", path: "/token", body: "grant_type=refresh_token&refresh_token=rt1&scope=", headers: map[string]string{"Content-Type": formType}},
		"json same":    {method: "POST", path: "/token", body: `{"grant_type":"refresh_token","refresh_token":"rt1","scope":"user"}`},
		"json unknown": {method: "POST", path: "/token", body: `{"grant_type":"refresh_token","refresh_token":"rt1","scope":"admin openid"}`},
		"json empty":   {method: "POST", path: "/token", body: `{"grant_type":"refresh_token","refresh_token":"rt1","scope":""}`},
	} {
		calls = 0
		rec := do(t, h, c)
		var tok map[string]any
		decode(t, rec, &tok)
		if rec.Code != 200 || calls != 1 || tok["scope"] != "user" || tok["access_token"] != "at2" {
			t.Fatalf("%s: got %d calls=%d body=%s", name, rec.Code, calls, rec.Body.String())
		}
	}
	// 重复的 scope 参数仍按 §3.2 "参数不得重复" 拒绝，与其他参数一致。
	calls = 0
	rec := do(t, h, call{method: "POST", path: "/token", body: "grant_type=refresh_token&refresh_token=rt1&scope=user&scope=user", headers: map[string]string{"Content-Type": formType}})
	if got := oauthErr(t, rec); rec.Code != 400 || got != "invalid_request" || calls != 0 {
		t.Fatalf("duplicate scope: got %d %q calls=%d", rec.Code, got, calls)
	}
}

func TestRevokeEndpointForm(t *testing.T) {
	calls := 0
	var gotRT string
	f := &fakeService{revoke: func(_ context.Context, rt string, meta user.Meta) error {
		calls++
		gotRT = rt
		return nil
	}}
	h := newHandler(t, f)
	form := func(body string) *httptest.ResponseRecorder {
		return do(t, h, call{method: "POST", path: "/revoke", body: body, headers: map[string]string{"Content-Type": formType + "; charset=utf-8"}})
	}
	rec := form("token=rt1&token_type_hint=refresh_token")
	if rec.Code != 200 || rec.Body.Len() != 0 || gotRT != "rt1" || calls != 1 || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("form revoke: %d %q calls=%d rt=%q", rec.Code, rec.Body.String(), calls, gotRT)
	}
	calls = 0
	if rec = form("token=unknown&client_id=app"); rec.Code != 200 || rec.Body.Len() != 0 || gotRT != "unknown" || calls != 1 {
		t.Fatalf("unknown token still 200, unknown params ignored: %d %q", rec.Code, rec.Body.String())
	}
	calls = 0
	if rec = form("token=rt1&token=rt1"); oauthErr(t, rec) != "invalid_request" || rec.Code != 400 || calls != 0 {
		t.Fatalf("duplicate token: %d %s", rec.Code, rec.Body.String())
	}
	if rec = form("token_type_hint=refresh_token"); oauthErr(t, rec) != "invalid_request" || rec.Code != 400 || calls != 0 {
		t.Fatalf("missing token: %d %s", rec.Code, rec.Body.String())
	}
	rec = do(t, h, call{method: "POST", path: "/revoke", body: "token=rt1", headers: map[string]string{"Content-Type": "text/plain"}})
	if oauthErr(t, rec) != "invalid_request" || rec.Code != 400 || calls != 0 {
		t.Fatalf("text/plain: %d %s", rec.Code, rec.Body.String())
	}
}

// 表单能力只对两个 OAuth 端点开放，其他端点仍是严格 JSON。
func TestNonOAuthEndpointRejectsForm(t *testing.T) {
	h := newHandler(t, &fakeService{})
	rec := do(t, h, call{method: "POST", path: "/users:signInWithCode", body: "kind=PHONE&credential=x", headers: map[string]string{"Content-Type": formType, "X-Device-Id": "dev-1"}})
	if code, _ := aipError(t, rec); rec.Code != 400 || code != "INVALID_ARGUMENT/MALFORMED_BODY" {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
	rec = do(t, h, call{method: "POST", path: "/users:sendSignInCode", body: "channel=PHONE&target=%2B8613800000000", headers: map[string]string{"Content-Type": formType}})
	if code, _ := aipError(t, rec); rec.Code != 400 || code != "INVALID_ARGUMENT/MALFORMED_BODY" {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
}
