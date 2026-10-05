package accountsvc_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/bbxx111/accountkit/examples/remoteauth"
)

// These requests exercise the actual service rather than deriving protocol
// expectations from its handler registration or TLS configuration.
func transportRequest(t *testing.T, client *http.Client, method, address, contentType, body string, headers map[string]string, want int) (http.Header, map[string]any) {
	t.Helper()
	r, err := http.NewRequest(method, address, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	resp, err := client.Do(r)
	if err != nil {
		t.Fatal("transport request failed")
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != want {
		t.Fatalf("%s %s: status=%d want=%d read error=%v", method, r.URL.Path, resp.StatusCode, want, err)
	}
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return resp.Header, out
}

func TestServiceIntegrationTransportModes(t *testing.T) {
	requireServiceIntegration(t)
	for _, mode := range []struct {
		name, scheme string
		extra        map[string]string
	}{
		{name: "production_default_https", scheme: "https://"},
		{name: "production_explicit_http", scheme: "http://", extra: map[string]string{"ACCOUNTSVC_TLS_ENABLED": "false", "ACCOUNTSVC_TLS_CERT_FILE": "", "ACCOUNTSVC_TLS_KEY_FILE": ""}},
	} {
		t.Run(mode.name, func(t *testing.T) {
			f := newIntegration(t)
			f.oidc()
			p := f.start(mode.extra)
			if !strings.HasPrefix(p.baseURL, mode.scheme) {
				t.Fatal("unexpected listener protocol")
			}
			for _, path := range []string{"/healthz", "/readyz"} {
				f.call("GET", p.baseURL+path, "", nil, 200)
			}
			f.call("POST", p.baseURL+"/internal/v1/introspect", "", nil, 404)
			pair := f.login(p, mode.name+"@example.test")
			access, refresh := pair["access_token"].(string), pair["refresh_token"].(string)
			form := url.Values{"token": {access}}.Encode()
			for _, authorization := range []string{"", "Basic YnVzaW5lc3M6d3Jvbmc=", "Bearer " + access, "Bearer " + f.env["FIXTURE_operator"]} {
				headers, out := transportRequest(t, f.client, "POST", p.baseURL+"/v1/introspect", "application/x-www-form-urlencoded", form, map[string]string{"Authorization": authorization}, 401)
				if out["error"] != "invalid_client" || !strings.HasPrefix(headers.Get("WWW-Authenticate"), "Basic ") || headers.Get("Cache-Control") != "no-store" || out["active"] != nil {
					t.Fatal("introspection authentication or challenge changed with transport")
				}
			}
			f.introspect(p, access, true)
			rotated := f.call("POST", p.baseURL+"/v1/token", "", map[string]string{"grant_type": "refresh_token", "refresh_token": refresh}, 200)
			f.secrets = append(f.secrets, rotated["access_token"].(string), rotated["refresh_token"].(string))
			f.introspect(p, rotated["access_token"].(string), true)
			client, err := remoteauth.NewClient(remoteauth.Config{Endpoint: p.baseURL + "/v1/introspect", ClientID: "business", ClientSecret: f.secrets[1], HTTPClient: f.client, AllowHTTP: mode.scheme == "http://"})
			if err != nil {
				t.Fatal(err)
			}
			result, err := client.Introspect(t.Context(), rotated["access_token"].(string))
			if err != nil || !result.Active || result.Subject != pair["user_id"] || result.Scope != "user" {
				t.Fatal("remote business authentication failed")
			}
			if mode.scheme == "http://" && !strings.Contains(p.output.String(), "http_tls_enabled=false") {
				// Structured logs must state the effective protocol without claiming
				// that a gateway has been verified.
				if !strings.Contains(p.output.String(), `"http_tls_enabled":false`) {
					t.Fatal("HTTP mode absent from startup log")
				}
			}
			p.stop()
			f.assertAuditPrivate()
			// SMTP remains implicit TLS with successful authenticated delivery in
			// both cases; explicitly disabling it must still fail production startup.
			for _, invalid := range []struct {
				name  string
				extra map[string]string
			}{
				{"ACCOUNTSVC_INTERNAL_ADDR", map[string]string{"ACCOUNTSVC_INTERNAL_ADDR": "private-legacy-address:9080"}},
				{"ACCOUNTSVC_TLS_ENABLED", map[string]string{"ACCOUNTSVC_TLS_ENABLED": "private-invalid-boolean"}},
				{"ACCOUNTSVC_TLS_ENABLED", map[string]string{"ACCOUNTSVC_TLS_ENABLED": "false"}},
				{"ACCOUNTSVC_SMTP_TLS_MODE", map[string]string{"ACCOUNTSVC_TLS_ENABLED": "false", "ACCOUNTSVC_TLS_CERT_FILE": "", "ACCOUNTSVC_TLS_KEY_FILE": "", "ACCOUNTSVC_SMTP_TLS_MODE": "none"}},
			} {
				output, err := f.command(invalid.extra, "serve")
				if err == nil || !strings.Contains(output, invalid.name) || strings.Contains(output, "private-") {
					t.Fatalf("startup should reject %s without leaking its value", invalid.name)
				}
			}
		})
	}
}
