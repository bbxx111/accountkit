package accountkit_test

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/tokens"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// This gateway is independent of idp's signing/response helpers. It checks the
// actual form signature with each application's key and consumes codes once.
type alipayHTTPApp struct {
	id        string
	appKey    *rsa.PrivateKey
	alipayKey *rsa.PrivateKey
}

type alipayHTTPGrant struct {
	app, open, union, name, avatar   string
	profileUnavailable, badSignature bool
}

type alipayHTTPGateway struct {
	t                   *testing.T
	server              *httptest.Server
	apps                map[string]alipayHTTPApp
	grants              map[string]alipayHTTPGrant
	mu                  sync.Mutex
	exchanges, profiles map[string]int
	access              map[string]string
}

func newAlipayHTTPGateway(t *testing.T, appIDs ...string) *alipayHTTPGateway {
	t.Helper()
	g := &alipayHTTPGateway{t: t, apps: map[string]alipayHTTPApp{}, grants: map[string]alipayHTTPGrant{}, exchanges: map[string]int{}, profiles: map[string]int{}, access: map[string]string{}}
	for _, appID := range appIDs {
		app := alipayHTTPApp{id: appID}
		var err error
		app.appKey, err = rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		app.alipayKey, err = rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		g.apps[appID] = app
	}
	g.server = httptest.NewServer(http.HandlerFunc(g.serveHTTP))
	t.Cleanup(g.server.Close)
	return g
}

func (g *alipayHTTPGateway) config(mode accountkit.AlipaySubjectMode) accountkit.Config {
	cfg := minimal()
	cfg.EnabledIdentityKinds = []enum.IdentityKind{enum.IdentityAlipay}
	cfg.RequiredIdentityKinds = []enum.IdentityKind{}
	cfg.AlipaySubjectMode, cfg.AlipayGatewayURL = mode, g.server.URL
	ids := make([]string, 0, len(g.apps))
	for id := range g.apps {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		app := g.apps[id]
		cfg.AlipayApps = append(cfg.AlipayApps, accountkit.AlipayApp{
			AppID:           id,
			PrivateKey:      string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(app.appKey)})),
			AlipayPublicKey: string(pem.EncodeToMemory(&pem.Block{Type: "RSA PUBLIC KEY", Bytes: x509.MarshalPKCS1PublicKey(&app.alipayKey.PublicKey)})),
		})
	}
	return cfg
}

func (g *alipayHTTPGateway) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" || r.ParseForm() != nil || len(r.URL.Query()) != 0 {
		g.t.Error("gateway requires a POST form without query parameters")
		http.Error(w, "invalid request", 400)
		return
	}
	app, ok := g.apps[r.PostForm.Get("app_id")]
	if !ok {
		g.t.Error("gateway received unregistered application")
		w.WriteHeader(400)
		return
	}
	form := r.PostForm
	for _, values := range form {
		if len(values) != 1 {
			g.t.Error("gateway received duplicate form field")
			w.WriteHeader(400)
			return
		}
	}
	signature, err := base64.StdEncoding.DecodeString(form.Get("sign"))
	keys := make([]string, 0, len(form)-1)
	for key := range form {
		if key != "sign" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+form.Get(key))
	}
	digest := sha256.Sum256([]byte(strings.Join(parts, "&")))
	if err != nil || rsa.VerifyPKCS1v15(&app.appKey.PublicKey, crypto.SHA256, digest[:], signature) != nil {
		g.t.Error("gateway rejected RSA2 application signature")
		w.WriteHeader(400)
		return
	}
	if form.Get("format") != "json" || form.Get("charset") != "utf-8" || form.Get("sign_type") != "RSA2" || form.Get("version") != "1.0" {
		g.t.Error("gateway received invalid common protocol fields")
		w.WriteHeader(400)
		return
	}
	if _, err := time.Parse("2006-01-02 15:04:05", form.Get("timestamp")); err != nil {
		g.t.Error("gateway received malformed timestamp")
		w.WriteHeader(400)
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	switch form.Get("method") {
	case "alipay.system.oauth.token":
		if form.Get("grant_type") != "authorization_code" || len(form) != 10 {
			g.t.Error("gateway received invalid exchange fields")
			w.WriteHeader(400)
			return
		}
		code := form.Get("code")
		grant, exists := g.grants[code]
		g.exchanges[code]++
		if !exists || grant.app != app.id || g.exchanges[code] > 1 {
			g.respond(w, app.alipayKey, "error_response", map[string]string{"code": "40002", "sub_code": "isv.code-invalid"})
			return
		}
		// A local opaque test token identifies the profile request. Never emitted in diagnostics.
		access := "synthetic-access-" + code
		g.access[access] = code
		key := app.alipayKey
		if grant.badSignature {
			key = app.appKey
		}
		g.respond(w, key, "alipay_system_oauth_token_response", map[string]string{"access_token": access, "open_id": grant.open, "union_id": grant.union})
	case "alipay.user.info.share":
		if len(form) != 9 {
			g.t.Error("gateway received invalid profile fields")
			w.WriteHeader(400)
			return
		}
		code, exists := g.access[form.Get("auth_token")]
		grant := g.grants[code]
		if !exists || grant.app != app.id {
			g.t.Error("gateway profile token/application mismatch")
			w.WriteHeader(400)
			return
		}
		g.profiles[code]++
		if grant.profileUnavailable {
			w.WriteHeader(503)
			return
		}
		g.respond(w, app.alipayKey, "alipay_user_info_share_response", map[string]string{"code": "10000", "open_id": grant.open, "nick_name": grant.name, "avatar": grant.avatar})
	default:
		g.t.Error("gateway received unexpected method")
		w.WriteHeader(400)
	}
}

