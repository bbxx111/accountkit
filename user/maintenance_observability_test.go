package user_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/internal/maintenancectx"
	"github.com/bbxx111/accountkit/internal/obstest"
	ob "github.com/bbxx111/accountkit/observability"
	"github.com/bbxx111/accountkit/user"
)

// A direct call owns its terminal; an enabled runner with the same recorder owns
// the terminal instead. The domain always owns counts of committed work.
func TestMaintenanceObservations(t *testing.T) {
	f := newFixture(t)
	recorded := &obstest.Recorder{}
	owner := ob.NewRecorder(recorded, nil)
	f.deps.Observer = owner
	var err error
	f.svc, err = user.NewService(f.deps)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if n, err := f.svc.CleanupIdentityFlows(ctx); err != nil || n != 0 {
		t.Fatalf("legacy cleanup: n=%d err=%v", n, err)
	}
	if n, err := f.svc.CleanupSessions(ctx); err != nil || n != 0 {
		t.Fatalf("session cleanup: n=%d err=%v", n, err)
	}
	if n, err := f.svc.PurgeDueUsers(ctx); err != nil || n != 0 {
		t.Fatalf("purge: n=%d err=%v", n, err)
	}
	if n, err := f.svc.RekeyDigests(ctx); err != nil || n != 0 {
		t.Fatalf("rekey: n=%d err=%v", n, err)
	}
	if n, err := f.svc.ReencryptSubjects(ctx); err != nil || n != 0 {
		t.Fatalf("reencrypt: n=%d err=%v", n, err)
	}
	want := []ob.Task{ob.TaskCleanupSignInCompletionFlows, ob.TaskCleanupSessions, ob.TaskPurgeUsers, ob.TaskRekeyDigests, ob.TaskReencryptSubjects}
	assertMaintenanceRuns(t, recorded.Snapshot(), want)

	recorded.Reset()
	token := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	if _, err := f.pool.Exec(ctx, `UPDATE session SET revoke_time = $1 WHERE user_id = $2`, f.clock.Add(-31*24*time.Hour), token.UserID); err != nil {
		t.Fatal(err)
	}
	recorded.Reset()
	marked := maintenancectx.WithTask(ctx, ob.TaskCleanupSessions, owner)
	if n, err := f.svc.CleanupSessions(marked); err != nil || n != 1 {
		t.Fatalf("marked cleanup: n=%d err=%v", n, err)
	}
	if got := recorded.Snapshot(); len(got) != 1 || got[0] != (ob.MaintenanceObservation{Kind: ob.MaintenanceItems, Task: ob.TaskCleanupSessions, Count: 1}) {
		t.Fatalf("matching runner owns terminal, domain owns count: %+v", got)
	}
	recorded.Reset()
	other := maintenancectx.WithTask(ctx, ob.TaskCleanupSessions, ob.NewRecorder(&obstest.Recorder{}, nil))
	if _, err := f.svc.CleanupSessions(other); err != nil {
		t.Fatal(err)
	}
	assertMaintenanceRuns(t, recorded.Snapshot(), []ob.Task{ob.TaskCleanupSessions})
	recorded.Reset()
	wrongTask := maintenancectx.WithTask(ctx, ob.TaskPurgeUsers, owner)
	if _, err := f.svc.CleanupSessions(wrongTask); err != nil {
		t.Fatal(err)
	}
	assertMaintenanceRuns(t, recorded.Snapshot(), []ob.Task{ob.TaskCleanupSessions})
}

func TestMaintenanceObservationsPartialFailure(t *testing.T) {
	f := newFixture(t)
	recorded := &obstest.Recorder{}
	f.deps.Observer = recorded
	rebuildMetricsService(t, f)
	first := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	second := f.signIn(t, enum.IdentityPhone, phone2, dev1)
	bad := findKind(t, f, second.UserID, enum.IdentityPhone, false)
	if _, err := f.pool.Exec(context.Background(), `UPDATE identity SET subject_ciphertext = NULL WHERE id = $1`, bad.ID); err != nil {
		t.Fatal(err)
	}
	rotate(t, f)
	recorded.Reset()
	n, err := f.svc.RekeyDigests(context.Background())
	if n != 1 || err == nil {
		t.Fatalf("partial rekey: n=%d err=%v", n, err)
	}
	if row := findKind(t, f, first.UserID, enum.IdentityPhone, false); row.DigestKeyVersion == nil || *row.DigestKeyVersion != 2 {
		t.Fatalf("successful row not committed: %+v", row)
	}
	got := recorded.Snapshot()
	if len(got) != 2 || got[0] != (ob.MaintenanceObservation{Kind: ob.MaintenanceItems, Task: ob.TaskRekeyDigests, Count: 1}) {
		t.Fatalf("partial work count: %+v", got)
	}
	run, ok := got[1].(ob.MaintenanceObservation)
	if !ok || run.Kind != ob.MaintenanceRun || run.Task != ob.TaskRekeyDigests || run.Result != ob.ResultError {
		t.Fatalf("partial failure terminal: %+v", got[1])
	}
}

