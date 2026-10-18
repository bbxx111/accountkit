package observability_test

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/observability"
	"github.com/bbxx111/accountkit/observability/internal/metricdata"
	oteladapter "github.com/bbxx111/accountkit/observability/otel"
	promadapter "github.com/bbxx111/accountkit/observability/prometheus"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/baggage"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	oteldata "go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/trace"
)

// This independent table pins the published contract, including every label.
var adapterContract = []struct {
	otel, prom, unit, labels string
	histogram                bool
}{
	{"accountkit.auth.operations", "accountkit_auth_operations_total", "{operation}", "operation,method,result,grant,mode,reason", false},
	{"accountkit.auth.operation.duration", "accountkit_auth_operation_duration_seconds", "s", "operation,method,result", true},
	{"accountkit.code.operations", "accountkit_code_operations_total", "{operation}", "operation,channel,purpose,result,reason", false},
	{"accountkit.code.operation.duration", "accountkit_code_operation_duration_seconds", "s", "operation,channel,purpose,result", true},
	{"accountkit.dependency.operations", "accountkit_dependency_operations_total", "{operation}", "component,operation,result", false},
	{"accountkit.dependency.operation.duration", "accountkit_dependency_operation_duration_seconds", "s", "component,operation,result", true},
	{"accountkit.degradations", "accountkit_degradations_total", "{event}", "scenario", false},
	{"accountkit.audit.events", "accountkit_audit_events_total", "{event}", "stage,result", false},
	{"accountkit.audit.omitted.fields", "accountkit_audit_omitted_fields_total", "{field}", "field", false},
	{"accountkit.maintenance.rounds", "accountkit_maintenance_rounds_total", "{round}", "result", false},
	{"accountkit.maintenance.runs", "accountkit_maintenance_runs_total", "{run}", "task,result", false},
	{"accountkit.maintenance.run.duration", "accountkit_maintenance_run_duration_seconds", "s", "task,result", true},
	{"accountkit.maintenance.items", "accountkit_maintenance_items_total", "{item}", "task", false},
}

var adapterBuckets = []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60}

func TestAdapterDescriptors(t *testing.T) {
	descriptors := metricdata.Descriptors()
	if len(descriptors) != len(adapterContract) {
		t.Fatalf("descriptor count = %d", len(descriptors))
	}
	for i, want := range adapterContract {
		got := descriptors[i]
		if got.ID != metricdata.ID(i) || got.OTelName != want.otel || got.PromName != want.prom || got.Unit != want.unit || got.Histogram != want.histogram || strings.Join(got.Labels, ",") != want.labels || got.Description == "" {
			t.Errorf("descriptor %d = %+v; want %+v", i, got, want)
		}
	}
	if !reflect.DeepEqual(metricdata.DefaultBuckets(), adapterBuckets) {
		t.Fatal("default buckets changed")
	}
}

type adapterPoint struct {
	Count   uint64
	Sum     float64
	Buckets []uint64 // finite cumulative buckets followed by +Inf
}

func syntheticObservations() []observability.Observation {
	return []observability.Observation{
		observability.AuthObservation{Operation: observability.AuthSignIn, Method: observability.MethodEmail, Result: observability.ResultSuccess, Grant: observability.GrantUser, Mode: observability.RefreshNone, Reason: observability.ReasonNone, Duration: 12500 * time.Microsecond},
		observability.CodeObservation{Operation: observability.CodeSend, Channel: observability.ChannelPhone, Purpose: observability.PurposeBind, Result: observability.ResultRejected, Reason: observability.ReasonCooldown, Duration: 25 * time.Millisecond},
		observability.DependencyObservation{Component: observability.ComponentGraceCache, Operation: observability.DependencyGet, Result: observability.ResultMiss, Duration: 125 * time.Millisecond},
		observability.DegradationObservation{Scenario: observability.DegradationAppleCachedKeyFallback, Count: 3},
		observability.AuditObservation{Kind: observability.AuditEvents, Stage: observability.AuditWrite, Result: observability.ResultWritten, Count: 2},
		observability.AuditObservation{Kind: observability.AuditOmittedFields, Field: observability.FieldClientIP, Count: 4},
		observability.MaintenanceObservation{Kind: observability.MaintenanceRound, Result: observability.ResultAcquired},
		observability.MaintenanceObservation{Kind: observability.MaintenanceRun, Task: observability.TaskCleanupSessions, Result: observability.ResultSuccess, Duration: 1500 * time.Millisecond},
		observability.MaintenanceObservation{Kind: observability.MaintenanceItems, Task: observability.TaskCleanupSessions, Count: 7},
	}
}

