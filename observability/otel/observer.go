// Package otel records accountkit observations with a host-owned MeterProvider.
// The host configures aggregation, export, and the provider's lifecycle.
package otel

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/bbxx111/accountkit/observability"
	"github.com/bbxx111/accountkit/observability/internal/metricdata"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const instrumentationScope = "github.com/bbxx111/accountkit/observability"

// Observer projects typed observations into cumulative counters and histograms.
// It is safe for concurrent use and only records into local instruments.
type Observer struct {
	instance   attribute.KeyValue
	counters   []metric.Int64Counter
	histograms []metric.Float64Histogram
}

var _ observability.Observer = (*Observer)(nil)

// New requires an explicit provider and a non-secret, stable instance matching
// [A-Za-z0-9][A-Za-z0-9._-]{0,63}. It does not use a global provider or own the
// provider's lifecycle. Reuse the Observer for the same logical instance;
// repeated construction may aggregate into the same SDK instruments.
// Histograms suggest the shared seconds buckets; a host View can override them.
func New(provider metric.MeterProvider, instance string) (*Observer, error) {
	if isNil(provider) {
		return nil, errors.New("observability/otel: meter provider is required")
	}
	if err := metricdata.ValidateInstance(instance); err != nil {
		return nil, err
	}
	meter := provider.Meter(instrumentationScope)
	if isNil(meter) {
		return nil, errors.New("observability/otel: provider returned a nil meter")
	}
	descriptors := metricdata.Descriptors()
	o := &Observer{
		instance:   attribute.String("accountkit_instance", instance),
		counters:   make([]metric.Int64Counter, len(descriptors)),
		histograms: make([]metric.Float64Histogram, len(descriptors)),
	}
	for _, d := range descriptors {
		var err error
		if d.Histogram {
			o.histograms[d.ID], err = meter.Float64Histogram(d.OTelName,
				metric.WithDescription(d.Description), metric.WithUnit(d.Unit),
				metric.WithExplicitBucketBoundaries(metricdata.DefaultBuckets()...),
			)
		} else {
			o.counters[d.ID], err = meter.Int64Counter(d.OTelName,
				metric.WithDescription(d.Description), metric.WithUnit(d.Unit),
			)
		}
		if err != nil {
			return nil, fmt.Errorf("observability/otel: create %s: %w", d.OTelName, err)
		}
	}
	return o, nil
}

// Observe ignores the request context, so baggage, cancellation, request values,
// and sampled trace information cannot become attributes or trace exemplars.
func (o *Observer) Observe(_ context.Context, observation observability.Observation) {
	metricdata.Project(observation, func(m metricdata.Measurement) {
		attributes := make([]attribute.KeyValue, 0, len(m.Labels)+1)
		attributes = append(attributes, o.instance)
		for _, label := range m.Labels {
			attributes = append(attributes, attribute.String(label.Name, label.Value))
		}
		options := metric.WithAttributeSet(attribute.NewSet(attributes...))
		if histogram := o.histograms[m.ID]; histogram != nil {
			histogram.Record(context.Background(), m.Value, options)
		} else {
			o.counters[m.ID].Add(context.Background(), m.Count, options)
		}
	})
}

func isNil(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}
