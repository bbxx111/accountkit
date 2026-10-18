// Package maintenancectx coordinates ownership of maintenance task terminals.
package maintenancectx

import (
	"context"

	"github.com/bbxx111/accountkit/observability"
)

type taskKey struct{}
type ownership struct {
	task  observability.Task
	owner *observability.Recorder
}

// WithTask marks an enabled built-in Runner task. Unknown tasks cannot suppress
// domain observations, and disabled runners leave the original context intact.
func WithTask(ctx context.Context, task observability.Task, owner *observability.Recorder) context.Context {
	if !owner.Enabled() || !builtIn(task) {
		return ctx
	}
	return context.WithValue(ctx, taskKey{}, ownership{task: task, owner: owner})
}

// OwnsTask reports only an exact enabled recorder and task match.
func OwnsTask(ctx context.Context, task observability.Task, owner *observability.Recorder) bool {
	if !owner.Enabled() || !builtIn(task) {
		return false
	}
	value, ok := ctx.Value(taskKey{}).(ownership)
	return ok && value.task == task && value.owner == owner
}

func builtIn(task observability.Task) bool {
	switch task {
	case observability.TaskCleanupSignInCompletionFlows, observability.TaskPurgeUsers,
		observability.TaskCleanupSessions, observability.TaskAuditRetention,
		observability.TaskRekeyDigests, observability.TaskReencryptSubjects:
		return true
	}
	return false
}
