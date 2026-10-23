package idp

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
)

const alipayTestApp = "2026000000000001"
const alipayTestApp2 = "2026000000000002"

var alipayTestKeys struct {
	sync.Once
	app, platform *rsa.PrivateKey
}

func alipayKeys(t *testing.T) (*rsa.PrivateKey, *rsa.PrivateKey) {
	t.Helper()
	alipayTestKeys.Do(func() {
		var err error
		alipayTestKeys.app, err = rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		alipayTestKeys.platform, err = rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
	})
	return alipayTestKeys.app, alipayTestKeys.platform
}

func alipayTestConfig(t *testing.T, endpoint string) AlipayConfig {
	t.Helper()
	app, platform := alipayKeys(t)
	private := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(app)}))
	public := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PUBLIC KEY", Bytes: x509.MarshalPKCS1PublicKey(&platform.PublicKey)}))
	return AlipayConfig{Apps: []AlipayApp{{AppID: alipayTestApp, PrivateKey: private, AlipayPublicKey: public}, {AppID: alipayTestApp2, PrivateKey: private, AlipayPublicKey: public}}, GatewayURL: endpoint}
}

// Fixtures sign literal bytes independently from the production response parser.
func alipaySigned(t *testing.T, node, raw string) string {
	t.Helper()
	_, key := alipayKeys(t)
	digest := sha256.Sum256([]byte(raw))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf(`{%q:%s,"sign":%q}`, node, raw, base64.StdEncoding.EncodeToString(sig))
}

