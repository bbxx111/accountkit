package user_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/internal/obstest"
	"github.com/bbxx111/accountkit/internal/userdb"
	ob "github.com/bbxx111/accountkit/observability"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/code"
)

func assertContextRevoked(t *testing.T, f *fixture, pair user.TokenResult, revoked bool, reason string) {
	t.Helper()
	ctx := context.Background()
	sess, err := user.TestRepoQueries(f.repo).GetSessionByRefreshHash(ctx, sha256Of(pair.RefreshToken))
	if err != nil {
		t.Fatal(err)
	}
	if (sess.RevokeTime != nil) != revoked || revoked && (sess.RevokeReason == nil || sess.RevokeReason.String() != reason) {
		t.Fatalf("revocation=%v reason=%v want revoked=%v reason=%s", sess.RevokeTime, sess.RevokeReason, revoked, reason)
	}
	if revoked {
		if _, err := f.svc.Refresh(ctx, pair.RefreshToken, meta1); !errors.Is(err, user.ErrInvalidGrant) {
			t.Fatalf("revoked refresh: %v", err)
		}
		if _, err := f.svc.Authenticate(ctx, pair.AccessToken); !errors.Is(err, user.ErrInvalidToken) {
			t.Fatalf("revoked access: %v", err)
		}
	}
}

func TestIdentityUnbindRevokesAuthenticationSessions(t *testing.T) {
	f, p, phoneID, current := replacementSetup(t, enum.IdentityPhone, phone1)
	ctx := context.Background()
	emailID := contextBindEmail(t, f, p, email1)
	f.advance(61 * time.Second)
	independent := f.signIn(t, enum.IdentityEmail, email1, user.Device{ID: "independent-other-source"})
	matched := f.signIn(t, enum.IdentityPhone, phone1, user.Device{ID: "matched-multiple"})
	mp := principalOf(t, f, matched)
	mustExec(t, f, `INSERT INTO session_authentication_identity(session_id,identity_id) VALUES ($1,$2)`, mp.SessionID, emailID)
	expired := f.signIn(t, enum.IdentityPhone, phone1, user.Device{ID: "expired-matched"})
	ep := principalOf(t, f, expired)
	mustExec(t, f, `UPDATE session SET refresh_expire_time=$2 WHERE id=$1`, ep.SessionID, *f.clock)
	foreign := f.signIn(t, enum.IdentityEmail, "foreign-context@example.test", user.Device{ID: "foreign-source"})
	// Fixture setup takes several minutes; refresh the proof without changing its source.
	p.AuthTime = *f.clock
	rotated, err := f.svc.Refresh(ctx, matched.RefreshToken, meta1)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.UnbindIdentity(ctx, p, phoneID, meta1); err != nil {
		t.Fatal(err)
	}
	for _, pair := range []user.TokenResult{current, rotated, expired} {
		assertContextRevoked(t, f, pair, true, "IDENTITY_UNBOUND")
	}
	if _, err := f.svc.Refresh(ctx, matched.RefreshToken, meta1); !errors.Is(err, user.ErrInvalidGrant) {
		t.Fatalf("cached grace revived: %v", err)
	}
	assertContextRevoked(t, f, independent, false, "")
	assertContextRevoked(t, f, foreign, false, "")
	authenticationSources(t, f, p, phoneID)
	if !hasEventReason(f.audit, enum.EventSessionRevoked, enum.ResultSuccess, "IDENTITY_UNBOUND") {
		t.Fatal("missing revocation audit")
	}
	for _, e := range f.audit.Events() {
		if e.Reason == "IDENTITY_UNBOUND" && (e.RequestID != meta1.RequestID || len(e.SubjectHint) > 8) {
			t.Fatalf("unsafe audit %+v", e)
		}
	}
}