func privateContext(t *testing.T) context.Context {
	t.Helper()
	member, err := baggage.NewMember("private-request", "synthetic-secret@example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	bag, err := baggage.New(member)
	if err != nil {
		t.Fatal(err)
	}
	ctx := baggage.ContextWithBaggage(context.Background(), bag)
	return trace.ContextWithSpanContext(ctx, trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}, TraceFlags: trace.FlagsSampled,
	}))
}

func observeAll(ctx context.Context, o observability.Observer) {
	for _, event := range syntheticObservations() {
		o.Observe(ctx, event)
	}
}

func labelKey(t *testing.T, name string, labels map[string]string, instance string) string {
	t.Helper()
	var want []string
	for _, d := range adapterContract {
		if d.otel == name {
			want = strings.Split(d.labels, ",")
			break
		}
	}
	if len(want) == 0 {
		t.Fatalf("unexpected metric %q", name)
	}
	want = append(want, "accountkit_instance")
	sort.Strings(want)
	got := make([]string, 0, len(labels))
	for k := range labels {
		got = append(got, k)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) || labels["accountkit_instance"] != instance {
		t.Fatalf("%s labels = %v; want keys %v, instance %q", name, labels, want, instance)
	}
	parts := make([]string, 0, len(got))
	for _, k := range got {
		parts = append(parts, k+"="+labels[k])
	}
	return name + "|" + strings.Join(parts, ",")
}

func readOTel(t *testing.T, reader *sdkmetric.ManualReader, instance string, buckets []float64) map[string]adapterPoint {
	t.Helper()
	var rm oteldata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	out := make(map[string]adapterPoint)
	for _, scope := range rm.ScopeMetrics {
		if scope.Scope.Name != "github.com/bbxx111/accountkit/observability" {
			t.Fatalf("scope = %q", scope.Scope.Name)
		}
		for _, m := range scope.Metrics {
			var expectedUnit string
			for _, d := range adapterContract {
				if d.otel == m.Name {
					expectedUnit = d.unit
					break
				}
			}
			if m.Unit != expectedUnit || m.Description == "" {
				t.Fatalf("descriptor %s unit %q description %q", m.Name, m.Unit, m.Description)
			}
			switch data := m.Data.(type) {
			case oteldata.Sum[int64]:
				if !data.IsMonotonic || data.Temporality != oteldata.CumulativeTemporality {
					t.Fatal("counter is not cumulative and monotonic")
				}
				for _, p := range data.DataPoints {
					if len(p.Exemplars) != 0 {
						t.Fatal("request trace entered counter exemplars")
					}
					labels := make(map[string]string)
					for _, a := range p.Attributes.ToSlice() {
						labels[string(a.Key)] = a.Value.AsString()
					}
					if instance != "" && labels["accountkit_instance"] != instance {
						continue
					}
					out[labelKey(t, m.Name, labels, labels["accountkit_instance"])] = adapterPoint{Count: uint64(p.Value)}
				}
			case oteldata.Histogram[float64]:
				if data.Temporality != oteldata.CumulativeTemporality {
					t.Fatal("histogram is not cumulative")
				}
				for _, p := range data.DataPoints {
					if !reflect.DeepEqual(p.Bounds, buckets) {
						t.Fatalf("%s bounds = %v; want %v", m.Name, p.Bounds, buckets)
					}
					if len(p.Exemplars) != 0 {
						t.Fatal("request trace entered histogram exemplars")
					}
					labels := make(map[string]string)
					for _, a := range p.Attributes.ToSlice() {
						labels[string(a.Key)] = a.Value.AsString()
					}
					if instance != "" && labels["accountkit_instance"] != instance {
						continue
					}
					cumulative := make([]uint64, len(p.BucketCounts))
					var n uint64
					for i, count := range p.BucketCounts {
						n += count
						cumulative[i] = n
					}
					out[labelKey(t, m.Name, labels, labels["accountkit_instance"])] = adapterPoint{Count: p.Count, Sum: p.Sum, Buckets: cumulative}
				}
			default:
				t.Fatalf("unexpected aggregation %T", m.Data)
			}
		}
	}
	return out
}

