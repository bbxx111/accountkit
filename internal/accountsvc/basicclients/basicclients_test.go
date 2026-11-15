package basicclients_test

import (
	"bytes"
	"encoding/base64"
	"net/http/httptest"
	"testing"

	"github.com/bbxx111/accountkit/internal/accountsvc/basicclients"
)

func secret(b byte) string { return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{b}, 32)) }

func TestMatcherAndRotation(t *testing.T) {
	m, err := basicclients.NewMatcher(map[string][]string{"svc": {secret(1), secret(2)}})
	if err != nil {
		t.Fatal(err)
	}
	for pass, want := range map[string]bool{secret(1): true, secret(2): true, secret(3): false} {
		r := httptest.NewRequest("GET", "/", nil)
		r.SetBasicAuth("svc", pass)
		if m.Authorized(r) != want {
			t.Fatalf("Authorized(%q) != %v", pass, want)
		}
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.SetBasicAuth("svc", secret(1))
	r.Header.Add("Authorization", "Basic Zm9vOmJhcg==")
	if m.Authorized(r) {
		t.Fatal("duplicate Authorization headers accepted")
	}
}

func TestMatchReturnsClientID(t *testing.T) {
	m, err := basicclients.NewMatcher(map[string][]string{"svc": {secret(1)}, "other": {secret(2)}})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.SetBasicAuth("other", secret(2))
	if id, ok := m.Match(r); !ok || id != "other" {
		t.Fatalf("Match = %q, %v", id, ok)
	}
	r = httptest.NewRequest("GET", "/", nil)
	r.SetBasicAuth("svc", secret(2))
	if id, ok := m.Match(r); ok || id != "" {
		t.Fatalf("secret of another client matched: %q, %v", id, ok)
	}
}
