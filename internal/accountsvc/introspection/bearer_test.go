package introspection_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/internal/accountsvc/introspection"
	"github.com/bbxx111/accountkit/internal/accountsvc/oidcjwt"
	"github.com/bbxx111/accountkit/internal/accountsvc/oidcjwt/oidcjwttest"
	"github.com/bbxx111/accountkit/internal/accountsvc/serviceauth"
	"github.com/bbxx111/accountkit/user"
)

const introspectionRealm = "accountsvc-introspection"

func bearerHandler(t *testing.T, p *oidcjwttest.Provider, basic bool, calls *int) http.Handler {
	t.Helper()
	tokens, err := oidcjwt.New(p.URL(), oidcjwttest.Audience, oidcjwt.Options{HTTPClient: p.Client(), RefreshInterval: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	cfg := serviceauth.Config{Realm: introspectionRealm, Endpoint: "introspect", Tokens: tokens, TokenClients: []string{"intro-client"}}
	if basic {
		cfg.Basic = map[string][]string{"business": {secret(1)}}
	}
	auth, err := serviceauth.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	h, err := introspection.NewWithAuthenticator(auth, func(context.Context, string) (user.Principal, error) {
		*calls++
		return user.Principal{UserID: "u_0000000000000", SessionID: "s_0000000000000", Scope: "user", AuthTime: time.Unix(1, 0)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func post(h http.Handler, method string, authorization ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "https://accountsvc.test/v1/introspect", strings.NewReader("token=consumer-access-token"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, a := range authorization {
		r.Header.Add("Authorization", a)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func oauthCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.Error
}

func TestBearerIntrospectionErrors(t *testing.T) {
	p := oidcjwttest.New(t)
	calls := 0
	h := bearerHandler(t, p, true, &calls)
	now := time.Now()
	valid := "Bearer " + p.Token(p.Claims(now, "intro-client"), "initial")
	for _, tc := range []struct {
		name          string
		method        string
		authorization []string
		status        int
		code          string
		challenges    []string
	}{
		{"valid token", "POST", []string{valid}, 200, "", nil},
		{"valid basic", "POST", []string{"Basic " + base64.StdEncoding.EncodeToString([]byte("business:"+secret(1)))}, 200, "", nil},
		{"no credentials", "POST", nil, 401, "invalid_client", []string{`Basic realm="accountsvc-introspection"`, `Bearer realm="accountsvc-introspection"`}},
		{"two headers", "POST", []string{valid, valid}, 401, "invalid_client", []string{`Basic realm="accountsvc-introspection"`, `Bearer realm="accountsvc-introspection"`}},
		{"invalid token", "POST", []string{"Bearer " + p.Token(p.Claims(now.Add(-time.Hour), "intro-client"), "initial")}, 401, "invalid_token", []string{`Basic realm="accountsvc-introspection"`, `Bearer realm="accountsvc-introspection", error="invalid_token"`}},
		{"invalid token precedes method check", "GET", []string{"Bearer not-a-jwt"}, 401, "invalid_token", []string{`Basic realm="accountsvc-introspection"`, `Bearer realm="accountsvc-introspection", error="invalid_token"`}},
		{"client of the purge feed", "POST", []string{"Bearer " + p.Token(p.Claims(now, "purge-client"), "initial")}, 403, "insufficient_scope", []string{`Bearer realm="accountsvc-introspection", error="insufficient_scope"`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := calls
			w := post(h, tc.method, tc.authorization...)
			if w.Code != tc.status || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("status %d cache %q body %s", w.Code, w.Header().Get("Cache-Control"), w.Body.String())
			}
			if got := w.Header().Values("WWW-Authenticate"); !reflect.DeepEqual(got, tc.challenges) {
				t.Fatalf("challenges = %q, want %q", got, tc.challenges)
			}
			if tc.status == 200 {
				if calls != before+1 {
					t.Fatal("authorized caller did not reach token validation")
				}
				return
			}
			if calls != before {
				t.Fatal("rejected caller reached token validation")
			}
			if code := oauthCode(t, w); code != tc.code {
				t.Fatalf("error = %q, want %q", code, tc.code)
			}
		})
	}
}

func TestBearerIntrospectionProviderUnavailable(t *testing.T) {
	p := oidcjwttest.New(t)
	p.SetFail(true)
	calls := 0
	h := bearerHandler(t, p, false, &calls)
	w := post(h, "POST", "Bearer "+p.Token(p.Claims(time.Now(), "intro-client"), "initial"))
	if w.Code != 503 || oauthCode(t, w) != "temporarily_unavailable" || len(w.Header().Values("WWW-Authenticate")) != 0 || w.Header().Get("Cache-Control") != "no-store" || calls != 0 {
		t.Fatalf("status %d headers %v body %s", w.Code, w.Header(), w.Body.String())
	}
}

func TestNewWithAuthenticatorRequiresAMethod(t *testing.T) {
	auth, err := serviceauth.New(serviceauth.Config{Realm: introspectionRealm})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := introspection.NewWithAuthenticator(auth, func(context.Context, string) (user.Principal, error) { return user.Principal{}, nil }); err == nil {
		t.Fatal("introspection without any caller authentication accepted")
	}
}
