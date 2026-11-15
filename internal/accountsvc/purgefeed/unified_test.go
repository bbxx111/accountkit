package purgefeed_test

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/internal/accountsvc/oidcjwt"
	"github.com/bbxx111/accountkit/internal/accountsvc/oidcjwt/oidcjwttest"
	"github.com/bbxx111/accountkit/internal/accountsvc/purgefeed"
	"github.com/bbxx111/accountkit/internal/accountsvc/serviceauth"
)

func unifiedFeed(t *testing.T, p *oidcjwttest.Provider, clients []serviceauth.ClientConfig, feed *fakeFeed) http.Handler {
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
	auth, err := serviceauth.New(serviceauth.Config{Realm: purgefeed.Realm, Registry: registry, RequiredPermission: "purged_users.read", BearerVerifier: verifier})
	if err != nil {
		t.Fatal(err)
	}
	h, err := purgefeed.NewWithAuthenticator(auth, feed.list)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestUnifiedPurgeFeedAuthorization(t *testing.T) {
	p := oidcjwttest.New(t)
	feed := &fakeFeed{records: records(2)}
	h := unifiedFeed(t, p, []serviceauth.ClientConfig{
		{ID: "both", AuthMethods: []string{"basic", "bearer"}, BearerSubjects: []string{"machine"}, Permissions: []string{"tokens.introspect", "purged_users.read"}, BasicSecrets: []string{secret(1)}},
		{ID: "limited", AuthMethods: []string{"basic", "bearer"}, BearerSubjects: []string{"machine"}, Permissions: []string{"tokens.introspect"}, BasicSecrets: []string{secret(2)}},
		{ID: "basic-only", AuthMethods: []string{"basic"}, Permissions: []string{"purged_users.read"}, BasicSecrets: []string{secret(3)}},
		{ID: "bearer-only", AuthMethods: []string{"bearer"}, BearerSubjects: []string{"machine"}, Permissions: []string{"purged_users.read"}},
	}, feed)
	basic := func(id string, b byte) string {
		return "Basic " + base64.StdEncoding.EncodeToString([]byte(id+":"+secret(b)))
	}
	bearer := func(id string) string { return "Bearer " + p.Token(p.Claims(time.Now(), id), "initial") }
	both := []string{`Basic realm="accountsvc-purge-feed"`, `Bearer realm="accountsvc-purge-feed"`}
	for _, tc := range []struct {
		name, authorization string
		status              int
		reason              string
		challenges          []string
	}{
		{"basic success", basic("both", 1), 200, "", nil},
		{"bearer success", bearer("both"), 200, "", nil},
		{"basic permission", basic("limited", 2), 403, "PERMISSION_DENIED/CLIENT_NOT_AUTHORIZED", nil},
		{"bearer permission", bearer("limited"), 403, "PERMISSION_DENIED/CLIENT_NOT_AUTHORIZED", []string{`Bearer realm="accountsvc-purge-feed", error="insufficient_scope"`}},
		{"unknown bearer", bearer("unknown"), 403, "PERMISSION_DENIED/CLIENT_NOT_AUTHORIZED", []string{`Bearer realm="accountsvc-purge-feed", error="insufficient_scope"`}},
		{"disabled bearer", bearer("basic-only"), 401, "UNAUTHENTICATED/INVALID_CLIENT", both},
		{"disabled basic", basic("bearer-only", 1), 401, "UNAUTHENTICATED/INVALID_CLIENT", both},
		{"invalid bearer", "Bearer invalid", 401, "UNAUTHENTICATED/TOKEN_INVALID", []string{both[0], both[1] + `, error="invalid_token"`}},
		{"missing", "", 401, "UNAUTHENTICATED/INVALID_CLIENT", both},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := feed.calls
			var headers []string
			if tc.authorization != "" {
				headers = []string{tc.authorization}
			}
			w := getWith(h, headers...)
			if w.Code != tc.status || w.Header().Get("Cache-Control") != "no-store" || !reflect.DeepEqual(w.Header().Values("WWW-Authenticate"), tc.challenges) {
				t.Fatalf("status=%d headers=%v body=%s", w.Code, w.Header(), w.Body)
			}
			if tc.status == 200 {
				if feed.calls != before+1 || len(decode(t, w).PurgedUsers) != 2 {
					t.Fatalf("feed not read: %s", w.Body)
				}
			} else if feed.calls != before || reason(t, w) != tc.reason {
				t.Fatalf("unauthorized feed read or code: %s", w.Body)
			}
		})
	}
	for _, method := range []string{"GET", "POST"} {
		r := httptest.NewRequest(method, "/v1/purgedUsers?page_size=invalid&page_token=bad", nil)
		r.Header.Set("Authorization", basic("limited", 2))
		before := feed.calls
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 || feed.calls != before {
			t.Fatalf("permission must precede method/query parsing: %d %s", w.Code, w.Body)
		}
	}
	// Authorized callers still receive the original pagination error.
	w := get(h, "?page_token=bad", "both", secret(1))
	if w.Code != 400 || reason(t, w) != "INVALID_ARGUMENT/INVALID_PAGE_TOKEN" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("pagination error changed: %d %s", w.Code, w.Body)
	}
}

func TestUnifiedPurgeFeedCapabilityAndProviderFailure(t *testing.T) {
	p := oidcjwttest.New(t)
	feed := &fakeFeed{records: records(1)}
	clients := []serviceauth.ClientConfig{{ID: "intro", AuthMethods: []string{"basic", "bearer"}, BearerSubjects: []string{"machine"}, Permissions: []string{"tokens.introspect"}, BasicSecrets: []string{secret(1)}}}
	h := unifiedFeed(t, p, clients, feed)
	for _, header := range []string{"", "Bearer invalid", "Basic invalid"} {
		r := httptest.NewRequest("POST", "/v1/purgedUsers?page_size=bad&page_token=bad", nil)
		if header != "" {
			r.Header.Set("Authorization", header)
			r.Header.Add("Authorization", header)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 503 || reason(t, w) != "UNAVAILABLE/PURGE_FEED_NOT_CONFIGURED" || w.Header().Get("Cache-Control") != "no-store" || len(w.Header().Values("WWW-Authenticate")) != 0 || feed.calls != 0 {
			t.Fatalf("not configured order: %d %v %s", w.Code, w.Header(), w.Body)
		}
	}
	if p.JWKSRequests() != 0 {
		t.Fatal("unconfigured capability consulted provider")
	}
	clients[0].Permissions = []string{"purged_users.read"}
	p.SetFail(true)
	h = unifiedFeed(t, p, clients, feed)
	w := getWith(h, "Bearer "+p.Token(p.Claims(time.Now(), "intro"), "initial"))
	if w.Code != 503 || reason(t, w) != "UNAVAILABLE/DEPENDENCY_UNAVAILABLE" || w.Header().Get("Retry-After") != "1" || w.Header().Get("Cache-Control") != "no-store" || len(w.Header().Values("WWW-Authenticate")) != 0 || feed.calls != 0 {
		t.Fatalf("provider failure: %d %v %s", w.Code, w.Header(), w.Body)
	}
}
