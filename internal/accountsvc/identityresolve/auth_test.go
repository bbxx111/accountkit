package identityresolve

import (
	"context"
	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/internal/accountsvc/oidcjwt"
	"github.com/bbxx111/accountkit/internal/accountsvc/oidcjwt/oidcjwttest"
	"github.com/bbxx111/accountkit/internal/accountsvc/serviceauth"
	"github.com/bbxx111/accountkit/user"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIdentityResolveBearerAndBasicActor(t *testing.T) {
	p := oidcjwttest.New(t)
	v, err := oidcjwt.New(p.URL(), oidcjwttest.Audience, oidcjwt.Options{HTTPClient: p.Client()})
	if err != nil {
		t.Fatal(err)
	}
	secret := "c3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3M="
	registry, err := serviceauth.NewRegistry([]serviceauth.ClientConfig{{ID: "resolver", AuthMethods: []string{"basic", "bearer"}, BearerSubjects: []string{"machine"}, Permissions: []string{"identities.resolve"}, BasicSecrets: []string{secret}}, {ID: "disabled", AuthMethods: []string{"basic"}, Permissions: []string{"identities.resolve"}, BasicSecrets: []string{"cnJycnJycnJycnJycnJycnJycnJycnJycnJycnJycnI="}}})
	if err != nil {
		t.Fatal(err)
	}
	auth, err := serviceauth.New(serviceauth.Config{Realm: Realm, Endpoint: "identities_resolve", Registry: registry, RequiredPermission: "identities.resolve", BearerVerifier: v})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	var actors []audit.Actor
	h, _ := NewWithAuthenticator(auth, func(ctx context.Context, in user.ResolveIdentityInput) (user.ResolvedIdentity, error) {
		calls++
		actors = append(actors, in.Actor)
		return user.ResolvedIdentity{State: enum.UserActive}, nil
	}, "ns", func(*http.Request) string { return "trusted" })
	bearer := func(id string) string { return "Bearer " + p.Token(p.Claims(time.Now(), id), "initial") }
	for _, tc := range []struct {
		name    string
		headers []string
		want    int
	}{{"basic", nil, 200}, {"bearer", []string{bearer("resolver")}, 200}, {"admin", []string{bearer("admin-frontend")}, 403}, {"disabled", []string{bearer("disabled")}, 401}, {"consumer", []string{"Bearer invalid-consumer-token"}, 401}, {"multiple", []string{bearer("resolver"), bearer("resolver")}, 401}} {
		t.Run(tc.name, func(t *testing.T) {
			before := calls
			r := httptest.NewRequest("POST", "/", strings.NewReader(`{"kind":"EMAIL","subject":"a@example.test"}`))
			r.Header.Set("Content-Type", "application/json")
			if tc.name == "basic" {
				r.SetBasicAuth("resolver", secret)
			}
			for _, header := range tc.headers {
				r.Header.Add("Authorization", header)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
			if tc.want != 200 && calls != before {
				t.Fatal("unauthorized domain call")
			}
		})
	}
	if len(actors) != 2 || actors[0] != actors[1] || actors[0].Type != audit.ActorService {
		t.Fatal(actors)
	}
	p.SetFail(true)
	v2, _ := oidcjwt.New(p.URL(), oidcjwttest.Audience, oidcjwt.Options{HTTPClient: p.Client()})
	a2, _ := serviceauth.New(serviceauth.Config{Realm: Realm, Registry: registry, RequiredPermission: "identities.resolve", BearerVerifier: v2})
	h2, _ := NewWithAuthenticator(a2, func(context.Context, user.ResolveIdentityInput) (user.ResolvedIdentity, error) {
		t.Fatal("unavailable verifier queried")
		return user.ResolvedIdentity{}, nil
	}, "ns", func(*http.Request) string { return "id" })
	r := httptest.NewRequest("POST", "/", nil)
	r.Header.Set("Authorization", bearer("resolver"))
	w := httptest.NewRecorder()
	h2.ServeHTTP(w, r)
	if w.Code != 503 || w.Header().Get("WWW-Authenticate") != "" {
		t.Fatal(w.Code, w.Header())
	}
}

func TestIdentityResolveBearerScopeAndSubject(t *testing.T) {
	p := oidcjwttest.New(t)
	v, err := oidcjwt.New(p.URL(), oidcjwttest.Audience, oidcjwt.Options{HTTPClient: p.Client()})
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	registry, err := serviceauth.NewRegistry([]serviceauth.ClientConfig{{ID: "resolver", AuthMethods: []string{"bearer"}, BearerSubjects: []string{"machine"}, Permissions: []string{"identities.resolve"}}})
	if err != nil {
		t.Fatal(err)
	}
	auth, err := serviceauth.New(serviceauth.Config{Realm: Realm, Registry: registry, RequiredPermission: "identities.resolve", BearerVerifier: v})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	h, err := NewWithAuthenticator(auth, func(context.Context, user.ResolveIdentityInput) (user.ResolvedIdentity, error) {
		calls++
		return user.ResolvedIdentity{}, nil
	}, "ns", func(*http.Request) string { return "trusted" })
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name              string
		want              int
		reason, challenge string
	}{{"missing scope", 403, "CLIENT_NOT_AUTHORIZED", "insufficient_scope"}, {"wrong subject", 401, "INVALID_CLIENT", "invalid_token"}} {
		t.Run(tc.name, func(t *testing.T) {
			c := p.Claims(time.Now(), "resolver")
			if tc.name == "missing scope" {
				delete(c, "scope")
			} else {
				c["sub"] = "human-admin"
			}
			r := httptest.NewRequest("POST", "/v1/identities:resolve", strings.NewReader(`{"kind":"EMAIL","subject":"a@example.test"}`))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Authorization", "Bearer "+p.Token(c, "initial"))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want || calls != 0 || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Body.String(), tc.reason) {
				t.Fatalf("status=%d calls=%d body=%s", w.Code, calls, w.Body)
			}
			if w.Header().Get("WWW-Authenticate") != `Bearer realm="accountsvc-identity-resolution", error="`+tc.challenge+`"` {
				t.Fatal(w.Header())
			}
		})
	}
}
