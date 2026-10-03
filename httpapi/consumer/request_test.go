package consumer_test

import (
	"testing"
)

func TestRouterSetsRequestIDAndReturns404ForUnknownRoute(t *testing.T) {
	h := newHandler(t, &fakeService{})
	rec := do(t, h, call{method: "GET", path: "/nope"})
	if rec.Code != 404 || rec.Header().Get("X-Request-Id") != "req-test" {
		t.Fatalf("unknown route: %d %q", rec.Code, rec.Header().Get("X-Request-Id"))
	}
	if status, _ := aipError(t, rec); status != "NOT_FOUND/ROUTE_NOT_FOUND" {
		t.Fatalf("404 body must be AIP-193: %s", rec.Body.String())
	}
	rec = do(t, h, call{method: "POST", path: "/users/me:frobnicate"}) // 真正不存在的自定义方法
	if rec.Code != 404 {
		t.Fatalf("unknown custom method: %d", rec.Code)
	}
	if status, _ := aipError(t, rec); status != "NOT_FOUND/ROUTE_NOT_FOUND" {
		t.Fatalf("404 body must be AIP-193: %s", rec.Body.String())
	}
	rec = do(t, h, call{method: "GET", path: "/token"}) // 存在的路径、错误的方法
	if rec.Code != 405 {
		t.Fatalf("method not allowed: %d", rec.Code)
	}
	if status, _ := aipError(t, rec); status != "INVALID_ARGUMENT/METHOD_NOT_ALLOWED" {
		t.Fatalf("405 body: %s", rec.Body.String())
	}
}

func TestNewRejectsMissingDeps(t *testing.T) {
	// 逐项缺失
	if _, err := newHandlerErr(t, func(d *consumerDeps) { d.Users = nil }); err == nil {
		t.Fatal("Users required")
	}
	if _, err := newHandlerErr(t, func(d *consumerDeps) { d.ClientIP = nil }); err == nil {
		t.Fatal("ClientIP required")
	}
	if _, err := newHandlerErr(t, func(d *consumerDeps) { d.RequestID = nil }); err == nil {
		t.Fatal("RequestID required")
	}
	if _, err := newHandlerErr(t, func(d *consumerDeps) { d.ReauthMaxAge = 0 }); err == nil {
		t.Fatal("ReauthMaxAge required")
	}
}
