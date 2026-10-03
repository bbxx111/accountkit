package accountsvc

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestTrustedClientIP(t *testing.T) {
	prefixes := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("2001:db8::/32")}
	for _, tc := range []struct{ peer, xff, want string }{
		{"198.51.100.8:1234", "203.0.113.7", "198.51.100.8"},
		{"10.0.0.1:1234", "203.0.113.7, 10.0.0.2", "203.0.113.7"},
		{"10.0.0.1:1234", "192.0.2.9, 198.51.100.8", "198.51.100.8"},
		{"10.0.0.1:1234", "bad, 10.0.0.2", "10.0.0.1"},
		{"10.0.0.1:1234", "203.0.113.7,", "10.0.0.1"},
		{"10.0.0.1:1234", "203.0.113.7:1234", "10.0.0.1"},
		{"[2001:db8::1]:1234", "2001:db9::7, 2001:db8::2", "2001:db9::7"},
		{"10.0.0.1:1234", "", "10.0.0.1"},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = tc.peer
		r.Header.Set("X-Forwarded-For", tc.xff)
		if got := resolveClientIP(r, prefixes); got != tc.want {
			t.Fatalf("peer %s: got %s want %s", tc.peer, got, tc.want)
		}
	}
}

func TestMetadataRequestIDReusedForEverySurface(t *testing.T) {
	for _, raw := range []string{"valid.request-1", "", "invalid spaces", strings.Repeat("x", 65)} {
		var first string
		h := requestMetadata(nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			first = requestID(r)
			if first != requestID(r) {
				t.Error("request ID regenerated")
			}
			if clientIP(r) != "192.0.2.1" {
				t.Error("client IP absent")
			}
			w.WriteHeader(204)
		}))
		r := httptest.NewRequest("GET", "/healthz", nil)
		r.RemoteAddr = "192.0.2.1:1234"
		r.Header.Set("X-Request-Id", raw)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Header().Get("X-Request-Id") != first || !requestIDPattern.MatchString(first) {
			t.Fatal("response/request ID mismatch")
		}
		if raw == "valid.request-1" && first != raw {
			t.Fatal("valid request ID lost")
		}
		if raw != "valid.request-1" && first == raw {
			t.Fatal("unsafe request ID accepted")
		}
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Add("X-Request-Id", "first")
	r.Header.Add("X-Request-Id", "second")
	rec := httptest.NewRecorder()
	requestMetadata(nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })).ServeHTTP(rec, r)
	if rec.Header().Get("X-Request-Id") == "first" {
		t.Fatal("duplicate request ID trusted")
	}
}
