package accountsvc_test

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/examples/remoteauth"
)

type countedTransport struct {
	base  *http.Transport
	calls atomic.Int32
}

func (c *countedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.calls.Add(1)
	return c.base.RoundTrip(r)
}

// testGateway is deliberately a local contract fixture, with no production
// gateway configuration, rate limiter, deployment identity or retry policy.
type testGateway struct {
	server    *httptest.Server
	transport *countedTransport
	logs      safeBuffer
}

func newTestGateway(t *testing.T, upstream string, cert tls.Certificate, internal bool) *testGateway {
	t.Helper()
	target, err := url.Parse(upstream)
	if err != nil {
		t.Fatal(err)
	}
	g := &testGateway{transport: &countedTransport{base: &http.Transport{}}}
	proxy := &httputil.ReverseProxy{
		Transport: g.transport,
		ErrorLog:  log.New(&g.logs, "", 0),
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
			// Rewrite removes inbound forwarding headers. SetXForwarded derives
			// new values from this connection, never a caller-supplied chain.
			r.SetXForwarded()
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, "upstream unavailable", http.StatusBadGateway)
		},
	}
	public := map[string]bool{
		"/v1/users:sendSignInCode": true, "/v1/users:signInWithCode": true,
		"/v1/users/me": true, "/v1/users/me/sessions": true,
		"/v1/token": true, "/v1/revoke": true,
	}
	g.server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		allowed := public[r.URL.Path]
		if internal {
			allowed = r.URL.Path == "/v1/introspect"
		}
		if !allowed {
			http.NotFound(w, r)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	g.server.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	g.server.StartTLS()
	t.Cleanup(func() {
		g.server.CloseClientConnections()
		g.server.Close()
		g.transport.base.CloseIdleConnections()
	})
	return g
}

