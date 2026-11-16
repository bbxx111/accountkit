package httpcontract

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChallenges(t *testing.T) {
	for _, c := range []struct{ realm, code, want string }{{"", "", "Bearer"}, {"user", "", `Bearer realm="user"`}, {"", "invalid_token", `Bearer error="invalid_token"`}, {"svc", "insufficient_scope", `Bearer realm="svc", error="insufficient_scope"`}} {
		if got := BearerChallenge(c.realm, c.code); got != c.want {
			t.Fatalf("%q != %q", got, c.want)
		}
	}
	if got := BasicChallenge("svc"); got != `Basic realm="svc"` {
		t.Fatal(got)
	}
}
func TestCachePolicies(t *testing.T) {
	w := httptest.NewRecorder()
	SetNoStore(w)
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Pragma") != "" {
		t.Fatal(w.Header())
	}
	SetOAuthNoStore(w)
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Pragma") != "no-cache" {
		t.Fatal(w.Header())
	}
}
func TestRequestID(t *testing.T) {
	for _, s := range []string{"a", "A0._-", strings.Repeat("x", 64)} {
		if !ValidRequestID(s) {
			t.Fatal(s)
		}
	}
	for _, s := range []string{"", "a b", "é", "a\n", strings.Repeat("x", 65)} {
		if ValidRequestID(s) {
			t.Fatal(s)
		}
	}
}