func readPrometheus(t *testing.T, registry *prometheus.Registry, instance string) map[string]adapterPoint {
	t.Helper()
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]adapterPoint)
	for _, family := range families {
		var name string
		for _, d := range adapterContract {
			if d.prom == family.GetName() {
				name = d.otel
				break
			}
		}
		if name == "" {
			t.Fatalf("unexpected family %q", family.GetName())
		}
		if family.GetHelp() == "" {
			t.Fatal("empty help")
		}
		for _, m := range family.Metric {
			labels := make(map[string]string)
			for _, label := range m.Label {
				labels[label.GetName()] = label.GetValue()
			}
			if instance != "" && labels["accountkit_instance"] != instance {
				continue
			}
			key := labelKey(t, name, labels, labels["accountkit_instance"])
			if m.Counter != nil {
				if m.Counter.Exemplar != nil {
					t.Fatal("request context entered counter exemplar")
				}
				out[key] = adapterPoint{Count: uint64(m.Counter.GetValue())}
			} else if m.Histogram != nil {
				h := m.Histogram
				bounds := make([]float64, len(h.Bucket))
				counts := make([]uint64, 0, len(h.Bucket)+1)
				for i, b := range h.Bucket {
					bounds[i] = b.GetUpperBound()
					counts = append(counts, b.GetCumulativeCount())
					if b.Exemplar != nil {
						t.Fatal("request context entered histogram exemplar")
					}
				}
				if !reflect.DeepEqual(bounds, adapterBuckets) {
					t.Fatalf("prometheus buckets = %v", bounds)
				}
				counts = append(counts, h.GetSampleCount())
				out[key] = adapterPoint{Count: h.GetSampleCount(), Sum: h.GetSampleSum(), Buckets: counts}
			} else {
				t.Fatalf("unexpected metric type: %v", family.GetType())
			}
		}
	}
	return out
}

func assertAdapterPoints(t *testing.T, got, want map[string]adapterPoint) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("series count got %d want %d", len(got), len(want))
	}
	for key, w := range want {
		g, ok := got[key]
		if !ok || g.Count != w.Count || math.Abs(g.Sum-w.Sum) > 1e-9*math.Max(1, math.Abs(w.Sum)) || !reflect.DeepEqual(g.Buckets, w.Buckets) {
			t.Errorf("%s: got %+v want %+v", key, g, w)
		}
	}
}

