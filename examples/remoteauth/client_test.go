package remoteauth_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/examples/remoteauth"
)

const active = `{"active":true,"sub":"u_0000000000001","scope":"user","sid":"s_0000000000001","auth_time":1700000000}`

func clientFor(t *testing.T, h http.Handler) *remoteauth.Client {
	t.Helper()
	s := httptest.NewTLSServer(h)
	t.Cleanup(s.Close)
	c, err := remoteauth.NewClient(remoteauth.Config{Endpoint: s.URL + "/internal/v1/introspect", ClientID: "business", ClientSecret: "service-secret", HTTPClient: s.Client()})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestClientCredentialsBodyAndNoCache(t *testing.T) {
	var calls atomic.Int32
	c := clientFor(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.RawQuery != "" || r.URL.Path != "/internal/v1/introspect" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
		}
		id, secret, ok := r.BasicAuth()
		if !ok || id != "business" || secret != "service-secret" {
			t.Error("consumer credential used as service credential")
		}
		if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Error("not form")
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if len(r.PostForm["token"]) != 1 || r.PostForm.Get("token") != "consumer+token/with=symbols" {
			t.Error("consumer token corrupted")
		}
		w.Header().Set("Content-Type", "application/json")
		if calls.Add(1) == 1 {
			_, _ = w.Write([]byte(active))
		} else {
			_, _ = w.Write([]byte(`{"active":false}`))
		}
	}))
	one, err := c.Introspect(context.Background(), "consumer+token/with=symbols")
	if err != nil || !one.Active || one.Subject != "u_0000000000001" || one.SessionID != "s_0000000000001" || one.Scope != "user" || one.AuthTime.Unix() != 1700000000 {
		t.Fatalf("first response %+v %v", one, err)
	}
	two, err := c.Introspect(context.Background(), "consumer+token/with=symbols")
	if err != nil || two.Active || calls.Load() != 2 {
		t.Fatalf("successful response cached: %+v %v", two, err)
	}
}

func TestMalformedOrFailedIntrospectionIsUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name, body, contentType string
		status                  int
	}{
		{name: "unauthorized service", body: `{"error":"invalid_client"}`, status: 401},
		{name: "unavailable", body: "private-dsn-secret", status: 503},
		{name: "invalid JSON", body: "private-secret"},
		{name: "missing active", body: `{}`},
		{name: "null", body: `null`},
		{name: "null active", body: `{"active":null}`},
		{name: "bad active type", body: `{"active":"true"}`},
		{name: "duplicate active", body: `{"active":false,"active":true}`},
		{name: "missing claims", body: `{"active":true}`},
		{name: "bad subject", body: strings.Replace(active, "u_0000000000001", "users/1", 1)},
		{name: "bad session", body: strings.Replace(active, "s_0000000000001", "sessions/1", 1)},
		{name: "missing auth_time", body: strings.Replace(active, `,"auth_time":1700000000`, "", 1)},
		{name: "fractional auth_time", body: strings.Replace(active, "1700000000", "1700000000.5", 1)},
		{name: "trailing JSON", body: active + `{}`},
		{name: "oversize", body: strings.Repeat(" ", 64*1024) + active},
		{name: "wrong content type", body: active, contentType: "text/html"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := clientFor(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ct := tc.contentType
				if ct == "" {
					ct = "application/json"
				}
				w.Header().Set("Content-Type", ct)
				status := tc.status
				if status == 0 {
					status = 200
				}
				w.WriteHeader(status)
				_, _ = w.Write([]byte(tc.body))
			}))
			_, err := c.Introspect(context.Background(), "consumer-token")
			if !errors.Is(err, remoteauth.ErrUnavailable) || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "consumer-token") {
				t.Fatalf("unsafe dependency classification: %v", err)
			}
		})
	}
}

func TestClientTimeoutAndRedirectRefuseAccess(t *testing.T) {
	release := make(chan struct{})
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer s.Close()
	defer close(release)
	c, err := remoteauth.NewClient(remoteauth.Config{Endpoint: s.URL, ClientID: "business", ClientSecret: "secret", HTTPClient: s.Client(), Timeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Introspect(context.Background(), "token"); !errors.Is(err, remoteauth.ErrUnavailable) {
		t.Fatalf("timeout misclassified: %v", err)
	}
	var redirected atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Store(true) }))
	defer target.Close()
	c = clientFor(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	if _, err = c.Introspect(context.Background(), "token"); !errors.Is(err, remoteauth.ErrUnavailable) || redirected.Load() {
		t.Fatal("redirect followed or misclassified")
	}
}

type failedTransport struct{}

func (failedTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("private-network-address")
}

