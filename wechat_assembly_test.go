package accountkit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWeChatConfiguredProviderMiniProgram(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/sns/jscode2session" || q.Get("js_code") != "test-code" || q.Get("code") != "" || q.Get("secret") != "test-secret" {
			t.Errorf("unexpected verifier request")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"openid":"test-openid","unionid":"test-unionid","session_key":"discard-me"}`))
	}))
	defer server.Close()
	for _, mode := range []WeChatSubjectMode{WeChatSubjectModeUnionID, WeChatSubjectModeOpenID} {
		providers, err := identityProviders(Config{WeChatApps: []WeChatApp{{AppID: "oauth", Secret: "oauth-secret"}}, WeChatMiniProgramApps: []WeChatApp{{AppID: "mini", Secret: "test-secret"}}, WeChatSubjectMode: mode, WeChatAPIBaseURL: server.URL}, Deps{HTTPClient: server.Client()})
		if err != nil || len(providers) != 1 {
			t.Fatalf("providers: %v %d", err, len(providers))
		}
		identity, err := providers[0].Verify(context.Background(), []byte(`{"app_id":"mini","code":"test-code"}`))
		want := "unionid:test-unionid"
		if mode == WeChatSubjectModeOpenID {
			want = "openid:mini:test-openid"
		}
		if err != nil || identity.Subject != want {
			t.Fatalf("verified identity=%+v err=%v", identity, err)
		}
	}
	_, err := identityProviders(Config{WeChatMiniProgramApps: []WeChatApp{{AppID: "mini", Secret: "test-secret"}}, WeChatSubjectMode: "AUTO"}, Deps{})
	if err == nil {
		t.Fatal("constructor error discarded")
	}
}
