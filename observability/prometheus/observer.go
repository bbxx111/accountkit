// Package prometheus records accountkit observations with a host Registerer.
// It never uses the default Registry or installs an HTTP handler.
package prometheus

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/bbxx111/accountkit/observability"
	"github.com/bbxx111/accountkit/observability/internal/metricdata"
	prom "github.com/prometheus/client_golang/prometheus"
)

// Observer records local counters and seconds histograms. It is safe for
// concurrent use. Successfully registered collectors belong to the host registry.
type Observer struct {
	counters   []*prom.CounterVec
	histograms []*prom.HistogramVec
}

var _ observability.Observer = (*Observer)(nil)

// New requires an explicit Registerer and a non-secret, stable instance matching
// [A-Za-z0-9][A-Za-z0-9._-]{0,63}. Reuse the Observer for the same instance;
// duplicate or incompatible registration returns an error. On failure, only
// collectors successfully registered by this invocation are unregistered.
// Successful collectors are retained for the host's registry lifecycle.
func New(registerer prom.Registerer, instance string) (*Observer, error) {
	if isNil(registerer) {
		return nil, errors.New("observability/prometheus: registerer is required")
	}
	if err := metricdata.ValidateInstance(instance); err != nil {
		return nil, err
	}
	descriptors := metricdata.Descriptors()
	o := &Observer{
		counters:   make([]*prom.CounterVec, len(descriptors)),
		histograms: make([]*prom.HistogramVec, len(descriptors)),
	}
	registered := make([]prom.Collector, 0, len(descriptors))
	for _, d := range descriptors {
		var collector prom.Collector
		if d.Histogram {
			h := prom.NewHistogramVec(prom.HistogramOpts{
				Name: d.PromName, Help: d.Description, ConstLabels: prom.Labels{"accountkit_instance": instance}, Buckets: metricdata.DefaultBuckets(),
			}, d.Labels)
			o.histograms[d.ID] = h
			collector = h
		} else {
			c := prom.NewCounterVec(prom.CounterOpts{
				Name: d.PromName, Help: d.Description, ConstLabels: prom.Labels{"accountkit_instance": instance},
			}, d.Labels)
			o.counters[d.ID] = c
			collector = c
		}
		if err := registerer.Register(collector); err != nil {
			for i := len(registered) - 1; i >= 0; i-- {
				registerer.Unregister(registered[i])
			}
			return nil, fmt.Errorf("observability/prometheus: register %s: %w", d.PromName, err)
		}
		registered = append(registered, collector)
	}
	return o, nil
}

// Observe ignores request context and never attaches trace exemplars or baggage.
func (o *Observer) Observe(_ context.Context, observation observability.Observation) {
	metricdata.Project(observation, func(m metricdata.Measurement) {
		labels := make([]string, len(m.Labels))
		for i, label := range m.Labels {
			labels[i] = label.Value
		}
		if histogram := o.histograms[m.ID]; histogram != nil {
			histogram.WithLabelValues(labels...).Observe(m.Value)
		} else {
			o.counters[m.ID].WithLabelValues(labels...).Add(float64(m.Count))
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
