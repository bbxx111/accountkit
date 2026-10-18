package maintenancectx_test

import (
	"context"
	"testing"

	"github.com/bbxx111/accountkit/internal/maintenancectx"
	"github.com/bbxx111/accountkit/internal/obstest"
	"github.com/bbxx111/accountkit/observability"
)

func TestTaskOwnership(t *testing.T) {
	owner := observability.NewRecorder(&obstest.Recorder{}, nil)
	other := observability.NewRecorder(&obstest.Recorder{}, nil)
	ctx := maintenancectx.WithTask(context.Background(), observability.TaskCleanupSessions, owner)
	if !maintenancectx.OwnsTask(ctx, observability.TaskCleanupSessions, owner) {
		t.Fatal("matching owner/task must suppress duplicate terminal")
	}
	if maintenancectx.OwnsTask(ctx, observability.TaskPurgeUsers, owner) || maintenancectx.OwnsTask(ctx, observability.TaskCleanupSessions, other) {
		t.Fatal("unrelated task or observer must remain independent")
	}
	for _, recorder := range []*observability.Recorder{nil, observability.NewRecorder(nil, nil)} {
		ctx := maintenancectx.WithTask(context.Background(), observability.TaskCleanupSessions, recorder)
		if maintenancectx.OwnsTask(ctx, observability.TaskCleanupSessions, recorder) {
			t.Fatal("disabled recorder cannot own task")
		}
	}
	ctx = maintenancectx.WithTask(context.Background(), observability.TaskOther, owner)
	if maintenancectx.OwnsTask(ctx, observability.TaskOther, owner) {
		t.Fatal("custom task must not own built-in terminal")
	}
}