func (g *alipayHTTPGateway) respond(w http.ResponseWriter, key *rsa.PrivateKey, node string, payload map[string]string) {
	raw, err := json.Marshal(payload)
	if err != nil {
		g.t.Error("gateway cannot marshal response")
		w.WriteHeader(500)
		return
	}
	digest := sha256.Sum256(raw)
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		g.t.Error("gateway cannot sign response")
		w.WriteHeader(500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{node: json.RawMessage(raw), "sign": base64.StdEncoding.EncodeToString(signature), "sign_type": "RSA2"})
}

func (g *alipayHTTPGateway) assertCalls(t *testing.T, code string, exchanges, profiles int) {
	t.Helper()
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.exchanges[code] != exchanges || g.profiles[code] != profiles {
		t.Fatalf("gateway call counts: exchange=%d profile=%d, want %d/%d", g.exchanges[code], g.profiles[code], exchanges, profiles)
	}
}

type alipayHTTPFixture struct {
	t      *testing.T
	server *httptest.Server
	pool   *pgxpool.Pool
	signer *tokens.Signer
}

func requireAlipayHTTPDependencies(t *testing.T) {
	t.Helper()
	_ = dbDSN(t)
	if os.Getenv("ACCOUNTSVC_TEST_REDIS_URL") == "" {
		t.Skip("ACCOUNTSVC_TEST_REDIS_URL not set; Alipay HTTP contract requires real Redis")
	}
}

