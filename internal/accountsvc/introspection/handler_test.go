package introspection_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/internal/accountsvc/introspection"
	"github.com/bbxx111/accountkit/user"
)

func secret(b byte) string { return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{b}, 32)) }

func TestParseClients(t *testing.T) {
	good := fmt.Sprintf(`{"business":[%q,%q]}`, secret(1), secret(2))
	got, err := introspection.ParseClients(good)
	if err != nil || len(got["business"]) != 2 || got["business"][1] != secret(2) {
		t.Fatalf("rotation config: %v, %v", got, err)
	}
	for _, raw := range []string{"", `null`, `{}`, `[]`, `{"": ["secret"]}`, `{"  ":["secret"]}`, `{"business":[]}`, `{"business":null}`, `{"business":"secret"}`, `{"business":["not-base64"]}`, fmt.Sprintf(`{"business":[%q]}`, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 31))), good + `{}`, fmt.Sprintf(`{"business":[%q],"business":[%q]}`, secret(1), secret(2)), fmt.Sprintf(`{"business":[%q],"\u0062usiness":[%q]}`, secret(1), secret(2))} {
		if _, err := introspection.ParseClients(raw); err == nil {
			t.Errorf("accepted invalid config %q", raw)
		} else if strings.Contains(err.Error(), secret(1)) || strings.Contains(err.Error(), "not-base64") {
			t.Fatal("configuration error leaked secret")
		}
	}
}

func TestClientAuthenticationAndRotation(t *testing.T) {
	calls := 0
	authenticate := func(context.Context, string) (user.Principal, error) {
		calls++
		return user.Principal{}, user.ErrInvalidToken
	}
	clients := map[string][]string{"business": {secret(1), secret(2)}}
	h, err := introspection.New(clients, authenticate)
	if err != nil {
		t.Fatal(err)
	}
	// Configuration is snapshotted: caller mutation must not alter accepted credentials.
	clients["business"][0] = secret(3)
	for _, tc := range []struct {
		name, user, password, authorization string
		want                                int
	}{
		{name: "old", user: "business", password: secret(1), want: 200},
		{name: "new", user: "business", password: secret(2), want: 200},
		{name: "mutation", user: "business", password: secret(3), want: 401},
		{name: "unknown", user: "other", password: secret(1), want: 401},
		{name: "wrong", user: "business", password: "consumer-or-admin-token", want: 401},
		{name: "missing", want: 401},
		{name: "bearer", authorization: "Bearer consumer-token", want: 401},
		{name: "malformed", authorization: "Basic !!!", want: 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := calls
			r := httptest.NewRequest("POST", "/v1/introspect?token=query", strings.NewReader("token=access"))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if tc.user != "" {
				r.SetBasicAuth(tc.user, tc.password)
			}
			if tc.authorization != "" {
				r.Header.Set("Authorization", tc.authorization)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status %d: %s", w.Code, w.Body)
			}
			if tc.want == 401 {
				if calls != before || w.Header().Get("WWW-Authenticate") == "" || !strings.Contains(w.Body.String(), `"error":"invalid_client"`) {
					t.Fatalf("authentication boundary: %v %s", w.Header(), w.Body)
				}
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("missing no-store")
			}
		})
	}
	rotated, err := introspection.New(map[string][]string{"business": {secret(2)}}, authenticate)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		password string
		want     int
	}{{secret(1), 401}, {secret(2), 200}} {
		w := request(rotated, "token=access", tc.password)
		if w.Code != tc.want {
			t.Fatalf("rotated: %d", w.Code)
		}
	}
}

