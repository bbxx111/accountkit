package user_test

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/code"
	"github.com/bbxx111/accountkit/user/db"
)

// Only the service clock advances concurrently. The code-store fixture clock is
// left untouched, so consuming a valid proof cannot race a mutable time pointer.
func reauthAtomicClock(t *testing.T, f *fixture, at time.Time, sensitive bool) *atomic.Int64 {
	t.Helper()
	nanos := &atomic.Int64{}
	nanos.Store(at.UnixNano())
	deps := f.deps
	deps.Now = func() time.Time { return time.Unix(0, nanos.Load()).In(time.FixedZone("expiry-test", 8*3600)) }
	deps.SensitiveOpVerification = &sensitive
	var err error
	f.svc, err = user.NewService(deps)
	if err != nil {
		t.Fatal(err)
	}
	return nanos
}

func reauthSession(t *testing.T, ctx context.Context, f *fixture, p user.Principal) db.Session {
	t.Helper()
	sess, err := f.repo.Q().GetActiveSessionByIDAndUser(ctx, db.GetActiveSessionByIDAndUserParams{ID: p.SessionID, UserID: p.UserID})
	if err != nil {
		t.Fatal(err)
	}
	return sess
}

func reauthLock(t *testing.T, ctx context.Context, f *fixture, p user.Principal, table string) pgx.Tx {
	t.Helper()
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	})
	if table == "user" {
		_, err = db.New(tx).LockUserByID(ctx, p.UserID)
	} else {
		_, err = db.New(tx).LockSessionByID(ctx, p.SessionID)
	}
	if err != nil {
		t.Fatal(err)
	}
	return tx
}

// Begin blocking the final identity read only after the preflight proof and
// user lock are complete, so a premature clock sample after the session lock
// but before required reads is observable without adding production hooks.
func reauthIdentityReadGate(t *testing.T, ctx context.Context, f *fixture, sessionGate pgx.Tx, done <-chan replacementRaceResult) pgx.Tx {
	t.Helper()
	gate, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = gate.Rollback(cleanup)
	})
	if _, err := gate.Exec(ctx, "LOCK TABLE identity IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	if err := sessionGate.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	replacementWaitForPID(t, ctx, f, gate.Conn().PgConn().PID(), done)
	return gate
}

// Removing lock-time expiry checks would revive these rows; merely adding SQL
// guards still fails the audit classification and the unchanged-row assertions.
func TestReauthenticateExpiryAfterLocks(t *testing.T) {
	for _, sensitive := range []bool{true, false} {
		for _, stage := range []string{"entry-before", "entry-equal", "user", "session", "identity"} {
			name := stage + "/sensitive-on"
			if !sensitive {
				name = stage + "/sensitive-off"
			}
			t.Run(name, func(t *testing.T) {
				f, p, _, login := replacementSetup(t, enum.IdentityPhone, phone1)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				entry := f.clock.UTC().Add(123 * time.Microsecond)
				expiry := entry
				if stage == "entry-before" {
					expiry = entry.Add(-time.Microsecond)
				} else if stage == "user" || stage == "session" || stage == "identity" {
					expiry = entry.Add(time.Second)
				}
				if _, err := f.pool.Exec(ctx, "UPDATE session SET refresh_expire_time=$2 WHERE id=$1", p.SessionID, expiry); err != nil {
					t.Fatal(err)
				}
				before := reauthSession(t, ctx, f, p)
				nanos := reauthAtomicClock(t, f, entry.Add(999*time.Nanosecond), sensitive)
				if err := f.svc.SendReauthenticationCode(ctx, p, enum.IdentityPhone, phone1, meta1); err != nil {
					t.Fatal(err)
				}
				plain := f.sent.code(phone1)
				eventStart := len(f.audit.Events())
				invoke := func() replacementRaceResult {
					out, err := f.svc.Reauthenticate(ctx, p, enum.IdentityPhone, phone1, plain, meta1)
					return replacementRaceResult{token: out, err: err}
				}
				var out replacementRaceResult
				if stage == "user" || stage == "session" || stage == "identity" {
					gate := reauthLock(t, ctx, f, p, stage)
					done := startReplacementRace(invoke)
					replacementWaitForPID(t, ctx, f, gate.Conn().PgConn().PID(), done)
					if stage == "identity" {
						gate = reauthIdentityReadGate(t, ctx, f, gate, done)
					}
					if err := f.deps.Codes.Verify(ctx, enum.IdentityPhone, enum.PurposeReauth, phone1, plain); !errors.Is(err, code.ErrExpired) {
						t.Fatalf("proof not consumed before lock wait: %v", err)
					}
					nanos.Store(expiry.UnixNano())
					if err := gate.Commit(ctx); err != nil {
						t.Fatal(err)
					}
					out = finishReplacementRace(t, ctx, done)
				} else {
					out = invoke()
				}
				if !errors.Is(out.err, user.ErrInvalidToken) || out.token != (user.TokenResult{}) {
					t.Errorf("expired reauth returned tokens or wrong error: error=%v access_present=%v", out.err, out.token.AccessToken != "")
				}
				if after := reauthSession(t, ctx, f, p); !reflect.DeepEqual(before, after) {
					t.Error("expired reauth changed stored session")
				}
				if err := f.deps.Codes.Verify(ctx, enum.IdentityPhone, enum.PurposeReauth, phone1, plain); !errors.Is(err, code.ErrExpired) {
					t.Errorf("expiry rejection restored proof: %v", err)
				}
				events := f.audit.Events()[eventStart:]
				if len(events) != 1 || events[0].Type != enum.EventReauthenticationFailed || events[0].Result != enum.ResultFailure || events[0].Reason != "SESSION_EXPIRED" || events[0].SessionID != p.SessionID || events[0].RequestID != meta1.RequestID {
					t.Errorf("want one correlated SESSION_EXPIRED audit, got %+v", events)
				}
				if revoked, err := f.deps.Revocation.IsRevoked(ctx, p.SessionID); err != nil || revoked {
					t.Errorf("natural expiry changed revocation set: %v %v", revoked, err)
				}
				if _, err := f.svc.Authenticate(ctx, login.AccessToken); err != nil {
					t.Errorf("natural expiry invalidated existing access: %v", err)
				}
			})
		}
	}
}

