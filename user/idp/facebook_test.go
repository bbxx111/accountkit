package idp_test

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user/idp"
)

const fbApp, fbSecret = "1234567890", "fb-app-secret"

type fbFixture struct {
	key       *rsa.PrivateKey
	now       time.Time
	jwks      *httptest.Server
	graph     *httptest.Server
	graphHits atomic.Int32
	jwksHits  atomic.Int32
	debug     func(w http.ResponseWriter, r *http.Request)
	me        func(w http.ResponseWriter, r *http.Request)
	fb        *idp.Facebook
}

func proofOf(token string) string {
	m := hmac.New(sha256.New, []byte(fbSecret))
	m.Write([]byte(token))
	return hex.EncodeToString(m.Sum(nil))
}

func newFBFixture(t *testing.T) *fbFixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fbFixture{key: key, now: time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)}
	f.jwks = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.jwksHits.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{jwkOf("fk1", &key.PublicKey)}})
	}))
	t.Cleanup(f.jwks.Close)
	f.debug = func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"app_id": fbApp, "type": "USER", "is_valid": true, "user_id": "asid-1", "expires_at": f.now.Add(time.Hour).Unix()}})
	}
	f.me = func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "asid-1", "name": "Ada", "email": "ada@example.test", "picture": map[string]any{"data": map[string]any{"url": "https://example.test/fb.png", "is_silhouette": false}}})
	}
	f.graph = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.graphHits.Add(1)
		q := r.URL.Query()
		switch r.URL.Path {
		case "/v26.0/debug_token":
			appToken := fbApp + "|" + fbSecret
			if q.Get("access_token") != appToken || q.Get("appsecret_proof") != proofOf(appToken) {
				w.WriteHeader(400)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": "bad app token", "type": "OAuthException", "code": 190}})
				return
			}
			f.debug(w, r)
		case "/v26.0/me":
			if q.Get("appsecret_proof") != proofOf(q.Get("access_token")) {
				w.WriteHeader(400)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": "invalid appsecret_proof", "type": "GraphMethodException", "code": 100}})
				return
			}
			f.me(w, r)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(f.graph.Close)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	f.fb = idp.NewFacebook(idp.FacebookOptions{
		AppID: fbApp, AppSecret: fbSecret, GraphAPIVersion: "v26.0", GraphAPIBaseURL: f.graph.URL, JWKSURL: f.jwks.URL,
		HTTP: f.graph.Client(), Nonces: idp.NewNonceRegistry(rdb, "t:"), NonceTTL: 10 * time.Minute,
		Now: func() time.Time { return f.now },
	})
	return f
}