func TestIdentityUnbindDomainGuards(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*testing.T, *fixture, *user.Principal)
		want   error
	}{
		{"scope", func(_ *testing.T, _ *fixture, p *user.Principal) { p.Scope = user.ScopeBind }, user.ErrInsufficientScope},
		{"stale", func(_ *testing.T, f *fixture, p *user.Principal) { p.AuthTime = f.clock.Add(-6 * time.Minute) }, user.ErrReauthenticationRequired},
		{"missing-auth", func(_ *testing.T, _ *fixture, p *user.Principal) { p.AuthTime = time.Time{} }, user.ErrReauthenticationRequired},
		{"missing-session", func(_ *testing.T, _ *fixture, p *user.Principal) { p.SessionID = "s_0000000000000" }, user.ErrInvalidToken},
		{"foreign-session", func(t *testing.T, f *fixture, p *user.Principal) {
			r := f.signIn(t, enum.IdentityEmail, "foreign-guard@example.test", dev2)
			p.SessionID = principalOf(t, f, r).SessionID
		}, user.ErrInvalidToken},
		{"expired-session", func(t *testing.T, f *fixture, p *user.Principal) {
			mustExec(t, f, `UPDATE session SET refresh_expire_time=$2 WHERE id=$1`, p.SessionID, *f.clock)
		}, user.ErrInvalidToken},
		{"revoked-session", func(t *testing.T, f *fixture, p *user.Principal) {
			mustExec(t, f, `UPDATE session SET revoke_time=$2 WHERE id=$1`, p.SessionID, *f.clock)
		}, user.ErrInvalidToken},
		{"frozen", func(t *testing.T, f *fixture, p *user.Principal) {
			mustExec(t, f, `UPDATE user_account SET state=2 WHERE id=$1`, p.UserID)
		}, user.ErrUserFrozen},
		{"pending", func(t *testing.T, f *fixture, p *user.Principal) {
			mustExec(t, f, `UPDATE user_account SET state=3 WHERE id=$1`, p.UserID)
		}, user.ErrInvalidToken},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, p, id, _ := replacementSetup(t, enum.IdentityPhone, phone1)
			contextBindEmail(t, f, p, email1)
			tc.change(t, f, &p)
			if err := f.svc.UnbindIdentity(context.Background(), p, id, meta1); !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
			var deleted bool
			if err := f.pool.QueryRow(context.Background(), `SELECT delete_time IS NOT NULL FROM identity WHERE id=$1`, id).Scan(&deleted); err != nil || deleted {
				t.Fatalf("guard deleted identity: %v", err)
			}
			if hasEvent(f.audit, enum.EventIdentityUnbound, enum.ResultSuccess) {
				t.Fatal("guard recorded success")
			}
		})
	}
	t.Run("disabled freshness retains session and scope guards", func(t *testing.T) {
		f, p, id, _ := replacementSetup(t, enum.IdentityPhone, phone1)
		contextBindEmail(t, f, p, email1)
		newServiceWith(t, f, func(d *user.Deps) { d.SensitiveOpVerification = replacementBool(false) })
		p.AuthTime = time.Time{}
		bad := p
		bad.Scope = user.ScopeBind
		if err := f.svc.UnbindIdentity(context.Background(), bad, id, meta1); !errors.Is(err, user.ErrInsufficientScope) {
			t.Fatalf("scope %v", err)
		}
		bad = p
		bad.SessionID = "s_0000000000000"
		if err := f.svc.UnbindIdentity(context.Background(), bad, id, meta1); !errors.Is(err, user.ErrInvalidToken) {
			t.Fatalf("session %v", err)
		}
		if err := f.svc.UnbindIdentity(context.Background(), p, id, meta1); err != nil {
			t.Fatal(err)
		}
	})
}

func TestReplacementCurrentAuthenticationSource(t *testing.T) {
	for _, source := range []string{"matched", "reauthenticated"} {
		t.Run(source, func(t *testing.T) {
			f, p, id, current := replacementSetup(t, enum.IdentityPhone, phone1)
			ctx := context.Background()
			emailID := contextBindEmail(t, f, p, email1)
			f.advance(61 * time.Second)
			if source == "reauthenticated" {
				if err := f.sendReauthenticationCode(ctx, p, enum.IdentityEmail, email1, meta1); err != nil {
					t.Fatal(err)
				}
				result, err := f.svc.Reauthenticate(ctx, p, f.credential(enum.PurposeReauth, enum.IdentityEmail, email1, f.sent.code(email1)), meta1)
				if err != nil {
					t.Fatal(err)
				}
				p = principalOf(t, f, result)
			}
			f.advance(61 * time.Second)
			other := f.signIn(t, enum.IdentityEmail, email1, user.Device{ID: "other-independent"})
			plain := replacementCode(t, f, p, enum.IdentityPhone, phone2)
			if _, err := f.svc.ReplaceIdentity(ctx, p, id, f.credential(enum.PurposeBind, enum.IdentityPhone, phone2, plain), meta1); err != nil {
				t.Fatal(err)
			}
			revoked := source != "reauthenticated"
			assertContextRevoked(t, f, current, revoked, "IDENTITY_REPLACED")
			assertContextRevoked(t, f, other, true, "IDENTITY_REPLACED")
			switch source {
			case "matched":
				authenticationSources(t, f, p, id)
			default:
				authenticationSources(t, f, p, emailID)
				out, err := f.svc.Refresh(ctx, current.RefreshToken, meta1)
				if err != nil {
					t.Fatalf("independent source refresh: %v", err)
				}
				fresh := principalOf(t, f, out)
				if !fresh.AuthTime.Equal(p.AuthTime) {
					t.Fatal("replacement modified auth_time")
				}
			}
		})
	}
}

