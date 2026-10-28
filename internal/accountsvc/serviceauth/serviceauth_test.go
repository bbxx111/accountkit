package serviceauth_test

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/internal/accountsvc/oidcjwt"
	"github.com/bbxx111/accountkit/internal/accountsvc/oidcjwt/oidcjwttest"
	"github.com/bbxx111/accountkit/internal/accountsvc/serviceauth"
	"github.com/golang-jwt/jwt/v5"
)

const realm = "accountsvc-introspection"

var basicSecret = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))

type env struct {
	p    *oidcjwttest.Provider
	now  time.Time
	unix atomic.Int64
	logs bytes.Buffer
	auth *serviceauth.Authenticator
}

func (e *env) clock() time.Time { return time.Unix(e.unix.Load(), 0) }
func (e *env) advance(d time.Duration) {
	e.unix.Store(e.now.Add(d).Unix())
}

func newEnv(t *testing.T, basic, bearer bool) *env {
	t.Helper()
	e := &env{p: oidcjwttest.New(t), now: time.Now().Truncate(time.Second)}
	e.unix.Store(e.now.Unix())
	cfg := serviceauth.Config{Realm: realm, Endpoint: "introspect", Logger: slog.New(slog.NewTextHandler(&e.logs, nil))}
	if basic {
		cfg.Basic = map[string][]string{"basic-client": {basicSecret}}
	}
	if bearer {
		v, err := oidcjwt.New(e.p.URL(), oidcjwttest.Audience, oidcjwt.Options{HTTPClient: e.p.Client(), Now: e.clock, RefreshInterval: 10 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		cfg.Tokens, cfg.BearerClients = v, []string{"intro-client"}
	}
	a, err := serviceauth.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.auth = a
	return e
}

func (e *env) do(headers ...string) (serviceauth.Result, []string) {
	r := httptest.NewRequest("POST", "https://accountsvc.test/v1/introspect", nil)
	for _, h := range headers {
		r.Header.Add("Authorization", h)
	}
	res := e.auth.Authenticate(r)
	return res, e.auth.Challenges(res)
}

func (e *env) bearer(claims jwt.MapClaims, kid string) string {
	return "Bearer " + e.p.Token(claims, kid)
}

var (
	bothChallenges    = []string{`Basic realm="accountsvc-introspection"`, `Bearer realm="accountsvc-introspection"`}
	invalidChallenges = []string{`Basic realm="accountsvc-introspection"`, `Bearer realm="accountsvc-introspection", error="invalid_token"`}
	scopeChallenge    = []string{`Bearer realm="accountsvc-introspection", error="insufficient_scope"`}
)

func expect(t *testing.T, res serviceauth.Result, challenges []string, outcome serviceauth.Outcome, wantChallenges []string) {
	t.Helper()
	if res.Outcome != outcome {
		t.Fatalf("outcome = %v, want %v (%+v)", res.Outcome, outcome, res)
	}
	if !reflect.DeepEqual(challenges, wantChallenges) {
		t.Fatalf("challenges = %q, want %q", challenges, wantChallenges)
	}
}

func TestBearerDecisions(t *testing.T) {
	e := newEnv(t, true, true)
	now := e.now
	claims := func(mut func(jwt.MapClaims)) jwt.MapClaims {
		c := e.p.Claims(now, "intro-client")
		if mut != nil {
			mut(c)
		}
		return c
	}
	t.Run("1 valid", func(t *testing.T) {
		res, ch := e.do(e.bearer(claims(nil), "initial"))
		expect(t, res, ch, serviceauth.OK, nil)
		if res.ClientID != "intro-client" || res.Method != serviceauth.MethodBearer {
			t.Fatalf("result = %+v", res)
		}
	})
	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	consumer := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{"iss": "consumer", "aud": "business", "sub": "u_0000000000000", "exp": now.Add(time.Minute).Unix(), "iat": now.Unix()})
	consumer.Header["kid"] = "1"
	consumerToken, err := consumer.SignedString(ec)
	if err != nil {
		t.Fatal(err)
	}
	noneToken, err := jwt.NewWithClaims(jwt.SigningMethodNone, claims(nil)).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatal(err)
	}
	jku := jwt.NewWithClaims(jwt.SigningMethodRS256, claims(nil))
	jku.Header["kid"], jku.Header["jku"] = "initial", "https://attacker.example/keys"
	jkuToken, err := jku.SignedString(e.p.Key)
	if err != nil {
		t.Fatal(err)
	}
	unauthenticated := map[string]string{
		"2 wrong audience":      e.bearer(claims(func(c jwt.MapClaims) { c["aud"] = "other" }), "initial"),
		"3 administrator token": e.bearer(claims(func(c jwt.MapClaims) { c["aud"], c["azp"] = "admin-api", "admin-console" }), "initial"),
		"4 consumer token":      "Bearer " + consumerToken,
		"7 azp missing":         e.bearer(claims(func(c jwt.MapClaims) { delete(c, "azp") }), "initial"),
		"7 azp empty":           e.bearer(claims(func(c jwt.MapClaims) { c["azp"] = "" }), "initial"),
		"7 azp not a string":    e.bearer(claims(func(c jwt.MapClaims) { c["azp"] = 7 }), "initial"),
		"8 expired beyond leeway": e.bearer(claims(func(c jwt.MapClaims) {
			c["exp"] = now.Add(-31 * time.Second).Unix()
			c["iat"] = now.Add(-time.Minute).Unix()
		}), "initial"),
		"9 lifetime over one hour": e.bearer(claims(func(c jwt.MapClaims) { c["exp"] = now.Add(time.Hour + time.Second).Unix() }), "initial"),
		"9 iat missing":            e.bearer(claims(func(c jwt.MapClaims) { delete(c, "iat") }), "initial"),
		"10 alg none":              "Bearer " + noneToken,
		"11 jku":                   "Bearer " + jkuToken,
		"oversized":                "Bearer " + strings.Repeat("a", 16*1024+1),
	}
	for name, header := range unauthenticated {
		t.Run(name, func(t *testing.T) {
			res, ch := e.do(header)
			expect(t, res, ch, serviceauth.Unauthenticated, invalidChallenges)
			if !res.BearerAttempted || res.ClientID != "" {
				t.Fatalf("result = %+v", res)
			}
		})
	}
	t.Run("8 within leeway", func(t *testing.T) {
		res, ch := e.do(e.bearer(claims(func(c jwt.MapClaims) {
			c["exp"] = now.Add(-20 * time.Second).Unix()
			c["iat"] = now.Add(-time.Minute).Unix()
		}), "initial"))
		expect(t, res, ch, serviceauth.OK, nil)
	})
	forbidden := map[string]string{
		"3b administrator token with service audience": e.bearer(claims(func(c jwt.MapClaims) {
			c["aud"], c["azp"] = []string{"admin-api", oidcjwttest.Audience}, "admin-console"
		}), "initial"),
		"5 unknown client":                e.bearer(claims(func(c jwt.MapClaims) { c["azp"] = "stranger" }), "initial"),
		"6 client of the other interface": e.bearer(claims(func(c jwt.MapClaims) { c["azp"] = "purge-client" }), "initial"),
	}
	for name, header := range forbidden {
		t.Run(name, func(t *testing.T) {
			res, ch := e.do(header)
			expect(t, res, ch, serviceauth.Forbidden, scopeChallenge)
			if res.ClientID == "" || res.ClientID == "intro-client" {
				t.Fatalf("forbidden result must name the client: %+v", res)
			}
		})
	}
	if !strings.Contains(e.logs.String(), "client_id=stranger") || !strings.Contains(e.logs.String(), "endpoint=introspect") || !strings.Contains(e.logs.String(), "auth_method=bearer") {
		t.Fatalf("403 must be logged with client, endpoint and method: %s", e.logs.String())
	}
	for _, header := range append(mapValues(unauthenticated), mapValues(forbidden)...) {
		if token := strings.TrimPrefix(header, "Bearer "); strings.Contains(e.logs.String(), token[:min(len(token), 40)]) {
			t.Fatal("logs contain a token")
		}
	}
	t.Run("12 missing kid", func(t *testing.T) {
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims(nil))
		signed, err := token.SignedString(e.p.Key)
		if err != nil {
			t.Fatal(err)
		}
		res, ch := e.do("Bearer " + signed)
		expect(t, res, ch, serviceauth.Unauthenticated, invalidChallenges)
	})
}

