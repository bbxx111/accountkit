package otel_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/observability"
	adapter "github.com/bbxx111/accountkit/observability/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestObserverConfiguration(t *testing.T) {
	var nilProvider *sdkmetric.MeterProvider
	for _, p := range []metric.MeterProvider{nil, nilProvider} {
		if got, err := adapter.New(p, "valid"); err == nil || got != nil {
			t.Fatal("nil provider accepted")
		}
	}
	for _, instance := range []string{"", " leading", "a/b", "a:b", "a@example.invalid", "secret\n", "中文", "_invalid", "-invalid", ".invalid", strings.Repeat("x", 65)} {
		if got, err := adapter.New(noop.NewMeterProvider(), instance); err == nil || got != nil {
			t.Fatalf("invalid instance %q accepted", instance)
		}
	}
	for _, instance := range []string{"a", "9", "a.B_c-9", strings.Repeat("X", 64)} {
		if _, err := adapter.New(noop.NewMeterProvider(), instance); err != nil {
			t.Fatalf("valid instance %q: %v", instance, err)
		}
	}
}

type failingProvider struct{ noop.MeterProvider }
type failingMeter struct{ noop.Meter }

func (failingProvider) Meter(string, ...metric.MeterOption) metric.Meter { return failingMeter{} }
func (failingMeter) Int64Counter(string, ...metric.Int64CounterOption) (metric.Int64Counter, error) {
	return nil, errors.New("counter unavailable")
}

func TestObserverInstrumentError(t *testing.T) {
	if o, err := adapter.New(failingProvider{}, "test"); err == nil || o != nil {
		t.Fatal("instrument failure accepted")
	}
}

type histogramFailureProvider struct{ noop.MeterProvider }
type histogramFailureMeter struct{ noop.Meter }

func (histogramFailureProvider) Meter(string, ...metric.MeterOption) metric.Meter {
	return histogramFailureMeter{}
}
func (histogramFailureMeter) Float64Histogram(string, ...metric.Float64HistogramOption) (metric.Float64Histogram, error) {
	return nil, errors.New("histogram unavailable")
}

type nilMeterProvider struct{ noop.MeterProvider }

func (nilMeterProvider) Meter(string, ...metric.MeterOption) metric.Meter { return nil }

func TestObserverHistogramError(t *testing.T) {
	if o, err := adapter.New(histogramFailureProvider{}, "test"); err == nil || o != nil {
		t.Fatal("histogram creation failure accepted")
	}
	if o, err := adapter.New(nilMeterProvider{}, "test"); err == nil || o != nil {
		t.Fatal("nil meter accepted")
	}
}

func TestObserverCustomView(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader), sdkmetric.WithView(sdkmetric.NewView(
		sdkmetric.Instrument{Name: "accountkit.auth.operation.duration"},
		sdkmetric.Stream{Aggregation: sdkmetric.AggregationExplicitBucketHistogram{Boundaries: []float64{.02, 1}}},
	)))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	o, err := adapter.New(provider, "custom")
	if err != nil {
		t.Fatal(err)
	}
	o.Observe(context.Background(), observability.AuthObservation{Operation: observability.AuthSignIn, Method: observability.MethodEmail, Result: observability.ResultSuccess, Grant: observability.GrantUser, Mode: observability.RefreshNone, Reason: observability.ReasonNone, Duration: 25 * time.Millisecond})
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	var seenCounter, seenHistogram bool
	for _, s := range rm.ScopeMetrics {
		for _, m := range s.Metrics {
			if m.Name == "accountkit.auth.operations" {
				data := m.Data.(metricdata.Sum[int64])
				seenCounter = len(data.DataPoints) == 1 && data.DataPoints[0].Value == 1 && m.Unit == "{operation}"
			}
			if m.Name == "accountkit.auth.operation.duration" {
				data := m.Data.(metricdata.Histogram[float64])
				p := data.DataPoints[0]
				seenHistogram = m.Unit == "s" && p.Count == 1 && p.Sum == .025 && reflect.DeepEqual(p.Bounds, []float64{.02, 1}) && reflect.DeepEqual(p.BucketCounts, []uint64{0, 1, 0})
			}
		}
	}
	if !seenCounter || !seenHistogram {
		t.Fatal("custom view changed units/counts or did not override buckets")
	}
}