// Hold a transaction inside the real first mutation, after its account/session locks.
// pg_blocking_pids proves the second operation is waiting on that mutation before release.
func contextMutationBarrier(t *testing.T, f *fixture, ctx context.Context, table, event string) pgx.Tx {
	t.Helper()
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	key := tx.Conn().PgConn().PID()
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(key)); err != nil {
		t.Fatal(err)
	}
	mustExec(t, f, fmt.Sprintf(`CREATE FUNCTION pause_context_mutation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(%d); RETURN NEW; END $$`, key))
	mustExec(t, f, fmt.Sprintf(`CREATE TRIGGER pause_context_mutation BEFORE %s ON %s FOR EACH ROW EXECUTE FUNCTION pause_context_mutation()`, event, table))
	return tx
}
func waitContextMutationChain(t *testing.T, f *fixture, ctx context.Context, tx pgx.Tx, done <-chan error) {
	t.Helper()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		var waiting bool
		err := f.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity a JOIN pg_stat_activity b ON b.pid=ANY(pg_blocking_pids(a.pid)) WHERE $1::int=ANY(pg_blocking_pids(b.pid)))`, tx.Conn().PgConn().PID()).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		select {
		case err := <-done:
			t.Fatalf("second mutation did not wait: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-tick.C:
		}
	}
}

func TestSessionContextConcurrentRevocation(t *testing.T) {
	for _, scenario := range []string{"reauth-before-unbind", "unbind-before-reauth", "login-before-unbind", "unbind-before-login", "refresh-before-unbind", "unbind-before-refresh", "replace-before-reauth", "reauth-before-replace", "revoke-before-reauth", "freeze-before-reauth", "delete-before-reauth"} {
		t.Run(scenario, func(t *testing.T) {
			f, p, id, current := replacementSetup(t, enum.IdentityPhone, phone1)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			emailID := contextBindEmail(t, f, p, email1)
			f.advance(61 * time.Second)
			p.AuthTime = *f.clock
			if err := f.sendReauthenticationCode(ctx, p, enum.IdentityEmail, email1, meta1); err != nil {
				t.Fatal(err)
			}
			reauthCred := f.credential(enum.PurposeReauth, enum.IdentityEmail, email1, f.sent.code(email1))
			reauth := func() error { _, err := f.svc.Reauthenticate(ctx, p, reauthCred, meta1); return err }
			unbind := func() error { return f.svc.UnbindIdentity(ctx, p, id, meta1) }
			refresh := func() error { _, err := f.svc.Refresh(ctx, current.RefreshToken, meta1); return err }
			plain := replacementCode(t, f, p, enum.IdentityPhone, phone2)
			replace := func() error {
				_, err := f.svc.ReplaceIdentity(ctx, p, id, f.credential(enum.PurposeBind, enum.IdentityPhone, phone2, plain), meta1)
				return err
			}
			var first, second func() error
			table, event := "identity", "UPDATE"
			var want error
			revoked := true
			var login user.TokenResult
			switch scenario {
			case "reauth-before-unbind":
				first, second = reauth, unbind
				table = "session"
				revoked = false
			case "unbind-before-reauth":
				first, second = unbind, reauth
				want = user.ErrInvalidToken
			case "refresh-before-unbind":
				first, second = refresh, unbind
				table = "session"
			case "unbind-before-refresh":
				first, second = unbind, refresh
				want = user.ErrInvalidGrant
			case "replace-before-reauth":
				first, second = replace, reauth
				want = user.ErrInvalidToken
			case "reauth-before-replace":
				first, second = reauth, replace
				table = "session"
				revoked = false
			case "revoke-before-reauth":
				first = func() error { return f.svc.Revoke(ctx, current.RefreshToken, meta1) }
				second = reauth
				table = "session"
				want = user.ErrInvalidToken
			case "freeze-before-reauth":
				first = func() error { _, err := f.svc.Freeze(ctx, admin1, p.UserID, "context race", meta1); return err }
				second = reauth
				table = "user_account"
				want = user.ErrInvalidToken
			case "delete-before-reauth":
				first = func() error { _, err := f.svc.DeleteMe(ctx, p, meta1); return err }
				second = reauth
				table = "user_account"
				want = user.ErrInvalidToken
			case "login-before-unbind", "unbind-before-login":
				if err := f.sendSignInCode(ctx, enum.IdentityPhone, phone1, meta1); err != nil {
					t.Fatal(err)
				}
				cred := f.credential(enum.PurposeSignIn, enum.IdentityPhone, phone1, f.sent.code(phone1))
				first = func() error {
					r, err := f.svc.SignInWithCode(ctx, cred, user.Device{ID: "race-login"}, meta1)
					if err == nil {
						login = *r.Tokens
					}
					return err
				}
				second = unbind
				table, event = "session", "INSERT"
				if scenario == "unbind-before-login" {
					first, second = unbind, first
					table, event = "identity", "UPDATE"
					want = code.ErrInvalid
				}
			}
			barrier := contextMutationBarrier(t, f, ctx, table, event)
			a, b := make(chan error, 1), make(chan error, 1)
			go func() { a <- first() }()
			waitIdentityRaceBlocked(t, ctx, f, barrier, a)
			go func() { b <- second() }()
			waitContextMutationChain(t, f, ctx, barrier, b)
			if err := barrier.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-a; err != nil {
				t.Fatalf("first mutation: %v", err)
			}
			if err := <-b; !errors.Is(err, want) {
				t.Fatalf("second mutation: %v want %v", err, want)
			}
			sess, err := user.TestRepoQueries(f.repo).LockSessionByID(ctx, p.SessionID)
			if err != nil || (sess.RevokeTime != nil) != revoked {
				t.Fatalf("final revocation=%v want=%v err=%v", sess.RevokeTime, revoked, err)
			}
			if !revoked {
				authenticationSources(t, f, p, emailID)
			}
			if revoked {
				if _, err := f.svc.Refresh(ctx, current.RefreshToken, meta1); !errors.Is(err, user.ErrInvalidGrant) {
					t.Fatalf("revived refresh %v", err)
				}
			}
			if login.RefreshToken != "" {
				assertContextRevoked(t, f, login, true, "IDENTITY_UNBOUND")
			}
		})
	}
}

func TestIdentityUnbindRollbackAndRedisFailure(t *testing.T) {
	for _, stage := range []string{"delete", "revoke", "redis"} {
		t.Run(stage, func(t *testing.T) {
			f, p, id, current := replacementSetup(t, enum.IdentityPhone, phone1)
			contextBindEmail(t, f, p, email1)
			ctx := context.Background()
			var logs bytes.Buffer
			if stage == "redis" {
				newServiceWith(t, f, func(d *user.Deps) { d.Logger = slog.New(slog.NewTextHandler(&logs, nil)) })
				f.mr.SetError("synthetic redis unavailable")
			} else {
				mustExec(t, f, `CREATE FUNCTION fail_unbind_context() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic rollback'; END $$`)
				table := "identity"
				if stage == "revoke" {
					table = "session"
				}
				mustExec(t, f, fmt.Sprintf(`CREATE TRIGGER fail_unbind_context BEFORE UPDATE ON %s FOR EACH ROW EXECUTE FUNCTION fail_unbind_context()`, table))
			}
			err := f.svc.UnbindIdentity(ctx, p, id, meta1)
			var deleted, revoked bool
			if e := f.pool.QueryRow(ctx, `SELECT delete_time IS NOT NULL FROM identity WHERE id=$1`, id).Scan(&deleted); e != nil {
				t.Fatal(e)
			}
			if e := f.pool.QueryRow(ctx, `SELECT revoke_time IS NOT NULL FROM session WHERE id=$1`, p.SessionID).Scan(&revoked); e != nil {
				t.Fatal(e)
			}
			if stage == "redis" {
				if err != nil || !deleted || !revoked {
					t.Fatalf("committed operation: deleted=%v revoked=%v err=%v", deleted, revoked, err)
				}
				if _, err := f.svc.Refresh(ctx, current.RefreshToken, meta1); !errors.Is(err, user.ErrInvalidGrant) {
					t.Fatalf("database revocation: %v", err)
				}
				if _, err := f.svc.Authenticate(ctx, current.AccessToken); err != nil {
					t.Fatalf("access fail-open changed: %v", err)
				}
				if !strings.Contains(logs.String(), "revocation set write failed (fail-open)") || !strings.Contains(logs.String(), "revocation check unavailable (fail-open)") {
					t.Fatal("missing warning")
				}
			} else {
				if err == nil || deleted || revoked {
					t.Fatalf("rollback failed: deleted=%v revoked=%v err=%v", deleted, revoked, err)
				}
				if hasEvent(f.audit, enum.EventIdentityUnbound, enum.ResultSuccess) || hasEvent(f.audit, enum.EventSessionRevoked, enum.ResultSuccess) {
					t.Fatal("rolled back success audit")
				}
			}
		})
	}
}

