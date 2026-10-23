package accountkit_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/enum"
)

// The mock is independent of idp's parser: it only serves fixed JSON for known tokens.
type weiboHTTPGrant struct {
	app, uid, name, avatar string
	// numeric renders uid/appkey/expire_in as JSON numbers instead of strings.
	numeric            bool
	profileUnavailable bool
}

type weiboHTTPServer struct {
	t           *testing.T
	server      *httptest.Server
	grants      map[string]weiboHTTPGrant
	mu          sync.Mutex
	token, show map[string]int
}

func newWeiboHTTPServer(t *testing.T, grants map[string]weiboHTTPGrant) *weiboHTTPServer {
	t.Helper()
	s := &weiboHTTPServer{t: t, grants: grants, token: map[string]int{}, show: map[string]int{}}
	s.server = httptest.NewServer(http.HandlerFunc(s.serveHTTP))
	t.Cleanup(s.server.Close)
	return s
}

func (s *weiboHTTPServer) serveHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch r.URL.Path {
	case "/oauth2/get_token_info":
		if r.Method != http.MethodPost || r.ParseForm() != nil {
			s.t.Error("token info must be a form POST")
		}
		token := r.PostForm.Get("access_token")
		s.token[token]++
		grant, ok := s.grants[token]
		if !ok {
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":"invalid_access_token","error_code":21332,"request":"/oauth2/get_token_info"}`)
			return
		}
		if grant.numeric {
			fmt.Fprintf(w, `{"uid":%s,"appkey":%s,"scope":"","create_at":1700000000,"expire_in":3600}`, grant.uid, grant.app)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"uid": grant.uid, "appkey": grant.app, "scope": "", "create_at": "1700000000", "expire_in": "3600"})
	case "/2/users/show.json":
		q := r.URL.Query()
		token := q.Get("access_token")
		s.show[token]++
		grant, ok := s.grants[token]
		if !ok || q.Get("uid") != grant.uid {
			s.t.Error("profile request does not match verified identity")
			w.WriteHeader(400)
			return
		}
		if grant.profileUnavailable {
			w.WriteHeader(403)
			fmt.Fprint(w, `{"error":"User requests out of rate limit!","error_code":10023,"request":"/2/users/show.json"}`)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"idstr": grant.uid, "screen_name": grant.name, "avatar_hd": grant.avatar})
	default:
		s.t.Error("unexpected Weibo path")
		w.WriteHeader(404)
	}
}

func (s *weiboHTTPServer) assertCalls(t *testing.T, token string, info, show int) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token[token] != info || s.show[token] != show {
		t.Fatalf("Weibo call counts: token_info=%d show=%d, want %d/%d", s.token[token], s.show[token], info, show)
	}
}

func (s *weiboHTTPServer) config(apps ...string) accountkit.Config {
	cfg := minimal()
	cfg.EnabledIdentityKinds = []enum.IdentityKind{enum.IdentityWeibo}
	cfg.RequiredIdentityKinds = []enum.IdentityKind{}
	cfg.WeiboAppIDs, cfg.WeiboAPIBaseURL = apps, s.server.URL
	return cfg
}

func weiboLoginBody(app, token string) map[string]any {
	return map[string]any{"kind": "WEIBO", "credential": map[string]string{"app_id": app, "access_token": token}}
}