// A stale pre-lock timestamp fails exact auth/update times and JWT issuance,
// while extending the refresh deadline or returning a refresh fails separately.
func TestReauthenticateUsesLockedDecisionTime(t *testing.T) {
	for _, stage := range []string{"entry", "user", "session", "identity"} {
		t.Run(stage, func(t *testing.T) {
			f, p, _, _ := replacementSetup(t, enum.IdentityPhone, phone1)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			entry := f.clock.UTC()
			decision := entry.Add(37*time.Second + 123*time.Microsecond)
			if stage == "entry" {
				entry = decision
			}
			// The session is valid by exactly one microsecond at the normalized
			// decision time. Submicrosecond application time must not round up.
			if _, err := f.pool.Exec(ctx, "UPDATE session SET refresh_expire_time=$2 WHERE id=$1", p.SessionID, decision.Add(time.Microsecond)); err != nil {
				t.Fatal(err)
			}
			before := reauthSession(t, ctx, f, p)
			nanos := reauthAtomicClock(t, f, entry.Add(999*time.Nanosecond), true)
			if err := f.svc.SendReauthenticationCode(ctx, p, enum.IdentityPhone, phone1, meta1); err != nil {
				t.Fatal(err)
			}
			plain := f.sent.code(phone1)
			invoke := func() replacementRaceResult {
				out, err := f.svc.Reauthenticate(ctx, p, enum.IdentityPhone, phone1, plain, meta1)
				return replacementRaceResult{token: out, err: err}
			}
			var out replacementRaceResult
			if stage == "entry" {
				out = invoke()
			} else {
				gate := reauthLock(t, ctx, f, p, stage)
				done := startReplacementRace(invoke)
				replacementWaitForPID(t, ctx, f, gate.Conn().PgConn().PID(), done)
				if stage == "identity" {
					gate = reauthIdentityReadGate(t, ctx, f, gate, done)
				}
				nanos.Store(decision.Add(999 * time.Nanosecond).UnixNano())
				if err := gate.Commit(ctx); err != nil {
					t.Fatal(err)
				}
				out = finishReplacementRace(t, ctx, done)
			}
			if out.err != nil {
				t.Fatal(out.err)
			}
			after := reauthSession(t, ctx, f, p)
			if !after.AuthTime.Equal(decision) || !after.UpdateTime.Equal(decision) {
				t.Errorf("auth_time=%s update_time=%s want locked UTC microsecond time %s", after.AuthTime, after.UpdateTime, decision)
			}
			unchanged := after
			unchanged.AuthTime, unchanged.UpdateTime = before.AuthTime, before.UpdateTime
			if !reflect.DeepEqual(before, unchanged) {
				t.Error("valid reauth changed fields beyond auth_time/update_time")
			}
			claims, err := f.deps.Signer.Parse(out.token.AccessToken)
			if err != nil || !claims.AuthTime.Equal(decision.Truncate(time.Second)) || !claims.IssuedAt.Equal(decision.Truncate(time.Second)) || !claims.ExpiresAt.Equal(decision.Truncate(time.Second).Add(15*time.Minute)) {
				t.Errorf("reauth claims use wrong decision time: %+v %v", claims, err)
			}
			if out.token.RefreshToken != "" || out.token.RefreshExpiresIn != 0 || out.token.ExpiresIn != 900 || out.token.UserID != p.UserID || out.token.Scope != user.ScopeUser {
				t.Error("reauth token response changed access-only contract")
			}
		})
	}
}

