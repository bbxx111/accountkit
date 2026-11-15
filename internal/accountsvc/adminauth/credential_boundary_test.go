package adminauth

import (
	"github.com/golang-jwt/jwt/v5"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCredentialSyntax(t *testing.T) {
	f := newFixture(t)
	now := time.Now()
	v := f.verifier(Options{Now: func() time.Time { return now }})
	token := f.token(f.claims(now), "initial")
	for _, tc := range []struct {
		name    string
		headers []string
		want    int
	}{
		{"case spaces", []string{"bEaReR   " + token}, 204},
		{"duplicate", []string{"Bearer " + token, "Bearer " + token}, 401},
		{"mixed", []string{"Basic abc", "Bearer " + token}, 401},
		{"tab", []string{"Bearer \t" + token}, 401},
		{"trailing newline", []string{"Bearer " + token + "\n"}, 401},
		{"comma", []string{"Bearer " + token + ",other"}, 401},
		{"over limit", []string{"Bearer " + strings.Repeat("a", 16385)}, 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.Header["Authorization"] = tc.headers
			w := httptest.NewRecorder()
			v.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })).ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if tc.want == 401 && w.Header().Get("WWW-Authenticate") != "Bearer error=\"invalid_token\"" {
				t.Fatalf("challenge=%q", w.Header().Get("WWW-Authenticate"))
			}
		})
	}
}

func TestBearerLengthBoundary(t *testing.T) {
	f := newFixture(t)
	now := time.Now()
	v := f.verifier(Options{Now: func() time.Time { return now }})
	for _, size := range []int{16384, 16385} {
		raw := ""
		for headerPad := 0; headerPad < 3 && raw == ""; headerPad++ {
			claims := f.claims(now)
			claims["padding"] = ""
			token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
			token.Header["kid"] = "initial"
			token.Header["padding"] = strings.Repeat("x", headerPad)
			base, err := token.SignedString(f.key)
			if err != nil {
				t.Fatal(err)
			}
			estimate := (size - len(base)) * 3 / 4
			for pad := estimate - 3; pad <= estimate+3; pad++ {
				claims["padding"] = strings.Repeat("x", pad)
				signed, err := token.SignedString(f.key)
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
		w := request(v, raw, "")
		want := 204
		if size > 16384 {
			want = 401
		}
		if w.Code != want {
			t.Fatalf("size=%d status=%d body=%s", size, w.Code, w.Body.String())
		}
	}
}