func TestIdentityUnbindRejectsCompletedFlowReplay(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	pending := pendingFlow(t, f, "revoked-completion")
	cred := completionCredential(t, f, pending, enum.IdentityEmail, email1)
	result, err := f.svc.CompleteSignIn(ctx, pending.SignInCompletionToken, cred, user.Device{}, meta1)
	if err != nil {
		t.Fatal(err)
	}
	pair := requireSignInTokens(t, result)
	p := principalOf(t, f, pair)
	rows, err := user.TestRepoQueries(f.repo).ListActiveIdentitiesByUser(ctx, p.UserID)
	if err != nil {
		t.Fatal(err)
	}
	var target string
	for _, r := range rows {
		if r.Kind == enum.IdentityWeChat {
			target = r.ID
		}
	}
	if target == "" {
		t.Fatal("missing pending identity")
	}
	if err := f.svc.UnbindIdentity(ctx, p, target, meta1); err != nil {
		t.Fatal(err)
	}
	assertContextRevoked(t, f, pair, true, "IDENTITY_UNBOUND")
	replay, err := f.svc.CompleteSignIn(ctx, pending.SignInCompletionToken, cred, user.Device{}, meta1)
	if !errors.Is(err, user.ErrSignInAlreadyCompleted) || replay.Tokens != nil {
		t.Fatalf("completion revived: token_present=%v err=%v", replay.Tokens != nil, err)
	}
	authenticationSources(t, f, p, rows[0].ID, rows[1].ID)
}

