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
type qqHTTPGrant struct {
	app, open, union, name, avatar string
	profileUnavailable             bool
}

type qqHTTPServer struct {
	t        *testing.T
	server   *httptest.Server
	grants   map[string]qqHTTPGrant
	mu       sync.Mutex
	me, info map[string]int
}

func newQQHTTPServer(t *testing.T, grants map[string]qqHTTPGrant) *qqHTTPServer {
	t.Helper()
	s := &qqHTTPServer{t: t, grants: grants, me: map[string]int{}, info: map[string]int{}}
	s.server = httptest.NewServer(http.HandlerFunc(s.serveHTTP))
	t.Cleanup(s.server.Close)
	return s
}

func (s *qqHTTPServer) serveHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	token := q.Get("access_token")
	s.mu.Lock()
	defer s.mu.Unlock()
	grant, ok := s.grants[token]
	switch r.URL.Path {
	case "/oauth2.0/me":
		s.me[token]++
		if q.Get("fmt") != "json" {
			s.t.Error("me must request JSON")
		}
		if !ok {
			fmt.Fprint(w, `{"error":100016,"error_description":"access token check failed"}`)
			return
		}
		body := map[string]string{"client_id": grant.app, "openid": grant.open}
		if q.Get("unionid") == "1" && grant.union != "" {
			body["unionid"] = grant.union
		}
		_ = json.NewEncoder(w).Encode(body)
	case "/user/get_user_info":
		s.info[token]++
		if !ok || q.Get("oauth_consumer_key") != grant.app || q.Get("openid") != grant.open {
			s.t.Error("profile request does not match verified identity")
			w.WriteHeader(400)
			return
		}
		if grant.profileUnavailable {
			w.WriteHeader(503)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ret": 0, "nickname": grant.name, "figureurl_qq_2": grant.avatar})
	default:
		s.t.Error("unexpected QQ path")
		w.WriteHeader(404)
	}
}

func (s *qqHTTPServer) assertCalls(t *testing.T, token string, me, info int) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.me[token] != me || s.info[token] != info {
		t.Fatalf("QQ call counts: me=%d info=%d, want %d/%d", s.me[token], s.info[token], me, info)
	}
}

func (s *qqHTTPServer) config(mode accountkit.QQSubjectMode, apps ...string) accountkit.Config {
	cfg := minimal()
	cfg.EnabledIdentityKinds = []enum.IdentityKind{enum.IdentityQQ}
	cfg.RequiredIdentityKinds = []enum.IdentityKind{}
	cfg.QQAppIDs, cfg.QQSubjectMode, cfg.QQAPIBaseURL = apps, mode, s.server.URL
	return cfg
}

func qqLoginBody(app, token string) map[string]any {
	return map[string]any{"kind": "QQ", "credential": map[string]string{"app_id": app, "access_token": token}}
}

func TestQQHTTPLoginContract(t *testing.T) {
	requireAlipayHTTPDependencies(t)
	const app, other = "1100000001", "1100000002"
	s := newQQHTTPServer(t, map[string]qqHTTPGrant{
		"first":     {app: app, open: "OPEN-ONE", union: "UID_ONE", profileUnavailable: true},
		"name":      {app: app, open: "OPEN-ONE", union: "UID_ONE", name: "QQ name"},
		"avatar":    {app: app, open: "OPEN-ONE", union: "UID_ONE", name: "Other", avatar: "http://thirdqq.qlogo.cn/g?s=100"},
		"other-app": {app: other, open: "OPEN-X", union: "UID_X"},
	})
	f := newAlipayHTTPFixture(t, s.config(accountkit.QQSubjectModeUnionID, app, other))
	token := f.tokenResult(f.call("POST", "/users:signInWithIdp", "", qqLoginBody(app, "first"), 200))
	f.assertMe(token, "", "")
	s.assertCalls(t, "first", 1, 1)
	again := f.tokenResult(f.call("POST", "/users:signInWithIdp", "", qqLoginBody(app, "first"), 200))
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
	}{{app, "unknown", "IDP_CREDENTIAL_INVALID"}, {app, "other-app", "IDP_APP_NOT_ALLOWED"}, {"1100000009", "first", "IDP_APP_NOT_ALLOWED"}} {
		body := f.call("POST", "/users:signInWithIdp", "", qqLoginBody(tc.app, tc.token), 400)
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
	token = f.tokenResult(f.call("POST", "/users:signInWithIdp", "", qqLoginBody(app, "name"), 200))
	f.assertMe(token, "QQ name", "")
	token = f.tokenResult(f.call("POST", "/users:signInWithIdp", "", qqLoginBody(app, "avatar"), 200))
	f.assertMe(token, "QQ name", "https://thirdqq.qlogo.cn/g?s=100")
	counts := f.counts()
	if counts[0] != 1 || counts[1] != 1 || counts[3] != 0 {
		t.Fatal("single-app QQ login did not retain one account/identity without pending rows")
	}
}

