// Package obstest provides an in-memory observer for cross-package behavior tests.
package obstest

import (
	"context"
	"sync"

	"github.com/bbxx111/accountkit/observability"
)

// Recorder safely captures concrete observation values. Its zero value is ready.
type Recorder struct {
	mu           sync.Mutex
	observations []observability.Observation
}

// Observe retains only supported immutable values; pointers are intentionally ignored.
func (r *Recorder) Observe(_ context.Context, observation observability.Observation) {
	switch observation.(type) {
	case observability.AuthObservation, observability.CodeObservation, observability.DependencyObservation,
		observability.DegradationObservation, observability.AuditObservation, observability.MaintenanceObservation:
	default:
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.observations = append(r.observations, observation)
}

// Snapshot returns a defensive copy of all captured values in arrival order.
func (r *Recorder) Snapshot() []observability.Observation {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]observability.Observation(nil), r.observations...)
}

// Reset clears captured observations while preserving concurrency safety.
func (r *Recorder) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.observations = nil
}
