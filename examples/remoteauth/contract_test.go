package remoteauth_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/examples/remoteauth"
	"github.com/bbxx111/accountkit/httpapi/authn"
	"github.com/bbxx111/accountkit/user"
)

// stubAuth 让库中间件产生与示例同一情形下的响应，用于逐字比较。
type stubAuth struct {
	p   user.Principal
	err error
}

func (s stubAuth) Authenticate(context.Context, string) (user.Principal, error) { return s.p, s.err }

type response struct {
	code            int
	body            string
	challenge, wait string
}

func capture(h http.Handler, authz string) response {
	r := httptest.NewRequest("GET", "/documents/one", nil)
	r.SetPathValue("resource", "one")
	if authz != "" {
		r.Header.Set("Authorization", authz)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return response{w.Code, strings.TrimSpace(w.Body.String()), w.Header().Get("WWW-Authenticate"), w.Header().Get("Retry-After")}
}

// TestExampleErrorsMatchLibraryMiddleware 证明外部业务服务按示例实现时，401/403/400/503 的状态码、
// 错误体、challenge 与 Retry-After 与库的 RequireScope/RequireRecentAuth 逐字一致，客户端无需区分来源。
func TestExampleErrorsMatchLibraryMiddleware(t *testing.T) {
	now := time.Unix(1700000060, 0)
	opts := func(a authn.Authenticator) authn.Options {
		return authn.Options{Auth: a, Now: func() time.Time { return now }}
	}
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	library := func(a authn.Authenticator) http.Handler {
		return authn.RequireScope(opts(a), user.ScopeUser)(authn.RequireRecentAuth(opts(a), 5*time.Minute, true)(ok))
	}
	introspected := func(body string, status int) *remoteauth.Client {
		return clientFor(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		}))
	}
	example := func(c *remoteauth.Client) http.Handler {
		return remoteauth.ProtectedHandler{Client: c, MaxAuthAge: 5 * time.Minute, Now: func() time.Time { return now }, OwnsResource: func(context.Context, string, string) (bool, error) { return true, nil }}
	}
	principal := func(scope string, authTime int64) user.Principal {
		return user.Principal{UserID: "u_0000000000001", SessionID: "s_0000000000001", Scope: scope, AuthTime: time.Unix(authTime, 0)}
	}
	activeBody := func(scope string, authTime int64) string {
		return fmt.Sprintf(`{"active":true,"sub":"u_0000000000001","scope":%q,"sid":"s_0000000000001","auth_time":%d}`, scope, authTime)
	}
	for name, tc := range map[string]struct {
		lib     http.Handler
		ex      http.Handler
		authz   string
		wantSts int
	}{
		"missing bearer":     {library(stubAuth{}), example(introspected(`{"active":false}`, 200)), "", 401},
		"invalid token":      {library(stubAuth{err: user.ErrInvalidToken}), example(introspected(`{"active":false}`, 200)), "Bearer t", 401},
		"insufficient scope": {library(stubAuth{p: principal(user.ScopeBind, 1700000000)}), example(introspected(activeBody(user.ScopeBind, 1700000000), 200)), "Bearer t", 403},
		"stale auth_time":    {library(stubAuth{p: principal(user.ScopeUser, 1699990000)}), example(introspected(activeBody(user.ScopeUser, 1699990000), 200)), "Bearer t", 400},
		"dependency down":    {library(stubAuth{err: user.ErrUnavailable}), example(introspected(`{"error":"temporarily_unavailable"}`, 503)), "Bearer t", 503},
	} {
		lib, ex := capture(tc.lib, tc.authz), capture(tc.ex, tc.authz)
		if lib.code != tc.wantSts || lib != ex {
			t.Errorf("%s:\nlibrary %+v\nexample %+v", name, lib, ex)
		}
	}
}
