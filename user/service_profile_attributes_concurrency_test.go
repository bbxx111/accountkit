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

func TestProfileAttributesConcurrency(t *testing.T) {
	for _, state := range []enum.UserState{enum.UserFrozen, enum.UserPendingDeletion} {
		t.Run(state.String(), func(t *testing.T) {
			f, id := profileAttributesFixture(t)
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
			go func() {
				_, err := f.svc.PatchProfileAttributes(ctx, id, user.ProfileAttributesPatch{Document: []byte(`{"theme":"a"}`)}, meta1)
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
	t.Run("different attributes", func(t *testing.T) {
		f, id := profileAttributesFixture(t)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		tx := identityRaceLock(t, ctx, f, id, "")
		done := make(chan error, 2)
		for _, document := range []string{`{"theme":"a"}`, `{"score":1}`} {
			go func() {
				_, err := f.svc.PatchProfileAttributes(ctx, id, user.ProfileAttributesPatch{Document: []byte(document)}, meta1)
				done <- err
			}()
		}
		waitIdentityRaceBlocked(t, ctx, f, tx, done)
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		a, b := <-done, <-done
		if a != nil || b != nil {
			t.Fatalf("concurrent: %v / %v", a, b)
		}
		v, err := f.svc.GetProfileAttributes(ctx, id)
		if err != nil || string(v.Data) != `{"score":1,"theme":"a"}` {
			t.Fatalf("nonoverlapping writes lost data: %s %v", v.Data, err)
		}
	})
	t.Run("cancel lock wait", func(t *testing.T) {
		f, id := profileAttributesFixture(t)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		tx := identityRaceLock(t, ctx, f, id, "")
		op, cancelOp := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() {
			_, err := f.svc.PatchProfileAttributes(op, id, user.ProfileAttributesPatch{Document: []byte(`{"theme":"a"}`)}, meta1)
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
		v, err := f.svc.GetProfileAttributes(ctx, id)
		if err != nil || v.UpdateTime != nil {
			t.Fatalf("cancelled mutation: %+v %v", v, err)
		}
	})
}

// Observe both dependencies in PostgreSQL's blocking graph before releasing the
// first writer. This establishes execution order without scheduling assumptions.
func TestProfileAttributesSerializedMerge(t *testing.T) {
	const schema = `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"theme":{"type":"string"},"score":{"type":"integer"},"flag":{"type":"boolean"},"nested":{"type":"object","properties":{"left":{"type":"integer"},"right":{"type":"integer"}},"additionalProperties":false},"entries":{"type":"array","items":{"type":"integer"}}},"dependentRequired":{"flag":["score"]},"additionalProperties":false}`
	for _, tc := range []struct {
		name, first, second, want string
		invalid                   bool
	}{
		{"nonoverlapping", `{"theme":"a"}`, `{"score":2}`, `{"score":2,"theme":"a"}`, false},
		{"same attribute", `{"theme":"a"}`, `{"theme":"b"}`, `{"theme":"b"}`, false},
		{"nested merge", `{"nested":{"left":1}}`, `{"nested":{"right":2}}`, `{"nested":{"left":1,"right":2}}`, false},
		{"array replacement", `{"entries":[1,2]}`, `{"entries":[3]}`, `{"entries":[3]}`, false},
		{"parent deletion", `{"nested":{"left":1}}`, `{"nested":null}`, `{}`, false},
		{"full result validation", `{"flag":true,"score":1}`, `{"score":null}`, `{"flag":true,"score":1}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			newServiceWith(t, f, func(d *user.Deps) {
				d.ProfileAttributes = &user.ProfileAttributesConfig{SchemaVersion: "v1", Schema: []byte(schema)}
			})
			id := f.signIn(t, enum.IdentityPhone, phone1, dev1).UserID
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			gate := replacementMutationGate(t, ctx, f, "user_account", "UPDATE", "NEW.profile_attributes IS DISTINCT FROM OLD.profile_attributes")
			first := make(chan replacementRaceResult, 1)
			go func() {
				_, err := f.svc.PatchProfileAttributes(ctx, id, user.ProfileAttributesPatch{Document: []byte(tc.first)}, meta1)
				first <- replacementRaceResult{err: err}
			}()
			pid := replacementWaitForPID(t, ctx, f, gate.Conn().PgConn().PID(), first)
			second := make(chan replacementRaceResult, 1)
			go func() {
				_, err := f.svc.PatchProfileAttributes(ctx, id, user.ProfileAttributesPatch{Document: []byte(tc.second)}, meta1)
				second <- replacementRaceResult{err: err}
			}()
			replacementWaitForPID(t, ctx, f, pid, second)
			if err := gate.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if out := finishReplacementRace(t, ctx, first); out.err != nil {
				t.Fatal(out.err)
			}
			out := finishReplacementRace(t, ctx, second)
			if tc.invalid {
				if !errors.Is(out.err, user.ErrInvalidArgument) {
					t.Fatal("invalid full result accepted:", out.err)
				}
			} else if out.err != nil {
				t.Fatal(out.err)
			}
			got, err := f.svc.GetProfileAttributes(ctx, id)
			if err != nil || string(got.Data) != tc.want || got.SchemaVersion == nil || got.SchemaFingerprint == nil || got.UpdateTime == nil {
				t.Fatalf("locked merge result: %+v %v, want %s", got, err, tc.want)
			}
			count := 0
			for _, e := range f.audit.RawEvents() {
				if e.Action == "USER_PROFILE_ATTRIBUTES_UPDATED" {
					count++
				}
			}
			wantEvents := 2
			if tc.invalid {
				wantEvents = 1
			}
			if count != wantEvents {
				t.Fatalf("success audit count=%d want=%d", count, wantEvents)
			}
		})
	}
}

func TestProfileAttributesPurgeBeforeWrite(t *testing.T) {
	f, id := profileAttributesFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := f.svc.PatchProfileAttributes(ctx, id, user.ProfileAttributesPatch{Document: []byte(`{"theme":"private"}`)}, meta1); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.AdminDeleteUser(ctx, admin1, id, meta1); err != nil {
		t.Fatal(err)
	}
	f.advance(360*time.Hour + time.Second)
	gate := replacementMutationGate(t, ctx, f, "user_account", "UPDATE", "NEW.state <> OLD.state")
	purge := make(chan replacementRaceResult, 1)
	go func() { _, err := f.svc.PurgeDueUsers(ctx); purge <- replacementRaceResult{err: err} }()
	pid := replacementWaitForPID(t, ctx, f, gate.Conn().PgConn().PID(), purge)
	write := make(chan replacementRaceResult, 1)
	go func() {
		_, err := f.svc.PatchProfileAttributes(ctx, id, user.ProfileAttributesPatch{Document: []byte(`{"theme":"late"}`)}, meta1)
		write <- replacementRaceResult{err: err}
	}()
	replacementWaitForPID(t, ctx, f, pid, write)
	if err := gate.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if out := finishReplacementRace(t, ctx, purge); out.err != nil {
		t.Fatal(out.err)
	}
	if out := finishReplacementRace(t, ctx, write); !errors.Is(out.err, user.ErrInvalidState) {
		t.Fatal(out.err)
	}
	row, err := f.repo.Q().GetUserByID(ctx, id)
	if err != nil || row.State != enum.UserDeleted || string(row.ProfileAttributes) != "{}" || row.ProfileAttributesSchemaVersion != nil || row.ProfileAttributesSchemaFingerprint != nil || row.ProfileAttributesUpdateTime == nil {
		t.Fatalf("late write restored purged data: %+v %v", row, err)
	}
}

func TestProfileAttributesCommitDisconnect(t *testing.T) {
	f, id := profileAttributesFixture(t)
	ctx := context.Background()
	mustExec(t, f, `CREATE FUNCTION synthetic_profile_attributes_disconnect() RETURNS trigger AS $$ BEGIN PERFORM pg_terminate_backend(pg_backend_pid()); RETURN NEW; END; $$ LANGUAGE plpgsql`)
	mustExec(t, f, `CREATE CONSTRAINT TRIGGER synthetic_profile_attributes_disconnect_trg AFTER UPDATE ON user_account DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION synthetic_profile_attributes_disconnect()`)
	v, err := f.svc.PatchProfileAttributes(ctx, id, user.ProfileAttributesPatch{Document: []byte(`{"theme":"synthetic-private"}`)}, meta1)
	if err == nil || v.Data != nil {
		t.Fatalf("uncertain commit returned success: %+v %v", v, err)
	}
	for _, e := range f.audit.RawEvents() {
		if e.Action == "USER_PROFILE_ATTRIBUTES_UPDATED" {
			t.Fatal("commit error emitted success event")
		}
	}
	// Do not infer rollback from the returned error: a host must read back.
}

func TestProfileAttributesWriteBeforeLifecycle(t *testing.T) {
	for _, state := range []enum.UserState{enum.UserFrozen, enum.UserPendingDeletion} {
		t.Run(state.String(), func(t *testing.T) {
			f, id := profileAttributesFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			gate := replacementMutationGate(t, ctx, f, "user_account", "UPDATE", "NEW.profile_attributes IS DISTINCT FROM OLD.profile_attributes")
			writeDone := make(chan replacementRaceResult, 1)
			go func() {
				_, err := f.svc.PatchProfileAttributes(ctx, id, user.ProfileAttributesPatch{Document: []byte(`{"theme":"a"}`)}, meta1)
				writeDone <- replacementRaceResult{err: err}
			}()
			writePID := replacementWaitForPID(t, ctx, f, gate.Conn().PgConn().PID(), writeDone)
			lifeDone := make(chan replacementRaceResult, 1)
			go func() {
				err := profileAttributesLifecycle(ctx, f, id, state)
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
			if err != nil || row.State != state {
				t.Fatalf("lifecycle lost committed extension: %+v %v", row, err)
			}
		})
	}
}

func profileAttributesLifecycle(ctx context.Context, f *fixture, id string, state enum.UserState) error {
	if state == enum.UserFrozen {
		_, err := f.svc.Freeze(ctx, admin1, id, "synthetic lifecycle", meta1)
		return err
	}
	_, err := f.svc.AdminDeleteUser(ctx, admin1, id, meta1)
	return err
}

func TestProfileAttributesLifecycleBeforeWrite(t *testing.T) {
	for _, state := range []enum.UserState{enum.UserFrozen, enum.UserPendingDeletion} {
		t.Run(state.String(), func(t *testing.T) {
			f, id := profileAttributesFixture(t)
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
			go func() { lifecycle <- replacementRaceResult{err: profileAttributesLifecycle(ctx, f, id, state)} }()
			lifePID := replacementWaitForPID(t, ctx, f, gate.Conn().PgConn().PID(), lifecycle)
			write := make(chan replacementRaceResult, 1)
			go func() {
				_, err := f.svc.PatchProfileAttributes(ctx, id, user.ProfileAttributesPatch{Document: []byte(`{"theme":"a"}`)}, meta1)
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
