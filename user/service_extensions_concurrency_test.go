package user_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestUserExtensionsConcurrency(t *testing.T) {
	for _, state := range []enum.UserState{enum.UserFrozen, enum.UserPendingDeletion} {
		t.Run(state.String(), func(t *testing.T) {
			f, id := extensionsFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			pc := f.pool.Config()
			pc.ConnConfig.RuntimeParams["default_transaction_isolation"] = "serializable"
			pool, err := pgxpool.NewWithConfig(ctx, pc)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			newServiceWith(t, f, func(d *user.Deps) { d.Repo = user.NewRepo(pool) })
			tx := identityRaceLock(t, ctx, f, id, "")
			done := make(chan error, 1)
			z := int64(0)
			go func() {
				_, err := f.svc.PatchExtensions(ctx, id, user.ExtensionsPatch{Document: []byte(`{"theme":"a"}`), ExpectedRevision: &z}, meta1)
				done <- err
			}()
			waitIdentityRaceBlocked(t, ctx, f, tx, done)
			if _, err := tx.Exec(ctx, `UPDATE user_account SET state=$2 WHERE id=$1`, id, state); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-done; !errors.Is(err, user.ErrInvalidState) {
				t.Fatalf("must observe latest state under host serializable default: %v", err)
			}
		})
	}
	t.Run("same revision", func(t *testing.T) {
		f, id := extensionsFixture(t)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		tx := identityRaceLock(t, ctx, f, id, "")
		done := make(chan error, 2)
		z := int64(0)
		for i := 0; i < 2; i++ {
			go func() {
				_, err := f.svc.PatchExtensions(ctx, id, user.ExtensionsPatch{Document: []byte(`{"theme":"a"}`), ExpectedRevision: &z}, meta1)
				done <- err
			}()
		}
		waitIdentityRaceBlocked(t, ctx, f, tx, done)
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		a, b := <-done, <-done
		if !((a == nil && errors.Is(b, user.ErrExtensionsConflict)) || (b == nil && errors.Is(a, user.ErrExtensionsConflict))) {
			t.Fatalf("concurrent: %v / %v", a, b)
		}
	})
	t.Run("cancel lock wait", func(t *testing.T) {
		f, id := extensionsFixture(t)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		tx := identityRaceLock(t, ctx, f, id, "")
		op, cancelOp := context.WithCancel(ctx)
		done := make(chan error, 1)
		z := int64(0)
		go func() {
			_, err := f.svc.PatchExtensions(op, id, user.ExtensionsPatch{Document: []byte(`{"theme":"a"}`), ExpectedRevision: &z}, meta1)
			done <- err
		}()
		waitIdentityRaceBlocked(t, ctx, f, tx, done)
		cancelOp()
		if err := <-done; err == nil {
			t.Fatal("cancelled write succeeded")
		}
		if err := tx.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
		v, err := f.svc.GetExtensions(ctx, id)
		if err != nil || v.Revision != 0 {
			t.Fatalf("cancelled mutation: %+v %v", v, err)
		}
	})
}

func TestUserExtensionsCommitDisconnect(t *testing.T) {
	f, id := extensionsFixture(t)
	ctx := context.Background()
	z := int64(0)
	mustExec(t, f, `CREATE FUNCTION synthetic_extensions_disconnect() RETURNS trigger AS $$ BEGIN PERFORM pg_terminate_backend(pg_backend_pid()); RETURN NEW; END; $$ LANGUAGE plpgsql`)
	mustExec(t, f, `CREATE CONSTRAINT TRIGGER synthetic_extensions_disconnect_trg AFTER UPDATE ON user_account DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION synthetic_extensions_disconnect()`)
	v, err := f.svc.PatchExtensions(ctx, id, user.ExtensionsPatch{Document: []byte(`{"theme":"synthetic-private"}`), ExpectedRevision: &z}, meta1)
	if err == nil || v.Data != nil {
		t.Fatalf("uncertain commit returned success: %+v %v", v, err)
	}
	for _, e := range f.audit.RawEvents() {
		if e.Action == "USER_EXTENSIONS_UPDATED" {
			t.Fatal("commit error emitted success event")
		}
	}
	// Do not infer rollback from the returned error: a host must read back.
}

