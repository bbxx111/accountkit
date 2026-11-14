package oidcjwt

import (
	"context"
	"errors"
	"github.com/golang-jwt/jwt/v5"
	"testing"
	"time"
)

func TestRFC9068TokenContract(t *testing.T) {
	p := newProvider(t)
	now := time.Now()
	v, err := New(p.server.URL, "svc", Options{HTTPClient: p.server.Client(), Profile: "rfc9068"})
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	valid := func() jwt.MapClaims {
		c := p.claims(now)
		c["sub"] = "human-or-machine"
		c["client_id"] = "client"
		c["jti"] = "unique"
		return c
	}
	for _, tc := range []struct {
		name, typ string
		mutate    func(jwt.MapClaims)
		bad       bool
	}{
		{"valid", "at+jwt", nil, false}, {"media type", "application/at+jwt", nil, false}, {"JWT", "JWT", nil, true}, {"missing typ", "", nil, true},
		{"missing client", "at+jwt", func(c jwt.MapClaims) { delete(c, "client_id") }, true},
		{"numeric client", "at+jwt", func(c jwt.MapClaims) { c["client_id"] = 1 }, true},
		{"empty client", "at+jwt", func(c jwt.MapClaims) { c["client_id"] = "" }, true},
		{"missing subject", "at+jwt", func(c jwt.MapClaims) { delete(c, "sub") }, true},
		{"array subject", "at+jwt", func(c jwt.MapClaims) { c["sub"] = []string{"human"} }, true},
		{"empty subject", "at+jwt", func(c jwt.MapClaims) { c["sub"] = "" }, true},
		{"null jti", "at+jwt", func(c jwt.MapClaims) { c["jti"] = nil }, true},
		{"empty jti", "at+jwt", func(c jwt.MapClaims) { c["jti"] = "" }, true},
		{"missing exp", "at+jwt", func(c jwt.MapClaims) { delete(c, "exp") }, true},
		{"array audience", "at+jwt", func(c jwt.MapClaims) { c["aud"] = []string{"other", "svc"} }, false},
		{"bad audience type", "at+jwt", func(c jwt.MapClaims) { c["aud"] = 3 }, true},
		{"missing issuer", "at+jwt", func(c jwt.MapClaims) { delete(c, "iss") }, true},
		{"missing iat", "at+jwt", func(c jwt.MapClaims) { delete(c, "iat") }, true},
		{"string iat", "at+jwt", func(c jwt.MapClaims) { c["iat"] = "1" }, true},
		{"string exp", "at+jwt", func(c jwt.MapClaims) { c["exp"] = "9999999999" }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := valid()
			if tc.mutate != nil {
				tc.mutate(c)
			}
			token := jwt.NewWithClaims(jwt.SigningMethodRS256, c)
			token.Header["kid"] = "initial"
			if tc.typ != "" {
				token.Header["typ"] = tc.typ
			} else {
				delete(token.Header, "typ")
			}
			raw, err := token.SignedString(p.key)
			if err != nil {
				t.Fatal(err)
			}
			_, err = v.Parse(context.Background(), raw)
			if tc.bad && !errors.Is(err, ErrInvalid) {
				t.Fatalf("expected invalid, got %v", err)
			}
			if !tc.bad && err != nil {
				t.Fatal(err)
			}
		})
	}
}