func contextReauthenticate(t *testing.T, f *fixture, p user.Principal, kind enum.IdentityKind, target string) user.Principal {
	t.Helper()
	f.advance(61 * time.Second)
	ctx := context.Background()
	if err := f.sendReauthenticationCode(ctx, p, kind, target, meta1); err != nil {
		t.Fatal(err)
	}
	result, err := f.svc.Reauthenticate(ctx, p, f.credential(enum.PurposeReauth, kind, target, f.sent.code(normalizedTarget(kind, target))), meta1)
	if err != nil {
		t.Fatal(err)
	}
	return principalOf(t, f, result)
}

func TestSessionContextPendingUnbindOrder(t *testing.T) {
	for _, completeFirst := range []bool{true, false} {
		t.Run(fmt.Sprintf("complete-first=%v", completeFirst), func(t *testing.T) {
			f, p, _, _ := replacementSetup(t, enum.IdentityPhone, phone1)
			emailID := contextBindEmail(t, f, p, email1)
			f.advance(61 * time.Second)
			pendingResult, err := f.svc.SignInWithIdp(context.Background(), wechatCred("wx1", "union:pending-unbind-order@openid"), user.Device{ID: "separate-pending-device"}, meta1)
			if err != nil || pendingResult.Pending == nil {
				t.Fatalf("pending: %v", err)
			}
			pending := pendingResult.Pending
			cred := completionCredential(t, f, pending, enum.IdentityEmail, email1)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			var completed user.TokenResult
			complete := func() error {
				r, err := f.svc.CompleteSignIn(ctx, pending.SignInCompletionToken, cred, user.Device{}, meta1)
				if err == nil {
					completed = *r.Tokens
				}
				return err
			}
			unbind := func() error { return f.svc.UnbindIdentity(ctx, p, emailID, meta1) }
			first, second := complete, unbind
			table, event := "session", "INSERT"
			if !completeFirst {
				first, second = unbind, complete
				table, event = "identity", "UPDATE"
			}
			barrier := contextMutationBarrier(t, f, ctx, table, event)
			a, b := make(chan error, 1), make(chan error, 1)
			go func() { a <- first() }()
			waitIdentityRaceBlocked(t, ctx, f, barrier, a)
			go func() { b <- second() }()
			waitContextMutationChain(t, f, ctx, barrier, b)
			if err := barrier.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-a; err != nil {
				t.Fatalf("first: %v", err)
			}
			err = <-b
			if completeFirst {
				if err != nil {
					t.Fatal(err)
				}
				assertContextRevoked(t, f, completed, true, "IDENTITY_UNBOUND")
			} else {
				if !errors.Is(err, user.ErrSignInCompletionInvalid) {
					t.Fatalf("stale pending proof: %v", err)
				}
				if completed.AccessToken != "" {
					t.Fatal("stale pending produced credentials")
				}
			}
		})
	}
}

