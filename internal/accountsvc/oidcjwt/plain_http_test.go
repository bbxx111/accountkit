package oidcjwt

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Plain HTTP issuers are a development-mode allowance: nothing changes unless AllowHTTP is set.
func TestPlainHTTPIssuerRequiresAllowHTTP(t *testing.T) {
	p := newPlainProvider(t)
	if !strings.HasPrefix(p.server.URL, "http://") {
		t.Fatalf("fixture is not plain HTTP: %s", p.server.URL)
	}
	if _, err := New(p.server.URL, "svc", Options{HTTPClient: p.server.Client()}); err == nil {
		t.Fatal("plain HTTP issuer accepted without AllowHTTP")
	}
	if ValidIssuer(p.server.URL) {
		t.Fatal("ValidIssuer accepted a plain HTTP issuer")
	}
	if !ValidPlainIssuer(p.server.URL) || !ValidPlainIssuer("https://idp.example.test/realms/x") {
		t.Fatal("ValidPlainIssuer rejected a well-formed issuer")
	}
	for _, issuer := range []string{"http://user@idp.example.test", "http://idp.example.test?x=1", "http://idp.example.test#f", " http://idp.example.test", "http://idp.example.test ", "ftp://idp.example.test", "http:///path", "localhost:8180", ""} {
		if ValidPlainIssuer(issuer) {
			t.Fatalf("ValidPlainIssuer accepted %q", issuer)
		}
	}
}

func TestAllowHTTPVerifiesOverPlainHTTP(t *testing.T) {
	p := newPlainProvider(t)
	now := time.Now().Truncate(time.Second)
	v, err := New(p.server.URL, "svc", Options{HTTPClient: p.server.Client(), Now: newClock(now).now, AllowHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := v.Load(ctx); err != nil {
		t.Fatalf("Load over plain HTTP: %v", err)
	}
	claims, err := v.Parse(ctx, p.token(p.claims(now), "initial"))
	if err != nil {
		t.Fatalf("Parse over plain HTTP: %v", err)
	}
	if claims["iss"] != p.server.URL {
		t.Fatalf("iss = %v, want %s", claims["iss"], p.server.URL)
	}
	// The exact issuer match is not relaxed: the HTTPS spelling of the same host is another issuer.
	mismatch := p.claims(now)
	mismatch["iss"] = "https://" + strings.TrimPrefix(p.server.URL, "http://")
	if _, err := v.Parse(ctx, p.token(mismatch, "initial")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("https iss accepted for http issuer: %v", err)
	}
	if discoveries, jwks := p.counts(); discoveries != 1 || jwks != 1 {
		t.Fatalf("discoveries=%d jwks=%d, want 1/1", discoveries, jwks)
	}
}

// A plain HTTP jwks_uri is rejected unless AllowHTTP is set, even when the issuer itself is HTTPS.
func TestPlainHTTPJWKSURIRequiresAllowHTTP(t *testing.T) {
	secure, plain := newProvider(t), newPlainProvider(t)
	secure.set(func() { secure.jwksOverride = plain.server.URL + "/keys" })
	plain.set(func() { plain.keys = secure.keys })
	now := time.Now().Truncate(time.Second)
	ctx := context.Background()
	v, err := New(secure.server.URL, "svc", Options{HTTPClient: secure.server.Client(), Now: newClock(now).now})
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Load(ctx); err == nil || errors.Is(err, ErrIssuerMismatch) {
		t.Fatalf("plain HTTP jwks_uri accepted without AllowHTTP: %v", err)
	}
	if _, jwks := plain.counts(); jwks != 0 {
		t.Fatal("plain JWKS endpoint was contacted without AllowHTTP")
	}
	v, err = New(secure.server.URL, "svc", Options{HTTPClient: secure.server.Client(), Now: newClock(now).now, AllowHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Load(ctx); err != nil {
		t.Fatalf("Load with AllowHTTP: %v", err)
	}
	if _, err := v.Parse(ctx, secure.token(secure.claims(now), "initial")); err != nil {
		t.Fatalf("Parse with plain JWKS: %v", err)
	}
}

// AllowHTTP relaxes the scheme only; redirects are still refused.
func TestAllowHTTPStillRejectsRedirects(t *testing.T) {
	p := newPlainProvider(t)
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, p.server.URL+r.URL.Path, http.StatusFound)
	}))
	t.Cleanup(redirector.Close)
	v, err := New(redirector.URL, "svc", Options{HTTPClient: redirector.Client(), AllowHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Load(context.Background()); err == nil {
		t.Fatal("redirected discovery accepted")
	}
	if discoveries, _ := p.counts(); discoveries != 0 {
		t.Fatal("redirect target was followed")
	}
}
