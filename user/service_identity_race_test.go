package user_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/code"
	"github.com/bbxx111/accountkit/user/db"
)

// A real blocking transaction makes the identity read precede the deletion
// commit; polling pg_blocking_pids observes the wait instead of guessing sleeps.
func identityRaceLock(t *testing.T, ctx context.Context, f *fixture, uid, sid string) pgx.Tx {
	t.Helper()
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	q := db.New(tx)
	if _, err := q.LockUserByID(ctx, uid); err != nil {
		t.Fatal(err)
	}
	if sid != "" {
		if _, err := q.LockSessionByID(ctx, sid); err != nil {
			t.Fatal(err)
		}
	}
	return tx
}

func waitIdentityRaceBlocked(t *testing.T, ctx context.Context, f *fixture, tx pgx.Tx, done <-chan error) {
	t.Helper()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		var blocked bool
		err := f.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE $1::int = ANY(pg_blocking_pids(pid)))`, tx.Conn().PgConn().PID()).Scan(&blocked)
		if err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		select {
		case err := <-done:
			t.Fatalf("operation completed before acquiring the held user lock: %v", err)
		case <-ctx.Done():
			t.Fatal("operation never reached row-lock wait:", ctx.Err())
		case <-tick.C:
		}
	}
}

func TestSignInRejectsIdentityRemovedWhileWaiting(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	first := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	if err := f.svc.SendSignInCode(ctx, enum.IdentityPhone, phone1, meta1); err != nil {
		t.Fatal(err)
	}
	plain := f.sent.code(phone1)
	idents, err := f.repo.Q().ListActiveIdentitiesByUser(ctx, first.UserID)
	if err != nil || len(idents) != 1 {
		t.Fatalf("identities: %v", err)
	}
	tx := identityRaceLock(t, ctx, f, first.UserID, "")
	done := make(chan error, 1)
	go func() {
		_, err := f.svc.SignInWithCode(ctx, enum.IdentityPhone, phone1, plain, user.Device{ID: "stale-login"}, meta1)
		done <- err
	}()
	waitIdentityRaceBlocked(t, ctx, f, tx, done)
	if _, err := db.New(tx).SoftDeleteIdentity(ctx, db.SoftDeleteIdentityParams{ID: idents[0].ID, UserID: first.UserID, Now: *f.clock}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, code.ErrInvalid) {
		t.Fatalf("stale identity must reject sign-in with CODE_INVALID, got %v", err)
	}
	sessions, err := f.repo.Q().ListActiveSessionsByUser(ctx, first.UserID)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("stale proof created a session: count=%d err=%v", len(sessions), err)
	}
	if !hasEventReason(f.audit, enum.EventSignInFailed, enum.ResultFailure, "CODE_INVALID") {
		t.Fatal("missing stale proof rejection audit")
	}
}

func TestReauthenticateRejectsIdentityRemovedWhileWaiting(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	first := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	p, err := f.svc.Authenticate(ctx, first.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SendReauthenticationCode(ctx, p, enum.IdentityPhone, phone1, meta1); err != nil {
		t.Fatal(err)
	}
	plain := f.sent.code(phone1)
	idents, err := f.repo.Q().ListActiveIdentitiesByUser(ctx, first.UserID)
	if err != nil || len(idents) != 1 {
		t.Fatalf("identities: %v", err)
	}
	tx := identityRaceLock(t, ctx, f, p.UserID, p.SessionID)
	done := make(chan error, 1)
	go func() {
		_, err := f.svc.Reauthenticate(ctx, p, enum.IdentityPhone, phone1, plain, meta1)
		done <- err
	}()
	waitIdentityRaceBlocked(t, ctx, f, tx, done)
	if _, err := db.New(tx).SoftDeleteIdentity(ctx, db.SoftDeleteIdentityParams{ID: idents[0].ID, UserID: first.UserID, Now: *f.clock}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, user.ErrNotAnchor) {
		t.Fatalf("stale identity must reject reauth with TARGET_NOT_ANCHOR, got %v", err)
	}
	sess, err := f.repo.Q().GetActiveSessionByIDAndUser(ctx, db.GetActiveSessionByIDAndUserParams{ID: p.SessionID, UserID: p.UserID})
	if err != nil || !sess.AuthTime.Equal(p.AuthTime) {
		t.Fatalf("stale proof advanced auth_time: %v", err)
	}
	if !hasEventReason(f.audit, enum.EventReauthenticationFailed, enum.ResultFailure, "NOT_ANCHOR") {
		t.Fatal("missing stale anchor rejection audit")
	}
}

func TestBulkSessionRevocationSerializesOnUser(t *testing.T) {
	for _, admin := range []bool{false, true} {
		name := "consumer"
		if admin {
			name = "admin"
		}
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			first := f.signIn(t, enum.IdentityPhone, phone1, dev1)
			p, err := f.svc.Authenticate(ctx, first.AccessToken)
			if err != nil {
				t.Fatal(err)
			}
			_ = f.signIn(t, enum.IdentityPhone, phone1, user.Device{ID: "other-device"})
			tx := identityRaceLock(t, ctx, f, p.UserID, "")
			done := make(chan error, 1)
			go func() {
				if admin {
					_, err := f.svc.AdminRevokeAllSessions(ctx, user.Admin{Subject: "admin"}, p.UserID, meta1)
					done <- err
				} else {
					done <- f.svc.RevokeOtherSessions(ctx, p.UserID, p.SessionID, meta1)
				}
			}()
			waitIdentityRaceBlocked(t, ctx, f, tx, done)
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			sessions, err := f.repo.Q().ListActiveSessionsByUser(ctx, p.UserID)
			want := 1
			if admin {
				want = 0
			}
			if err != nil || len(sessions) != want {
				t.Fatalf("remaining sessions=%d want=%d err=%v", len(sessions), want, err)
			}
		})
	}
}

func TestBulkSessionRevocationPreservesUserSemantics(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.svc.RevokeOtherSessions(ctx, "u_0000000000000", "s_0000000000000", meta1); err != nil {
		t.Fatalf("consumer missing user remains successful: %v", err)
	}
	if _, err := f.svc.AdminRevokeAllSessions(ctx, user.Admin{}, "u_0000000000000", meta1); !errors.Is(err, user.ErrNotFound) {
		t.Fatalf("admin missing user: %v", err)
	}
	first := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	p, err := f.svc.Authenticate(ctx, first.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.signIn(t, enum.IdentityPhone, phone1, user.Device{ID: "other-device"})
	mustExec(t, f, `UPDATE user_account SET state = 2 WHERE id = $1`, p.UserID)
	if err := f.svc.RevokeOtherSessions(ctx, p.UserID, p.SessionID, meta1); err != nil {
		t.Fatalf("consumer frozen user: %v", err)
	}
	if n, err := f.svc.AdminRevokeAllSessions(ctx, user.Admin{}, p.UserID, meta1); err != nil || n != 1 {
		t.Fatalf("admin frozen user: n=%d err=%v", n, err)
	}
}
