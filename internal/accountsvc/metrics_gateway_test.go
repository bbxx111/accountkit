package accountsvc_test

import (
	"context"
	"crypto/tls"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"testing"
)

// The deployment contract requires two independent boundaries: the public
// allowlist omits metrics, and the monitoring gateway checks the actual peer.
func TestServiceIntegrationMetricsGateway(t *testing.T) {
	f := newIntegration(t)
	p := f.start(map[string]string{
		"ACCOUNTSVC_TLS_ENABLED": "false", "ACCOUNTSVC_TLS_CERT_FILE": "", "ACCOUNTSVC_TLS_KEY_FILE": "",
		"ACCOUNTSVC_METRICS_ENABLED": "true", "ACCOUNTSVC_METRICS_INSTANCE": "gateway.metrics-1",
	})
	pair := f.login(p, "gateway-metrics-private@example.test")
	public := newTestGateway(t, p.baseURL, f.cert, false)
	for _, method := range []string{"GET", "POST"} {
		transportRequest(t, f.client, method, public.server.URL+"/metrics", "", "", nil, http.StatusNotFound)
	}
	if public.transport.calls.Load() != 0 {
		t.Fatal("public metrics request reached accountsvc")
	}

	monitor := newMetricsMonitoringGateway(t, p.baseURL, f.cert)
	// Use a real TCP peer outside the monitoring allowlist. Forged forwarding
	// headers must not turn that peer into an authorized monitoring source.
	deniedTransport := f.client.Transport.(*http.Transport).Clone()
	dialer := &net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP("127.0.0.2")}}
	deniedTransport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, address)
	}
	deniedClient := &http.Client{Transport: deniedTransport, Timeout: f.client.Timeout}
	t.Cleanup(deniedClient.CloseIdleConnections)
	transportRequest(t, deniedClient, "GET", monitor.server.URL+"/metrics", "", "", map[string]string{
		"X-Forwarded-For": "127.0.0.1", "Forwarded": "for=127.0.0.1",
	}, http.StatusForbidden)
	if monitor.transport.calls.Load() != 0 {
		t.Fatal("unapproved monitoring peer reached accountsvc")
	}
	families := scrapeServiceMetrics(t, f, monitor.server.URL+"/metrics", "gateway.metrics-1", pair["user_id"].(string))
	metric := requireServiceMetric(t, families, "accountkit_auth_operations_total", map[string]string{
		"operation": "sign_in", "method": "email", "result": "success", "grant": "user", "mode": "none", "reason": "none",
	})
	if metric.GetCounter().GetValue() != 1 || monitor.transport.calls.Load() != 1 {
		t.Fatal("approved monitoring scrape did not reach the service exactly once")
	}
	transportRequest(t, f.client, "GET", monitor.server.URL+"/admin/v1/users", "", "", nil, http.StatusNotFound)
	if monitor.transport.calls.Load() != 1 {
		t.Fatal("monitoring gateway forwarded a non-metrics route")
	}
	p.stop()
	f.assertAuditPrivate()
	f.assertSafe(public.logs.String())
	f.assertSafe(monitor.logs.String())
}

// This is a local contract fixture, not a production gateway configuration.
// The separate monitoring listener represents a restricted monitoring network;
// it permits only GET /metrics from its configured real-peer allowlist.
func newMetricsMonitoringGateway(t *testing.T, upstream string, cert tls.Certificate) *testGateway {
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
			r.SetXForwarded()
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, "upstream unavailable", http.StatusBadGateway)
		},
	}
	allowed := netip.MustParsePrefix("127.0.0.1/32")
	g.server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peer, err := netip.ParseAddrPort(r.RemoteAddr)
		if err != nil || !allowed.Contains(peer.Addr()) {
			http.Error(w, "monitoring source required", http.StatusForbidden)
			return
		}
		if r.URL.Path != "/metrics" || r.Method != http.MethodGet {
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
