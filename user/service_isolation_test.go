package user_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/internal/userdb"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/code"
)

// Give every service connection the host default, including connections opened
// during the race. The original fixture still owns schema creation and cleanup.
func isolationFixture(t *testing.T, isolation string) *fixture {
	t.Helper()
	f := newFixture(t)
	cfg := f.pool.Config()
	cfg.ConnConfig.RuntimeParams["default_transaction_isolation"] = isolation
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	f.pool, f.repo = pool, user.NewRepo(pool)
	f.deps.Repo = f.repo
	f.svc, err = user.NewService(f.deps)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// These races catch inheriting a transaction-wide host snapshot: a row lock
// alone does not refresh that snapshot after the preceding mutation commits.
func TestServiceTransactionsUseReadCommitted(t *testing.T) {
	for _, isolation := range []string{"repeatable read", "serializable"} {
		t.Run(isolation, func(t *testing.T) {
			t.Run("unbind-before-sign-in", func(t *testing.T) {
				f := isolationFixture(t, isolation)
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				first := f.signIn(t, enum.IdentityPhone, phone1, dev1)
				p := principalOf(t, f, first)
				contextBindEmail(t, f, p, email1)
				idents, err := user.TestRepoQueries(f.repo).ListActiveIdentitiesByUser(ctx, p.UserID)
				if err != nil {
					t.Fatal(err)
				}
				var phoneID string
				for _, identity := range idents {
					if identity.Kind == enum.IdentityPhone {
						phoneID = identity.ID
					}
				}
				if phoneID == "" {
					t.Fatal("missing phone fixture")
				}
				if err := f.sendSignInCode(ctx, enum.IdentityPhone, phone1, meta1); err != nil {
					t.Fatal(err)
				}
				cred := f.credential(enum.PurposeSignIn, enum.IdentityPhone, phone1, f.sent.code(phone1))
				gate := replacementMutationGate(t, ctx, f, "identity", "UPDATE", fmt.Sprintf("NEW.id='%s' AND NEW.delete_time IS NOT NULL", phoneID))
				unbind := startReplacementRace(func() replacementRaceResult {
					return replacementRaceResult{err: f.svc.UnbindIdentity(ctx, p, phoneID, meta1)}
				})
				unbindPID := replacementWaitForPID(t, ctx, f, gate.Conn().PgConn().PID(), unbind)
				login := startReplacementRace(func() replacementRaceResult {
					_, err := f.svc.SignInWithCode(ctx, cred, user.Device{ID: "isolation-stale-proof"}, meta1)
					return replacementRaceResult{err: err}
				})
				replacementWaitForPID(t, ctx, f, unbindPID, login)
				if err := gate.Commit(ctx); err != nil {
					t.Fatal(err)
				}
				if out := finishReplacementRace(t, ctx, unbind); out.err != nil {
					t.Fatalf("unbind: %v", out.err)
				}
				out := finishReplacementRace(t, ctx, login)
				var staleSessions, staleSources int
				if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM session WHERE device_id='isolation-stale-proof'`).Scan(&staleSessions); err != nil {
					t.Fatal(err)
				}
				if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM session_authentication_identity a JOIN session s ON s.id=a.session_id WHERE s.device_id='isolation-stale-proof'`).Scan(&staleSources); err != nil {
					t.Fatal(err)
				}
				if !errors.Is(out.err, code.ErrInvalid) || staleSessions != 0 || staleSources != 0 {
					t.Fatalf("stale login: err=%v sessions=%d sources=%d; want CODE_INVALID and no new rows", out.err, staleSessions, staleSources)
				}
			})

			t.Run("last-binding-slot", func(t *testing.T) {
				f := isolationFixture(t, isolation)
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				p := principalOf(t, f, f.signIn(t, enum.IdentityPhone, phone1, dev1))
				const secondEmail = "second-isolation@example.test"
				firstCode := replacementCode(t, f, p, enum.IdentityEmail, email1)
				secondCode := replacementCode(t, f, p, enum.IdentityEmail, secondEmail)
				gate := replacementMutationGate(t, ctx, f, "identity", "INSERT", fmt.Sprintf("NEW.user_id='%s'", p.UserID))
				bind := func(target, plain string) replacementRaceResult {
					_, _, err := f.svc.BindWithCode(ctx, p, f.credential(enum.PurposeBind, enum.IdentityEmail, target, plain), meta1)
					return replacementRaceResult{err: err}
				}
				first := startReplacementRace(func() replacementRaceResult { return bind(email1, firstCode) })
				firstPID := replacementWaitForPID(t, ctx, f, gate.Conn().PgConn().PID(), first)
				second := startReplacementRace(func() replacementRaceResult { return bind(secondEmail, secondCode) })
				replacementWaitForPID(t, ctx, f, firstPID, second)
				if err := gate.Commit(ctx); err != nil {
					t.Fatal(err)
				}
				if out := finishReplacementRace(t, ctx, first); out.err != nil {
					t.Fatalf("first binding: %v", out.err)
				}
				out := finishReplacementRace(t, ctx, second)
				var count int
				if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM identity WHERE user_id=$1 AND kind=$2 AND delete_time IS NULL`, p.UserID, enum.IdentityEmail).Scan(&count); err != nil {
					t.Fatal(err)
				}
				if !errors.Is(out.err, user.ErrIdentityKindLimit) || count != 1 {
					t.Fatalf("second binding: err=%v count=%d; want IDENTITY_KIND_LIMIT and one EMAIL", out.err, count)
				}
			})

			t.Run("concurrent-refresh-pair", func(t *testing.T) {
				f := isolationFixture(t, isolation)
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				original := f.signIn(t, enum.IdentityPhone, phone1, dev1)
				p := principalOf(t, f, original)
				gate := replacementMutationGate(t, ctx, f, "session", "UPDATE", fmt.Sprintf("NEW.id='%s' AND NEW.rotate_time IS DISTINCT FROM OLD.rotate_time", p.SessionID))
				refresh := func() replacementRaceResult {
					pair, err := f.svc.Refresh(ctx, original.RefreshToken, meta1)
					return replacementRaceResult{token: pair, err: err}
				}
				first := startReplacementRace(refresh)
				firstPID := replacementWaitForPID(t, ctx, f, gate.Conn().PgConn().PID(), first)
				second := startReplacementRace(refresh)
				replacementWaitForPID(t, ctx, f, firstPID, second)
				if err := gate.Commit(ctx); err != nil {
					t.Fatal(err)
				}
				a, b := finishReplacementRace(t, ctx, first), finishReplacementRace(t, ctx, second)
				if a.err != nil || b.err != nil {
					t.Fatalf("refresh errors: first=%v second=%v", a.err, b.err)
				}
				if a.token.RefreshToken == "" || a.token.RefreshToken == original.RefreshToken || a.token.RefreshToken != b.token.RefreshToken || a.token.AccessToken != b.token.AccessToken {
					t.Fatal("concurrent refresh did not return the same rotated token pair")
				}
			})

			// The raw helper's host-default isolation is an internal invariant;
			// the domain operations above are the public READ COMMITTED contract.
			t.Run("internal-repo-host-default", func(t *testing.T) {
				f := isolationFixture(t, isolation)
				ctx := context.Background()
				first := f.signIn(t, enum.IdentityPhone, phone1, dev1)
				if err := user.TestRepoWithTxRaw(f.repo, ctx, func(tx pgx.Tx, _ *db.Queries) error {
					var got string
					if err := tx.QueryRow(ctx, `SHOW transaction_isolation`).Scan(&got); err != nil {
						return err
					}
					if got != isolation {
						return fmt.Errorf("internal withTxRaw isolation=%q want=%q", got, isolation)
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				// Both higher host levels must retain their first-read snapshot.
				if err := user.TestRepoWithTx(f.repo, ctx, func(q *db.Queries) error {
					before, err := q.GetUserByID(ctx, first.UserID)
					if err != nil {
						return err
					}
					if _, err := f.pool.Exec(ctx, `UPDATE user_account SET display_name='outside transaction' WHERE id=$1`, first.UserID); err != nil {
						return err
					}
					after, err := q.GetUserByID(ctx, first.UserID)
					if err != nil {
						return err
					}
					if before.DisplayName != nil || after.DisplayName != nil {
						return errors.New("internal withTx lost host snapshot isolation")
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}