func request(h http.Handler, body, password string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/v1/introspect", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetBasicAuth("business", password)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestProtocolBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, method, query, body, contentType, password string
		want                                             int
		token                                            string
	}{
		{name: "form", body: "token=access", want: 200, token: "access"},
		{name: "hint ignored", body: "token=access&token_type_hint=refresh_token", want: 200, token: "access"},
		{name: "unknown hint", body: "token=access&token_type_hint=unknown", want: 200, token: "access"},
		{name: "query ignored", query: "?token=query&token=again", body: "token=body", want: 200, token: "body"},
		{name: "query only", query: "?token=query", want: 400},
		{name: "missing", body: "token_type_hint=access_token", want: 400},
		{name: "empty", body: "token=", want: 400},
		{name: "whitespace", body: "token=++", want: 400},
		{name: "duplicate", body: "token=a&token=b", want: 400},
		{name: "duplicate escaped", body: "token=a&%74oken=b", want: 400},
		{name: "bad escape", body: "token=%XX", want: 400},
		{name: "bad separator", body: "token=a;b", want: 400},
		{name: "over limit", body: "token=" + strings.Repeat("x", 64*1024), want: 413},
		{name: "at limit", body: "token=" + strings.Repeat("x", 64*1024-6), want: 200, token: strings.Repeat("x", 64*1024-6)},
		{name: "json", body: `{"token":"access"}`, contentType: "application/json", want: 415},
		{name: "missing content type", body: "token=access", contentType: "-", want: 415},
		{name: "invalid content type", body: "token=access", contentType: "application/x-www-form-urlencoded; broken", want: 415},
		{name: "charset", body: "token=access", contentType: "application/x-www-form-urlencoded; charset=UTF-8", want: 200, token: "access"},
		{name: "get", method: "GET", body: "token=access", want: 405},
		{name: "bad auth before body limit", body: strings.Repeat("x", 64*1024+1), password: "bad", want: 401},
		{name: "bad auth before content type", body: "invalid", password: "bad", contentType: "application/json", want: 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			h, err := introspection.New(map[string][]string{"business": {secret(1)}}, func(ctx context.Context, raw string) (user.Principal, error) {
				calls++
				if raw != tc.token {
					t.Fatal("body token was replaced")
				}
				return user.Principal{UserID: "u_0000000000001", SessionID: "s_0000000000001", Scope: "user", AuthTime: time.Unix(1700000000, 0)}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			method := tc.method
			if method == "" {
				method = "POST"
			}
			ct := tc.contentType
			if ct == "" {
				ct = "application/x-www-form-urlencoded"
			}
			password := tc.password
			if password == "" {
				password = secret(1)
			}
			r := httptest.NewRequest(method, "/v1/introspect"+tc.query, strings.NewReader(tc.body))
			if ct != "-" {
				r.Header.Set("Content-Type", ct)
			}
			r.SetBasicAuth("business", password)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status %d want %d: %s", w.Code, tc.want, w.Body)
			}
			if (tc.want == 200 && calls != 1) || (tc.want != 200 && calls != 0) {
				t.Fatalf("unexpected authentication calls: %d", calls)
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("missing no-store")
			}
			var result map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if tc.want == 200 {
				if len(result) != 5 || result["active"] != true || result["sub"] != "u_0000000000001" || result["scope"] != "user" || result["sid"] != "s_0000000000001" || result["auth_time"] != float64(1700000000) {
					t.Fatalf("nonminimal context: %v", result)
				}
			} else {
				if _, ok := result["error"].(string); !ok {
					t.Fatalf("non-OAuth error: %v", result)
				}
			}
		})
	}
}

func TestSafeAuthenticationErrors(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want int
		body string
	}{
		{fmt.Errorf("wrapped: %w", user.ErrInvalidToken), 200, `{"active":false}`},
		{errors.New("private-dsn-secret"), 503, `{"error":"temporarily_unavailable"}`},
	} {
		h, err := introspection.New(map[string][]string{"business": {secret(1)}}, func(context.Context, string) (user.Principal, error) { return user.Principal{}, tc.err })
		if err != nil {
			t.Fatal(err)
		}
		w := request(h, "token=private-token", secret(1))
		if w.Code != tc.want || strings.TrimSpace(w.Body.String()) != tc.body {
			t.Fatalf("unsafe response %d %s", w.Code, w.Body)
		}
	}
}

func TestNewRejectsInvalidConfiguration(t *testing.T) {
	authenticate := func(context.Context, string) (user.Principal, error) { return user.Principal{}, nil }
	for _, clients := range []map[string][]string{nil, {}, {"": {secret(1)}}, {"business": {}}, {"business": {"short"}}} {
		if _, err := introspection.New(clients, authenticate); err == nil {
			t.Fatal("accepted invalid credentials")
		}
	}
	if _, err := introspection.New(map[string][]string{"business": {secret(1)}}, nil); err == nil {
		t.Fatal("accepted nil authenticator")
	}
}

type failedBody struct{ reads int }

func (b *failedBody) Read([]byte) (int, error) {
	b.reads++
	return 0, errors.New("private body failure")
}
func (*failedBody) Close() error { return nil }

func TestAuthenticationPrecedesBodyReadAndRejectsDuplicateHeaders(t *testing.T) {
	h, err := introspection.New(map[string][]string{"business": {secret(1)}}, func(context.Context, string) (user.Principal, error) {
		t.Fatal("malformed request reached consumer auth")
		return user.Principal{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name                     string
		authenticated, duplicate bool
		want, reads              int
	}{
		{name: "missing authentication", want: 401},
		{name: "authenticated read failure", authenticated: true, want: 400, reads: 1},
		{name: "duplicate authentication", authenticated: true, duplicate: true, want: 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &failedBody{}
			r := httptest.NewRequest("POST", "/v1/introspect", nil)
			r.Body = body
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if tc.authenticated {
				r.SetBasicAuth("business", secret(1))
			}
			if tc.duplicate {
				r.Header.Add("Authorization", r.Header.Get("Authorization"))
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want || body.reads != tc.reads || strings.Contains(w.Body.String(), "private") {
				t.Fatalf("status=%d reads=%d body=%s", w.Code, body.reads, w.Body)
			}
		})
	}
}

func TestChunkedBodyCannotBypassLimit(t *testing.T) {
	h, err := introspection.New(map[string][]string{"business": {secret(1)}}, func(context.Context, string) (user.Principal, error) {
		t.Fatal("oversize body reached consumer auth")
		return user.Principal{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/v1/introspect", io.NopCloser(strings.NewReader("token="+strings.Repeat("x", 64*1024))))
	r.ContentLength = -1
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetBasicAuth("business", secret(1))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 413 {
		t.Fatalf("chunked limit: %d", w.Code)
	}
}