func TestObserverMetrics(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	o, err := oteladapter.New(provider, "host-1")
	if err != nil {
		t.Fatal(err)
	}
	registry := prometheus.NewRegistry()
	p, err := promadapter.New(registry, "host-1")
	if err != nil {
		t.Fatal(err)
	}
	ctx := privateContext(t)
	observeAll(ctx, o)
	observeAll(ctx, p)
	a := readOTel(t, reader, "host-1", adapterBuckets)
	b := readPrometheus(t, registry, "host-1")
	if len(a) != 13 || len(b) != 13 {
		t.Fatalf("want all 13 metrics; otel=%d prom=%d", len(a), len(b))
	}
	assertAdapterPoints(t, a, b)
	expected := map[string]struct {
		count uint64
		sum   float64
	}{
		"accountkit.auth.operations|accountkit_instance=host-1,grant=user,method=email,mode=none,operation=sign_in,reason=none,result=success": {1, 0},
		"accountkit.auth.operation.duration|accountkit_instance=host-1,method=email,operation=sign_in,result=success":                          {1, .0125},
		"accountkit.code.operations|accountkit_instance=host-1,channel=phone,operation=send,purpose=bind,reason=cooldown,result=rejected":      {1, 0},
		"accountkit.code.operation.duration|accountkit_instance=host-1,channel=phone,operation=send,purpose=bind,result=rejected":              {1, .025},
		"accountkit.dependency.operations|accountkit_instance=host-1,component=grace_cache,operation=get,result=miss":                          {1, 0},
		"accountkit.dependency.operation.duration|accountkit_instance=host-1,component=grace_cache,operation=get,result=miss":                  {1, .125},
		"accountkit.degradations|accountkit_instance=host-1,scenario=apple_cached_key_fallback":                                                {3, 0},
		"accountkit.audit.events|accountkit_instance=host-1,result=written,stage=write":                                                        {2, 0},
		"accountkit.audit.omitted.fields|accountkit_instance=host-1,field=client_ip":                                                           {4, 0},
		"accountkit.maintenance.rounds|accountkit_instance=host-1,result=acquired":                                                             {1, 0},
		"accountkit.maintenance.runs|accountkit_instance=host-1,result=success,task=cleanup_sessions":                                          {1, 0},
		"accountkit.maintenance.run.duration|accountkit_instance=host-1,result=success,task=cleanup_sessions":                                  {1, 1.5},
		"accountkit.maintenance.items|accountkit_instance=host-1,task=cleanup_sessions":                                                        {7, 0},
	}
	for key, want := range expected {
		point, found := a[key]
		if !found || point.Count != want.count || point.Sum != want.sum {
			t.Errorf("%s: got %+v (found=%v), want %+v", key, point, found, want)
		}
	}
	// A second collection must retain cumulative totals without new events.
	assertAdapterPoints(t, readOTel(t, reader, "host-1", adapterBuckets), a)
	assertAdapterPoints(t, readPrometheus(t, registry, "host-1"), b)
	for key, point := range a {
		if strings.HasPrefix(key, "accountkit.auth.operation.duration|") && (point.Count != 1 || point.Sum != .0125) {
			t.Errorf("noninteger second measurement = %+v", point)
		}
	}
}

func TestAdaptersEquivalent(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	o, err := oteladapter.New(provider, "concurrent")
	if err != nil {
		t.Fatal(err)
	}
	registry := prometheus.NewRegistry()
	p, err := promadapter.New(registry, "concurrent")
	if err != nil {
		t.Fatal(err)
	}
	durations := []time.Duration{0, time.Millisecond, 5 * time.Millisecond, 10 * time.Millisecond, 25 * time.Millisecond, 50 * time.Millisecond, 100 * time.Millisecond, 250 * time.Millisecond, 500 * time.Millisecond, time.Second, 2500 * time.Millisecond, 5 * time.Second, 10 * time.Second, 30 * time.Second, 60 * time.Second, 70 * time.Second, 123456789 * time.Nanosecond}
	const workers, repeats = 8, 10
	var wg sync.WaitGroup
	// Collection races with recording, as in a host scrape/export cycle. Each
	// reader is local and performs no work against accountkit's dependencies.
	wg.Go(func() {
		for range repeats {
			readOTel(t, reader, "concurrent", adapterBuckets)
			readPrometheus(t, registry, "concurrent")
		}
	})
	for range workers {
		wg.Go(func() {
			for range repeats {
				observeAll(context.Background(), o)
				observeAll(context.Background(), p)
				for _, duration := range durations {
					e := observability.AuthObservation{Operation: observability.AuthAuthenticate, Method: observability.MethodNone, Result: observability.ResultSuccess, Grant: observability.GrantUser, Mode: observability.RefreshNone, Reason: observability.ReasonNone, Duration: duration}
					o.Observe(context.Background(), e)
					p.Observe(context.Background(), e)
				}
			}
		})
	}
	wg.Wait()
	a := readOTel(t, reader, "concurrent", adapterBuckets)
	assertAdapterPoints(t, a, readPrometheus(t, registry, "concurrent"))
	const key = "accountkit.auth.operation.duration|accountkit_instance=concurrent,method=none,operation=authenticate,result=success"
	point, found := a[key]
	if !found {
		t.Fatalf("missing authenticate histogram: %s", key)
	}
	if point.Count != workers*repeats*uint64(len(durations)) {
		t.Fatalf("concurrent count = %d", point.Count)
	}
	var sum float64
	wantBuckets := make([]uint64, len(adapterBuckets)+1)
	for _, d := range durations {
		sum += d.Seconds()
		for i, upper := range adapterBuckets {
			if d.Seconds() <= upper {
				wantBuckets[i] += workers * repeats
			}
		}
		wantBuckets[len(adapterBuckets)] += workers * repeats
	}
	if math.Abs(point.Sum-sum*workers*repeats) > 1e-8 || !reflect.DeepEqual(point.Buckets, wantBuckets) {
		t.Fatalf("bucket boundaries/overflow: got %+v, want sum %v buckets %v", point, sum*workers*repeats, wantBuckets)
	}
}

