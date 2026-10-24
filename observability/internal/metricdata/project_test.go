package metricdata_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	o "github.com/bbxx111/accountkit/observability"
	"github.com/bbxx111/accountkit/observability/internal/metricdata"
)

func collect(in o.Observation) []metricdata.Measurement {
	var out []metricdata.Measurement
	metricdata.Project(in, func(m metricdata.Measurement) { out = append(out, m) })
	return out
}

func TestAlipayMetricProjection(t *testing.T) {
	for _, tc := range []struct {
		in           o.Observation
		label, value string
		count        int
	}{
		{o.AuthObservation{Operation: o.AuthSignIn, Method: o.MethodAlipay, Result: o.ResultSuccess, Grant: o.GrantUser}, "method", "alipay", 2},
		{o.DependencyObservation{Component: o.ComponentIDP, Operation: o.DependencyVerifyAlipay, Result: o.ResultSuccess}, "operation", "verify_alipay", 2},
		{o.DegradationObservation{Scenario: o.DegradationAlipayProfileUnavailable, Count: 1}, "scenario", "alipay_profile_unavailable", 1},
	} {
		ms := collect(tc.in)
		if len(ms) != tc.count {
			t.Fatalf("Alipay projection %T count=%d", tc.in, len(ms))
		}
		for _, m := range ms {
			found := false
			for _, l := range m.Labels {
				if l.Name == tc.label {
					found = true
					if l.Value != tc.value {
						t.Fatalf("Alipay label=%+v", l)
					}
				}
			}
			if !found {
				t.Fatal("missing label")
			}
		}
	}
	for _, op := range []o.AuthOperation{o.AuthCompleteSignIn, o.AuthReauthenticate} {
		if ms := collect(o.AuthObservation{Operation: op, Method: o.MethodAlipay, Result: o.ResultSuccess, Grant: o.GrantUser}); len(ms) != 0 {
			t.Fatal("Alipay accepted for anchor-only operation")
		}
	}
}

func TestQQMetricProjection(t *testing.T) {
	for _, tc := range []struct {
		in           o.Observation
		label, value string
		count        int
	}{
		{o.AuthObservation{Operation: o.AuthSignIn, Method: o.MethodQQ, Result: o.ResultSuccess, Grant: o.GrantUser}, "method", "qq", 2},
		{o.DependencyObservation{Component: o.ComponentIDP, Operation: o.DependencyVerifyQQ, Result: o.ResultSuccess}, "operation", "verify_qq", 2},
		{o.DegradationObservation{Scenario: o.DegradationQQProfileUnavailable, Count: 1}, "scenario", "qq_profile_unavailable", 1},
	} {
		ms := collect(tc.in)
		if len(ms) != tc.count {
			t.Fatalf("QQ projection %T count=%d", tc.in, len(ms))
		}
		for _, m := range ms {
			found := false
			for _, l := range m.Labels {
				if l.Name == tc.label {
					found = true
					if l.Value != tc.value {
						t.Fatalf("QQ label=%+v", l)
					}
				}
			}
			if !found {
				t.Fatal("missing label")
			}
		}
	}
	for _, op := range []o.AuthOperation{o.AuthCompleteSignIn, o.AuthReauthenticate} {
		if ms := collect(o.AuthObservation{Operation: op, Method: o.MethodQQ, Result: o.ResultSuccess, Grant: o.GrantUser}); len(ms) != 0 {
			t.Fatal("QQ accepted for anchor-only operation")
		}
	}
}

func TestWeiboMetricProjection(t *testing.T) {
	for _, tc := range []struct {
		in           o.Observation
		label, value string
		count        int
	}{
		{o.AuthObservation{Operation: o.AuthSignIn, Method: o.MethodWeibo, Result: o.ResultSuccess, Grant: o.GrantUser}, "method", "weibo", 2},
		{o.DependencyObservation{Component: o.ComponentIDP, Operation: o.DependencyVerifyWeibo, Result: o.ResultSuccess}, "operation", "verify_weibo", 2},
		{o.DegradationObservation{Scenario: o.DegradationWeiboProfileUnavailable, Count: 1}, "scenario", "weibo_profile_unavailable", 1},
	} {
		ms := collect(tc.in)
		if len(ms) != tc.count {
			t.Fatalf("Weibo projection %T count=%d", tc.in, len(ms))
		}
		for _, m := range ms {
			found := false
			for _, l := range m.Labels {
				if l.Name == tc.label {
					found = true
					if l.Value != tc.value {
						t.Fatalf("Weibo label=%+v", l)
					}
				}
			}
			if !found {
				t.Fatal("missing label")
			}
		}
	}
	for _, op := range []o.AuthOperation{o.AuthCompleteSignIn, o.AuthReauthenticate} {
		if ms := collect(o.AuthObservation{Operation: op, Method: o.MethodWeibo, Result: o.ResultSuccess, Grant: o.GrantUser}); len(ms) != 0 {
			t.Fatal("Weibo accepted for anchor-only operation")
		}
	}
}

