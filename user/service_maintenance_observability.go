package user

import (
	"context"
	"errors"
	"time"

	"github.com/bbxx111/accountkit/internal/maintenancectx"
	"github.com/bbxx111/accountkit/observability"
)

// observeMaintenance is deferred by direct domain maintenance methods. The
// domain owns item counts; only an enabled, matching Runner owns its terminal.
// A business panic is still propagated after recording its terminal.
func (s *Service) observeMaintenance(ctx context.Context, task observability.Task, count func() int64, err *error, start time.Time) {
	panicValue := recover()
	elapsed := time.Since(start)
	if n := count(); n > 0 {
		s.observer.Observe(ctx, observability.MaintenanceObservation{Kind: observability.MaintenanceItems, Task: task, Count: n})
	}
	if !maintenancectx.OwnsTask(ctx, task, s.observer) {
		result := observability.ResultSuccess
		switch {
		case panicValue != nil:
			result = observability.ResultPanic
		case errors.Is(*err, context.Canceled):
			result = observability.ResultCanceled
		case errors.Is(*err, context.DeadlineExceeded):
			result = observability.ResultDeadlineExceeded
		case *err != nil:
			result = observability.ResultError
		}
		s.observer.Observe(ctx, observability.MaintenanceObservation{Kind: observability.MaintenanceRun, Task: task, Result: result, Duration: elapsed})
	}
	if panicValue != nil {
		panic(panicValue)
	}
}