func TestWeiboHTTPLoginContract(t *testing.T) {
	requireAlipayHTTPDependencies(t)
	const app, other = "1234567890", "1234567891"
	s := newWeiboHTTPServer(t, map[string]weiboHTTPGrant{
		"first":     {app: app, uid: "2000000001", profileUnavailable: true},
		"name":      {app: app, uid: "2000000001", name: "Weibo name", avatar: "http://tvax1.sinaimg.cn/hd.jpg", numeric: true},
		"avatar":    {app: app, uid: "2000000001", name: "Other", avatar: "https://tvax1.sinaimg.cn/hd.jpg"},
		"other-app": {app: other, uid: "2000000009"},
	})
	f := newAlipayHTTPFixture(t, s.config(app, other))
	token := f.tokenResult(f.call("POST", "/users:signInWithIdp", "", weiboLoginBody(app, "first"), 200))
	f.assertMe(token, "", "")
	s.assertCalls(t, "first", 1, 1)
	again := f.tokenResult(f.call("POST", "/users:signInWithIdp", "", weiboLoginBody(app, "first"), 200))
	if again.user != token.user {
		t.Fatal("repeated access token did not sign in the same account")
	}
	// A second same-device login supersedes the first session, so refresh the newest token pair.
	refreshed := f.tokenResult(f.call("POST", "/token", "", map[string]string{"grant_type": "refresh_token", "refresh_token": again.refresh}, 200))
	if refreshed.user != token.user || refreshed.refresh == again.refresh {
		t.Fatal("refresh did not rotate")
	}
	before := f.counts()
	for _, tc := range []struct {
		app, token, reason string
	}{{app, "unknown", "IDP_CREDENTIAL_INVALID"}, {app, "other-app", "IDP_APP_NOT_ALLOWED"}, {"1234567899", "first", "IDP_APP_NOT_ALLOWED"}} {
		body := f.call("POST", "/users:signInWithIdp", "", weiboLoginBody(tc.app, tc.token), 400)
		var failure struct {
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal(body["error"], &failure); err != nil || failure.Reason != tc.reason {
			t.Fatalf("%s: unexpected safe error classification", tc.token)
		}
		if f.counts() != before {
			t.Fatal("rejected login changed account, identity, session or pending rows")
		}
	}
	s.assertCalls(t, "other-app", 1, 0)
	s.assertCalls(t, "unknown", 1, 0)
	// Numeric token info resolves to the same identity; the HTTP avatar is dropped.
	token = f.tokenResult(f.call("POST", "/users:signInWithIdp", "", weiboLoginBody(app, "name"), 200))
	f.assertMe(token, "Weibo name", "")
	token = f.tokenResult(f.call("POST", "/users:signInWithIdp", "", weiboLoginBody(app, "avatar"), 200))
	f.assertMe(token, "Weibo name", "https://tvax1.sinaimg.cn/hd.jpg")
	counts := f.counts()
	if counts[0] != 1 || counts[1] != 1 || counts[3] != 0 {
		t.Fatal("single-app Weibo login did not retain one account/identity without pending rows")
	}
}

func TestWeiboHTTPMultiAppIdentityContract(t *testing.T) {
	requireAlipayHTTPDependencies(t)
	const appA, appB = "1234567890", "1234567891"
	s := newWeiboHTTPServer(t, map[string]weiboHTTPGrant{
		"a": {app: appA, uid: "2000000001", profileUnavailable: true, numeric: true},
		"b": {app: appB, uid: "2000000001", name: "Cross", avatar: "https://tvax1.sinaimg.cn/cross.jpg"},
		"c": {app: appB, uid: "2000000002", name: "Someone Else"},
	})
	f := newAlipayHTTPFixture(t, s.config(appA, appB))
	a := f.tokenResult(f.call("POST", "/users:signInWithIdp", "", weiboLoginBody(appA, "a"), 200))
	b := f.tokenResult(f.call("POST", "/users:signInWithIdp", "", weiboLoginBody(appB, "b"), 200))
	if a.user != b.user {
		t.Fatal("same uid across Weibo apps created separate accounts")
	}
	f.assertMe(b, "Cross", "https://tvax1.sinaimg.cn/cross.jpg")
	var subject string
	var raw []byte
	if err := f.pool.QueryRow(context.Background(), `SELECT provider_subject, provider_meta FROM identity WHERE kind = 'WEIBO' AND delete_time IS NULL AND user_id = $1`, a.user).Scan(&subject, &raw); err != nil {
		t.Fatal(err)
	}
	var meta map[string]json.RawMessage
	if err := json.Unmarshal(raw, &meta); err != nil || subject != "uid:2000000001" || meta["unionid"] != nil || meta["openids"] != nil {
		t.Fatal("Weibo identity must use the global uid subject without app-scoped metadata")
	}
	c := f.tokenResult(f.call("POST", "/users:signInWithIdp", "", weiboLoginBody(appB, "c"), 200))
	if c.user == a.user {
		t.Fatal("different uid shared an account")
	}
	counts := f.counts()
	if counts[0] != 2 || counts[1] != 2 {
		t.Fatal("two uids did not keep two accounts/identities")
	}
}
