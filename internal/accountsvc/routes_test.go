package accountsvc

import (
	"context"
	"encoding/base64"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bbxx111/accountkit/internal/accountsvc/identityresolve"
	"github.com/bbxx111/accountkit/internal/accountsvc/introspection"
	"github.com/bbxx111/accountkit/internal/accountsvc/purgefeed"
	"github.com/bbxx111/accountkit/internal/accountsvc/serviceauth"
	"github.com/bbxx111/accountkit/user"
)

// A swapped permission, wrong mount, or method-specific service mount must fail
// these requests against the production authenticator factory and handlers.
func TestRouteServicePermissionsAndAuthenticationOrder(t *testing.T) {
	permissions := []string{"tokens.introspect", "purged_users.read", "identities.resolve"}
	clients := make([]serviceauth.ClientConfig, 3)
	secrets := make([]string, 3)
	for i, permission := range permissions {
		secrets[i] = base64.StdEncoding.EncodeToString([]byte(strings.Repeat(string(rune('a'+i)), 32)))
		clients[i] = serviceauth.ClientConfig{ID: []string{"intro", "purge", "resolve"}[i], AuthMethods: []string{"basic"}, Permissions: []string{permission}, BasicSecrets: []string{secrets[i]}}
	}
	registry, err := serviceauth.NewRegistry(clients)
	if err != nil {
		t.Fatal(err)
	}
	introAuth, purgeAuth, resolveAuth, _, err := serviceAuthenticators(Config{ServiceClients: registry}, slog.New(slog.DiscardHandler), nil)
	if err != nil {
		t.Fatal(err)
	}
	calls := [3]int{}
	intro, err := introspection.NewWithAuthenticator(introAuth, func(context.Context, string) (user.Principal, error) {
		calls[0]++
		return user.Principal{}, user.ErrInvalidToken
	})
	if err != nil {
		t.Fatal(err)
	}
	purge, err := purgefeed.NewWithAuthenticator(purgeAuth, func(context.Context, int64, int) ([]user.PurgedUser, int64, error) { calls[1]++; return nil, 0, nil })
	if err != nil {
		t.Fatal(err)
	}
	resolve, err := identityresolve.NewWithAuthenticator(resolveAuth, func(context.Context, user.ResolveIdentityInput) (user.ResolvedIdentity, error) {
		calls[2]++
		return user.ResolvedIdentity{}, user.ErrNotFound
	}, "test", func(*http.Request) string { return "request" })
	if err != nil {
		t.Fatal(err)
	}
	h := serviceHandler(Config{}, http.NotFoundHandler(), http.NotFoundHandler(), intro, purge, nil, resolve, &healthState{})
	cases := []struct {
		path, method, contentType, body string
		want                            int
	}{
		{"/v1/introspect", "POST", "application/x-www-form-urlencoded", "token=access", 200},
		{"/v1/purgedUsers", "GET", "", "", 200},
		{"/v1/identities:resolve", "POST", "application/json", `{"kind":"EMAIL","subject":"test@example.com"}`, 404},
	}
	for i, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			for credential := -3; credential < 3; credential++ {
				for _, wrongMethod := range []bool{false, true} {
					method := tc.method
					if wrongMethod {
						method = "DELETE"
					}
					r := httptest.NewRequest(method, tc.path, strings.NewReader(tc.body))
					r.Header.Set("Content-Type", tc.contentType)
					if credential != i {
						// Invalid business inputs must not mask a caller authentication error.
						r.Header.Set("Content-Type", "application/invalid")
						r.URL.RawQuery = "page_size=invalid"
					}
					if credential == -2 {
						r.Header.Set("Authorization", "Bearer consumer-token")
					}
					if credential == -1 {
						r.Header.Set("Authorization", "Bearer administrator-token")
					}
					if credential >= 0 {
						r.SetBasicAuth(clients[credential].ID, secrets[credential])
					}
					want := 401
					if credential >= 0 {
						want = 403
					}
					if credential == i {
						want = tc.want
						if wrongMethod {
							want = 405
						}
					}
					before := calls
					w := httptest.NewRecorder()
					h.ServeHTTP(w, r)
					if w.Code != want {
						t.Fatalf("credential=%d method=%s: status=%d want=%d body=%s", credential, method, w.Code, want, w.Body)
					}
					if credential == i && !wrongMethod {
						before[i]++
					}
					if calls != before {
						t.Fatalf("unexpected business calls: %v want %v", calls, before)
					}
					if credential == i && wrongMethod && w.Header().Get("Allow") != tc.method {
						t.Fatal("method declaration differs from handler")
					}
				}
			}
		})
	}
	// Exercise declared service paths, rather than checking a copied constant table.
	n := 0
	for _, route := range routeDeclarations() {
		if route.category != routeService {
			continue
		}
		n++
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("DELETE", route.path, nil))
		if w.Code != 401 {
			t.Fatalf("declared service route %s not protected: %d", route.path, w.Code)
		}
		matched := false
		for i, permission := range permissions {
			if permission != route.permission {
				continue
			}
			matched = true
			tc := cases[i]
			r := httptest.NewRequest(route.methods, route.path, strings.NewReader(tc.body))
			r.Header.Set("Content-Type", tc.contentType)
			r.SetBasicAuth(clients[i].ID, secrets[i])
			before := calls
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			before[i]++
			if w.Code != tc.want || calls != before {
				t.Fatalf("declaration %s/%s did not reach its authorized operation: status=%d calls=%v want=%v", route.path, permission, w.Code, calls, before)
			}
		}
		if !matched {
			t.Fatalf("unrecognized declared service permission %q", route.permission)
		}
	}
	if n != 3 {
		t.Fatalf("service routes=%d", n)
	}
}

func TestRouteInventoryAccessBoundaries(t *testing.T) {
	mark := func(status int) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) })
	}
	state := &healthState{}
	h := serviceHandler(Config{MetricsEnabled: true}, mark(201), mark(202), mark(203), mark(204), mark(205), mark(206), state, mark(207))
	seen := map[routeCategory]int{}
	for _, route := range routeDeclarations() {
		seen[route.category]++
		method, path, want := route.methods, route.path, 0
		switch route.category {
		case routeConsumer:
			method, path, want = "GET", path+"/users/me", 201
		case routeAdmin:
			method, path, want = "GET", path+"/users", 202
		case routePublic:
			method, want = "GET", 205
		case routeService:
			continue // Real authentication and handlers are covered above.
		case routeOperations:
			switch path {
			case "/healthz":
				want = 200
			case "/readyz":
				want = 503
			case "/metrics":
				want = 207
			default:
				t.Fatalf("unknown operations route %s", path)
			}
		default:
			t.Fatalf("unknown route category %s", route.category)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, path, nil))
		if w.Code != want {
			t.Fatalf("%s %s category %s: got %d want %d", method, path, route.category, w.Code, want)
		}
	}
	if len(seen) != 5 {
		t.Fatalf("route categories=%v", seen)
	}
}