func TestUserExtensionsWriteBeforeLifecycle(t *testing.T) {
	for _, state := range []enum.UserState{enum.UserFrozen, enum.UserPendingDeletion} {
		t.Run(state.String(), func(t *testing.T) {
			f, id := extensionsFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			gate := replacementMutationGate(t, ctx, f, "user_account", "UPDATE", "NEW.extensions_revision > OLD.extensions_revision")
			z := int64(0)
			writeDone := make(chan replacementRaceResult, 1)
			go func() {
				_, err := f.svc.PatchExtensions(ctx, id, user.ExtensionsPatch{Document: []byte(`{"theme":"a"}`), ExpectedRevision: &z}, meta1)
				writeDone <- replacementRaceResult{err: err}
			}()
			writePID := replacementWaitForPID(t, ctx, f, gate.Conn().PgConn().PID(), writeDone)
			lifeDone := make(chan replacementRaceResult, 1)
			go func() {
				err := extensionsLifecycle(ctx, f, id, state)
				lifeDone <- replacementRaceResult{err: err}
			}()
			// The lifecycle lock is visibly queued behind the writer before releasing it.
			replacementWaitForPID(t, ctx, f, writePID, lifeDone)
			if err := gate.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if out := finishReplacementRace(t, ctx, writeDone); out.err != nil {
				t.Fatal(out.err)
			}
			if out := finishReplacementRace(t, ctx, lifeDone); out.err != nil {
				t.Fatal(out.err)
			}
			row, err := f.repo.Q().GetUserByID(ctx, id)
			if err != nil || row.ExtensionsRevision != 1 || row.State != state {
				t.Fatalf("lifecycle lost committed extension: %+v %v", row, err)
			}
		})
	}
}

func extensionsLifecycle(ctx context.Context, f *fixture, id string, state enum.UserState) error {
	if state == enum.UserFrozen {
		_, err := f.svc.Freeze(ctx, admin1, id, "synthetic lifecycle", meta1)
		return err
	}
	_, err := f.svc.AdminDeleteUser(ctx, admin1, id, meta1)
	return err
}

func TestUserExtensionsLifecycleBeforeWrite(t *testing.T) {
	for _, state := range []enum.UserState{enum.UserFrozen, enum.UserPendingDeletion} {
		t.Run(state.String(), func(t *testing.T) {
			f, id := extensionsFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			pc := f.pool.Config()
			pc.ConnConfig.RuntimeParams["default_transaction_isolation"] = "repeatable read"
			pool, err := pgxpool.NewWithConfig(ctx, pc)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			newServiceWith(t, f, func(d *user.Deps) { d.Repo = user.NewRepo(pool) })
			gate := replacementMutationGate(t, ctx, f, "user_account", "UPDATE", "NEW.state <> OLD.state")
			lifecycle := make(chan replacementRaceResult, 1)
			go func() { lifecycle <- replacementRaceResult{err: extensionsLifecycle(ctx, f, id, state)} }()
			lifePID := replacementWaitForPID(t, ctx, f, gate.Conn().PgConn().PID(), lifecycle)
			write := make(chan replacementRaceResult, 1)
			z := int64(0)
			go func() {
				_, err := f.svc.PatchExtensions(ctx, id, user.ExtensionsPatch{Document: []byte(`{"theme":"a"}`), ExpectedRevision: &z}, meta1)
				write <- replacementRaceResult{err: err}
			}()
			replacementWaitForPID(t, ctx, f, lifePID, write)
			if err := gate.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if out := finishReplacementRace(t, ctx, lifecycle); out.err != nil {
				t.Fatal(out.err)
			}
			if out := finishReplacementRace(t, ctx, write); !errors.Is(out.err, user.ErrInvalidState) {
				t.Fatal("writer must reject latest lifecycle state:", out.err)
			}
		})
	}
}
