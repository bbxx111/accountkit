package identityresolve

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/internal/accountsvc/serviceauth"
	"github.com/bbxx111/accountkit/user"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIdentityResolveProtocol(t *testing.T) {
	secret := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("s", 32)))
	registry, err := serviceauth.NewRegistry([]serviceauth.ClientConfig{{ID: "resolver", AuthMethods: []string{"basic"}, Permissions: []string{"identities.resolve"}, BasicSecrets: []string{secret}}, {ID: "other", AuthMethods: []string{"basic"}, Permissions: []string{"tokens.introspect"}, BasicSecrets: []string{base64.StdEncoding.EncodeToString([]byte(strings.Repeat("o", 32)))}}})
	if err != nil {
		t.Fatal(err)
	}
	auth, err := serviceauth.New(serviceauth.Config{Realm: Realm, Endpoint: "identities_resolve", Registry: registry, RequiredPermission: "identities.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, body, method, media, query string
		want                             int
		domain                           error
	}{
		{name: "ok", body: `{"kind":"EMAIL","subject":"a@example.test"}`, want: 200},
		{name: "empty", body: `{}`, want: 400}, {name: "null", body: `null`, want: 400}, {name: "array", body: `[]`, want: 400}, {name: "unknown", body: `{"kind":"EMAIL","subject":"a","actor":{}}`, want: 400}, {name: "duplicate", body: `{"kind":"EMAIL","kind":"EMAIL","subject":"a"}`, want: 400}, {name: "trailing", body: `{} {}`, want: 400}, {name: "invalid utf8", body: string([]byte{123, 255, 125}), want: 400}, {name: "kind", body: `{"kind":"WECHAT","subject":"a"}`, want: 400}, {name: "null subject", body: `{"kind":"EMAIL","subject":null}`, want: 400}, {name: "query", body: `{"kind":"EMAIL","subject":"a"}`, query: "?a=1", want: 400}, {name: "method", method: "GET", want: 405}, {name: "media", media: "text/plain", want: 415}, {name: "large", body: strings.Repeat("x", 4097), want: 413},
		{name: "not found", body: `{"kind":"EMAIL","subject":"a"}`, domain: user.ErrNotFound, want: 404}, {name: "unavailable", body: `{"kind":"EMAIL","subject":"a"}`, domain: user.ErrUnavailable, want: 503}, {name: "internal", body: `{"kind":"EMAIL","subject":"a"}`, domain: errors.New("private"), want: 500}, {name: "invalid target", body: `{"kind":"EMAIL","subject":"a"}`, domain: user.ErrInvalidTarget, want: 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			h, e := NewWithAuthenticator(auth, func(ctx context.Context, in user.ResolveIdentityInput) (user.ResolvedIdentity, error) {
				calls++
				if in.Actor.ID != "resolver" || in.Actor.Namespace != "ns" || in.Meta.RequestID != "trusted" {
					t.Fatal(in)
				}
				return user.ResolvedIdentity{UserID: "u_0000000000001", State: enum.UserPendingDeletion}, tc.domain
			}, "ns", func(*http.Request) string { return "trusted" })
			if e != nil {
				t.Fatal(e)
			}
			method := tc.method
			if method == "" {
				method = "POST"
			}
			r := httptest.NewRequest(method, "/"+tc.query, strings.NewReader(tc.body))
			r.SetBasicAuth("resolver", secret)
			media := tc.media
			if media == "" {
				media = "application/json; charset=utf-8"
			}
			r.Header.Set("Content-Type", media)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
			if tc.want == 405 && w.Header().Get("Allow") != "POST" {
				t.Fatal(w.Header())
			}
			if tc.want == 200 {
				var out map[string]any
				json.Unmarshal(w.Body.Bytes(), &out)
				if len(out) != 4 || out["display_name"] != "" || out["avatar_url"] != "" || out["state"] != "PENDING_DELETION" {
					t.Fatal(out)
				}
			}
			if tc.want >= 400 && tc.domain == nil && calls != 0 {
				t.Fatal("invalid input queried domain")
			}
			if strings.Contains(w.Body.String(), "private") {
				t.Fatal("error leaked")
			}
		})
	}
}

func TestIdentityResolveAuthorizationBeforeBody(t *testing.T) {
	secret := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("s", 32)))
	for _, tc := range []struct {
		name        string
		clients     []serviceauth.ClientConfig
		credentials bool
		want        int
	}{
		{name: "disabled", want: 503},
		{name: "missing", clients: []serviceauth.ClientConfig{{ID: "resolver", AuthMethods: []string{"basic"}, Permissions: []string{"identities.resolve"}, BasicSecrets: []string{secret}}}, want: 401},
		{name: "forbidden", credentials: true, clients: []serviceauth.ClientConfig{{ID: "other", AuthMethods: []string{"basic"}, Permissions: []string{"tokens.introspect"}, BasicSecrets: []string{secret}}, {ID: "resolver", AuthMethods: []string{"basic"}, Permissions: []string{"identities.resolve"}, BasicSecrets: []string{base64.StdEncoding.EncodeToString([]byte(strings.Repeat("r", 32)))}}}, want: 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry, err := serviceauth.NewRegistry(tc.clients)
			if err != nil {
				t.Fatal(err)
			}
			auth, err := serviceauth.New(serviceauth.Config{Realm: Realm, Registry: registry, RequiredPermission: "identities.resolve"})
			if err != nil {
				t.Fatal(err)
			}
			h, _ := NewWithAuthenticator(auth, func(context.Context, user.ResolveIdentityInput) (user.ResolvedIdentity, error) {
				t.Fatal("domain invoked")
				return user.ResolvedIdentity{}, nil
			}, "ns", func(*http.Request) string { return "id" })
			r := httptest.NewRequest("DELETE", "/?invalid", nil)
			r.Body = unreadBody{t}
			if tc.credentials {
				r.SetBasicAuth("other", secret)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal(w.Code)
			}
			if tc.want == 401 && w.Header().Get("WWW-Authenticate") != `Basic realm="accountsvc-identity-resolution"` {
				t.Fatal(w.Header())
			}
		})
	}
}

type unreadBody struct{ t *testing.T }

func (b unreadBody) Read([]byte) (int, error) { b.t.Fatal("unauthorized body read"); return 0, nil }
func (b unreadBody) Close() error             { return nil }