func TestIdentityUnbindFreshnessAfterWait(t *testing.T) {
	for _, stage := range []string{"user", "session"} {
		for _, expiredSession := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/session-expired=%v", stage, expiredSession), func(t *testing.T) {
				f, p, id, _ := replacementSetup(t, enum.IdentityPhone, phone1)
				contextBindEmail(t, f, p, email1)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				expiry := f.clock.Add(time.Minute)
				mustExec(t, f, `UPDATE session SET refresh_expire_time=$2 WHERE id=$1`, p.SessionID, expiry)
				clock := reauthAtomicClock(t, f, *f.clock, true)
				barrier := reauthLock(t, ctx, f, p, stage)
				done := make(chan error, 1)
				go func() { done <- f.svc.UnbindIdentity(ctx, p, id, meta1) }()
				waitIdentityRaceBlocked(t, ctx, f, barrier, done)
				want := user.ErrReauthenticationRequired
				next := p.AuthTime.Add(5*time.Minute + time.Nanosecond)
				if expiredSession {
					next = expiry
					want = user.ErrInvalidToken
				} else {
					if _, err := barrier.Exec(ctx, `UPDATE session SET refresh_expire_time=$2 WHERE id=$1`, p.SessionID, expiry.Add(time.Hour)); err != nil {
						t.Fatal(err)
					}
				}
				clock.Store(next.UnixNano())
				if err := barrier.Commit(ctx); err != nil {
					t.Fatal(err)
				}
				if err := <-done; !errors.Is(err, want) {
					t.Fatalf("locked decision: %v want %v", err, want)
				}
				var deleted, revoked bool
				if err := f.pool.QueryRow(ctx, `SELECT i.delete_time IS NOT NULL,s.revoke_time IS NOT NULL FROM identity i CROSS JOIN session s WHERE i.id=$1 AND s.id=$2`, id, p.SessionID).Scan(&deleted, &revoked); err != nil || deleted || revoked {
					t.Fatalf("wait rejection changed state: %v", err)
				}
			})
		}
	}
}

