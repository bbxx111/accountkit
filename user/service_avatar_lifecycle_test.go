package user_test

import (
	"context"
	"errors"
	"github.com/bbxx111/accountkit/anonymize"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"reflect"
	"testing"
	"time"
)

type avatarRollbackAnonymizer struct {
	t    *testing.T
	seen bool
}

func (a *avatarRollbackAnonymizer) Name() string     { return "synthetic_avatar_host" }
func (a *avatarRollbackAnonymizer) Tables() []string { return nil }
func (a *avatarRollbackAnonymizer) Anonymize(ctx context.Context, tx pgx.Tx, id string) error {
	var cleared bool
	if err := tx.QueryRow(ctx, `SELECT avatar_url IS NULL AND state=$2 FROM user_account WHERE id=$1`, id, enum.UserDeleted).Scan(&cleared); err != nil {
		a.t.Fatal(err)
	}
	if !cleared {
		a.t.Error("callback must see cleared avatar in same transaction")
	}
	a.seen = true
	return errors.New("synthetic callback failure")
}

func TestAvatarLifecycle(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	id := f.signIn(t, enum.IdentityPhone, phone1, dev1).UserID
	const avatar = "https://example.com/synthetic-avatar"
	if _, err := f.svc.UpdateProfile(ctx, id, user.ProfilePatch{AvatarURL: avatarPointer(avatar)}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Freeze(ctx, admin1, id, "synthetic", meta1); err != nil {
		t.Fatal(err)
	}
	frozen, err := f.svc.GetMe(ctx, id)
	if err != nil || frozen.AvatarURL != avatar {
		t.Fatal("frozen avatar", err)
	}
	admin, err := f.svc.GetUserDetail(ctx, id)
	if err != nil || admin.AvatarURL != avatar {
		t.Fatal("admin avatar", err)
	}
	if _, err := f.svc.UpdateProfile(ctx, id, user.ProfilePatch{AvatarURL: avatarPointer("")}); !errors.Is(err, user.ErrInvalidState) {
		t.Fatal("frozen update", err)
	}
	if _, err := f.svc.Unfreeze(ctx, admin1, id, "synthetic", meta1); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.AdminDeleteUser(ctx, admin1, id, meta1); err != nil {
		t.Fatal(err)
	}
	pending, _ := f.svc.GetMe(ctx, id)
	if pending.AvatarURL != avatar {
		t.Fatal("cooling period lost avatar")
	}
	profiles, err := f.svc.BatchPublicProfiles(ctx, []string{id})
	if err != nil || profiles[id].AvatarURL != "" {
		t.Fatal("public pending avatar", err)
	}
	if _, err := f.svc.AdminUndeleteUser(ctx, admin1, id, meta1); err != nil {
		t.Fatal(err)
	}
	restored, _ := f.svc.GetMe(ctx, id)
	if restored.AvatarURL != avatar {
		t.Fatal("restore lost avatar")
	}
	if _, err := f.svc.AdminDeleteUser(ctx, admin1, id, meta1); err != nil {
		t.Fatal(err)
	}
	before, err := f.repo.Q().GetUserByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	a := &avatarRollbackAnonymizer{t: t}
	newServiceWith(t, f, func(d *user.Deps) { d.Anonymizers = []anonymize.Anonymizer{a} })
	f.advance(360*time.Hour + time.Second)
	if n, err := f.svc.PurgeDueUsers(ctx); err == nil || n != 0 || !a.seen {
		t.Fatal("callback rollback", n, err)
	}
	after, _ := f.repo.Q().GetUserByID(ctx, id)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("failed callback changed row")
	}
	newServiceWith(t, f, func(d *user.Deps) { d.Anonymizers = nil })
	if n, err := f.svc.PurgeDueUsers(ctx); err != nil || n != 1 {
		t.Fatal("purge", n, err)
	}
	after, _ = f.repo.Q().GetUserByID(ctx, id)
	if after.AvatarUrl != nil {
		t.Fatal("purge retained avatar")
	}
	if _, err := f.svc.UpdateProfile(ctx, id, user.ProfilePatch{AvatarURL: avatarPointer(avatar)}); !errors.Is(err, user.ErrInvalidState) {
		t.Fatal("deleted write", err)
	}
	profiles, err = f.svc.BatchPublicProfiles(ctx, []string{id})
	if err != nil || profiles[id].AvatarURL != "" {
		t.Fatal("public deleted avatar", err)
	}
}

func TestAvatarConcurrency(t *testing.T) {
	t.Run("different fields", func(t *testing.T) {
		f := newFixture(t)
		id := f.signIn(t, enum.IdentityPhone, phone1, dev1).UserID
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		gate := replacementMutationGate(t, ctx, f, "user_account", "UPDATE", "NEW.avatar_url IS DISTINCT FROM OLD.avatar_url")
		first := make(chan replacementRaceResult, 1)
		go func() {
			_, err := f.svc.UpdateProfile(ctx, id, user.ProfilePatch{AvatarURL: avatarPointer("https://example.com/a")})
			first <- replacementRaceResult{err: err}
		}()
		pid := replacementWaitForPID(t, ctx, f, gate.Conn().PgConn().PID(), first)
		second := make(chan replacementRaceResult, 1)
		go func() {
			_, err := f.svc.UpdateProfile(ctx, id, user.ProfilePatch{DisplayName: avatarPointer("name")})
			second <- replacementRaceResult{err: err}
		}()
		replacementWaitForPID(t, ctx, f, pid, second)
		if err := gate.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if out := finishReplacementRace(t, ctx, first); out.err != nil {
			t.Fatal(out.err)
		}
		if out := finishReplacementRace(t, ctx, second); out.err != nil {
			t.Fatal(out.err)
		}
		m, err := f.svc.GetMe(ctx, id)
		if err != nil || m.AvatarURL != "https://example.com/a" || m.DisplayName != "name" {
			t.Fatalf("lost concurrent field: %+v %v", m, err)
		}
	})
	for _, state := range []enum.UserState{enum.UserFrozen, enum.UserPendingDeletion, enum.UserDeleted} {
		t.Run(state.String(), func(t *testing.T) {
			f := newFixture(t)
			id := f.signIn(t, enum.IdentityPhone, phone1, dev1).UserID
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cfg := f.pool.Config()
			cfg.ConnConfig.RuntimeParams["default_transaction_isolation"] = "serializable"
			pool, err := pgxpool.NewWithConfig(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			newServiceWith(t, f, func(d *user.Deps) { d.Repo = user.NewRepo(pool) })
			tx := identityRaceLock(t, ctx, f, id, "")
			done := make(chan error, 1)
			go func() {
				_, err := f.svc.UpdateProfile(ctx, id, user.ProfilePatch{AvatarURL: avatarPointer("https://example.com/a")})
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
				t.Fatal("must recheck latest state", err)
			}
			row, _ := f.repo.Q().GetUserByID(ctx, id)
			if row.AvatarUrl != nil {
				t.Fatal("non-active write stored avatar")
			}
		})
	}
}
