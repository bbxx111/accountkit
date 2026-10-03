package authn_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/httpapi/authn"
	"github.com/bbxx111/accountkit/user"
)

type fakeAuth struct {
	tokens map[string]user.Principal
	err    error
}

func (f fakeAuth) Authenticate(_ context.Context, raw string) (user.Principal, error) {
	if f.err != nil {
		return user.Principal{}, f.err
	}
	p, ok := f.tokens[raw]
	if !ok {
		return user.Principal{}, fmt.Errorf("%w: unknown token", user.ErrInvalidToken)
	}
	return p, nil
}

var now = time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

func opts(a authn.Authenticator) authn.Options {
	return authn.Options{Auth: a, Now: func() time.Time { return now }}
}

func echo() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := authn.PrincipalFrom(r.Context())
		if !ok {
			http.Error(w, "no principal", 500)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"uid": p.UserID, "scope": p.Scope})
	})
}

func do(h http.Handler, authz string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "/x", nil)
	if authz != "" {
		req.Header.Set("Authorization", authz)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func reason(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var b struct {
		Error struct {
			Status string `json:"status"`
			Reason string `json:"reason"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatalf("body not AIP-193 json: %s", rec.Body.String())
	}
	return b.Error.Status + "/" + b.Error.Reason
}

func TestBearerMissingAndInvalid(t *testing.T) {
	a := fakeAuth{tokens: map[string]user.Principal{"good": {UserID: "u_1", SessionID: "s_1", Scope: "user", AuthTime: now}}}
	h := authn.Bearer(opts(a))(echo())

	rec := do(h, "")
	if rec.Code != 401 || rec.Header().Get("WWW-Authenticate") != `Bearer realm="user"` || reason(t, rec) != "UNAUTHENTICATED/TOKEN_MISSING" {
		t.Fatalf("missing: %d %q %s", rec.Code, rec.Header().Get("WWW-Authenticate"), rec.Body.String())
	}
	for _, hdr := range []string{"Basic abc", "Bearer", "Bearer ", "bearer good extra"} {
		rec = do(h, hdr)
		if rec.Code != 401 || reason(t, rec) != "UNAUTHENTICATED/TOKEN_MISSING" {
			t.Fatalf("malformed %q: %d %s", hdr, rec.Code, rec.Body.String())
		}
	}
	rec = do(h, "Bearer bad")
	if rec.Code != 401 || rec.Header().Get("WWW-Authenticate") != `Bearer realm="user", error="invalid_token"` || reason(t, rec) != "UNAUTHENTICATED/TOKEN_INVALID" {
		t.Fatalf("invalid: %d %q %s", rec.Code, rec.Header().Get("WWW-Authenticate"), rec.Body.String())
	}
	rec = do(h, "Bearer good")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"uid":"u_1"`) {
		t.Fatalf("good: %d %s", rec.Code, rec.Body.String())
	}
	// 大小写不敏感的 scheme
	if rec = do(h, "bearer good"); rec.Code != 200 {
		t.Fatalf("scheme must be case-insensitive: %d", rec.Code)
	}
}

func TestBearerUnavailableIs503AndSkipsWhenAlreadyAuthenticated(t *testing.T) {
	h := authn.Bearer(opts(fakeAuth{err: user.ErrUnavailable}))(echo())
	rec := do(h, "Bearer any")
	if rec.Code != 503 || reason(t, rec) != "UNAVAILABLE/DEPENDENCY_UNAVAILABLE" || rec.Header().Get("Retry-After") != "1" {
		t.Fatalf("unavailable: %d %s retry=%q", rec.Code, rec.Body.String(), rec.Header().Get("Retry-After"))
	}
	// 已有 Principal（例如宿主更外层已认证）→ 不再调用 Authenticator
	req := httptest.NewRequest("GET", "/x", nil)
	req = req.WithContext(authn.WithPrincipal(req.Context(), user.Principal{UserID: "u_pre", Scope: "user"}))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "u_pre") {
		t.Fatalf("pre-authenticated must pass: %d %s", rec.Code, rec.Body.String())
	}
}

func TestRequireScope(t *testing.T) {
	a := fakeAuth{tokens: map[string]user.Principal{
		"full": {UserID: "u_1", Scope: "user", AuthTime: now},
		"bind": {UserID: "u_2", Scope: "user:bind", AuthTime: now},
	}}
	h := authn.RequireScope(opts(a), "user")(echo())
	if rec := do(h, "Bearer full"); rec.Code != 200 {
		t.Fatalf("user scope must pass: %d", rec.Code)
	}
	rec := do(h, "Bearer bind")
	if rec.Code != 403 || reason(t, rec) != "PERMISSION_DENIED/INSUFFICIENT_SCOPE" || rec.Header().Get("WWW-Authenticate") != "" {
		t.Fatalf("insufficient: %d %s", rec.Code, rec.Body.String())
	}
	if rec = do(h, ""); rec.Code != 401 {
		t.Fatalf("RequireScope must authenticate first: %d", rec.Code)
	}
	multi := authn.RequireScope(opts(a), "user", "user:bind")(echo())
	if rec = do(multi, "Bearer bind"); rec.Code != 200 {
		t.Fatalf("allowed list: %d", rec.Code)
	}
}

func TestRequireRecentAuth(t *testing.T) {
	a := fakeAuth{tokens: map[string]user.Principal{
		"fresh": {UserID: "u_1", Scope: "user", AuthTime: now.Add(-4 * time.Minute)},
		"stale": {UserID: "u_1", Scope: "user", AuthTime: now.Add(-5*time.Minute - time.Second)},
		"edge":  {UserID: "u_1", Scope: "user", AuthTime: now.Add(-5 * time.Minute)},
		"zero":  {UserID: "u_1", Scope: "user"}, // auth_time 缺失/解码为零值 → 视为过期（fail-safe）
	}}
	o := opts(a)
	chain := func(enabled bool) http.Handler {
		return authn.Bearer(o)(authn.RequireRecentAuth(o, 5*time.Minute, enabled)(echo()))
	}
	h := chain(true)
	if rec := do(h, "Bearer fresh"); rec.Code != 200 {
		t.Fatalf("fresh: %d", rec.Code)
	}
	if rec := do(h, "Bearer edge"); rec.Code != 200 {
		t.Fatalf("exactly maxAge must pass: %d", rec.Code)
	}
	for _, tok := range []string{"stale", "zero"} {
		rec := do(h, "Bearer "+tok)
		if rec.Code != 400 || reason(t, rec) != "FAILED_PRECONDITION/REAUTHENTICATION_REQUIRED" {
			t.Fatalf("%s: %d %s", tok, rec.Code, rec.Body.String())
		}
	}
	if rec := do(chain(false), "Bearer stale"); rec.Code != 200 {
		t.Fatalf("disabled must pass: %d", rec.Code)
	}
	// 未经 Bearer 直接到达 → 401 TOKEN_MISSING（enabled=true 与 enabled=false 都必须先认证）
	bare := authn.RequireRecentAuth(o, 5*time.Minute, true)(echo())
	if rec := do(bare, ""); rec.Code != 401 || reason(t, rec) != "UNAUTHENTICATED/TOKEN_MISSING" {
		t.Fatalf("unauthenticated: %d %s", rec.Code, rec.Body.String())
	}
	bareDisabled := authn.RequireRecentAuth(o, 5*time.Minute, false)(echo())
	if rec := do(bareDisabled, ""); rec.Code != 401 || reason(t, rec) != "UNAUTHENTICATED/TOKEN_MISSING" {
		t.Fatalf("disabled but unauthenticated must still 401: %d %s", rec.Code, rec.Body.String())
	}
}

func TestPrincipalFromEmpty(t *testing.T) {
	if _, ok := authn.PrincipalFrom(context.Background()); ok {
		t.Fatal("empty ctx must not carry a principal")
	}
}