func mapValues(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

func TestProviderFailureAndRecovery(t *testing.T) {
	e := newEnv(t, true, true)
	e.p.SetFail(true)
	token := e.bearer(e.p.Claims(e.now, "intro-client"), "initial")
	res, ch := e.do(token)
	expect(t, res, ch, serviceauth.Unavailable, nil)
	t.Run("13 within cooldown", func(t *testing.T) {
		before := e.p.JWKSRequests()
		for range 5 {
			res, ch := e.do(token)
			expect(t, res, ch, serviceauth.Unavailable, nil)
		}
		if e.p.JWKSRequests() != before {
			t.Fatal("failure cooldown re-fetched")
		}
	})
	t.Run("18 basic unaffected", func(t *testing.T) {
		res, ch := e.do("Basic " + base64.StdEncoding.EncodeToString([]byte("basic-client:"+basicSecret)))
		expect(t, res, ch, serviceauth.OK, nil)
		if res.Method != serviceauth.MethodBasic || res.ClientID != "basic-client" {
			t.Fatalf("result = %+v", res)
		}
	})
	t.Run("14 recovery", func(t *testing.T) {
		e.p.SetFail(false)
		e.advance(10 * time.Second)
		res, ch := e.do(token)
		expect(t, res, ch, serviceauth.OK, nil)
	})
}

func TestRandomKidsAreBounded(t *testing.T) {
	e := newEnv(t, false, true)
	if res, _ := e.do(e.bearer(e.p.Claims(e.now, "intro-client"), "initial")); res.Outcome != serviceauth.OK {
		t.Fatalf("warm-up failed: %+v", res)
	}
	before := e.p.JWKSRequests()
	var wg sync.WaitGroup
	for i := range 50 {
		header := e.bearer(e.p.Claims(e.now, "intro-client"), strings.Repeat("r", i+1))
		wg.Go(func() {
			if res, _ := e.do(header); res.Outcome != serviceauth.Unauthenticated {
				t.Errorf("random kid outcome = %v", res.Outcome)
			}
		})
	}
	wg.Wait()
	if n := e.p.JWKSRequests() - before; n > 0 {
		t.Fatalf("random kids within the cooldown after a successful fetch triggered %d JWKS requests", n)
	}
	e.advance(10 * time.Second)
	for i := range 50 {
		header := e.bearer(e.p.Claims(e.now, "intro-client"), "x"+strings.Repeat("r", i+1))
		wg.Go(func() { e.do(header) })
	}
	wg.Wait()
	if n := e.p.JWKSRequests() - before; n != 1 {
		t.Fatalf("one window must allow exactly one JWKS request, got %d", n)
	}
}

func TestNewKidWithinWindowThenAccepted(t *testing.T) {
	e := newEnv(t, false, true)
	if res, _ := e.do(e.bearer(e.p.Claims(e.now, "intro-client"), "initial")); res.Outcome != serviceauth.OK {
		t.Fatal("warm-up failed")
	}
	e.p.AddKey("rotated")
	rotated := e.bearer(e.p.Claims(e.now, "intro-client"), "rotated")
	res, ch := e.do(rotated)
	expect(t, res, ch, serviceauth.Unauthenticated, []string{`Bearer realm="accountsvc-introspection", error="invalid_token"`})
	e.advance(10 * time.Second)
	res, ch = e.do(rotated)
	expect(t, res, ch, serviceauth.OK, nil)
}

func TestIssuerMismatchIsUnavailable(t *testing.T) {
	e := newEnv(t, false, true)
	e.p.SetIssuer("https://other.example.test")
	res, ch := e.do(e.bearer(e.p.Claims(e.now, "intro-client"), "initial"))
	expect(t, res, ch, serviceauth.Unavailable, nil)
}

func TestProviderTimeoutIsUnavailable(t *testing.T) {
	e := newEnv(t, false, true)
	e.auth = mustNew(t, serviceauth.Config{Realm: realm, Endpoint: "introspect", Tokens: tokensFor(t, e), BearerClients: []string{"intro-client"}, Timeout: 100 * time.Millisecond})
	e.p.Block()
	defer e.p.Unblock()
	res, ch := e.do(e.bearer(e.p.Claims(e.now, "intro-client"), "initial"))
	expect(t, res, ch, serviceauth.Unavailable, nil)
}

func tokensFor(t *testing.T, e *env) *oidcjwt.Verifier {
	t.Helper()
	v, err := oidcjwt.New(e.p.URL(), oidcjwttest.Audience, oidcjwt.Options{HTTPClient: e.p.Client(), Now: e.clock, RefreshInterval: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func mustNew(t *testing.T, cfg serviceauth.Config) *serviceauth.Authenticator {
	t.Helper()
	a, err := serviceauth.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestHeaderHandling(t *testing.T) {
	e := newEnv(t, true, true)
	valid := e.bearer(e.p.Claims(e.now, "intro-client"), "initial")
	basic := "Basic " + base64.StdEncoding.EncodeToString([]byte("basic-client:"+basicSecret))
	t.Run("15 two headers", func(t *testing.T) {
		for _, pair := range [][]string{{basic, valid}, {valid, valid}} {
			res, ch := e.do(pair...)
			expect(t, res, ch, serviceauth.Unauthenticated, bothChallenges)
			if res.BearerAttempted || res.Method != serviceauth.MethodNone {
				t.Fatalf("result = %+v", res)
			}
		}
	})
	t.Run("16 no credentials", func(t *testing.T) {
		res, ch := e.do()
		expect(t, res, ch, serviceauth.Unauthenticated, bothChallenges)
	})
	t.Run("unknown scheme", func(t *testing.T) {
		res, ch := e.do("Digest abc")
		expect(t, res, ch, serviceauth.Unauthenticated, bothChallenges)
	})
	t.Run("wrong basic secret", func(t *testing.T) {
		res, ch := e.do("Basic " + base64.StdEncoding.EncodeToString([]byte("basic-client:wrong")))
		expect(t, res, ch, serviceauth.Unauthenticated, bothChallenges)
	})
	t.Run("lowercase bearer scheme", func(t *testing.T) {
		res, ch := e.do("bearer " + strings.TrimPrefix(valid, "Bearer "))
		expect(t, res, ch, serviceauth.OK, nil)
	})
	t.Run("17 bearer when only basic is enabled", func(t *testing.T) {
		b := newEnv(t, true, false)
		res, ch := b.do(valid)
		expect(t, res, ch, serviceauth.Unauthenticated, []string{`Basic realm="accountsvc-introspection"`})
		if res.BearerAttempted {
			t.Fatal("bearer must not count as attempted when disabled")
		}
	})
}

func TestNewValidation(t *testing.T) {
	e := newEnv(t, false, true)
	tokens := tokensFor(t, e)
	for name, cfg := range map[string]serviceauth.Config{
		"clients without verifier": {Realm: realm, Endpoint: "introspect", BearerClients: []string{"a"}},
		"verifier without clients": {Realm: realm, Endpoint: "introspect", Tokens: tokens},
		"empty client":             {Realm: realm, Endpoint: "introspect", Tokens: tokens, BearerClients: []string{""}},
		"invalid basic":            {Realm: realm, Endpoint: "introspect", Basic: map[string][]string{"a": {"short"}}},
		"missing realm":            {Endpoint: "introspect", Tokens: tokens, BearerClients: []string{"a"}},
	} {
		if _, err := serviceauth.New(cfg); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	a := mustNew(t, serviceauth.Config{Realm: realm, Endpoint: "introspect"})
	if a.Enabled() {
		t.Fatal("authenticator without methods reports enabled")
	}
	if res := a.Authenticate(httptest.NewRequest(http.MethodGet, "/", nil)); res.Outcome != serviceauth.Unauthenticated {
		t.Fatalf("disabled authenticator outcome = %v", res.Outcome)
	}
}