func (f *fbFixture) idToken(t *testing.T, over map[string]any) string {
	t.Helper()
	claims := jwt.MapClaims{"iss": "https://www.facebook.com", "aud": fbApp, "sub": "asid-1", "iat": f.now.Unix(), "exp": f.now.Add(time.Hour).Unix(), "nonce": "n1", "name": "Ada", "picture": "https://example.test/fb.png", "email": "ada@example.test"}
	for k, v := range over {
		if v == nil {
			delete(claims, k)
		} else {
			claims[k] = v
		}
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = "fk1"
	s, err := tok.SignedString(f.key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestFacebookCredentialShapes(t *testing.T) {
	f := newFBFixture(t)
	for name, raw := range map[string]string{
		"both":        `{"id_token":"x","nonce":"n","access_token":"t"}`,
		"only nonce":  `{"nonce":"n"}`,
		"empty token": `{"access_token":""}`,
		"empty nonce": `{"id_token":"x","nonce":""}`,
		"unknown":     `{"access_token":"t","x":1}`,
		"none":        `{}`,
		"not object":  `"t"`,
	} {
		if _, err := f.fb.Verify(context.Background(), json.RawMessage(raw)); !errors.Is(err, idp.ErrMalformedCredential) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if f.graphHits.Load() != 0 || f.jwksHits.Load() != 0 {
		t.Fatal("malformed credential must not reach Facebook")
	}
}

func TestFacebookLimitedLogin(t *testing.T) {
	f := newFBFixture(t)
	ctx := context.Background()
	id, err := f.fb.Verify(ctx, json.RawMessage(`{"id_token":"`+f.idToken(t, nil)+`","nonce":"n1"}`))
	if err != nil || id.Kind != enum.IdentityFacebook || id.Subject != "asid-1" || id.Name != "Ada" || id.Picture != "https://example.test/fb.png" || id.HintEmail != "ada@example.test" {
		t.Fatalf("limited: %+v %v", id, err)
	}
	if _, err := f.fb.VerifyLimited(ctx, f.idToken(t, nil), "n1"); !errors.Is(err, idp.ErrNonceReplayed) {
		t.Fatalf("replay: %v", err)
	}
	if _, err := f.fb.VerifyLimited(ctx, f.idToken(t, map[string]any{"aud": "other", "nonce": "n2"}), "n2"); !errors.Is(err, idp.ErrAppNotAllowed) {
		t.Fatalf("aud: %v", err)
	}
	if _, err := f.fb.VerifyLimited(ctx, f.idToken(t, map[string]any{"iss": "https://limited.facebook.com", "nonce": "n3"}), "n3"); !errors.Is(err, idp.ErrInvalidCredential) {
		t.Fatalf("iss: %v", err)
	}
}

func TestFacebookClassicLogin(t *testing.T) {
	f := newFBFixture(t)
	ctx := context.Background()
	for i := 0; i < 2; i++ { // 同一 token 可重复使用
		id, err := f.fb.Verify(ctx, json.RawMessage(`{"access_token":"user-token"}`))
		if err != nil || id.Kind != enum.IdentityFacebook || id.Subject != "asid-1" || id.Name != "Ada" || id.Picture != "https://example.test/fb.png" || id.HintEmail != "ada@example.test" || !id.ExpireTime.Equal(f.now.Add(time.Hour)) {
			t.Fatalf("classic %d: %+v %v", i, id, err)
		}
	}
}

func TestFacebookClassicRejections(t *testing.T) {
	cases := map[string]struct {
		debug map[string]any
		meID  string
		want  error
	}{
		"invalid":    {debug: map[string]any{"app_id": fbApp, "type": "USER", "is_valid": false, "user_id": "asid-1"}, want: idp.ErrInvalidCredential},
		"page type":  {debug: map[string]any{"app_id": fbApp, "type": "PAGE", "is_valid": true, "user_id": "asid-1"}, want: idp.ErrInvalidCredential},
		"other app":  {debug: map[string]any{"app_id": "999", "type": "USER", "is_valid": true, "user_id": "asid-1"}, want: idp.ErrAppNotAllowed},
		"id differs": {debug: map[string]any{"app_id": fbApp, "type": "USER", "is_valid": true, "user_id": "asid-1"}, meID: "asid-2", want: idp.ErrInvalidCredential},
	}
	for name, c := range cases {
		f := newFBFixture(t)
		f.debug = func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": c.debug})
		}
		if c.meID != "" {
			f.me = func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"id": c.meID})
			}
		}
		if _, err := f.fb.VerifyClassic(context.Background(), "user-token"); !errors.Is(err, c.want) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestFacebookClassicDependencyAndConfigErrors(t *testing.T) {
	f := newFBFixture(t)
	f.debug = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(502) }
	if _, err := f.fb.VerifyClassic(context.Background(), "user-token"); !errors.Is(err, idp.ErrUnavailable) {
		t.Fatalf("5xx: %v", err)
	}
	f = newFBFixture(t)
	f.me = func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(400)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"type": "OAuthException", "code": 190}})
	}
	if _, err := f.fb.VerifyClassic(context.Background(), "user-token"); !errors.Is(err, idp.ErrInvalidCredential) {
		t.Fatalf("me 190: %v", err)
	}
	wrong := idp.NewFacebook(idp.FacebookOptions{AppID: fbApp, AppSecret: "wrong", GraphAPIVersion: "v26.0", GraphAPIBaseURL: f.graph.URL, JWKSURL: f.jwks.URL, HTTP: f.graph.Client()})
	_, err := wrong.VerifyClassic(context.Background(), "secret-user-token")
	if !errors.Is(err, idp.ErrMisconfigured) || strings.Contains(err.Error(), "secret-user-token") || strings.Contains(err.Error(), "wrong") {
		t.Fatalf("misconfigured: %v", err)
	}
}

func TestFacebookClassicSilhouetteAndNoExpiry(t *testing.T) {
	f := newFBFixture(t)
	f.debug = func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"app_id": fbApp, "type": "USER", "is_valid": true, "user_id": "asid-1", "expires_at": 0}})
	}
	f.me = func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "asid-1", "picture": map[string]any{"data": map[string]any{"url": "https://example.test/default.png", "is_silhouette": true}}})
	}
	id, err := f.fb.VerifyClassic(context.Background(), "user-token")
	if err != nil || id.Picture != "" || !id.ExpireTime.IsZero() || id.HintEmail != "" {
		t.Fatalf("silhouette: %+v %v", id, err)
	}
}

func TestFacebookPolicyFingerprint(t *testing.T) {
	base := idp.FacebookOptions{AppID: fbApp, AppSecret: fbSecret, GraphAPIVersion: "v26.0", GraphAPIBaseURL: "https://graph.facebook.com", JWKSURL: "https://www.facebook.com/.well-known/oauth/openid/jwks/"}
	fp := idp.NewFacebook(base).PolicyFingerprint()
	if strings.Contains(fp, fbSecret) {
		t.Fatal("fingerprint must not contain secret")
	}
	secret := base
	secret.AppSecret = "rotated"
	if idp.NewFacebook(secret).PolicyFingerprint() != fp {
		t.Fatal("secret must not affect fingerprint")
	}
	for name, mutate := range map[string]func(*idp.FacebookOptions){
		"app":     func(o *idp.FacebookOptions) { o.AppID = "other" },
		"version": func(o *idp.FacebookOptions) { o.GraphAPIVersion = "v25.0" },
		"graph":   func(o *idp.FacebookOptions) { o.GraphAPIBaseURL = "https://proxy.example" },
		"jwks": func(o *idp.FacebookOptions) {
			o.JWKSURL = "https://limited.facebook.com/.well-known/oauth/openid/jwks/"
		},
	} {
		o := base
		mutate(&o)
		if idp.NewFacebook(o).PolicyFingerprint() == fp {
			t.Fatalf("%s must change fingerprint", name)
		}
	}
}