func newAlipayHTTPFixture(t *testing.T, cfg accountkit.Config) *alipayHTTPFixture {
	t.Helper()
	requireAlipayHTTPDependencies(t)
	redisURL := os.Getenv("ACCOUNTSVC_TEST_REDIS_URL")
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal("parse real Redis URL")
	}
	opts.MaxRetries = -1
	rdb := redis.NewClient(opts)
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	cfg.KeyPrefix = "alipay-http:" + hex.EncodeToString(nonce[:]) + ":"
	t.Cleanup(func() {
		defer rdb.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		// Delete only the randomly owned test prefix; never flush the shared database.
		keys, err := rdb.Keys(ctx, cfg.KeyPrefix+"*").Result()
		if err != nil {
			t.Error("list owned Redis keys", err)
			return
		}
		if len(keys) > 0 {
			if err := rdb.Del(ctx, keys...).Err(); err != nil {
				t.Error("clean owned Redis keys", err)
			}
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Fatal("configured real Redis unavailable")
	}
	a, pool, _ := integrationInstance(t, cfg, rdb)
	if err := a.Migrate(ctx); err != nil {
		t.Fatal("migrate real PostgreSQL", err)
	}
	a.Start(context.Background())
	router := chi.NewRouter()
	router.Mount("/v1", a.EndUserHandler())
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	signer, err := tokens.NewSigner(tokens.Options{Keys: cfg.JWTKeys, Active: cfg.JWTActiveKey, Issuer: cfg.JWTIssuer, Audience: cfg.JWTAudience})
	if err != nil {
		t.Fatal(err)
	}
	return &alipayHTTPFixture{t: t, server: srv, pool: pool, signer: signer}
}

// call never includes request/response bodies in failures: both may hold credentials.
func (f *alipayHTTPFixture) call(method, path, bearer string, body any, status int) map[string]json.RawMessage {
	f.t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		f.t.Fatal(err)
	}
	req, err := http.NewRequest(method, f.server.URL+"/v1"+path, bytes.NewReader(raw))
	if err != nil {
		f.t.Fatal("create HTTP request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Device-Id", "alipay-http-device")
	req.Header.Set("X-Device-Name", "Alipay HTTP contract")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		f.t.Fatal("public HTTP request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != status {
		f.t.Fatalf("%s %s: HTTP %d, want %d", method, path, resp.StatusCode, status)
	}
	var result map[string]json.RawMessage
	if err := json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(&result); err != nil {
		f.t.Fatal("decode public HTTP response")
	}
	return result
}

func alipayHTTPString(t *testing.T, body map[string]json.RawMessage, key string) string {
	t.Helper()
	var value string
	if err := json.Unmarshal(body[key], &value); err != nil {
		t.Fatalf("missing/invalid HTTP field %s", key)
	}
	return value
}

type alipayHTTPTokens struct{ access, refresh, user, session string }

func (f *alipayHTTPFixture) tokenResult(body map[string]json.RawMessage) alipayHTTPTokens {
	f.t.Helper()
	result := alipayHTTPTokens{access: alipayHTTPString(f.t, body, "access_token"), refresh: alipayHTTPString(f.t, body, "refresh_token"), user: alipayHTTPString(f.t, body, "user_id")}
	if result.access == "" || result.refresh == "" || !strings.HasPrefix(result.user, "u_") || alipayHTTPString(f.t, body, "token_type") != "Bearer" || alipayHTTPString(f.t, body, "scope") != "user" {
		f.t.Fatal("invalid public token contract")
	}
	var expires int
	if err := json.Unmarshal(body["expires_in"], &expires); err != nil || expires <= 0 {
		f.t.Fatal("invalid access token expiry")
	}
	claims, err := f.signer.Parse(result.access)
	if err != nil {
		f.t.Fatal("public JWT failed signature/issuer/audience/expiry verification")
	}
	if claims.UserID != result.user || !strings.HasPrefix(claims.SessionID, "s_") || claims.Scope != "user" || claims.AuthTime.IsZero() {
		f.t.Fatal("public JWT claims differ from login result")
	}
	result.session = claims.SessionID
	return result
}

func (f *alipayHTTPFixture) login(app, code string) alipayHTTPTokens {
	f.t.Helper()
	return f.tokenResult(f.call("POST", "/users:signInWithIdp", "", map[string]any{"kind": "ALIPAY", "credential": map[string]string{"app_id": app, "code": code}}, 200))
}

func (f *alipayHTTPFixture) assertMe(token alipayHTTPTokens, name, avatar string) {
	f.t.Helper()
	body := f.call("GET", "/users/me", token.access, nil, 200)
	if alipayHTTPString(f.t, body, "name") != "users/"+token.user || alipayHTTPString(f.t, body, "state") != "ACTIVE" || alipayHTTPString(f.t, body, "display_name") != name || alipayHTTPString(f.t, body, "avatar_url") != avatar {
		f.t.Fatal("public me account/profile differs from expected values")
	}
}

func (f *alipayHTTPFixture) counts() [4]int {
	f.t.Helper()
	var result [4]int
	err := f.pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM user_account), (SELECT count(*) FROM identity), (SELECT count(*) FROM session), (SELECT count(*) FROM sign_in_completion_flow)`).Scan(&result[0], &result[1], &result[2], &result[3])
	if err != nil {
		f.t.Fatal(err)
	}
	return result
}

func TestAlipayHTTPLoginContract(t *testing.T) {
	requireAlipayHTTPDependencies(t)
	const app = "2026000000000001"
	g := newAlipayHTTPGateway(t, app)
	g.grants = map[string]alipayHTTPGrant{
		"first":    {app: app, open: "open-one", union: "union-one", profileUnavailable: true},
		"forged":   {app: app, open: "forged-open", union: "forged-union", badSignature: true},
		"name":     {app: app, open: "open-one", union: "union-one", name: "Provider name"},
		"avatar":   {app: app, open: "open-one", union: "union-one", name: "Replacement name", avatar: "https://images.example.test/first.png"},
		"preserve": {app: app, open: "open-one", union: "union-one", name: "Another name", avatar: "https://images.example.test/other.png"},
	}
	f := newAlipayHTTPFixture(t, g.config(accountkit.AlipaySubjectModeUnionID))
	token := f.login(app, "first")
	f.assertMe(token, "", "")
	g.assertCalls(t, "first", 1, 1)
	refreshed := f.tokenResult(f.call("POST", "/token", "", map[string]string{"grant_type": "refresh_token", "refresh_token": token.refresh}, 200))
	if refreshed.user != token.user || refreshed.session != token.session || refreshed.refresh == token.refresh || refreshed.access == token.access {
		t.Fatal("refresh did not rotate the same account/session token pair")
	}
	f.assertMe(refreshed, "", "")
	before := f.counts()
	for _, tc := range []struct {
		code   string
		status int
		reason string
	}{{"forged", 500, ""}, {"first", 400, "IDP_CREDENTIAL_INVALID"}} {
		body := f.call("POST", "/users:signInWithIdp", "", map[string]any{"kind": "ALIPAY", "credential": map[string]string{"app_id": app, "code": tc.code}}, tc.status)
		var failure struct {
			Reason  string `json:"reason"`
			Status  string `json:"status"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(body["error"], &failure); err != nil || failure.Reason != tc.reason {
			t.Fatal("unexpected safe login error classification")
		}
		if tc.status == 500 && (failure.Status != "INTERNAL" || failure.Message != "internal error") {
			t.Fatal("forged response did not return the fixed internal error")
		}
		if _, exists := body["access_token"]; exists {
			t.Fatal("rejected login issued a token")
		}
		if f.counts() != before {
			t.Fatal("rejected login changed account, identity, session or pending rows")
		}
	}
	g.assertCalls(t, "forged", 1, 0)
	g.assertCalls(t, "first", 2, 1)
	token = f.login(app, "name")
	f.assertMe(token, "Provider name", "")
	f.call("PATCH", "/users/me", token.access, map[string]string{"display_name": "Chosen name"}, 200)
	token = f.login(app, "avatar")
	f.assertMe(token, "Chosen name", "https://images.example.test/first.png")
	token = f.login(app, "preserve")
	f.assertMe(token, "Chosen name", "https://images.example.test/first.png")
	if token.user != refreshed.user {
		t.Fatal("later profile login changed account")
	}
	for _, code := range []string{"name", "avatar", "preserve"} {
		g.assertCalls(t, code, 1, 1)
	}
	counts := f.counts()
	if counts[0] != 1 || counts[1] != 1 || counts[3] != 0 {
		t.Fatal("single-app login did not retain one account/identity without pending rows")
	}
}

