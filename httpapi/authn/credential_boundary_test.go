package authn_test

import (
	"context"
	"github.com/bbxx111/accountkit/httpapi/authn"
	"github.com/bbxx111/accountkit/user"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type countingAuth struct{ calls int }

func (a *countingAuth) Authenticate(_ context.Context, _ string) (user.Principal, error) {
	a.calls++
	return user.Principal{UserID: "u_test", Scope: "user"}, nil
}
func TestBearerCredentialBoundary(t *testing.T) {
	for _, tc := range []struct {
		name    string
		headers []string
		valid   bool
	}{
		{"duplicate", []string{"Bearer good", "Bearer good"}, false},
		{"mixed", []string{"Bearer good", "Basic abc"}, false},
		{"case spaces", []string{"bEaReR   good"}, true},
		{"limit", []string{"Bearer " + strings.Repeat("a", 16384)}, true},
		{"over limit", []string{"Bearer " + strings.Repeat("a", 16385)}, false},
		{"tab", []string{"Bearer \tgood"}, false},
		{"newline", []string{"Bearer good\n"}, false},
		{"comma", []string{"Bearer good,other"}, false},
		{"empty", []string{"Bearer "}, false},
		{"embedded", []string{"Bearer good other"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &countingAuth{}
			reached := false
			h := authn.Bearer(opts(a))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { reached = true; w.WriteHeader(204) }))
			r := httptest.NewRequest("GET", "/", nil)
			r.Header["Authorization"] = tc.headers
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if tc.valid {
				if w.Code != 204 || a.calls != 1 || !reached {
					t.Fatalf("status=%d calls=%d reached=%v", w.Code, a.calls, reached)
				}
			} else {
				if w.Code != 401 || a.calls != 0 || reached {
					t.Fatalf("status=%d calls=%d reached=%v", w.Code, a.calls, reached)
				}
				if w.Header().Get("WWW-Authenticate") != "Bearer realm=\"user\"" {
					t.Fatalf("challenge=%q", w.Header().Get("WWW-Authenticate"))
				}
			}
		})
	}
}

func TestCredentialBoundaryPreservesTrustedContextAndStrictParsing(t *testing.T) {
	for _, strict := range []bool{false, true} {
		plain := &countingAuth{}
		calls := 0
		next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
		var h http.Handler = authn.Bearer(opts(plain))(next)
		if strict {
			h = authn.RequireScopeStrict(fakeStrict{calls: &calls}, user.ScopeUser)(next)
		}
		r := httptest.NewRequest("GET", "/", nil)
		r = r.WithContext(authn.WithPrincipal(r.Context(), user.Principal{UserID: "u_host", Scope: user.ScopeUser}))
		r.Header["Authorization"] = []string{"Bearer good", "Bearer good"}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		want := 204
		if strict {
			want = 401
		}
		if w.Code != want || plain.calls != 0 || calls != 0 {
			t.Fatalf("strict=%v status=%d plain=%d strict calls=%d", strict, w.Code, plain.calls, calls)
		}
	}
}
