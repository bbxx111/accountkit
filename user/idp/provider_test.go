package idp_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user/idp"
)

var malformedCredentials = []string{
	`null`, `[]`, `"x"`, `{}`, `{"unknown":"x"}`, `{} {}`, `{`,
}

func TestProvidersRejectMalformedCredentialBeforeIO(t *testing.T) {
	var wechatHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wechatHits.Add(1)
		_, _ = w.Write([]byte(`{"unionid":"u"}`))
	}))
	t.Cleanup(srv.Close)
	apple, google := newAppleFixture(t), newGoogleFixture(t)
	cases := []struct {
		provider idp.Provider
		kind     enum.IdentityKind
		extra    []string
	}{
		{idp.NewWeChat([]idp.App{{AppID: "wx1", Secret: "s"}}, srv.URL, srv.Client(), nil), enum.IdentityWeChat,
			[]string{`{"app_id":"wx1"}`, `{"code":"c"}`, `{"app_id":"wx1","code":""}`, `{"app_id":"wx1","code":"c","x":1}`, `{"app_id":1,"code":"c"}`}},
		{apple.apple, enum.IdentityApple,
			[]string{`{"id_token":"t"}`, `{"nonce":"n"}`, `{"id_token":"","nonce":"n"}`, `{"id_token":"t","nonce":"n","x":1}`}},
		{google.google, enum.IdentityGoogle,
			[]string{`{"id_token":"t"}`, `{"nonce":"n"}`, `{"id_token":"t","nonce":""}`, `{"id_token":"t","nonce":"n","app_id":"x"}`}},
	}
	for _, c := range cases {
		if c.provider.Kind() != c.kind {
			t.Fatalf("kind = %s, want %s", c.provider.Kind(), c.kind)
		}
		for _, raw := range append(append([]string{}, malformedCredentials...), c.extra...) {
			if _, err := c.provider.Verify(context.Background(), json.RawMessage(raw)); !errors.Is(err, idp.ErrMalformedCredential) {
				t.Fatalf("%s %s: err=%v, want ErrMalformedCredential", c.kind, raw, err)
			}
		}
	}
	if wechatHits.Load() != 0 || apple.hits.Load() != 0 || google.hits.Load() != 0 {
		t.Fatalf("malformed credentials reached providers: wechat=%d apple=%d google=%d", wechatHits.Load(), apple.hits.Load(), google.hits.Load())
	}
	if keys := apple.mr.Keys(); len(keys) != 0 {
		t.Fatalf("apple nonce registered: %v", keys)
	}
	if keys := google.mr.Keys(); len(keys) != 0 {
		t.Fatalf("google nonce registered: %v", keys)
	}
}

func TestProvidersVerifyWellFormedCredential(t *testing.T) {
	ctx := context.Background()
	g := newGoogleFixture(t)
	raw, _ := json.Marshal(map[string]string{"id_token": g.token(t, nil, "", nil), "nonce": "raw-nonce-1"})
	id, err := g.google.Verify(ctx, raw)
	if err != nil || id.Kind != enum.IdentityGoogle || id.Subject != "google-sub-1" {
		t.Fatalf("google verify: %+v %v", id, err)
	}
	a := newAppleFixture(t)
	raw, _ = json.Marshal(map[string]string{"id_token": a.token(t, nil, nil, "", nil), "nonce": rawNonce})
	if id, err = a.apple.Verify(ctx, raw); err != nil || id.Kind != enum.IdentityApple {
		t.Fatalf("apple verify: %+v %v", id, err)
	}
	srv, _ := fakeWeChat(t, map[string]string{"good": `{"openid":"o_1","unionid":"u_union_1"}`})
	w := idp.NewWeChat([]idp.App{{AppID: "wx1", Secret: "s"}}, srv.URL, srv.Client(), nil)
	if id, err = w.Verify(ctx, json.RawMessage(`{"app_id":"wx1","code":"good"}`)); err != nil || id.Kind != enum.IdentityWeChat || id.Subject != "u_union_1" {
		t.Fatalf("wechat verify: %+v %v", id, err)
	}
}

func TestGooglePolicyFingerprintTracksClientIDs(t *testing.T) {
	a := idp.NewGoogle(idp.GoogleOptions{ClientIDs: []string{"b", "a"}})
	b := idp.NewGoogle(idp.GoogleOptions{ClientIDs: []string{"a", "b"}})
	c := idp.NewGoogle(idp.GoogleOptions{ClientIDs: []string{"a", "c"}})
	if a.PolicyFingerprint() == "" || a.PolicyFingerprint() != b.PolicyFingerprint() || a.PolicyFingerprint() == c.PolicyFingerprint() {
		t.Fatal("google fingerprint must be order-independent and track client IDs")
	}
}
