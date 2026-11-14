package introspection_test

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/internal/accountsvc/introspection"
	"github.com/bbxx111/accountkit/internal/accountsvc/oidcjwt"
	"github.com/bbxx111/accountkit/internal/accountsvc/oidcjwt/oidcjwttest"
	"github.com/bbxx111/accountkit/internal/accountsvc/serviceauth"
	"github.com/bbxx111/accountkit/user"
)

func unifiedIntrospection(t *testing.T, p *oidcjwttest.Provider, clients []serviceauth.ClientConfig, calls *int) http.Handler {
	t.Helper()
	registry, err := serviceauth.NewRegistry(clients)
	if err != nil {
		t.Fatal(err)
	}
	var verifier *oidcjwt.Verifier
	if registry.BearerEnabled() {
		verifier, err = oidcjwt.New(p.URL(), oidcjwttest.Audience, oidcjwt.Options{HTTPClient: p.Client(), RefreshInterval: 10 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
	}
	auth, err := serviceauth.New(serviceauth.Config{Realm: introspection.Realm, Registry: registry, RequiredPermission: "tokens.introspect", BearerVerifier: verifier})
	if err != nil {
		t.Fatal(err)
	}
	h, err := introspection.NewWithAuthenticator(auth, func(context.Context, string) (user.Principal, error) {
		*calls++
		return user.Principal{}, user.ErrInvalidToken
	})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestUnifiedIntrospectionAuthorization(t *testing.T) {
	p := oidcjwttest.New(t)
	calls := 0
	h := unifiedIntrospection(t, p, []serviceauth.ClientConfig{
		{ID: "both", AuthMethods: []string{"basic", "bearer"}, Permissions: []string{"tokens.introspect", "purged_users.read"}, BasicSecrets: []string{secret(1)}},
		{ID: "limited", AuthMethods: []string{"basic", "bearer"}, Permissions: []string{"purged_users.read"}, BasicSecrets: []string{secret(2)}},
		{ID: "basic-only", AuthMethods: []string{"basic"}, Permissions: []string{"tokens.introspect"}, BasicSecrets: []string{secret(3)}},
		{ID: "bearer-only", AuthMethods: []string{"bearer"}, Permissions: []string{"tokens.introspect"}},
	}, &calls)
	basic := func(id string, b byte) string {
		return "Basic " + base64.StdEncoding.EncodeToString([]byte(id+":"+secret(b)))
	}
	bearer := func(id string) string { return "Bearer " + p.Token(p.Claims(time.Now(), id), "initial") }
	both := []string{`Basic realm="accountsvc-introspection"`, `Bearer realm="accountsvc-introspection"`}
	for _, tc := range []struct {
		name, authorization string
		status              int
		code                string
		challenges          []string
	}{
		{"basic success invalid consumer", basic("both", 1), 200, "", nil},
		{"bearer success invalid consumer", bearer("both"), 200, "", nil},
		{"basic permission", basic("limited", 2), 403, "insufficient_scope", nil},
		{"bearer permission", bearer("limited"), 403, "insufficient_scope", []string{`Bearer realm="accountsvc-introspection", error="insufficient_scope"`}},
		{"unknown bearer", bearer("unknown"), 403, "insufficient_scope", []string{`Bearer realm="accountsvc-introspection", error="insufficient_scope"`}},
		{"disabled bearer", bearer("basic-only"), 401, "invalid_client", both},
		{"disabled basic", basic("bearer-only", 1), 401, "invalid_client", both},
		{"invalid bearer", "Bearer not-a-jwt", 401, "invalid_token", []string{both[0], both[1] + `, error="invalid_token"`}},
		{"missing", "", 401, "invalid_client", both},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := calls
			var headers []string
			if tc.authorization != "" {
				headers = []string{tc.authorization}
			}
			w := post(h, "POST", headers...)
			if w.Code != tc.status || w.Header().Get("Cache-Control") != "no-store" || !reflect.DeepEqual(w.Header().Values("WWW-Authenticate"), tc.challenges) {
				t.Fatalf("status=%d headers=%v body=%s", w.Code, w.Header(), w.Body)
			}
			if tc.status == 200 {
				if calls != before+1 || w.Body.String() != "{\"active\":false}\n" {
					t.Fatalf("consumer result: calls=%d body=%s", calls, w.Body)
				}
			} else if calls != before || oauthCode(t, w) != tc.code {
				t.Fatalf("unauthorized callback or code: calls=%d body=%s", calls, w.Body)
			}
		})
	}
	// Permission failure must precede method, content type and any body reads.
	for _, method := range []string{"GET", "POST"} {
		body := &failedBody{}
		r := httptest.NewRequest(method, "/v1/introspect?token=ignored", nil)
		r.Body = body
		r.Header.Set("Content-Type", "invalid")
		r.Header.Set("Authorization", basic("limited", 2))
		before := calls
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 || body.reads != 0 || calls != before {
			t.Fatalf("authorization order: status=%d reads=%d calls=%d", w.Code, body.reads, calls)
		}
	}
}

func TestUnifiedIntrospectionCapabilityAndProviderFailure(t *testing.T) {
	p := oidcjwttest.New(t)
	clients := []serviceauth.ClientConfig{{ID: "cleanup", AuthMethods: []string{"basic", "bearer"}, Permissions: []string{"purged_users.read"}, BasicSecrets: []string{secret(1)}}}
	calls := 0
	h := unifiedIntrospection(t, p, clients, &calls)
	for _, header := range []string{"", "Bearer invalid", "Basic invalid"} {
		r := httptest.NewRequest("DELETE", "/v1/introspect", nil)
		body := &failedBody{}
		r.Body = body
		if header != "" {
			r.Header.Set("Authorization", header)
			r.Header.Add("Authorization", header)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 503 || oauthCode(t, w) != "introspection_not_configured" || w.Header().Get("Cache-Control") != "no-store" || len(w.Header().Values("WWW-Authenticate")) != 0 || body.reads != 0 || calls != 0 {
			t.Fatalf("not configured order: status=%d headers=%v reads=%d body=%s", w.Code, w.Header(), body.reads, w.Body)
		}
	}
	if p.JWKSRequests() != 0 {
		t.Fatal("unconfigured capability consulted provider")
	}
	clients[0].Permissions = []string{"tokens.introspect"}
	p.SetFail(true)
	h = unifiedIntrospection(t, p, clients, &calls)
	w := post(h, "POST", "Bearer "+p.Token(p.Claims(time.Now(), "cleanup"), "initial"))
	if w.Code != 503 || oauthCode(t, w) != "temporarily_unavailable" || w.Header().Get("Cache-Control") != "no-store" || len(w.Header().Values("WWW-Authenticate")) != 0 || calls != 0 {
		t.Fatalf("provider unavailable: %d %v %s", w.Code, w.Header(), w.Body)
	}
}
