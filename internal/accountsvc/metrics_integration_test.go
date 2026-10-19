package accountsvc_test

import (
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"testing"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

// This catches missing service-level observer injection: real email delivery,
// sign-in and introspection must reach the registry exposed by that process.
func TestServiceIntegrationMetrics(t *testing.T) {
	f := newIntegration(t)
	p := f.start(nil)
	transportRequest(t, f.client, "GET", p.baseURL+"/metrics", "", "", nil, http.StatusNotFound)
	p.stop()

	proxy := newRedisProxy(t, f.redisURL)
	redisURL, err := url.Parse(f.redisURL)
	if err != nil {
		t.Fatal(err)
	}
	redisURL.Host = proxy.ln.Addr().String()
	p = f.start(map[string]string{
		"ACCOUNTSVC_METRICS_ENABLED": "true", "ACCOUNTSVC_METRICS_INSTANCE": "integration.metrics-1",
		"ACCOUNTSVC_REDIS_URL": redisURL.String(),
	})
	f.call("GET", p.baseURL+"/healthz", "", nil, http.StatusOK)
	f.call("GET", p.baseURL+"/readyz", "", nil, http.StatusOK)
	pair := f.login(p, "metrics-private@example.test")
	userID := pair["user_id"].(string)
	access := pair["access_token"].(string)
	f.introspect(p, access, true)
	families := scrapeServiceMetrics(t, f, p.baseURL+"/metrics", "integration.metrics-1", userID)
	for _, labels := range []map[string]string{
		{"operation": "sign_in", "method": "email", "result": "success", "grant": "user", "mode": "none", "reason": "none"},
		{"operation": "authenticate", "method": "none", "result": "success", "grant": "user", "mode": "none", "reason": "none"},
	} {
		metric := requireServiceMetric(t, families, "accountkit_auth_operations_total", labels)
		if metric.GetCounter().GetValue() != 1 {
			t.Fatalf("%s count = %v, want 1", labels["operation"], metric.GetCounter().GetValue())
		}
		requireServiceDuration(t, families, "accountkit_auth_operation_duration_seconds", map[string]string{
			"operation": labels["operation"], "method": labels["method"], "result": "success",
		})
	}
	for _, operation := range []string{"send", "verify"} {
		metric := requireServiceMetric(t, families, "accountkit_code_operations_total", map[string]string{
			"operation": operation, "channel": "email", "purpose": "sign_in", "result": "success", "reason": "none",
		})
		if metric.GetCounter().GetValue() != 1 {
			t.Fatalf("code %s count = %v, want 1", operation, metric.GetCounter().GetValue())
		}
		requireServiceDuration(t, families, "accountkit_code_operation_duration_seconds", map[string]string{
			"operation": operation, "channel": "email", "purpose": "sign_in", "result": "success",
		})
	}
	senderLabels := map[string]string{"component": "sender", "operation": "email", "result": "success"}
	if requireServiceMetric(t, families, "accountkit_dependency_operations_total", senderLabels).GetCounter().GetValue() != 1 {
		t.Fatal("successful SMTP delivery missing from dependency metrics")
	}
	requireServiceDuration(t, families, "accountkit_dependency_operation_duration_seconds", senderLabels)
	transportRequest(t, f.client, "POST", p.baseURL+"/metrics", "", "", nil, http.StatusMethodNotAllowed)

	// Readiness can fail while unauthenticated monitoring still reads local
	// aggregates; scraping must not itself add dependency observations.
	proxy.fail(true)
	f.call("GET", p.baseURL+"/readyz", "", nil, http.StatusServiceUnavailable)
	f.call("GET", p.baseURL+"/healthz", "", nil, http.StatusOK)
	after := scrapeServiceMetrics(t, f, p.baseURL+"/metrics", "integration.metrics-1", userID)
	for _, metric := range families["accountkit_dependency_operations_total"].GetMetric() {
		labels := serviceMetricLabels(metric)
		delete(labels, "accountkit_instance")
		got := requireServiceMetric(t, after, "accountkit_dependency_operations_total", labels)
		if got.GetCounter().GetValue() != metric.GetCounter().GetValue() {
			t.Fatal("metrics scrape or readiness added a logical dependency call")
		}
	}
	proxy.fail(false)
	f.call("GET", p.baseURL+"/readyz", "", nil, http.StatusOK)
	p.stop()
	f.assertAuditPrivate()

	// Rolling back to the default removes the route and retains login state.
	p = f.start(nil)
	transportRequest(t, f.client, "GET", p.baseURL+"/metrics", "", "", nil, http.StatusNotFound)
	f.introspect(p, access, true)
	p.stop()
}

func scrapeServiceMetrics(t *testing.T, f *integrationFixture, address, instance string, privateValues ...string) map[string]*dto.MetricFamily {
	t.Helper()
	resp, err := f.client.Get(address)
	if err != nil {
		t.Fatal("metrics transport failed")
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("metrics status=%d, read error=%v", resp.StatusCode, err)
	}
	f.assertSafe(string(raw))
	for _, value := range privateValues {
		if value != "" && strings.Contains(string(raw), value) {
			t.Fatal("metrics response exposed a private identity")
		}
	}
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("metrics response permits caching")
	}
	parser := expfmt.NewTextParser(model.LegacyValidation)
	families, err := parser.TextToMetricFamilies(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal("metrics response is not valid Prometheus exposition")
	}
	for name, family := range families {
		if !strings.HasPrefix(name, "accountkit_") {
			t.Fatalf("unexpected runtime or HTTP collector %s", name)
		}
		for _, metric := range family.GetMetric() {
			if serviceMetricLabels(metric)["accountkit_instance"] != instance {
				t.Fatal("metrics missing configured instance label")
			}
			for _, label := range metric.GetLabel() {
				switch label.GetName() {
				case "user_id", "session_id", "identity_id", "email", "target", "token", "device_id", "request_id":
					t.Fatal("metrics exposes a private or unbounded label")
				}
			}
		}
	}
	return families
}

func serviceMetricLabels(metric *dto.Metric) map[string]string {
	labels := make(map[string]string, len(metric.GetLabel()))
	for _, label := range metric.GetLabel() {
		labels[label.GetName()] = label.GetValue()
	}
	return labels
}

func requireServiceMetric(t *testing.T, families map[string]*dto.MetricFamily, name string, want map[string]string) *dto.Metric {
	t.Helper()
	for _, metric := range families[name].GetMetric() {
		labels := serviceMetricLabels(metric)
		if len(labels) != len(want)+1 {
			continue
		}
		matches := true
		for key, value := range want {
			if labels[key] != value {
				matches = false
			}
		}
		if matches {
			return metric
		}
	}
	t.Fatalf("missing %s with expected finite labels %v", name, want)
	return nil
}

func requireServiceDuration(t *testing.T, families map[string]*dto.MetricFamily, name string, labels map[string]string) {
	t.Helper()
	histogram := requireServiceMetric(t, families, name, labels).GetHistogram()
	if histogram.GetSampleCount() != 1 || histogram.GetSampleSum() <= 0 || math.IsNaN(histogram.GetSampleSum()) || math.IsInf(histogram.GetSampleSum(), 0) {
		t.Fatalf("%s has invalid operation duration/count", name)
	}
}
