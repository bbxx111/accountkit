package basicclients_test

import (
	"bytes"
	"encoding/base64"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bbxx111/accountkit/internal/accountsvc/basicclients"
)

func secret(b byte) string { return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{b}, 32)) }

func TestParseNamesVariableWithoutEchoingValue(t *testing.T) {
	_, err := basicclients.Parse(`{"a":["short-secret"]}`, "ACCOUNTSVC_PURGE_FEED_CLIENTS")
	if err == nil || !strings.Contains(err.Error(), "ACCOUNTSVC_PURGE_FEED_CLIENTS") || strings.Contains(err.Error(), "short-secret") {
		t.Fatalf("err = %v", err)
	}
	if _, err := basicclients.Parse(`{"a":["`+secret(1)+`"],"a":["`+secret(2)+`"]}`, "X"); err == nil {
		t.Fatal("duplicate client ID accepted")
	}
}

func TestMatcherAndSharedSecret(t *testing.T) {
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
	if !basicclients.SharedSecret(map[string][]string{"a": {secret(1)}}, map[string][]string{"b": {secret(1)}}) ||
		basicclients.SharedSecret(map[string][]string{"a": {secret(1)}}, map[string][]string{"b": {secret(2)}}) {
		t.Fatal("SharedSecret mismatch")
	}
}