func TestMaintenanceObservationsCASMissNoItems(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	recorded := &obstest.Recorder{}
	f.deps.Observer = recorded
	rebuildMetricsService(t, f)
	token := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	rotate(t, f)
	row := findKind(t, f, token.UserID, enum.IdentityPhone, false)
	if row.DigestKeyVersion == nil || *row.DigestKeyVersion != 1 {
		t.Fatalf("row must start on old digest version: %+v", row)
	}
	digest, _ := f.dig.Digest(phone1)
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	var lockedID string
	if err := tx.QueryRow(ctx, `SELECT id FROM identity WHERE id = $1 FOR UPDATE`, row.ID).Scan(&lockedID); err != nil || lockedID != row.ID {
		t.Fatalf("lock identity: id=%q err=%v", lockedID, err)
	}
	type outcome struct {
		n   int
		err error
	}
	completed := make(chan error, 1)
	results := make(chan outcome, 1)
	recorded.Reset()
	go func() {
		n, err := f.svc.RekeyDigests(ctx)
		results <- outcome{n: n, err: err}
		completed <- err
	}()
	waitIdentityRaceBlocked(t, ctx, f, tx, completed)
	tag, err := tx.Exec(ctx, `UPDATE identity SET subject_digest = $1, digest_key_version = 2 WHERE id = $2`, digest, row.ID)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("winning update: rows=%d err=%v", tag.RowsAffected(), err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	result := <-results
	if result.n != 0 || result.err != nil {
		t.Fatalf("missed CAS must not count another worker's update: n=%d err=%v", result.n, result.err)
	}
	if after := findKind(t, f, token.UserID, enum.IdentityPhone, false); after.DigestKeyVersion == nil || *after.DigestKeyVersion != 2 || after.SubjectDigest == nil || *after.SubjectDigest != digest {
		t.Fatalf("concurrent winner's update not preserved: %+v", after)
	}
	assertMaintenanceRuns(t, recorded.Snapshot(), []ob.Task{ob.TaskRekeyDigests})
}

func TestMaintenanceObservationsObserverPanic(t *testing.T) {
	f := newFixture(t)
	f.deps.Observer = metricsObserverFunc(func(context.Context, ob.Observation) { panic("private-observer-payload") })
	rebuildMetricsService(t, f)
	if n, err := f.svc.CleanupSessions(context.Background()); n != 0 || err != nil {
		t.Fatalf("observer panic changed maintenance result: n=%d err=%v", n, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if n, err := f.svc.CleanupSessions(canceled); n != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled cleanup: n=%d err=%v", n, err)
	}
}

func TestMaintenanceObservationsDurationExcludesCallback(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	token := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	if _, err := f.pool.Exec(ctx, `UPDATE session SET revoke_time = $1 WHERE user_id = $2`, f.clock.Add(-31*24*time.Hour), token.UserID); err != nil {
		t.Fatal(err)
	}
	var duration time.Duration
	var itemsCallbackEntry time.Time
	f.deps.Observer = metricsObserverFunc(func(_ context.Context, event ob.Observation) {
		m, ok := event.(ob.MaintenanceObservation)
		if !ok {
			return
		}
		switch m.Kind {
		case ob.MaintenanceItems:
			itemsCallbackEntry = time.Now()
			time.Sleep(200 * time.Millisecond)
		case ob.MaintenanceRun:
			duration = m.Duration
		}
	})
	rebuildMetricsService(t, f)
	callStart := time.Now()
	if n, err := f.svc.CleanupSessions(ctx); err != nil || n != 1 {
		t.Fatalf("cleanup: n=%d err=%v", n, err)
	}
	if itemsCallbackEntry.IsZero() || duration <= 0 || duration > itemsCallbackEntry.Sub(callStart) {
		t.Fatalf("run duration %s extends past first observer callback at %s from call start", duration, itemsCallbackEntry.Sub(callStart))
	}
}

func assertMaintenanceRuns(t *testing.T, got []ob.Observation, want []ob.Task) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("observations: got %+v, want one run per task %v", got, want)
	}
	for i, event := range got {
		run, ok := event.(ob.MaintenanceObservation)
		if !ok || run.Kind != ob.MaintenanceRun || run.Task != want[i] || run.Result != ob.ResultSuccess || run.Duration < 0 {
			t.Fatalf("observation %d: %+v, want success run of %s", i, event, want[i])
		}
	}
}
