// Package metricdata owns the shared backend-neutral metric contract.
package metricdata

// ID is the stable identity used to connect projected values to instruments.
type ID uint8

const (
	AuthOperations ID = iota
	AuthDuration
	CodeOperations
	CodeDuration
	DependencyOperations
	DependencyDuration
	Degradations
	AuditEvents
	AuditOmittedFields
	MaintenanceRounds
	MaintenanceRuns
	MaintenanceDuration
	MaintenanceItems
)

// Descriptor describes one instrument. Labels excludes accountkit_instance,
// which each adapter appends from its fixed, validated constructor argument.
type Descriptor struct {
	ID          ID
	OTelName    string
	PromName    string
	Unit        string
	Description string
	Labels      []string
	Histogram   bool
}

var catalog = [...]Descriptor{
	{AuthOperations, "accountkit.auth.operations", "accountkit_auth_operations_total", UnitOperation, "Normally returned public authentication calls.", []string{LabelOperation, LabelMethod, LabelResult, LabelGrant, LabelMode, LabelReason}, false},
	{AuthDuration, "accountkit.auth.operation.duration", "accountkit_auth_operation_duration_seconds", UnitSeconds, "Elapsed duration of public authentication calls.", []string{LabelOperation, LabelMethod, LabelResult}, true},
	{CodeOperations, "accountkit.code.operations", "accountkit_code_operations_total", UnitOperation, "Public code sends and actual logical code verifications.", []string{LabelOperation, LabelChannel, LabelPurpose, LabelResult, LabelReason}, false},
	{CodeDuration, "accountkit.code.operation.duration", "accountkit_code_operation_duration_seconds", UnitSeconds, "Elapsed duration of code operations.", []string{LabelOperation, LabelChannel, LabelPurpose, LabelResult}, true},
	{DependencyOperations, "accountkit.dependency.operations", "accountkit_dependency_operations_total", UnitOperation, "Logical dependency calls, not wire requests.", []string{LabelComponent, LabelOperation, LabelResult}, false},
	{DependencyDuration, "accountkit.dependency.operation.duration", "accountkit_dependency_operation_duration_seconds", UnitSeconds, "Elapsed duration of logical dependency calls.", []string{LabelComponent, LabelOperation, LabelResult}, true},
	{Degradations, "accountkit.degradations", "accountkit_degradations_total", UnitEvent, "Actual occurrences of degraded behavior.", []string{LabelScenario}, false},
	{AuditEvents, "accountkit.audit.events", "accountkit_audit_events_total", UnitEvent, "Audit events classified at library-owned processing stages.", []string{LabelStage, LabelResult}, false},
	{AuditOmittedFields, "accountkit.audit.omitted.fields", "accountkit_audit_omitted_fields_total", "{field}", "Optional audit fields omitted during authentication mapping.", []string{LabelField}, false},
	{MaintenanceRounds, "accountkit.maintenance.rounds", "accountkit_maintenance_rounds_total", "{round}", "Maintenance lock-round results.", []string{LabelResult}, false},
	{MaintenanceRuns, "accountkit.maintenance.runs", "accountkit_maintenance_runs_total", "{run}", "Actual maintenance task completions.", []string{LabelTask, LabelResult}, false},
	{MaintenanceDuration, "accountkit.maintenance.run.duration", "accountkit_maintenance_run_duration_seconds", UnitSeconds, "Elapsed duration of maintenance task runs.", []string{LabelTask, LabelResult}, true},
	{MaintenanceItems, "accountkit.maintenance.items", "accountkit_maintenance_items_total", "{item}", "Items actually processed, including completed portions of failed tasks.", []string{LabelTask}, false},
}

// Descriptors returns a deep copy, including independent label-name slices.
func Descriptors() []Descriptor {
	out := append([]Descriptor(nil), catalog[:]...)
	for i := range out {
		out[i].Labels = append([]string(nil), out[i].Labels...)
	}
	return out
}

// DefaultBuckets returns independent finite boundaries in seconds. Backends also
// retain observations above the final boundary.
func DefaultBuckets() []float64 {
	return []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60}
}
