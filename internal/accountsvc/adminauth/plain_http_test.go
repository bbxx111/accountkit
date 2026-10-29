package adminauth

import (
	"context"
	"strings"
	"testing"
	"time"
)

// AllowHTTP is the development-mode allowance: without it a plain HTTP issuer is rejected
// before any I/O, with it discovery, JWKS and token verification work over plain HTTP.
func TestAllowHTTPAcceptsPlainIssuer(t *testing.T) {
	f := newPlainFixture(t)
	if !strings.HasPrefix(f.server.URL, "http://") {
		t.Fatalf("fixture is not plain HTTP: %s", f.server.URL)
	}
	cfg := f.config()
	if cfg.Validate() == nil {
		t.Fatal("plain HTTP issuer accepted without AllowHTTP")
	}
	if _, err := New(context.Background(), cfg, Options{HTTPClient: f.server.Client()}); err == nil {
		t.Fatal("New accepted a plain HTTP issuer without AllowHTTP")
	}
	if f.count() != 0 {
		t.Fatal("JWKS was fetched for a rejected issuer")
	}
	cfg.AllowHTTP = true
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate with AllowHTTP: %v", err)
	}
	v, err := New(context.Background(), cfg, Options{HTTPClient: f.server.Client()})
	if err != nil {
		t.Fatalf("New with AllowHTTP: %v", err)
	}
	now := time.Now()
	if w := request(v, f.token(f.claims(now), "initial"), "operator"); w.Code != 204 {
		t.Fatalf("plain HTTP issuer token rejected: %d %s", w.Code, w.Body.String())
	}
	for _, issuer := range []string{"http://u:p@issuer.test", "http://issuer.test?q=1", "http://issuer.test#frag", "ftp://issuer.test"} {
		if (Config{Issuer: issuer, Audience: "admin", AllowHTTP: true}).Validate() == nil {
			t.Fatalf("AllowHTTP accepted malformed issuer %q", issuer)
		}
	}
}