func TestAlipayHTTPMultiAppIdentityContract(t *testing.T) {
	requireAlipayHTTPDependencies(t)
	const appA, appB = "2026000000000001", "2026000000000002"
	t.Run("UNIONID shares identity and merges OpenIDs", func(t *testing.T) {
		g := newAlipayHTTPGateway(t, appA, appB)
		g.grants = map[string]alipayHTTPGrant{
			"a-first":    {app: appA, open: "open-a", union: "shared-union", profileUnavailable: true},
			"b-fill":     {app: appB, open: "open-b", union: "shared-union", name: "Cross app name", avatar: "https://images.example.test/cross.png"},
			"a-preserve": {app: appA, open: "open-a", union: "shared-union", name: "Different name", avatar: "https://images.example.test/different.png"},
		}
		f := newAlipayHTTPFixture(t, g.config(accountkit.AlipaySubjectModeUnionID))
		a := f.login(appA, "a-first")
		f.assertMe(a, "", "")
		b := f.login(appB, "b-fill")
		if a.user != b.user {
			t.Fatal("same UnionID across apps created separate accounts")
		}
		f.assertMe(b, "Cross app name", "https://images.example.test/cross.png")
		f.call("PATCH", "/users/me", b.access, map[string]string{"display_name": "Chosen cross name", "avatar_url": "https://images.example.test/chosen.png"}, 200)
		a = f.login(appA, "a-preserve")
		f.assertMe(a, "Chosen cross name", "https://images.example.test/chosen.png")
		var subject, userID string
		var meta struct {
			UnionID string            `json:"unionid"`
			OpenIDs map[string]string `json:"openids"`
		}
		var raw []byte
		if err := f.pool.QueryRow(context.Background(), `SELECT user_id, provider_subject, provider_meta FROM identity WHERE kind = 'ALIPAY' AND delete_time IS NULL`).Scan(&userID, &subject, &raw); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &meta); err != nil {
			t.Fatal(err)
		}
		if userID != a.user || subject != "unionid:shared-union" || meta.UnionID != "shared-union" || len(meta.OpenIDs) != 2 || meta.OpenIDs[appA] != "open-a" || meta.OpenIDs[appB] != "open-b" {
			t.Fatal("UnionID identity did not merge both application OpenIDs")
		}
		counts := f.counts()
		if counts[0] != 1 || counts[1] != 1 || counts[3] != 0 {
			t.Fatal("UnionID did not produce one account and one active identity")
		}
		for _, code := range []string{"a-first", "b-fill", "a-preserve"} {
			g.assertCalls(t, code, 1, 1)
		}
	})
	t.Run("OPENID isolates apps and never upgrades to UnionID", func(t *testing.T) {
		g := newAlipayHTTPGateway(t, appA, appB)
		g.grants = map[string]alipayHTTPGrant{
			"a-open":  {app: appA, open: "same-open"},
			"b-open":  {app: appB, open: "same-open"},
			"a-union": {app: appA, open: "same-open", union: "later-shared-union"},
			"b-union": {app: appB, open: "same-open", union: "later-shared-union"},
		}
		f := newAlipayHTTPFixture(t, g.config(accountkit.AlipaySubjectModeOpenID))
		a, b := f.login(appA, "a-open"), f.login(appB, "b-open")
		if a.user == b.user {
			t.Fatal("same OpenID text across apps shared an account")
		}
		laterA, laterB := f.login(appA, "a-union"), f.login(appB, "b-union")
		if laterA.user != a.user || laterB.user != b.user {
			t.Fatal("later UnionID upgraded/merged fixed OpenID accounts")
		}
		f.assertMe(laterA, "", "")
		f.assertMe(laterB, "", "")
		for app, userID := range map[string]string{appA: a.user, appB: b.user} {
			var subject string
			if err := f.pool.QueryRow(context.Background(), `SELECT provider_subject FROM identity WHERE user_id = $1 AND kind = 'ALIPAY' AND delete_time IS NULL`, userID).Scan(&subject); err != nil {
				t.Fatal(err)
			}
			if subject != "openid:"+app+":same-open" {
				t.Fatal("OpenID identity subject changed after later UnionID")
			}
		}
		counts := f.counts()
		if counts[0] != 2 || counts[1] != 2 || counts[3] != 0 {
			t.Fatal("OpenID mode did not preserve two independent accounts/identities")
		}
		for _, code := range []string{"a-open", "b-open", "a-union", "b-union"} {
			g.assertCalls(t, code, 1, 1)
		}
	})
}
