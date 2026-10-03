package consumer_test

import (
	"context"
	"errors"
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
	check(`{"grant_type":"refresh_token","refresh_token":"x","scope":"user"}`, 400, "invalid_request", "") // 未知字段
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