func TestMetricProjection(t *testing.T) {
	cases := []struct {
		in      o.Observation
		names   []string
		count   int64
		seconds float64
	}{
		{o.AuthObservation{Operation: o.AuthSignIn, Method: o.MethodPhone, Result: o.ResultSuccess, Grant: o.GrantUser, Duration: 1500 * time.Millisecond}, []string{"accountkit.auth.operations", "accountkit.auth.operation.duration"}, 1, 1.5},
		{o.CodeObservation{Operation: o.CodeSend, Channel: o.ChannelEmail, Purpose: o.PurposeBind, Result: o.ResultRejected, Reason: o.ReasonCooldown}, []string{"accountkit.code.operations", "accountkit.code.operation.duration"}, 1, 0},
		{o.DependencyObservation{Component: o.ComponentGraceCache, Operation: o.DependencyGet, Result: o.ResultMiss, Duration: time.Millisecond}, []string{"accountkit.dependency.operations", "accountkit.dependency.operation.duration"}, 1, .001},
		{o.DegradationObservation{Scenario: o.DegradationRevocationCheckFailOpen, Count: 2}, []string{"accountkit.degradations"}, 2, 0},
		{o.AuditObservation{Kind: o.AuditEvents, Stage: o.AuditWrite, Result: o.ResultDuplicate, Count: 3}, []string{"accountkit.audit.events"}, 3, 0},
		{o.AuditObservation{Kind: o.AuditOmittedFields, Field: o.FieldClientIP, Count: 4}, []string{"accountkit.audit.omitted.fields"}, 4, 0},
		{o.MaintenanceObservation{Kind: o.MaintenanceRound, Result: o.ResultSkipped}, []string{"accountkit.maintenance.rounds"}, 1, 0},
		{o.MaintenanceObservation{Kind: o.MaintenanceRun, Task: o.TaskPurgeUsers, Result: o.ResultPanic, Duration: 2 * time.Second}, []string{"accountkit.maintenance.runs", "accountkit.maintenance.run.duration"}, 1, 2},
		{o.MaintenanceObservation{Kind: o.MaintenanceItems, Task: o.TaskPurgeUsers, Count: 7}, []string{"accountkit.maintenance.items"}, 7, 0},
	}
	descriptors := metricdata.Descriptors()
	if len(descriptors) != 13 {
		t.Fatalf("descriptors: %d", len(descriptors))
	}
	byID := map[metricdata.ID]metricdata.Descriptor{}
	for _, d := range descriptors {
		byID[d.ID] = d
	}
	seen := map[string]bool{}
	for _, tc := range cases {
		got := collect(tc.in)
		var names []string
		for _, m := range got {
			d := byID[m.ID]
			names = append(names, d.OTelName)
			seen[d.OTelName] = true
			if d.Histogram {
				if m.Value != tc.seconds {
					t.Fatalf("duration: %+v", m)
				}
			} else if m.Count != tc.count {
				t.Fatalf("count: %+v", m)
			}
			var labels []string
			for _, l := range m.Labels {
				labels = append(labels, l.Name)
			}
			if !reflect.DeepEqual(labels, d.Labels) {
				t.Fatalf("labels %v != %v", labels, d.Labels)
			}
			if d.Histogram {
				for _, l := range labels {
					if l == "reason" {
						t.Fatal("reason in duration labels")
					}
				}
			}
		}
		if !reflect.DeepEqual(names, tc.names) {
			t.Fatalf("projection %T: %v", tc.in, names)
		}
	}
	if len(seen) != 13 {
		t.Fatalf("missing projections: %v", seen)
	}
	// Descriptor mutation must not change future adapters or projection labels.
	descriptors[0].Labels[0] = "secret"
	descriptors[0].OTelName = "secret"
	if metricdata.Descriptors()[0].OTelName == "secret" || metricdata.Descriptors()[0].Labels[0] == "secret" {
		t.Fatal("mutable global descriptor")
	}
	buckets := metricdata.DefaultBuckets()
	buckets[0] = 999
	if metricdata.DefaultBuckets()[0] != .001 {
		t.Fatal("mutable global buckets")
	}
}