func TestNetworkFailureAndConfiguration(t *testing.T) {
	c, err := remoteauth.NewClient(remoteauth.Config{Endpoint: "https://account.example/internal/v1/introspect", ClientID: "business", ClientSecret: "secret", HTTPClient: &http.Client{Transport: failedTransport{}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Introspect(context.Background(), "token"); !errors.Is(err, remoteauth.ErrUnavailable) || strings.Contains(err.Error(), "private") {
		t.Fatalf("network error: %v", err)
	}
	for _, endpoint := range []string{"http://account.example/introspect", "https://user:password@account.example/introspect", "https://account.example/introspect?secret=x", "https://account.example/introspect#fragment", "relative"} {
		if _, err := remoteauth.NewClient(remoteauth.Config{Endpoint: endpoint, ClientID: "business", ClientSecret: "secret"}); err == nil {
			t.Errorf("accepted unsafe endpoint %q", endpoint)
		}
	}
}

func TestProtectedHandlerAuthorization(t *testing.T) {
	for _, tc := range []struct {
		name, body, header   string
		owned                bool
		upstreamStatus, want int
		ownerCalled          bool
	}{
		{name: "allowed", body: active, owned: true, want: 204, ownerCalled: true},
		{name: "bind scope", body: strings.Replace(active, `"user"`, `"user:bind"`, 1), owned: true, want: 403},
		{name: "undelete scope", body: strings.Replace(active, `"user"`, `"user:undelete"`, 1), owned: true, want: 403},
		{name: "old authentication", body: strings.Replace(active, "1700000000", "1699990000", 1), owned: true, want: 400},
		{name: "future authentication", body: strings.Replace(active, "1700000000", "1800000000", 1), owned: true, want: 400},
		{name: "other owner", body: active, want: 404, ownerCalled: true},
		{name: "inactive", body: `{"active":false}`, want: 401},
		{name: "upstream auth failure", body: `{"error":"invalid_client"}`, upstreamStatus: 401, want: 503},
		{name: "malformed response", body: `{}`, want: 503},
		{name: "missing bearer", body: active, header: "-", want: 401},
		{name: "service credential not bearer", body: active, header: "Basic abc", want: 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			c := clientFor(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				if tc.upstreamStatus != 0 {
					w.WriteHeader(tc.upstreamStatus)
				}
				_, _ = w.Write([]byte(tc.body))
			}))
			ownerCalled := false
			h := remoteauth.ProtectedHandler{Client: c, MaxAuthAge: 5 * time.Minute, Now: func() time.Time { return time.Unix(1700000060, 0) }, OwnsResource: func(ctx context.Context, userID, resourceID string) (bool, error) {
				ownerCalled = true
				if userID != "u_0000000000001" || resourceID != "document-1" {
					t.Error("ownership query not scoped to principal")
				}
				return tc.owned, nil
			}}
			r := httptest.NewRequest("GET", "/documents/document-1", nil)
			r.SetPathValue("resource", "document-1")
			header := tc.header
			if header == "" {
				header = "Bearer consumer-token"
			}
			if header != "-" {
				r.Header.Set("Authorization", header)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want || ownerCalled != tc.ownerCalled {
				t.Fatalf("authorization %d %s; ownership=%v", w.Code, w.Body, ownerCalled)
			}
			if tc.want == 503 && w.Header().Get("WWW-Authenticate") != "" {
				t.Fatal("dependency failure prompts reauthentication")
			}
			if tc.header != "" && calls.Load() != 0 {
				t.Fatal("malformed bearer called upstream")
			}
		})
	}
}

func TestProtectedRequestsDoNotReuseSuccessfulIntrospection(t *testing.T) {
	var calls atomic.Int32
	c := clientFor(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if calls.Add(1) == 1 {
			_, _ = w.Write([]byte(active))
		} else {
			w.WriteHeader(503)
		}
	}))
	h := remoteauth.ProtectedHandler{Client: c, MaxAuthAge: time.Hour, Now: func() time.Time { return time.Unix(1700000060, 0) }, OwnsResource: func(context.Context, string, string) (bool, error) { return true, nil }}
	for _, want := range []int{204, 503} {
		r := httptest.NewRequest("GET", "/documents/one", nil)
		r.SetPathValue("resource", "one")
		r.Header.Set("Authorization", "Bearer token")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("status %d want %d", w.Code, want)
		}
	}
	if calls.Load() != 2 {
		t.Fatal("protected handler cached successful result")
	}
}

func TestProtectedResourceFailureIsUnavailable(t *testing.T) {
	c := clientFor(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(active))
	}))
	h := remoteauth.ProtectedHandler{Client: c, MaxAuthAge: time.Minute, Now: func() time.Time { return time.Unix(1700000001, 0) }, OwnsResource: func(context.Context, string, string) (bool, error) {
		return false, errors.New("private-database-password")
	}}
	r := httptest.NewRequest("GET", "/documents/one", nil)
	r.SetPathValue("resource", "one")
	r.Header.Set("Authorization", "Bearer token")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 503 || strings.Contains(w.Body.String(), "private") || w.Header().Get("WWW-Authenticate") != "" {
		t.Fatalf("resource dependency failure: %d %s", w.Code, w.Body)
	}
}

func TestExplicitDevelopmentHTTPAndCallerCancellation(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"active":false}`))
	}))
	defer s.Close()
	c, err := remoteauth.NewClient(remoteauth.Config{Endpoint: s.URL, ClientID: "business", ClientSecret: "secret", AllowHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	result, err := c.Introspect(context.Background(), "token")
	if err != nil || result.Active {
		t.Fatalf("development HTTP failed: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = c.Introspect(ctx, "token"); !errors.Is(err, remoteauth.ErrUnavailable) {
		t.Fatalf("canceled call misclassified: %v", err)
	}
}