func TestAdapterInvalidObservations(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	o, err := oteladapter.New(provider, "privacy")
	if err != nil {
		t.Fatal(err)
	}
	registry := prometheus.NewRegistry()
	p, err := promadapter.New(registry, "privacy")
	if err != nil {
		t.Fatal(err)
	}
	for _, observer := range []observability.Observer{o, p} {
		ctx, cancel := context.WithCancel(privateContext(t))
		cancel()
		for _, event := range []observability.Observation{
			nil,
			(*observability.AuthObservation)(nil),
			observability.AuthObservation{Operation: observability.AuthSignIn, Result: observability.ResultSuccess, Duration: -time.Second},
			observability.DegradationObservation{Scenario: observability.DegradationAppleCachedKeyFallback, Count: -1},
			observability.DegradationObservation{Scenario: observability.DegradationAppleCachedKeyFallback, Count: 0},
			observability.AuditObservation{Kind: observability.AuditEvents, Stage: observability.AuditWrite, Result: observability.ResultWritten, Count: 0},
		} {
			observer.Observe(ctx, event)
		}
		// Private input categories collapse into a single bounded unknown/other
		// series. Already-canceled request context must not discard a fact.
		for i := range 20 {
			observer.Observe(ctx, observability.AuthObservation{Operation: observability.AuthSignIn, Method: observability.Method(fmt.Sprintf("private-%d@example.invalid", i)), Result: observability.ResultRejected, Reason: observability.Reason(fmt.Sprintf("private-token-%d", i))})
			observer.Observe(ctx, observability.MaintenanceObservation{Kind: observability.MaintenanceItems, Task: observability.Task(fmt.Sprintf("private-task-%d", i)), Count: 1})
		}
	}
	a := readOTel(t, reader, "privacy", adapterBuckets)
	assertAdapterPoints(t, a, readPrometheus(t, registry, "privacy"))
	if len(a) != 3 {
		t.Fatalf("invalid input created extra series: %v", a)
	}
	for key, point := range a {
		if strings.Contains(key, "private-") || strings.Contains(key, "example.invalid") || point.Count != 20 {
			t.Fatalf("private/unbounded classifications: %s %+v", key, point)
		}
		if strings.HasPrefix(key, "accountkit.auth.operations|") && (!strings.Contains(key, "method=unknown") || !strings.Contains(key, "reason=unknown")) {
			t.Fatal("unknown auth categories not normalized")
		}
		if strings.HasPrefix(key, "accountkit.maintenance.items|") && !strings.Contains(key, "task=other") {
			t.Fatal("custom maintenance name not normalized")
		}
	}
}

func TestObserverInstanceIsolation(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	registry := prometheus.NewRegistry()
	for i, instance := range []string{"instance.a", "instance_b"} {
		o, err := oteladapter.New(provider, instance)
		if err != nil {
			t.Fatal(err)
		}
		p, err := promadapter.New(registry, instance)
		if err != nil {
			t.Fatal(err)
		}
		for range i + 1 {
			observeAll(context.Background(), o)
			observeAll(context.Background(), p)
		}
	}
	for i, instance := range []string{"instance.a", "instance_b"} {
		a := readOTel(t, reader, instance, adapterBuckets)
		b := readPrometheus(t, registry, instance)
		assertAdapterPoints(t, a, b)
		if len(a) != 13 {
			t.Fatal("instance lost metrics")
		}
		for key, point := range a {
			if strings.HasPrefix(key, "accountkit.maintenance.items|") && point.Count != uint64(7*(i+1)) {
				t.Errorf("%s count=%d", key, point.Count)
			}
		}
	}
	if _, err := promadapter.New(registry, "instance.a"); err == nil {
		t.Fatal("duplicate instance registration accepted")
	}
	if got := len(readPrometheus(t, registry, "")); got != 26 {
		t.Fatal(fmt.Sprintf("duplicate registration changed existing data: %d", got))
	}
}