func TestQQHTTPMultiAppIdentityContract(t *testing.T) {
	requireAlipayHTTPDependencies(t)
	const appA, appB = "1100000001", "1100000002"
	t.Run("UNIONID shares identity and merges OpenIDs", func(t *testing.T) {
		s := newQQHTTPServer(t, map[string]qqHTTPGrant{
			"a": {app: appA, open: "open-a", union: "UID_SHARED", profileUnavailable: true},
			"b": {app: appB, open: "open-b", union: "UID_SHARED", name: "Cross", avatar: "https://thirdqq.qlogo.cn/cross"},
		})
		f := newAlipayHTTPFixture(t, s.config(accountkit.QQSubjectModeUnionID, appA, appB))
		a := f.tokenResult(f.call("POST", "/users:signInWithIdp", "", qqLoginBody(appA, "a"), 200))
		b := f.tokenResult(f.call("POST", "/users:signInWithIdp", "", qqLoginBody(appB, "b"), 200))
		if a.user != b.user {
			t.Fatal("same UnionID across QQ apps created separate accounts")
		}
		f.assertMe(b, "Cross", "https://thirdqq.qlogo.cn/cross")
		var subject string
		var raw []byte
		if err := f.pool.QueryRow(context.Background(), `SELECT provider_subject, provider_meta FROM identity WHERE kind = 'QQ' AND delete_time IS NULL`).Scan(&subject, &raw); err != nil {
			t.Fatal(err)
		}
		var meta struct {
			UnionID string            `json:"unionid"`
			OpenIDs map[string]string `json:"openids"`
		}
		if err := json.Unmarshal(raw, &meta); err != nil || subject != "unionid:UID_SHARED" || meta.UnionID != "UID_SHARED" || meta.OpenIDs[appA] != "open-a" || meta.OpenIDs[appB] != "open-b" {
			t.Fatal("UnionID identity did not merge both application OpenIDs")
		}
	})
	t.Run("OPENID isolates apps", func(t *testing.T) {
		s := newQQHTTPServer(t, map[string]qqHTTPGrant{
			"a": {app: appA, open: "SAME-OPEN", union: "UID_IGNORED"},
			"b": {app: appB, open: "SAME-OPEN", union: "UID_IGNORED"},
		})
		f := newAlipayHTTPFixture(t, s.config(accountkit.QQSubjectModeOpenID, appA, appB))
		a := f.tokenResult(f.call("POST", "/users:signInWithIdp", "", qqLoginBody(appA, "a"), 200))
		b := f.tokenResult(f.call("POST", "/users:signInWithIdp", "", qqLoginBody(appB, "b"), 200))
		if a.user == b.user {
			t.Fatal("same OpenID text across apps shared an account")
		}
		counts := f.counts()
		if counts[0] != 2 || counts[1] != 2 {
			t.Fatal("OPENID mode did not keep two accounts/identities")
		}
	})
}
