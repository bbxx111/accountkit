package serviceauth_test

import (
	"encoding/base64"
	"github.com/bbxx111/accountkit/internal/accountsvc/serviceauth"
	"github.com/golang-jwt/jwt/v5"
	"strings"
	"testing"
)

func TestCredentialSyntax(t *testing.T) {
	e := newEnv(t, true, true)
	valid := e.bearer(e.p.Claims(e.now, "intro-client"), "initial")
	basic := "Basic " + base64.StdEncoding.EncodeToString([]byte("basic-client:"+basicSecret))
	for _, tc := range []struct {
		name, header string
		ok           bool
	}{
		{"bearer case spaces", "bEaReR   " + strings.TrimPrefix(valid, "Bearer "), true},
		{"basic case spaces", "bAsIc   " + strings.TrimPrefix(basic, "Basic "), true},
		{"bearer tab", strings.Replace(valid, "Bearer ", "Bearer \t", 1), false},
		{"bearer trailing tab", valid + "\t", false},
		{"basic trailing newline", basic + "\n", false},
		{"bearer comma", valid + ",other", false},
		{"bearer over limit", "Bearer " + strings.Repeat("a", 16385), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, _ := e.do(tc.header)
			if tc.ok && res.Outcome != serviceauth.OK {
				t.Fatalf("result=%+v", res)
			}
			if !tc.ok && res.Outcome != serviceauth.Unauthenticated {
				t.Fatalf("result=%+v", res)
			}
		})
	}
}

func TestBearerLengthBoundary(t *testing.T) {
	e := newEnv(t, true, true)
	for _, size := range []int{16384, 16385} {
		raw := ""
		for headerPad := 0; headerPad < 3 && raw == ""; headerPad++ {
			claims := e.p.Claims(e.now, "intro-client")
			claims["padding"] = ""
			token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
			token.Header["kid"] = "initial"
			token.Header["padding"] = strings.Repeat("x", headerPad)
			base, err := token.SignedString(e.p.Key)
			if err != nil {
				t.Fatal(err)
			}
			estimate := (size - len(base)) * 3 / 4
			for pad := estimate - 3; pad <= estimate+3; pad++ {
				claims["padding"] = strings.Repeat("x", pad)
				signed, err := token.SignedString(e.p.Key)
				if err != nil {
					t.Fatal(err)
				}
				if len(signed) == size {
					raw = signed
					break
				}
			}
		}
		if raw == "" {
			t.Fatalf("cannot construct token of %d bytes", size)
		}
		res, ch := e.do("Bearer " + raw)
		if size == 16384 {
			expect(t, res, ch, serviceauth.OK, nil)
		} else {
			expect(t, res, ch, serviceauth.Unauthenticated, invalidChallenges)
			if !res.BearerAttempted {
				t.Fatal("oversized bearer must preserve invalid_token challenge")
			}
		}
	}
}
