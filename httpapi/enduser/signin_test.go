package enduser_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/code"
	"github.com/bbxx111/accountkit/user/idp"
)

func TestSendSignInCode(t *testing.T) {
	var got struct {
		ch     enum.IdentityKind
		target string
		meta   user.Meta
	}
	f := &fakeService{sendSignInCode: func(_ context.Context, ch enum.IdentityKind, target string, meta user.Meta) error {
		got.ch, got.target, got.meta = ch, target, meta
		if target == "" {
			return user.ErrInvalidTarget // 领域层归一化失败的行为
		}
		if target == "+8613800000000" {
			return &code.RateLimitedError{Dimension: "COOLDOWN", RetryAfter: 42 * time.Second}
		}
		if target == "down" {
			return user.ErrUnavailable
		}
		return nil
	}}
	h := newHandler(t, f)

	rec := do(t, h, call{method: "POST", path: "/users:sendSignInCode", body: map[string]string{"channel": "PHONE", "target": "+8613812341234"}})
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != "{}" || rec.Header().Get("X-Request-Id") != "req-test" {
		t.Fatalf("ok: %d %s", rec.Code, rec.Body.String())
	}
	if got.ch != enum.IdentityPhone || got.target != "+8613812341234" || got.meta.IP != "203.0.113.9" || got.meta.RequestID != "req-test" {
		t.Fatalf("service args: %+v", got)
	}
	rec = do(t, h, call{method: "POST", path: "/users:sendSignInCode", body: map[string]string{"channel": "EMAIL", "target": "a@b.co"}})
	if rec.Code != 200 || got.ch != enum.IdentityEmail {
		t.Fatal("email channel")
	}

	rec = do(t, h, call{method: "POST", path: "/users:sendSignInCode", body: map[string]string{"channel": "PHONE", "target": "+8613800000000"}})
	status, body := aipError(t, rec)
	if rec.Code != 429 || status != "RESOURCE_EXHAUSTED/COOLDOWN" || body["retry_after_seconds"] != float64(42) || rec.Header().Get("Retry-After") != "42" {
		t.Fatalf("rate limited: %d %s %v", rec.Code, status, body)
	}
	rec = do(t, h, call{method: "POST", path: "/users:sendSignInCode", body: map[string]string{"channel": "PHONE", "target": "down"}})
	if status, _ := aipError(t, rec); rec.Code != 503 || status != "UNAVAILABLE/DEPENDENCY_UNAVAILABLE" {
		t.Fatalf("unavailable: %d %s", rec.Code, status)
	}

	for name, body := range map[string]any{
		"empty":         "",
		"not json":      "{",
		"unknown field": `{"channel":"PHONE","target":"x","extra":1}`,
		"two objects":   `{"channel":"PHONE","target":"x"}{}`,
		"array":         `[]`,
		"null":          `null`,
	} {
		rec = do(t, h, call{method: "POST", path: "/users:sendSignInCode", body: body})
		if status, _ := aipError(t, rec); rec.Code != 400 || status != "INVALID_ARGUMENT/MALFORMED_BODY" {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	rec = do(t, h, call{method: "POST", path: "/users:sendSignInCode", body: map[string]string{"channel": "WECHAT", "target": "x"}})
	if status, _ := aipError(t, rec); rec.Code != 400 || status != "INVALID_ARGUMENT/CHANNEL_INVALID" {
		t.Fatalf("channel: %d %s", rec.Code, status)
	}
	rec = do(t, h, call{method: "POST", path: "/users:sendSignInCode", body: map[string]string{"channel": "PHONE"}})
	if status, _ := aipError(t, rec); rec.Code != 400 || status != "INVALID_ARGUMENT/INVALID_TARGET" {
		t.Fatalf("empty target: %d %s", rec.Code, status)
	}
	// 超过 64 KiB
	big := `{"channel":"PHONE","target":"` + strings.Repeat("9", 70*1024) + `"}`
	rec = do(t, h, call{method: "POST", path: "/users:sendSignInCode", body: big})
	if status, _ := aipError(t, rec); rec.Code != 400 || status != "INVALID_ARGUMENT/MALFORMED_BODY" {
		t.Fatalf("oversize: %d %s", rec.Code, status)
	}
}

// TestSendSignInCodeEmptyClientIPIs500 覆盖 Deps.ClientIP 返回空字符串的情形：
// 这是服务端配置错误，不是客户端错误，必须 500 而不是把空 IP 传给领域层。
func TestSendSignInCodeEmptyClientIPIs500(t *testing.T) {
	hh, err := newHandlerErr(t, func(d *endUserDeps) { d.ClientIP = func(*http.Request) string { return "" } })
	if err != nil {
		t.Fatal(err)
	}
	h := hh.Router()
	rec := do(t, h, call{method: "POST", path: "/users:sendSignInCode", body: map[string]string{"channel": "PHONE", "target": "+8613812341234"}})
	if rec.Code != 500 {
		t.Fatalf("empty client ip must 500: %d %s", rec.Code, rec.Body.String())
	}
}

func TestSignInWithCode(t *testing.T) {
	var got struct {
		ch           enum.IdentityKind
		target, code string
		dev          user.Device
	}
	f := &fakeService{signInWithCode: func(_ context.Context, ch enum.IdentityKind, target, c string, dev user.Device, meta user.Meta) (user.TokenResult, error) {
		got.ch, got.target, got.code, got.dev = ch, target, c, dev
		switch c {
		case "000000":
			return user.TokenResult{}, code.ErrInvalid
		case "111111":
			return user.TokenResult{}, code.ErrExpired
		case "222222":
			return user.TokenResult{}, code.ErrExhausted
		case "333333":
			return user.TokenResult{}, user.ErrUserFrozen
		case "444444":
			return user.TokenResult{}, errors.New("pg: down")
		}
		return user.TokenResult{AccessToken: "at", RefreshToken: "rt", ExpiresIn: 900, RefreshExpiresIn: 2592000, Scope: "user", UserID: "u_1", IsNewUser: true}, nil
	}}
	h := newHandler(t, f)
	body := map[string]any{"phone": map[string]string{"target": "+8613812341234", "code": "123456"}}

	rec := do(t, h, call{method: "POST", path: "/users:signInWithCode", body: body, headers: devHeaders()})
	var tok map[string]any
	decode(t, rec, &tok)
	if rec.Code != 200 || tok["access_token"] != "at" || tok["token_type"] != "Bearer" || tok["expires_in"] != float64(900) || tok["refresh_token"] != "rt" || tok["refresh_expires_in"] != float64(2592000) || tok["scope"] != "user" || tok["user_id"] != "u_1" || tok["is_new_user"] != true {
		t.Fatalf("token response: %d %v", rec.Code, tok)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("token responses must be no-store")
	}
	if got.ch != enum.IdentityPhone || got.target != "+8613812341234" || got.code != "123456" || got.dev.ID != devHeaders()["X-Device-Id"] || got.dev.Name != "Pixel 9" {
		t.Fatalf("args: %+v", got)
	}

	// 设备头
	rec = do(t, h, call{method: "POST", path: "/users:signInWithCode", body: body})
	if status, _ := aipError(t, rec); rec.Code != 400 || status != "INVALID_ARGUMENT/DEVICE_ID_INVALID" {
		t.Fatalf("no device: %d %s", rec.Code, status)
	}
	rec = do(t, h, call{method: "POST", path: "/users:signInWithCode", body: body, headers: map[string]string{"X-Device-Id": "ok", "X-Device-Name": strings.Repeat("x", 65)}})
	if status, _ := aipError(t, rec); status != "INVALID_ARGUMENT/DEVICE_NAME_INVALID" {
		t.Fatalf("device name: %s", status)
	}

	// 凭证形状
	for name, b := range map[string]any{
		"none": map[string]any{},
		"both": map[string]any{"phone": map[string]string{"target": "a", "code": "b"}, "email": map[string]string{"target": "a", "code": "b"}},
	} {
		rec = do(t, h, call{method: "POST", path: "/users:signInWithCode", body: b, headers: devHeaders()})
		if status, _ := aipError(t, rec); rec.Code != 400 || status != "INVALID_ARGUMENT/CREDENTIAL_ONEOF" {
			t.Fatalf("%s: %d %s", name, rec.Code, status)
		}
	}
	rec = do(t, h, call{method: "POST", path: "/users:signInWithCode", body: map[string]any{"wechat": map[string]string{"app_id": "wx", "code": "c"}}, headers: devHeaders()})
	if status, _ := aipError(t, rec); status != "INVALID_ARGUMENT/CREDENTIAL_KIND_NOT_ALLOWED" {
		t.Fatalf("wechat here: %s", status)
	}
	rec = do(t, h, call{method: "POST", path: "/users:signInWithCode", body: map[string]any{"email": map[string]string{"target": "a@b.co"}}, headers: devHeaders()})
	if status, _ := aipError(t, rec); status != "INVALID_ARGUMENT/CREDENTIAL_INCOMPLETE" {
		t.Fatalf("incomplete: %s", status)
	}

	// 领域错误映射（码错不是 401）
	for c, want := range map[string]string{"000000": "400 INVALID_ARGUMENT/CODE_INVALID", "111111": "400 INVALID_ARGUMENT/CODE_EXPIRED", "222222": "400 INVALID_ARGUMENT/CODE_ATTEMPTS_EXHAUSTED", "333333": "403 PERMISSION_DENIED/USER_FROZEN", "444444": "500 INTERNAL/"} {
		rec = do(t, h, call{method: "POST", path: "/users:signInWithCode", body: map[string]any{"phone": map[string]string{"target": "+8613812341234", "code": c}}, headers: devHeaders()})
		status, _ := aipError(t, rec)
		if got := itoa(rec.Code) + " " + status; got != want {
			t.Fatalf("code %s: got %q want %q", c, got, want)
		}
		if rec.Header().Get("WWW-Authenticate") != "" {
			t.Fatalf("code %s: must not be a 401-style challenge", c)
		}
	}
}

func TestSignInWithIdp(t *testing.T) {
	var got struct {
		cred user.IdpCredential
		dev  user.Device
	}
	f := &fakeService{signInWithIdp: func(_ context.Context, cred user.IdpCredential, dev user.Device, meta user.Meta) (user.TokenResult, error) {
		got.cred, got.dev = cred, dev
		switch {
		case cred.Kind == enum.IdentityWeChat && cred.Code == "bad":
			return user.TokenResult{}, idp.ErrInvalidCredential
		case cred.Kind == enum.IdentityWeChat && cred.AppID == "wx-no":
			return user.TokenResult{}, idp.ErrAppNotAllowed
		case cred.Kind == enum.IdentityApple && cred.Nonce == "replay":
			return user.TokenResult{}, idp.ErrNonceReplayed
		case cred.Kind == enum.IdentityWeChat && cred.Code == "down":
			return user.TokenResult{}, idp.ErrUnavailable
		case cred.Kind == enum.IdentityWeChat && cred.Code == "miscfg":
			return user.TokenResult{}, idp.ErrMisconfigured
		case cred.Kind == enum.IdentityWeChat && cred.Code == "frozen":
			return user.TokenResult{}, user.ErrUserFrozen
		}
		res := user.TokenResult{AccessToken: "at", RefreshToken: "rt", ExpiresIn: 900, RefreshExpiresIn: 2592000, Scope: "user:bind", UserID: "u_1", IsNewUser: true}
		if cred.Kind == enum.IdentityApple {
			res.HintEmail = "x@privaterelay.appleid.com"
		}
		return res, nil
	}}
	h := newHandler(t, f)

	rec := do(t, h, call{method: "POST", path: "/users:signInWithIdp", body: map[string]any{"wechat": map[string]string{"app_id": "wx1", "code": "c1"}}, headers: devHeaders()})
	var tok map[string]any
	decode(t, rec, &tok)
	if rec.Code != 200 || tok["scope"] != "user:bind" || tok["is_new_user"] != true || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("wechat: %d %v", rec.Code, tok)
	}
	if _, has := tok["hint_email"]; has {
		t.Fatal("wechat response must omit hint_email")
	}
	if got.cred.Kind != enum.IdentityWeChat || got.cred.AppID != "wx1" || got.cred.Code != "c1" || got.dev.ID != devHeaders()["X-Device-Id"] {
		t.Fatalf("args: %+v", got)
	}
	rec = do(t, h, call{method: "POST", path: "/users:signInWithIdp", body: map[string]any{"apple": map[string]string{"id_token": "t", "nonce": "n"}}, headers: devHeaders()})
	decode(t, rec, &tok)
	if rec.Code != 200 || tok["hint_email"] != "x@privaterelay.appleid.com" || got.cred.Kind != enum.IdentityApple || got.cred.IDToken != "t" || got.cred.Nonce != "n" {
		t.Fatalf("apple: %d %v %+v", rec.Code, tok, got)
	}

	// 设备头与凭证形状
	rec = do(t, h, call{method: "POST", path: "/users:signInWithIdp", body: map[string]any{"wechat": map[string]string{"app_id": "wx1", "code": "c1"}}})
	if status, _ := aipError(t, rec); status != "INVALID_ARGUMENT/DEVICE_ID_INVALID" {
		t.Fatalf("no device: %s", status)
	}
	rec = do(t, h, call{method: "POST", path: "/users:signInWithIdp", body: map[string]any{"phone": map[string]string{"target": "+86138", "code": "1"}}, headers: devHeaders()})
	if status, _ := aipError(t, rec); status != "INVALID_ARGUMENT/CREDENTIAL_KIND_NOT_ALLOWED" {
		t.Fatalf("phone here: %s", status)
	}
	rec = do(t, h, call{method: "POST", path: "/users:signInWithIdp", body: map[string]any{"wechat": map[string]string{"app_id": "wx1"}}, headers: devHeaders()})
	if status, _ := aipError(t, rec); status != "INVALID_ARGUMENT/CREDENTIAL_INCOMPLETE" {
		t.Fatalf("incomplete: %s", status)
	}
	rec = do(t, h, call{method: "POST", path: "/users:signInWithIdp", body: `{"wechat":null,"apple":null}`, headers: devHeaders()})
	if status, _ := aipError(t, rec); status != "INVALID_ARGUMENT/CREDENTIAL_ONEOF" {
		t.Fatalf("nulls: %s", status)
	}

	// 领域错误映射
	for name, c := range map[string]struct {
		body any
		want string
	}{
		"invalid": {map[string]any{"wechat": map[string]string{"app_id": "wx1", "code": "bad"}}, "400 INVALID_ARGUMENT/IDP_CREDENTIAL_INVALID"},
		"app":     {map[string]any{"wechat": map[string]string{"app_id": "wx-no", "code": "c"}}, "400 INVALID_ARGUMENT/IDP_APP_NOT_ALLOWED"},
		"replay":  {map[string]any{"apple": map[string]string{"id_token": "t", "nonce": "replay"}}, "400 INVALID_ARGUMENT/IDP_NONCE_REPLAYED"},
		"down":    {map[string]any{"wechat": map[string]string{"app_id": "wx1", "code": "down"}}, "503 UNAVAILABLE/IDP_UNAVAILABLE"},
		"miscfg":  {map[string]any{"wechat": map[string]string{"app_id": "wx1", "code": "miscfg"}}, "500 INTERNAL/"},
		"frozen":  {map[string]any{"wechat": map[string]string{"app_id": "wx1", "code": "frozen"}}, "403 PERMISSION_DENIED/USER_FROZEN"},
	} {
		rec = do(t, h, call{method: "POST", path: "/users:signInWithIdp", body: c.body, headers: devHeaders()})
		status, _ := aipError(t, rec)
		if got := itoa(rec.Code) + " " + status; got != c.want {
			t.Errorf("%s: got %q want %q", name, got, c.want)
		}
		if rec.Header().Get("WWW-Authenticate") != "" {
			t.Errorf("%s: never a 401 challenge", name)
		}
		if name == "down" && rec.Header().Get("Retry-After") != "1" {
			t.Errorf("503 must carry Retry-After: 1")
		}
		if name == "miscfg" && contains(rec.Body.String(), "misconfigured") {
			t.Errorf("500 must not leak the misconfiguration detail")
		}
	}
}

func TestSignInWithIdpPendingDeletionIs403(t *testing.T) {
	f := &fakeService{signInWithIdp: func(context.Context, user.IdpCredential, user.Device, user.Meta) (user.TokenResult, error) {
		return user.TokenResult{}, user.ErrUserPendingDeletion
	}}
	h := newHandler(t, f)
	rec := do(t, h, call{method: "POST", path: "/users:signInWithIdp", body: map[string]any{"wechat": map[string]string{"app_id": "wx1", "code": "c1"}}, headers: devHeaders()})
	if status, _ := aipError(t, rec); rec.Code != 403 || status != "PERMISSION_DENIED/USER_PENDING_DELETION" {
		t.Fatalf("pending deletion via idp: %d %s", rec.Code, status)
	}
}
