package prometheus_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bbxx111/accountkit/observability"
	"github.com/bbxx111/accountkit/observability/internal/metricdata"
	adapter "github.com/bbxx111/accountkit/observability/prometheus"
	"github.com/prometheus/client_golang/prometheus"
)

func TestObserverConfiguration(t *testing.T) {
	var nilRegistry *prometheus.Registry
	for _, r := range []prometheus.Registerer{nil, nilRegistry} {
		if got, err := adapter.New(r, "valid"); err == nil || got != nil {
			t.Fatal("nil registerer accepted")
		}
	}
	for _, instance := range []string{"", " leading", "a/b", "a:b", "a@example.invalid", "secret\n", "中文", "_invalid", "-invalid", ".invalid", strings.Repeat("x", 65)} {
		if got, err := adapter.New(prometheus.NewRegistry(), instance); err == nil || got != nil {
			t.Fatalf("invalid instance %q accepted", instance)
		}
	}
	for _, instance := range []string{"a", "9", "a.B_c-9", strings.Repeat("X", 64)} {
		if _, err := adapter.New(prometheus.NewRegistry(), instance); err != nil {
			t.Fatalf("valid instance %q: %v", instance, err)
		}
	}
}

type trackingRegisterer struct {
	prometheus.Registerer
	registered, unregistered []prometheus.Collector
	failAt                   int
}

func (r *trackingRegisterer) Register(c prometheus.Collector) error {
	if r.failAt > 0 && len(r.registered) == r.failAt {
		return errors.New("synthetic registration failure")
	}
	if err := r.Registerer.Register(c); err != nil {
		return err
	}
	r.registered = append(r.registered, c)
	return nil
}
func (r *trackingRegisterer) Unregister(c prometheus.Collector) bool {
	r.unregistered = append(r.unregistered, c)
	return r.Registerer.Unregister(c)
}

func TestRegistrationRollback(t *testing.T) {
	for _, mode := range []string{"registerer_error", "existing_collector", "incompatible_descriptor"} {
		t.Run(mode, func(t *testing.T) {
			registry := prometheus.NewRegistry()
			host := prometheus.NewCounter(prometheus.CounterOpts{Name: "host_work_total", Help: "Host-owned work."})
			if err := registry.Register(host); err != nil {
				t.Fatal(err)
			}
			host.Add(11)
			r := &trackingRegisterer{Registerer: registry, failAt: 6}
			if mode != "registerer_error" {
				r.failAt = 0
				d := metricdata.Descriptors()[6]
				if mode == "incompatible_descriptor" {
					d.Description = "A different host definition of this metric."
				}
				existing := prometheus.NewCounterVec(prometheus.CounterOpts{Name: d.PromName, Help: d.Description, ConstLabels: prometheus.Labels{"accountkit_instance": "rollback"}}, d.Labels)
				if err := registry.Register(existing); err != nil {
					t.Fatal(err)
				}
				existing.WithLabelValues("apple_cached_key_fallback").Add(17)
			}
			if o, err := adapter.New(r, "rollback"); err == nil || o != nil {
				t.Fatal("registration error ignored")
			}
			if len(r.registered) != 6 || len(r.unregistered) != 6 {
				t.Fatalf("rollback registered=%d unregistered=%d", len(r.registered), len(r.unregistered))
			}
			for _, removed := range r.unregistered {
				found := false
				for _, owned := range r.registered {
					if owned == removed {
						found = true
					}
				}
				if !found {
					t.Fatal("unregistered collector not owned by this construction")
				}
			}
			families, err := registry.Gather()
			if err != nil {
				t.Fatal(err)
			}
			wantFamilies := 1
			if mode != "registerer_error" {
				wantFamilies++
			}
			if len(families) != wantFamilies {
				t.Fatalf("rollback families=%d", len(families))
			}
			for _, f := range families {
				want := float64(11)
				if f.GetName() == "accountkit_degradations_total" {
					want = 17
				}
				if f.Metric[0].Counter.GetValue() != want {
					t.Fatal("preexisting data changed")
				}
			}
			// A Registry retains descriptor consistency metadata even after an
			// unregister, so a host's incompatible definition remains authoritative.
			if mode != "incompatible_descriptor" {
				o, err := adapter.New(registry, "next")
				if err != nil {
					t.Fatal(err)
				}
				o.Observe(context.Background(), observability.DegradationObservation{Scenario: observability.DegradationAppleCachedKeyFallback, Count: 1})
				if _, err := registry.Gather(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