// The replacement decision must use the same UTC microsecond timestamp for
// expiry and mutation, rather than allowing PostgreSQL to round nanoseconds.
func TestReplaceIdentityExpiryMicroseconds(t *testing.T) {
	f, p, id, _ := replacementSetup(t, enum.IdentityPhone, phone1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	decision := f.clock.UTC().Add(123 * time.Microsecond)
	if _, err := f.pool.Exec(ctx, "UPDATE session SET refresh_expire_time=$2 WHERE id=$1", p.SessionID, decision.Add(time.Microsecond)); err != nil {
		t.Fatal(err)
	}
	before := reauthSession(t, ctx, f, p)
	reauthAtomicClock(t, f, decision.Add(999*time.Nanosecond), true)
	plain := replacementCode(t, f, p, enum.IdentityPhone, phone2)
	_, err := f.svc.ReplaceIdentity(ctx, p, id, enum.IdentityPhone, phone2, plain, meta1)
	if err != nil {
		t.Fatalf("replacement remains valid before normalized expiry: %v", err)
	}
	if after := reauthSession(t, ctx, f, p); !reflect.DeepEqual(before, after) {
		t.Fatal("replacement changed preserved current session")
	}
	var deletedAt time.Time
	if err := f.pool.QueryRow(ctx, "SELECT delete_time FROM identity WHERE id=$1", id).Scan(&deletedAt); err != nil || !deletedAt.Equal(decision) {
		t.Fatalf("replacement deletion time=%s want UTC microsecond time %s: %v", deletedAt, decision, err)
	}
}

func TestReplaceIdentityExpiryAfterLocks(t *testing.T) {
	for _, sensitive := range []bool{true, false} {
		for _, stage := range []string{"user", "session"} {
			name := stage + "/sensitive-on"
			if !sensitive {
				name = stage + "/sensitive-off"
			}
			t.Run(name, func(t *testing.T) {
				f, p, id, _ := replacementSetup(t, enum.IdentityPhone, phone1)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				entry := f.clock.UTC()
				expiry := entry.Add(time.Second)
				if _, err := f.pool.Exec(ctx, "UPDATE session SET refresh_expire_time=$2 WHERE id=$1", p.SessionID, expiry); err != nil {
					t.Fatal(err)
				}
				before := reauthSession(t, ctx, f, p)
				nanos := reauthAtomicClock(t, f, entry, sensitive)
				plain := replacementCode(t, f, p, enum.IdentityPhone, phone2)
				gate := reauthLock(t, ctx, f, p, stage)
				done := startReplacementRace(func() replacementRaceResult {
					out, err := f.svc.ReplaceIdentity(ctx, p, id, enum.IdentityPhone, phone2, plain, meta1)
					return replacementRaceResult{identity: out, err: err}
				})
				replacementWaitForPID(t, ctx, f, gate.Conn().PgConn().PID(), done)
				nanos.Store(expiry.UnixNano())
				if err := gate.Commit(ctx); err != nil {
					t.Fatal(err)
				}
				out := finishReplacementRace(t, ctx, done)
				if !errors.Is(out.err, user.ErrInvalidToken) || out.identity.ID != "" {
					t.Fatalf("expired replacement: %v identity=%s", out.err, out.identity.ID)
				}
				if after := reauthSession(t, ctx, f, p); !reflect.DeepEqual(before, after) {
					t.Error("expired replacement changed stored session")
				}
				identities, err := f.svc.ListIdentities(ctx, p.UserID)
				if err != nil || len(identities) != 1 || identities[0].ID != id {
					t.Errorf("expired replacement changed identity: %v", err)
				}
				if err := f.deps.Codes.Verify(ctx, enum.IdentityPhone, enum.PurposeBind, phone2, plain); !errors.Is(err, code.ErrExpired) {
					t.Errorf("expired replacement restored consumed code: %v", err)
				}
				if !hasEventReason(f.audit, enum.EventIdentityReplaceRejected, enum.ResultFailure, "TOKEN_INVALID") {
					t.Error("replacement changed existing TOKEN_INVALID audit contract")
				}
			})
		}
	}
}