func alipayCredential(app string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"app_id":%q,"code":"synthetic-code+ /&="}`, app))
}

func TestAlipayExchange(t *testing.T) {
	for _, mode := range []AlipaySubjectMode{AlipaySubjectModeUnionID, AlipaySubjectModeOpenID} {
		t.Run(string(mode), func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "POST" || r.URL.RawQuery != "" || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
					t.Error("wrong request transport")
				}
				if err := r.ParseForm(); err != nil {
					t.Error(err)
					return
				}
				q := r.PostForm
				if q.Get("method") != "alipay.system.oauth.token" || q.Get("code") != "synthetic-code+ /&=" || q.Get("grant_type") != "authorization_code" || q.Get("format") != "json" || q.Get("charset") != "utf-8" || q.Get("sign_type") != "RSA2" || q.Get("version") != "1.0" || q.Get("timestamp") != "2026-10-23 08:00:00" || len(q) != 10 {
					t.Errorf("incorrect protocol parameters")
				}
				keys := make([]string, 0, len(q))
				for k := range q {
					if k != "sign" {
						keys = append(keys, k)
					}
				}
				sort.Strings(keys)
				parts := make([]string, 0, len(keys))
				for _, k := range keys {
					parts = append(parts, k+"="+q.Get(k))
				}
				signature, err := base64.StdEncoding.DecodeString(q.Get("sign"))
				if err != nil {
					t.Error(err)
					return
				}
				digest := sha256.Sum256([]byte(strings.Join(parts, "&")))
				app, _ := alipayKeys(t)
				if err := rsa.VerifyPKCS1v15(&app.PublicKey, crypto.SHA256, digest[:], signature); err != nil {
					t.Errorf("bad request signature: %v", err)
				}
				fmt.Fprint(w, alipaySigned(t, "alipay_system_oauth_token_response", `{ "union_id" : "same-union", "open_id":"same-open", "user_id":"ignored", "scope":"auth_user", "expires_in":3600 }`))
			}))
			defer srv.Close()
			c := alipayTestConfig(t, srv.URL)
			c.SubjectMode = mode
			p, err := NewAlipay(AlipayOptions{Config: c, Now: func() time.Time { return time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC) }})
			if err != nil {
				t.Fatal(err)
			}
			for _, app := range []string{alipayTestApp, alipayTestApp2} {
				id, err := p.Verify(context.Background(), alipayCredential(app))
				if err != nil {
					t.Fatal(err)
				}
				want := "unionid:same-union"
				if mode == AlipaySubjectModeOpenID {
					want = "openid:" + app + ":same-open"
				}
				if id.Kind != enum.IdentityAlipay || id.Subject != want || id.UnionID != "same-union" || id.OpenIDs[app] != "same-open" || !id.ExpireTime.IsZero() {
					t.Errorf("wrong identity: %+v", id)
				}
			}
			if calls != 2 {
				t.Errorf("exchanged %d times", calls)
			}
		})
	}
}

func TestAlipayRejectsUntrustedExchange(t *testing.T) {
	signed := func(raw string) string { return alipaySigned(t, "alipay_system_oauth_token_response", raw) }
	errorNode := func(sub string) string {
		return alipaySigned(t, "error_response", fmt.Sprintf(`{"code":"40002","sub_code":%q,"sub_msg":"synthetic-private-upstream"}`, sub))
	}
	for _, tc := range []struct {
		name, body string
		status     int
		want       error
	}{
		{"invalid_code", errorNode("isv.code-invalid"), 200, ErrInvalidCredential},
		{"wrong_app", errorNode("isv.unmatched-app-id"), 200, ErrAppNotAllowed},
		{"grant_config", errorNode("isv.grant-type-invalid"), 200, ErrMisconfigured},
		{"app_config", errorNode("isv.app-id-invalid"), 200, ErrMisconfigured},
		{"busy", errorNode("isp.unknow-error"), 200, ErrUnavailable},
		{"unknown_error", errorNode("unrecognized-error"), 200, ErrMisconfigured},
		{"wrong_error_message_type", alipaySigned(t, "error_response", `{"code":"40002","sub_code":"isv.code-invalid","msg":{},"sub_msg":123}`), 200, ErrMisconfigured},
		{"error_without_code", alipaySigned(t, "error_response", `{"sub_code":"isv.code-invalid"}`), 200, ErrMisconfigured},
		{"error_with_success_code", alipaySigned(t, "error_response", `{"code":"10000","sub_code":"isv.code-invalid"}`), 200, ErrMisconfigured},
		{"contradictory_success", signed(`{"code":"10000","sub_code":"isv.code-invalid","union_id":"union"}`), 200, ErrMisconfigured},
		{"unsigned_error", `{"error_response":{"sub_code":"isv.code-invalid"}}`, 200, ErrMisconfigured},
		{"unsigned_success", `{"alipay_system_oauth_token_response":{"union_id":"union"}}`, 200, ErrMisconfigured},
		{"bad_signature", `{"alipay_system_oauth_token_response":{"union_id":"union"},"sign":"AAAA"}`, 200, ErrMisconfigured},
		{"duplicate_identity", signed(`{"union_id":"union","union_id":"other"}`), 200, ErrMisconfigured},
		{"escaped_duplicate", signed(`{"union_id":"union","\u0075nion_id":"other"}`), 200, ErrMisconfigured},
		{"duplicate_nested", signed(`{"union_id":"union","extra":{"x":1,"x":2}}`), 200, ErrMisconfigured},
		{"duplicate_node", strings.TrimSuffix(signed(`{"union_id":"union"}`), "}") + `,"alipay_system_oauth_token_response":{}}`, 200, ErrMisconfigured},
		{"two_nodes", strings.TrimSuffix(signed(`{"union_id":"union"}`), "}") + `,"error_response":{}}`, 200, ErrMisconfigured},
		{"trailing", signed(`{"union_id":"union"}`) + ` {}`, 200, ErrMisconfigured},
		{"wrong_type", signed(`{"union_id":123}`), 200, ErrMisconfigured},
		{"null_identity", signed(`{"union_id":null}`), 200, ErrMisconfigured},
		{"missing_union", signed(`{"open_id":"open","user_id":"never-fallback"}`), 200, ErrMisconfigured},
		{"space_identity", signed(`{"union_id":" union "}`), 200, ErrMisconfigured},
		{"control_identity", signed(`{"union_id":"union\n"}`), 200, ErrMisconfigured},
		{"long_identity", signed(`{"union_id":"` + strings.Repeat("a", 257) + `"}`), 200, ErrMisconfigured},
		{"too_large", signed(`{"union_id":"union","padding":"` + strings.Repeat("a", 64<<10) + `"}`), 200, ErrMisconfigured},
		{"truncated", `{"alipay_system_oauth_token_response":`, 200, ErrMisconfigured},
		{"rate_limit", "", 429, ErrUnavailable}, {"upstream_failure", "", 503, ErrUnavailable},
		{"http_unauthorized", errorNode("isv.code-invalid"), 401, ErrMisconfigured},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()
			p, err := NewAlipay(AlipayOptions{Config: alipayTestConfig(t, srv.URL)})
			if err != nil {
				t.Fatal(err)
			}
			_, err = p.Verify(context.Background(), alipayCredential(alipayTestApp))
			if !errors.Is(err, tc.want) {
				t.Fatalf("error=%v want %v", err, tc.want)
			}
			if strings.Contains(err.Error(), "synthetic-private-upstream") || calls != 1 {
				t.Errorf("unsafe error or retry: %v, calls=%d", err, calls)
			}
		})
	}
}

func TestAlipayRawResponseBytes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, alipaySigned(t, "alipay_system_oauth_token_response", `{ "union_id" : "uni\u006fn", "open_id":"open\/id" }`))
	}))
	defer srv.Close()
	p, _ := NewAlipay(AlipayOptions{Config: alipayTestConfig(t, srv.URL)})
	id, err := p.Verify(context.Background(), alipayCredential(alipayTestApp))
	if err != nil || id.Subject != "unionid:union" || id.OpenIDs[alipayTestApp] != "open/id" {
		t.Fatalf("raw-node signature: %+v %v", id, err)
	}
}

func TestAlipayCredentialAndRedirect(t *testing.T) {
	calls := 0
	followed := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { followed++ }))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return nil }}
	p, _ := NewAlipay(AlipayOptions{Config: alipayTestConfig(t, srv.URL), HTTP: client})
	for _, raw := range []string{`{}`, `null`, `[]`, `{"app_id":"x","code":123}`, `{"app_id":"x","code":"secret","other":1}`, `{"app_id":"x","code":"one","code":"two"}`, `{"APP_ID":"x","code":"x"}`} {
		_, err := p.Verify(context.Background(), json.RawMessage(raw))
		if !errors.Is(err, ErrMalformedCredential) {
			t.Errorf("credential accepted: %s: %v", raw, err)
		}
	}
	_, err := p.Verify(context.Background(), alipayCredential("2026000000000099"))
	if !errors.Is(err, ErrAppNotAllowed) {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("credential validation performed I/O")
	}
	_, err = p.Verify(context.Background(), alipayCredential(alipayTestApp))
	if !errors.Is(err, ErrUnavailable) || calls != 1 || followed != 0 {
		t.Fatalf("redirect/retry: %v, calls=%d followed=%d", err, calls, followed)
	}
	if client.CheckRedirect == nil {
		t.Fatal("shared client modified")
	}
}

func TestAlipayPolicyFingerprint(t *testing.T) {
	c := alipayTestConfig(t, "https://example.com/gateway.do")
	p, _ := NewAlipay(AlipayOptions{Config: c})
	baseline := p.PolicyFingerprint()
	c.Apps[0], c.Apps[1] = c.Apps[1], c.Apps[0]
	platform := alipayTestKeys.platform
	der, _ := x509.MarshalPKIXPublicKey(&platform.PublicKey)
	c.Apps[0].AlipayPublicKey = "\n" + string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})) + "\n"
	c.Apps[0].PrivateKey = string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(platform)}))
	same, _ := NewAlipay(AlipayOptions{Config: c})
	if same.PolicyFingerprint() != baseline {
		t.Fatal("order/PEM/private key changed fingerprint")
	}
	for _, mutate := range []func(*AlipayConfig){func(c *AlipayConfig) { c.SubjectMode = AlipaySubjectModeOpenID }, func(c *AlipayConfig) { c.GatewayURL = "https://example.com/other" }, func(c *AlipayConfig) { c.Apps = c.Apps[:1] }, func(c *AlipayConfig) {
		c.Apps[0].AlipayPublicKey = string(pem.EncodeToMemory(&pem.Block{Type: "RSA PUBLIC KEY", Bytes: x509.MarshalPKCS1PublicKey(&alipayTestKeys.app.PublicKey)}))
	}} {
		changed := alipayTestConfig(t, "https://example.com/gateway.do")
		mutate(&changed)
		other, err := NewAlipay(AlipayOptions{Config: changed})
		if err != nil {
			t.Fatal(err)
		}
		if other.PolicyFingerprint() == baseline {
			t.Fatal("policy change ignored")
		}
	}
}

func TestAlipaySeparateApplicationKeys(t *testing.T) {
	app, platform := alipayKeys(t)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if err := r.ParseForm(); err != nil {
			t.Error(err)
			return
		}
		key, signer := app, platform
		if r.PostForm.Get("app_id") == alipayTestApp2 {
			key, signer = platform, app
		}
		values := r.PostForm
		signature, _ := base64.StdEncoding.DecodeString(values.Get("sign"))
		values.Del("sign")
		keys := make([]string, 0, len(values))
		for k := range values {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, k+"="+values.Get(k))
		}
		digest := sha256.Sum256([]byte(strings.Join(parts, "&")))
		if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], signature); err != nil {
			t.Error("wrong application signing key")
		}
		raw := `{"union_id":"shared-union"}`
		digest = sha256.Sum256([]byte(raw))
		signature, err := rsa.SignPKCS1v15(rand.Reader, signer, crypto.SHA256, digest[:])
		if err != nil {
			t.Error(err)
			return
		}
		fmt.Fprintf(w, `{"alipay_system_oauth_token_response":%s,"sign":%q}`, raw, base64.StdEncoding.EncodeToString(signature))
	}))
	defer srv.Close()
	c := alipayTestConfig(t, srv.URL)
	c.Apps[1].PrivateKey = string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(platform)}))
	c.Apps[1].AlipayPublicKey = string(pem.EncodeToMemory(&pem.Block{Type: "RSA PUBLIC KEY", Bytes: x509.MarshalPKCS1PublicKey(&app.PublicKey)}))
	p, err := NewAlipay(AlipayOptions{Config: c})
	if err != nil {
		t.Fatal(err)
	}
	before := p.PolicyFingerprint()
	c.Apps[0] = AlipayApp{}
	c.Apps[1] = AlipayApp{}
	for _, appID := range []string{alipayTestApp, alipayTestApp2} {
		id, err := p.Verify(context.Background(), alipayCredential(appID))
		if err != nil || id.Subject != "unionid:shared-union" {
			t.Fatalf("application keys: %+v %v", id, err)
		}
	}
	if calls != 2 || p.PolicyFingerprint() != before {
		t.Fatal("caller mutated provider configuration")
	}
}

func TestAlipayOpenIDRequiresOpenID(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprint(w, alipaySigned(t, "alipay_system_oauth_token_response", `{"union_id":"union","user_id":"never-fallback"}`))
	}))
	defer srv.Close()
	c := alipayTestConfig(t, srv.URL)
	c.SubjectMode = AlipaySubjectModeOpenID
	p, err := NewAlipay(AlipayOptions{Config: c})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Verify(context.Background(), alipayCredential(alipayTestApp))
	if !errors.Is(err, ErrMisconfigured) || calls != 1 {
		t.Fatalf("openid fallback: %v calls=%d", err, calls)
	}
}