func TestMetricProjectionRejectsAndNormalizes(t *testing.T) {
	for _, in := range []o.Observation{
		nil, (*o.AuthObservation)(nil), &o.AuthObservation{Operation: o.AuthSignIn, Result: o.ResultSuccess},
		o.AuthObservation{Operation: o.AuthSignIn, Result: o.ResultSuccess, Duration: -1},
		o.CodeObservation{Operation: o.CodeSend, Result: o.ResultSuccess, Duration: -1},
		o.DependencyObservation{Component: o.ComponentGraceCache, Operation: o.DependencyIssue, Result: o.ResultSuccess},
		o.DegradationObservation{Scenario: o.DegradationRevocationCheckFailOpen, Count: -1},
		o.DegradationObservation{Scenario: o.DegradationScenario("secret"), Count: 1},
		o.AuditObservation{Kind: o.AuditEvents, Stage: o.AuditMapping, Result: o.ResultWritten, Count: 1},
		o.AuditObservation{Kind: o.AuditOmittedFields, Count: -1},
		o.MaintenanceObservation{Kind: o.MaintenanceRun, Result: o.ResultAcquired},
		o.MaintenanceObservation{Kind: o.MaintenanceItems, Count: -1},
		o.AuthObservation{Operation: o.AuthRefresh, Result: o.ResultPending},
		o.AuthObservation{Operation: o.AuthSignIn, Result: o.ResultRejected, Grant: o.GrantUser},
		o.AuthObservation{Operation: o.AuthSignIn, Result: o.ResultSuccess, Mode: o.RefreshRotation},
	} {
		if got := collect(in); len(got) != 0 {
			t.Errorf("accepted invalid %#+v: %+v", in, got)
		}
	}
	for i := range 100 {
		secret := fmt.Sprintf("secret-%d@example.com", i)
		for _, in := range []o.Observation{
			o.AuthObservation{Operation: o.AuthSignIn, Method: o.Method(secret), Result: o.ResultRejected, Reason: o.Reason(secret)},
			o.CodeObservation{Operation: o.CodeSend, Channel: o.Channel(secret), Purpose: o.Purpose(secret), Result: o.ResultRejected, Reason: o.Reason(secret)},
			o.AuditObservation{Kind: o.AuditOmittedFields, Field: o.AuditField(secret), Count: 1},
			o.MaintenanceObservation{Kind: o.MaintenanceRun, Task: o.Task(secret), Result: o.ResultSuccess},
		} {
			got := collect(in)
			if len(got) == 0 {
				t.Fatalf("normalizable value rejected: %+v", in)
			}
			if strings.Contains(fmt.Sprint(got), "secret") {
				t.Fatalf("raw classification leaked: %+v", got)
			}
		}
	}
	first := collect(o.AuthObservation{Operation: o.AuthSignIn, Result: o.ResultSuccess})
	first[0].Labels[0].Value = "secret"
	if strings.Contains(fmt.Sprint(collect(o.AuthObservation{Operation: o.AuthSignIn, Result: o.ResultSuccess})), "secret") {
		t.Fatal("measurement aliases global storage")
	}
	if len(collect(o.AuditObservation{Kind: o.AuditEvents, Stage: o.AuditWrite, Result: o.ResultWritten, Count: 0})) != 0 {
		t.Fatal("zero count emitted")
	}
}

func TestMetricProjectionDependencyOutcomes(t *testing.T) {
	// Cache misses are meaningful only for get; credential rejection is meaningful
	// only at challenge admission/verification or IdP verification boundaries.
	for _, in := range []o.DependencyObservation{
		{Component: o.ComponentGraceCache, Operation: o.DependencyPut, Result: o.ResultMiss},
		{Component: o.ComponentRevocation, Operation: o.DependencyCheck, Result: o.ResultRejected},
		{Component: o.ComponentSender, Operation: o.DependencySMS, Result: o.ResultRejected},
		{Component: o.ComponentCodeStore, Operation: o.DependencyDiscard, Result: o.ResultRejected},
	} {
		if got := collect(in); len(got) != 0 {
			t.Errorf("impossible dependency result accepted: %+v", in)
		}
	}
	for _, in := range []o.DependencyObservation{
		{Component: o.ComponentCodeStore, Operation: o.DependencyIssue, Result: o.ResultRejected},
		{Component: o.ComponentCodeStore, Operation: o.DependencyVerify, Result: o.ResultRejected},
		{Component: o.ComponentIDP, Operation: o.DependencyVerifyApple, Result: o.ResultRejected},
	} {
		if len(collect(in)) != 2 {
			t.Errorf("valid rejection ignored: %+v", in)
		}
	}
}