func TestIdentityUnbindAuditFailureAndObservations(t *testing.T) {
	f, p, id, current := replacementSetup(t, enum.IdentityPhone, phone1)
	contextBindEmail(t, f, p, email1)
	ctx := context.Background()
	var logs bytes.Buffer
	mustExec(t, f, `CREATE FUNCTION fail_unbind_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic audit outage'; END $$`)
	mustExec(t, f, `CREATE TRIGGER fail_unbind_audit BEFORE INSERT ON audit_event FOR EACH ROW EXECUTE FUNCTION fail_unbind_audit()`)
	async := audit.NewAsync(audit.NewStore(f.pool), audit.AsyncOptions{QueueSize: 64, BatchSize: 1, FlushInterval: time.Millisecond, Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	async.Start(ctx)
	defer async.Close()
	r := &obstest.Recorder{}
	newServiceWith(t, f, func(d *user.Deps) { d.Audit = async; d.Observer = r })
	if err := f.svc.UnbindIdentity(ctx, p, id, meta1); err != nil {
		t.Fatal(err)
	}
	async.Close()
	if async.Stats().Failed == 0 {
		t.Fatal("audit outage not exercised")
	}
	deps := dependencyValues(r, ob.ComponentRevocation, ob.DependencyRevoke)
	if len(deps) != 1 || deps[0].Result != ob.ResultSuccess {
		t.Fatalf("bounded revoke observation: %+v", deps)
	}
	assertContextRevoked(t, f, current, true, "IDENTITY_UNBOUND")
	r.Reset()
	if _, err := f.svc.Authenticate(ctx, current.AccessToken); !errors.Is(err, user.ErrInvalidToken) {
		t.Fatal(err)
	}
	requireAuth(t, r, ob.AuthAuthenticate, ob.ResultRejected, ob.ReasonSessionRevoked, ob.GrantNone, ob.RefreshNone)
	for _, secret := range []string{phone1, email1, current.AccessToken, current.RefreshToken} {
		if strings.Contains(logs.String(), secret) {
			t.Fatal("audit failure leaked secret")
		}
	}
}

func TestIdentityUnbindSameKindSources(t *testing.T) {
	f, p, id, current := replacementSetup(t, enum.IdentityEmail, email1)
	newServiceWith(t, f, func(d *user.Deps) { d.MaxIdentitiesPerKind = 2 })
	const otherEmail = "retained-same-kind@example.test"
	otherID := contextBindEmail(t, f, p, otherEmail)
	f.advance(61 * time.Second)
	other := f.signIn(t, enum.IdentityEmail, otherEmail, user.Device{ID: "same-kind-independent"})
	if err := f.svc.UnbindIdentity(context.Background(), p, id, meta1); err != nil {
		t.Fatal(err)
	}
	assertContextRevoked(t, f, current, true, "IDENTITY_UNBOUND")
	assertContextRevoked(t, f, other, false, "")
	authenticationSources(t, f, principalOf(t, f, other), otherID)
}

func TestIdentityUnbindChecksUnrevokedSourceInvariants(t *testing.T) {
	for _, state := range []string{"active", "expired", "revoked", "foreign"} {
		t.Run(state, func(t *testing.T) {
			f, p, phoneID, current := replacementSetup(t, enum.IdentityPhone, phone1)
			ctx := context.Background()
			contextBindEmail(t, f, p, email1)
			f.advance(61 * time.Second)
			target := email1
			if state == "foreign" {
				target = "foreign-missing-source@example.test"
			}
			other := f.signIn(t, enum.IdentityEmail, target, user.Device{ID: "missing-source-other"})
			op := principalOf(t, f, other)
			mustExec(t, f, `DELETE FROM session_authentication_identity WHERE session_id=$1`, op.SessionID)
			if state == "expired" {
				mustExec(t, f, `UPDATE session SET refresh_expire_time=$2 WHERE id=$1`, op.SessionID, *f.clock)
			}
			if state == "revoked" {
				mustExec(t, f, `UPDATE session SET revoke_time=$2 WHERE id=$1`, op.SessionID, *f.clock)
			}
			p.AuthTime = *f.clock
			err := f.svc.UnbindIdentity(ctx, p, phoneID, meta1)
			if state == "active" || state == "expired" {
				if err == nil || errors.Is(err, user.ErrInvalidToken) || errors.Is(err, user.ErrUnavailable) {
					t.Fatalf("other unrevoked session missing sources must reject as internal invariant: %v", err)
				}
				if _, err := user.TestRepoQueries(f.repo).GetActiveIdentityByIDAndUser(ctx, db.GetActiveIdentityByIDAndUserParams{ID: phoneID, UserID: p.UserID}); err != nil {
					t.Fatalf("rejected unbind changed identity: %v", err)
				}
				assertContextRevoked(t, f, current, false, "")
			} else {
				if err != nil {
					t.Fatalf("irrelevant missing sources blocked unbind: %v", err)
				}
				assertContextRevoked(t, f, current, true, "IDENTITY_UNBOUND")
			}
		})
	}
}