func TestServiceIntegrationGatewayBoundary(t *testing.T) {
	f := newIntegration(t)
	f.env["ACCOUNTKIT_CODE_COOLDOWN"] = "1m"
	p := f.start(map[string]string{
		"ACCOUNTSVC_TLS_ENABLED": "false", "ACCOUNTSVC_TLS_CERT_FILE": "", "ACCOUNTSVC_TLS_KEY_FILE": "",
		"ACCOUNTSVC_TRUSTED_PROXY_CIDRS": "127.0.0.1/32",
	})
	public := newTestGateway(t, p.baseURL, f.cert, false)
	internal := newTestGateway(t, p.baseURL, f.cert, true)
	for _, path := range []string{"/v1/introspect", "/healthz", "/readyz", "/internal/v1/introspect", "/admin/v1/users", "/v1/not-allowlisted"} {
		for _, method := range []string{"GET", "POST"} {
			transportRequest(t, f.client, method, public.server.URL+path, "application/x-www-form-urlencoded", "token=private-blocked-token", nil, 404)
		}
	}
	if public.transport.calls.Load() != 0 {
		t.Fatal("public denied routes reached accountsvc")
	}
	headers := map[string]string{
		"X-Request-Id": "gateway-send", "X-Device-Id": "gateway-device", "X-Device-Name": "Gateway phone",
		"X-Forwarded-For": "198.51.100.9", "X-Forwarded-Host": "forged.example", "X-Forwarded-Proto": "http",
		"Forwarded": "for=198.51.100.9;proto=http",
	}
	target := "gateway@example.test"
	body := `{"channel":"EMAIL","target":"` + target + `"}`
	h, _ := transportRequest(t, f.client, "POST", public.server.URL+"/v1/users:sendSignInCode", "application/json", body, headers, 200)
	if h.Get("X-Request-Id") != "gateway-send" {
		t.Fatal("request ID not preserved")
	}
	m := f.smtp.mail(t)
	if m.target != target {
		t.Fatal("gateway changed mail recipient")
	}
	f.secrets = append(f.secrets, target, m.code, "private-blocked-token")
	h, out := transportRequest(t, f.client, "POST", public.server.URL+"/v1/users:sendSignInCode", "application/json", body, headers, 429)
	retry, err := strconv.Atoi(h.Get("Retry-After"))
	errObj, _ := out["error"].(map[string]any)
	if err != nil || retry < 1 || errObj["reason"] != "COOLDOWN" {
		t.Fatal("library cooldown Retry-After changed at gateway")
	}
	headers["X-Request-Id"] = "gateway-signin"
	raw, _ := json.Marshal(map[string]any{"email": map[string]string{"target": target, "code": m.code}})
	h, pair := transportRequest(t, f.client, "POST", public.server.URL+"/v1/users:signInWithCode", "application/json", string(raw), headers, 200)
	if h.Get("Cache-Control") != "no-store" || h.Get("X-Request-Id") != "gateway-signin" {
		t.Fatal("credential response cache or tracing semantics changed")
	}
	access, refresh := pair["access_token"].(string), pair["refresh_token"].(string)
	f.secrets = append(f.secrets, access, refresh)
	headers["Authorization"] = "Bearer " + access
	transportRequest(t, f.client, "GET", public.server.URL+"/v1/users/me", "", "", headers, 200)
	_, sessions := transportRequest(t, f.client, "GET", public.server.URL+"/v1/users/me/sessions", "", "", headers, 200)
	items, _ := sessions["sessions"].([]any)
	if len(items) != 1 {
		t.Fatal("gateway login session missing")
	}
	session, _ := items[0].(map[string]any)
	if session["device_id"] != "gateway-device" || session["device_name"] != "Gateway phone" {
		t.Fatal("gateway changed device metadata")
	}
	headers["Authorization"] = "Bearer invalid-consumer-token"
	h, _ = transportRequest(t, f.client, "GET", public.server.URL+"/v1/users/me", "", "", headers, 401)
	if !strings.HasPrefix(h.Get("WWW-Authenticate"), "Bearer ") {
		t.Fatal("Bearer challenge lost")
	}
	form := url.Values{"token": {access}}.Encode()
	for _, auth := range []string{"", "Bearer " + access, "Basic YnVzaW5lc3M6d3Jvbmc="} {
		h, result := transportRequest(t, f.client, "POST", internal.server.URL+"/v1/introspect", "application/x-www-form-urlencoded", form, map[string]string{"Authorization": auth}, 401)
		if result["error"] != "invalid_client" || !strings.HasPrefix(h.Get("WWW-Authenticate"), "Basic ") || h.Get("Cache-Control") != "no-store" {
			t.Fatal("TLS termination bypassed Basic or lost its challenge")
		}
	}
	basic := "Basic " + base64.StdEncoding.EncodeToString([]byte("business:"+f.secrets[1]))
	h, result := transportRequest(t, f.client, "POST", internal.server.URL+"/v1/introspect", "application/x-www-form-urlencoded", form, map[string]string{"Authorization": basic}, 200)
	if result["active"] != true || h.Get("Cache-Control") != "no-store" {
		t.Fatal("authorized internal TLS request failed")
	}
	client, err := remoteauth.NewClient(remoteauth.Config{Endpoint: internal.server.URL + "/v1/introspect", ClientID: "business", ClientSecret: f.secrets[1], HTTPClient: f.client})
	if err != nil {
		t.Fatal(err)
	}
	if result, err := client.Introspect(t.Context(), access); err != nil || !result.Active || result.Subject != pair["user_id"] {
		t.Fatal("remoteauth failed through external TLS termination")
	}
	delete(headers, "Authorization")
	headers["X-Request-Id"] = "gateway-refresh"
	raw, _ = json.Marshal(map[string]string{"grant_type": "refresh_token", "refresh_token": refresh})
	h, rotated := transportRequest(t, f.client, "POST", public.server.URL+"/v1/token", "application/json", string(raw), headers, 200)
	if h.Get("Cache-Control") != "no-store" || h.Get("X-Request-Id") != "gateway-refresh" {
		t.Fatal("OAuth response headers changed")
	}
	f.secrets = append(f.secrets, rotated["access_token"].(string), rotated["refresh_token"].(string))
	if public.transport.calls.Load() != 7 || internal.transport.calls.Load() != 5 {
		t.Fatal("proxy unexpectedly repeated an authentication or write request")
	}
	// Contrast a configured proxy's independently supplied chain with the
	// gateway's reconstructed chain and the untrusted direct request below.
	// The known forwarded IP is synthetic test data, not a spoofed identity.
	transportRequest(t, f.client, "POST", p.baseURL+"/v1/users:sendSignInCode", "application/json", `{"channel":"EMAIL","target":"trusted-chain@example.test"}`, map[string]string{"X-Request-Id": "trusted-chain", "X-Forwarded-For": "203.0.113.77"}, 200)
	m = f.smtp.mail(t)
	f.secrets = append(f.secrets, m.target, m.code)
	p.stop()
	f.assertAuditPrivate()
	var ip, device string
	if err := f.pool.QueryRow(t.Context(), "SELECT host(ip), device_id FROM audit_event WHERE request_id='gateway-signin' AND event_type=$1", enum.EventSignIn).Scan(&ip, &device); err != nil || ip != "127.0.0.1" || device != "gateway-device" {
		t.Fatal("forged forwarding header affected audit IP or device metadata")
	}
	if err := f.pool.QueryRow(t.Context(), "SELECT host(ip) FROM audit_event WHERE request_id='trusted-chain' AND event_type=$1", enum.EventCodeSent).Scan(&ip); err != nil || ip != "203.0.113.77" {
		t.Fatal("service ignored a configured trusted proxy chain")
	}
	f.assertSafe(public.logs.String())
	f.assertSafe(internal.logs.String())
	// The reverse proxy is trusted only through explicit service configuration.
	// With no trusted CIDR, an injected chain must fall back to the real peer.
	p = f.start(map[string]string{"ACCOUNTSVC_TLS_ENABLED": "false", "ACCOUNTSVC_TLS_CERT_FILE": "", "ACCOUNTSVC_TLS_KEY_FILE": ""})
	headers["X-Request-Id"] = "untrusted-direct"
	body = `{"channel":"EMAIL","target":"untrusted@example.test"}`
	transportRequest(t, f.client, "POST", p.baseURL+"/v1/users:sendSignInCode", "application/json", body, headers, 200)
	m = f.smtp.mail(t)
	f.secrets = append(f.secrets, m.target, m.code)
	p.stop()
	if err := f.pool.QueryRow(t.Context(), "SELECT host(ip) FROM audit_event WHERE request_id='untrusted-direct' AND event_type=$1", enum.EventCodeSent).Scan(&ip); err != nil || ip != "127.0.0.1" {
		t.Fatal("service trusted an unconfigured forwarding peer")
	}
	f.assertAuditPrivate()
	t.Run("caller_cancel", func(t *testing.T) { gatewayCancellation(t, false) })
	t.Run("proxy_exit", func(t *testing.T) { gatewayCancellation(t, true) })
}

// A blocking local upstream checks only the proxy fixture's cancellation and
// resource lifecycle; it does not assert SMTP delivery can be undone on cancel.
func gatewayCancellation(t *testing.T, stopProxy bool) {
	entered, canceled := make(chan struct{}), make(chan struct{})
	release := make(chan struct{})
	defer close(release) // Bound fixture cleanup even when an assertion fails.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-r.Context().Done():
			close(canceled)
		case <-release:
		}
	}))
	t.Cleanup(upstream.Close)
	cert, _, _ := integrationCertificate(t)
	g := newTestGateway(t, upstream.URL, cert, false)
	client := g.server.Client()
	t.Cleanup(client.CloseIdleConnections)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r, _ := http.NewRequestWithContext(ctx, "GET", g.server.URL+"/v1/users/me", nil)
	done := make(chan error, 1)
	go func() {
		resp, err := client.Do(r)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("proxy did not reach cancellation upstream")
	}
	if stopProxy {
		g.server.CloseClientConnections()
	} else {
		cancel()
	}
	select {
	case <-canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("proxy did not propagate cancellation")
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("interrupted request reported success")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("proxy client request did not terminate")
	}
	if g.transport.calls.Load() != 1 {
		t.Fatal("proxy retried interrupted request")
	}
}
