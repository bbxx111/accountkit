// Package observability defines optional, best-effort local observations.
// Observers must return promptly and must not use observations as a durable ledger.
package observability

import (
	"context"
	"time"
)

// Observer records typed facts. Implementations must be concurrency-safe and return
// promptly; Recorder isolates panics but cannot interrupt a blocking implementation.
type Observer interface {
	Observe(context.Context, Observation)
}

// Observation is a closed set of immutable values. Consumers should ignore unknown
// types. Only the six concrete value types are supported; pointers are ignored by
// the built-in metric projection.
type Observation interface{ observation() }

// AuthObservation describes one normally returned public authentication call.
// Duration includes internal retries. Pending and failed calls have no Grant or Mode.
type AuthObservation struct {
	Operation AuthOperation
	Method    Method
	Result    Result
	Grant     Grant
	Mode      RefreshMode
	Reason    Reason
	Duration  time.Duration
}

func (AuthObservation) observation() {}

// CodeObservation describes one public send request or actual logical verification.
type CodeObservation struct {
	Operation CodeOperation
	Channel   Channel
	Purpose   Purpose
	Result    Result
	Reason    Reason
	Duration  time.Duration
}

func (CodeObservation) observation() {}

// DependencyObservation counts one logical call, rather than wire requests or commands.
type DependencyObservation struct {
	Component Component
	Operation DependencyOperation
	Result    Result
	Duration  time.Duration
}

func (DependencyObservation) observation() {}

// DegradationObservation counts actual occurrences of a fixed degraded scenario.
type DegradationObservation struct {
	Scenario DegradationScenario
	Count    int64
}

func (DegradationObservation) observation() {}

// AuditObservation reports either event counts (Stage/Result) or omitted field
// counts (Field). Count is the actual increment, never a cumulative snapshot.
type AuditObservation struct {
	Kind   AuditKind
	Stage  AuditStage
	Result Result
	Field  AuditField
	Count  int64
}

func (AuditObservation) observation() {}

// MaintenanceObservation reports a round, a completed task run, or processed items.
// Count is used only for items. Duration is used only for a run. Round/run each
// count once; item counts retain completed work even when the task later fails.
type MaintenanceObservation struct {
	Kind     MaintenanceKind
	Task     Task
	Result   Result
	Duration time.Duration
	Count    int64
}

func (MaintenanceObservation) observation() {}
