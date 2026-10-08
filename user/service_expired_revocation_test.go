package user_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
)

func TestExpiredSessionsRemainRevocable(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reason enum.RevokeReason
		all    bool
	}{
		{"consumer-single", enum.RevokeUserRevokedDevice, false},
		{"admin-single", enum.RevokeAdmin, false},
		{"consumer-revokeOthers", enum.RevokeUserRevokedDevice, false},
		{"admin-revokeAll", enum.RevokeAdmin, true},
		{"rfc7009", enum.RevokeUserLogout, false},
		{"same-device-relogin", enum.RevokeReplacedByRelogin, false},
		{"freeze", enum.RevokeUserFrozen, true},
		{"delete", enum.RevokeUserDeleted, true},
		{"identity-replacement", enum.RevokeIdentityReplaced, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			current := f.signIn(t, enum.IdentityEmail, email1, dev1)
			p := principalOf(t, f, current)
			otherDevice := user.Device{ID: "expired-revocation-other"}
			old := f.signIn(t, enum.IdentityEmail, email1, otherDevice)
			oldPrincipal := principalOf(t, f, old)
			foreign := f.signIn(t, enum.IdentityEmail, "foreign-expiry@example.test", user.Device{ID: "foreign-expiry"})
			foreignPrincipal := principalOf(t, f, foreign)
			// Keep the issued access token valid while making refresh exactly expired.
			if _, err := f.pool.Exec(ctx, "UPDATE session SET refresh_expire_time=$2 WHERE id=$1", oldPrincipal.SessionID, f.clock.UTC().Truncate(time.Microsecond)); err != nil {
				t.Fatal(err)
			}
			if _, err := f.svc.Authenticate(ctx, old.AccessToken); err != nil {
				t.Fatal("expired refresh invalidated access:", err)
			}
			beforeEvents := len(f.audit.Events())
			var err error
			switch tc.name {
			case "consumer-single":
				err = f.svc.RevokeSession(ctx, p.UserID, oldPrincipal.SessionID, meta1)
			case "admin-single":
				err = f.svc.AdminRevokeSession(ctx, admin1, p.UserID, oldPrincipal.SessionID, meta1)
			case "consumer-revokeOthers":
				err = f.svc.RevokeOtherSessions(ctx, p.UserID, p.SessionID, meta1)
			case "admin-revokeAll":
				var n int
				n, err = f.svc.AdminRevokeAllSessions(ctx, admin1, p.UserID, meta1)
				if n != 2 {
					t.Errorf("revoked_count=%d want=2 including expired row", n)
				}
			case "rfc7009":
				err = f.svc.Revoke(ctx, old.RefreshToken, meta1)
			case "same-device-relogin":
				if err = f.sendSignInCode(ctx, enum.IdentityEmail, email1, meta1); err == nil {
					var pair user.TokenResult
					pair, err = f.svc.SignInWithCode(ctx, f.credential(enum.PurposeSignIn, enum.IdentityEmail, email1, f.sent.code(email1)), otherDevice, meta1)
					if err == nil && pair.UserID != p.UserID {
						t.Error("relogin changed user")
					}
					if err == nil {
						if _, authErr := f.svc.Authenticate(ctx, pair.AccessToken); authErr != nil {
							t.Fatal("new relogin access rejected:", authErr)
						}
					}
				}
			case "freeze":
				_, err = f.svc.Freeze(ctx, admin1, p.UserID, "expiry coverage", meta1)
			case "delete":
				_, err = f.svc.DeleteMe(ctx, p, meta1)
			case "identity-replacement":
				var identities []user.IdentityInfo
				identities, err = f.svc.ListIdentities(ctx, p.UserID)
				if err == nil {
					err = f.sendBindCode(ctx, p, enum.IdentityEmail, "replacement-expiry@example.test", meta1)
				}
				if err == nil {
					_, err = f.svc.ReplaceIdentity(ctx, p, identities[0].ID, f.credential(enum.PurposeBind, enum.IdentityEmail, "replacement-expiry@example.test", f.sent.code("replacement-expiry@example.test")), meta1)
				}
			}
			if err != nil {
				t.Fatal("expired session revocation failed:", err)
			}
			var revoked bool
			var reason enum.RevokeReason
			if err := f.pool.QueryRow(ctx, "SELECT revoke_time IS NOT NULL, revoke_reason FROM session WHERE id=$1", oldPrincipal.SessionID).Scan(&revoked, &reason); err != nil {
				t.Fatal(err)
			}
			if !revoked || reason != tc.reason {
				t.Fatalf("expired row: revoked=%v reason=%s want=%s", revoked, reason, tc.reason)
			}
			if !f.mr.Exists("t:revoked:" + oldPrincipal.SessionID) {
				t.Error("expired row omitted from Redis revocation")
			}
			if _, err := f.svc.Authenticate(ctx, old.AccessToken); !errors.Is(err, user.ErrInvalidToken) {
				t.Errorf("expired row access after revoke: %v", err)
			}
			if _, err := f.svc.Refresh(ctx, old.RefreshToken, meta1); !errors.Is(err, user.ErrInvalidGrant) {
				t.Errorf("expired row refresh after revoke: %v", err)
			}
			var currentRevoked, foreignRevoked bool
			if err := f.pool.QueryRow(ctx, "SELECT revoke_time IS NOT NULL FROM session WHERE id=$1", p.SessionID).Scan(&currentRevoked); err != nil {
				t.Fatal(err)
			}
			if err := f.pool.QueryRow(ctx, "SELECT revoke_time IS NOT NULL FROM session WHERE id=$1", foreignPrincipal.SessionID).Scan(&foreignRevoked); err != nil {
				t.Fatal(err)
			}
			if currentRevoked != tc.all || foreignRevoked {
				t.Error("revocation coverage changed current/foreign resources")
			}
			if _, err := f.svc.Authenticate(ctx, foreign.AccessToken); err != nil {
				t.Error("foreign access changed:", err)
			}
			if !tc.all {
				if _, err := f.svc.Authenticate(ctx, current.AccessToken); err != nil {
					t.Error("current access changed:", err)
				}
			}
			count := 0
			for _, e := range f.audit.Events()[beforeEvents:] {
				if e.Type == enum.EventSessionRevoked {
					count++
					if e.UserID != p.UserID || e.Reason != tc.reason.String() || e.Result != enum.ResultSuccess || e.RequestID != meta1.RequestID {
						t.Error("revocation audit semantics changed")
					}
					if (tc.name == "admin-single" || tc.name == "admin-revokeAll" || tc.name == "freeze") && (e.Actor != enum.ActorAdmin || e.AdminSubject != admin1.Subject) {
						t.Error("admin revocation audit lost actor")
					}
				}
			}
			want := 1
			if tc.all {
				want = 2
			}
			if count != want {
				t.Errorf("revocation audit count=%d want=%d including expired row", count, want)
			}
		})
	}
	t.Run("retention-30-days", func(t *testing.T) {
		f := newFixture(t)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		var sessions []string
		for _, device := range []string{"expired-before", "expired-equal", "expired-after", "revoked-before", "revoked-equal", "revoked-after"} {
			pair := f.signIn(t, enum.IdentityEmail, email1, user.Device{ID: device})
			p := principalOf(t, f, pair)
			sessions = append(sessions, p.SessionID)
		}
		before := f.clock.Add(-user.SessionRetention).UTC().Truncate(time.Microsecond)
		for i, sid := range sessions {
			deadline := before.Add(time.Duration(i%3-1) * time.Microsecond)
			var err error
			if i < 3 {
				_, err = f.pool.Exec(ctx, "UPDATE session SET refresh_expire_time=$2 WHERE id=$1", sid, deadline)
			} else {
				_, err = f.pool.Exec(ctx, "UPDATE session SET revoke_time=$2, revoke_reason=$3 WHERE id=$1", sid, deadline, enum.RevokeUserLogout)
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		n, err := f.svc.CleanupSessions(ctx)
		if err != nil || n != 2 {
			t.Fatalf("30-day cleanup: deleted=%d err=%v want=2", n, err)
		}
		for i, sid := range sessions {
			var exists bool
			if err := f.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM session WHERE id=$1)", sid).Scan(&exists); err != nil {
				t.Fatal(err)
			}
			if exists != (i%3 != 0) {
				t.Errorf("retention boundary row %d exists=%v", i, exists)
			}
		}
	})
}
