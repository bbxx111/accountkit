package purgefeed_test

import (
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

func bearerFeed(t *testing.T, p *oidcjwttest.Provider, basic bool, feed *fakeFeed) http.Handler {
	t.Helper()
	tokens, err := oidcjwt.New(p.URL(), oidcjwttest.Audience, oidcjwt.Options{HTTPClient: p.Client(), RefreshInterval: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	cfg := serviceauth.Config{Realm: purgefeed.Realm, Endpoint: "purged_users", BearerVerifier: tokens, BearerClients: []string{"purge-client"}}
	if basic {
		cfg.Basic = map[string][]string{"ai-food": {secret(1)}}
	}
	auth, err := serviceauth.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	h, err := purgefeed.NewWithAuthenticator(auth, feed.list)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func getWith(h http.Handler, authorization ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "/v1/purgedUsers", nil)
	for _, a := range authorization {
		r.Header.Add("Authorization", a)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestPurgeFeedBearerErrors(t *testing.T) {
	p := oidcjwttest.New(t)
	feed := &fakeFeed{records: records(1)}
	h := bearerFeed(t, p, true, feed)
	now := time.Now()
	valid := "Bearer " + p.Token(p.Claims(now, "purge-client"), "initial")
	both := []string{`Basic realm="accountsvc-purge-feed"`, `Bearer realm="accountsvc-purge-feed"`}
	for _, tc := range []struct {
		name          string
		authorization []string
		status        int
		reason        string
		challenges    []string
	}{
		{"valid token", []string{valid}, 200, "", nil},
		{"no credentials", nil, 401, "UNAUTHENTICATED/INVALID_CLIENT", both},
		{"two headers", []string{valid, valid}, 401, "UNAUTHENTICATED/INVALID_CLIENT", both},
		{"expired token", []string{"Bearer " + p.Token(p.Claims(now.Add(-time.Hour), "purge-client"), "initial")}, 401, "UNAUTHENTICATED/TOKEN_INVALID", []string{`Basic realm="accountsvc-purge-feed"`, `Bearer realm="accountsvc-purge-feed", error="invalid_token"`}},
		{"introspection client", []string{"Bearer " + p.Token(p.Claims(now, "intro-client"), "initial")}, 403, "PERMISSION_DENIED/CLIENT_NOT_AUTHORIZED", []string{`Bearer realm="accountsvc-purge-feed", error="insufficient_scope"`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := feed.calls
			w := getWith(h, tc.authorization...)
			if w.Code != tc.status || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("status %d cache %q body %s", w.Code, w.Header().Get("Cache-Control"), w.Body)
			}
			if got := w.Header().Values("WWW-Authenticate"); !reflect.DeepEqual(got, tc.challenges) {
				t.Fatalf("challenges = %q, want %q", got, tc.challenges)
			}
			if tc.status == 200 {
				if feed.calls != before+1 || len(decode(t, w).PurgedUsers) != 1 {
					t.Fatal("authorized caller did not receive the page")
				}
				return
			}
			if feed.calls != before {
				t.Fatal("rejected caller read the feed")
			}
			if got := reason(t, w); got != tc.reason {
				t.Fatalf("reason = %q, want %q", got, tc.reason)
			}
		})
	}
}

func TestPurgeFeedBearerProviderUnavailable(t *testing.T) {
	p := oidcjwttest.New(t)
	p.SetFail(true)
	feed := &fakeFeed{records: records(1)}
	h := bearerFeed(t, p, false, feed)
	w := getWith(h, "Bearer "+p.Token(p.Claims(time.Now(), "purge-client"), "initial"))
	if w.Code != 503 || reason(t, w) != "UNAVAILABLE/DEPENDENCY_UNAVAILABLE" || w.Header().Get("Retry-After") != "1" || len(w.Header().Values("WWW-Authenticate")) != 0 || feed.calls != 0 {
		t.Fatalf("status %d headers %v body %s", w.Code, w.Header(), w.Body)
	}
}

func TestPurgeFeedNotConfiguredWithoutAnyMethod(t *testing.T) {
	auth, err := serviceauth.New(serviceauth.Config{Realm: purgefeed.Realm})
	if err != nil {
		t.Fatal(err)
	}
	feed := &fakeFeed{records: records(1)}
	h, err := purgefeed.NewWithAuthenticator(auth, feed.list)
	if err != nil {
		t.Fatal(err)
	}
	if w := getWith(h); w.Code != 503 || reason(t, w) != "UNAVAILABLE/PURGE_FEED_NOT_CONFIGURED" || feed.calls != 0 {
		t.Fatalf("not configured: %d %s", w.Code, w.Body)
	}
}
