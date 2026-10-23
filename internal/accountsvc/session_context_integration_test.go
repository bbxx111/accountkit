package accountsvc_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestServiceIntegrationCurrentSessionContext(t *testing.T) {
	f := newIntegration(t)
	f.env["ACCOUNTKIT_MAX_IDENTITIES_PER_KIND"] = "2"
	p := f.start(map[string]string{"ACCOUNTSVC_TLS_ENABLED": "false", "ACCOUNTSVC_TLS_CERT_FILE": "", "ACCOUNTSVC_TLS_KEY_FILE": "", "ACCOUNTSVC_TRUSTED_PROXY_CIDRS": "127.0.0.1/32"})
	g := newTestGateway(t, p.baseURL, f.cert, false)
	call := func(method, path, token string, body any, headers map[string]string, want int) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(body)
		r, _ := http.NewRequest(method, g.server.URL+"/v1"+path, bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Device-Id", "service-context")
		r.Header.Set("X-Device-Name", "Original Phone")
		r.Header.Set("X-Request-Id", "service-context")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		resp, e := f.client.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		data, e := io.ReadAll(resp.Body)
		resp.Body.Close()
		if e != nil {
			t.Fatal(e)
		}
		if resp.StatusCode != want {
			t.Fatalf("%s expected %d got %d", path, want, resp.StatusCode)
		}
		if strings.HasSuffix(path, "/current") && (resp.Header.Get("Cache-Control") != "no-store" || want == 401 && resp.Header.Get("WWW-Authenticate") == "") {
			t.Fatal("cache/challenge lost")
		}
		var out map[string]any
		if len(data) > 0 {
			json.Unmarshal(data, &out)
		}
		f.rememberChallenge(body, out)
		return out
	}
	const original = "svc-context@example.test"
	const retained = "svc-context-retained@example.test"
	call("POST", "/users:sendSignInCode", "", map[string]string{"channel": "EMAIL", "target": original}, nil, 200)
	mail := f.smtp.mail(t)
	body := map[string]any{"kind": "EMAIL", "credential": map[string]string{"target": original, "code_id": f.codeID(original), "code": mail.code}}
	call("POST", "/users:signInWithCode", "", body, map[string]string{"X-Client-Type": "INVALID"}, 400)
	pair := call("POST", "/users:signInWithCode", "", body, map[string]string{"X-Client-Type": "WEB", "X-Client-Browser-Name": "Firefox"}, 200)
	access := pair["access_token"].(string)
	f.secrets = append(f.secrets, original, retained, mail.code, access, pair["refresh_token"].(string))
	initial := call("GET", "/users/me/sessions/current", access, nil, nil, 200)
	if _, exists := initial["authentication_identities_known"]; exists {
		t.Fatal("removed known flag exposed")
	}
	if initial["client_info"].(map[string]any)["browser"].(map[string]any)["name"] != "Firefox" {
		t.Fatal("missing initial context")
	}
	f.call("POST", p.baseURL+"/v1/users/me:sendBindCode", access, map[string]string{"channel": "EMAIL", "target": retained}, 200)
	mail = f.smtp.mail(t)
	f.secrets = append(f.secrets, mail.code)
	bound := f.call("POST", p.baseURL+"/v1/users/me/identities", access, map[string]any{"kind": "EMAIL", "credential": map[string]string{"target": retained, "code_id": f.codeID(retained), "code": mail.code}}, 201)
	rotated := call("POST", "/token", "", map[string]string{"grant_type": "refresh_token", "refresh_token": pair["refresh_token"].(string)}, map[string]string{"X-Client-Type": "NATIVE", "X-Client-App-Name": "Service App"}, 200)
	f.secrets = append(f.secrets, rotated["access_token"].(string), rotated["refresh_token"].(string))
	latest := call("GET", "/users/me/sessions/current", access, nil, nil, 200)
	if latest["client_info"].(map[string]any)["app"].(map[string]any)["name"] != "Service App" {
		t.Fatal("proxy optional refresh headers not observed")
	}
	// Legacy clients retain their unchanged login flow and omit the new structured fields.
	legacy := f.login(p, "svc-context-legacy@example.test")
	legacyAccess := legacy["access_token"].(string)
	old := call("GET", "/users/me/sessions/current", legacyAccess, nil, nil, 200)
	if _, ok := old["client_info"]; ok {
		t.Fatal("legacy snapshot invented")
	}
	time.Sleep(1100 * time.Millisecond)
	f.call("POST", p.baseURL+"/v1/users/me:sendReauthenticationCode", access, map[string]string{"channel": "EMAIL", "target": retained}, 200)
	mail = f.smtp.mail(t)
	f.secrets = append(f.secrets, mail.code)
	// The proxy allowlist only adds the explicit new GET; reauthentication itself remains covered on the service entry point.
	reauth := f.call("POST", p.baseURL+"/v1/users/me:reauthenticate", access, map[string]any{"kind": "EMAIL", "credential": map[string]string{"target": retained, "code_id": f.codeID(retained), "code": mail.code}}, 200)
	f.secrets = append(f.secrets, reauth["access_token"].(string))
	current := call("GET", "/users/me/sessions/current", access, nil, map[string]string{"X-Client-Type": "INVALID"}, 200)
	if current["name"] != initial["name"] || len(current["authentication_identities"].([]any)) != 1 {
		t.Fatal("proof or session changed")
	}
	sources := current["authentication_identities"].([]any)
	if sources[0].(map[string]any)["name"] != bound["name"] {
		t.Fatal("reauthentication did not replace authentication source")
	}
	boundParts := strings.Split(bound["name"].(string), "/")
	f.call("DELETE", p.baseURL+"/v1/users/me/identities/"+boundParts[len(boundParts)-1], reauth["access_token"].(string), nil, 204)
	call("GET", "/users/me/sessions/current", access, nil, nil, 401)
	p.stop()
	f.assertAuditPrivate()
	f.assertSafe(g.logs.String())
}