func TestMetricProjectionLabelValuesAndOwnership(t *testing.T) {
	got := collect(o.AuthObservation{Operation: o.AuthSignIn, Method: o.MethodEmail, Result: o.ResultSuccess, Grant: o.GrantBind})
	want := []metricdata.Label{{Name: "operation", Value: "sign_in"}, {Name: "method", Value: "email"}, {Name: "result", Value: "success"}, {Name: "grant", Value: "bind"}, {Name: "mode", Value: "none"}, {Name: "reason", Value: "none"}}
	if !reflect.DeepEqual(got[0].Labels, want) {
		t.Fatalf("auth labels: %+v", got[0].Labels)
	}
	got = collect(o.AuthObservation{Operation: o.AuthSignIn, Method: "private-method", Result: o.ResultRejected, Reason: "private-reason"})
	if got[0].Labels[1].Value != "unknown" || got[0].Labels[5].Value != "unknown" {
		t.Fatalf("unknown normalization: %+v", got)
	}
	got = collect(o.MaintenanceObservation{Kind: o.MaintenanceRun, Task: "private-host-task", Result: o.ResultSuccess})
	if got[0].Labels[0].Value != "other" {
		t.Fatalf("host task normalization: %+v", got)
	}
	metricdata.Project(o.AuthObservation{Operation: o.AuthRefresh, Result: o.ResultSuccess, Grant: o.GrantUser, Mode: o.RefreshGrace, Duration: time.Second}, func(m metricdata.Measurement) {
		if m.ID == metricdata.AuthOperations {
			m.Labels[0].Value = "mutated"
		} else if m.Labels[0].Value != "refresh" {
			t.Fatal("counter callback corrupted histogram labels")
		}
	})
}

func TestMetricProjectionProviderValues(t *testing.T) {
	for method, want := range map[o.Method]string{o.MethodOIDC: "oidc", o.MethodProvider: "provider"} {
		got := collect(o.AuthObservation{Operation: o.AuthSignIn, Method: method, Result: o.ResultSuccess, Grant: o.GrantUser})
		if len(got) == 0 || got[0].Labels[1].Value != want {
			t.Fatalf("method %s: %+v", method, got)
		}
		if got := collect(o.AuthObservation{Operation: o.AuthReauthenticate, Method: method, Result: o.ResultSuccess, Grant: o.GrantUser}); len(got) != 0 {
			t.Fatalf("third-party method %s must not reauthenticate: %+v", method, got)
		}
	}
	for _, op := range []o.DependencyOperation{o.DependencyVerifyOIDC, o.DependencyVerifyProvider} {
		if len(collect(o.DependencyObservation{Component: o.ComponentIDP, Operation: op, Result: o.ResultRejected})) != 2 {
			t.Fatalf("dependency %s rejected", op)
		}
	}
	if len(collect(o.DegradationObservation{Scenario: o.DegradationOIDCCachedKeyFallback, Count: 1})) == 0 {
		t.Fatal("OIDC cached-key degradation rejected")
	}
}

func TestMetricProjectionFacebookValues(t *testing.T) {
	if o.MethodFacebook != "facebook" || o.DependencyVerifyFacebook != "verify_facebook" || o.DegradationFacebookCachedKeyFallback != "facebook_cached_key_fallback" {
		t.Fatal("facebook observability constants drifted")
	}
	got := collect(o.AuthObservation{Operation: o.AuthSignIn, Method: o.MethodFacebook, Result: o.ResultSuccess, Grant: o.GrantUser})
	if len(got) == 0 || got[0].Labels[1].Value != "facebook" {
		t.Fatalf("method facebook: %+v", got)
	}
	if got := collect(o.AuthObservation{Operation: o.AuthReauthenticate, Method: o.MethodFacebook, Result: o.ResultSuccess, Grant: o.GrantUser}); len(got) != 0 {
		t.Fatalf("facebook must not reauthenticate: %+v", got)
	}
	got = collect(o.DependencyObservation{Component: o.ComponentIDP, Operation: o.DependencyVerifyFacebook, Result: o.ResultRejected})
	if len(got) != 2 {
		t.Fatalf("dependency facebook: %+v", got)
	}
	for _, m := range got {
		for _, l := range m.Labels {
			if l.Name == "operation" && l.Value != "verify_facebook" {
				t.Fatalf("operation label: %+v", m)
			}
		}
	}
	got = collect(o.DegradationObservation{Scenario: o.DegradationFacebookCachedKeyFallback, Count: 1})
	if len(got) != 1 || got[0].Labels[0].Value != "facebook_cached_key_fallback" {
		t.Fatalf("degradation facebook: %+v", got)
	}
}
