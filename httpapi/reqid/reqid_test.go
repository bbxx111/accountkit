package reqid_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bbxx111/accountkit/httpapi/reqid"
)

func TestMiddlewareSetsHeaderAndContext(t *testing.T) {
	var seen string
	h := reqid.Middleware(func(*http.Request) string { return "rid-1" })(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = reqid.From(r.Context())
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if seen != "rid-1" || rec.Header().Get("X-Request-Id") != "rid-1" {
		t.Fatalf("seen=%q header=%q", seen, rec.Header().Get("X-Request-Id"))
	}
	if reqid.From(httptest.NewRequest("GET", "/", nil).Context()) != "" {
		t.Fatal("From without middleware must be empty")
	}
}
