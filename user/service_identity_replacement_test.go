package user_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/ids"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/code"
)

func replacementSetup(t *testing.T, kind enum.IdentityKind, old string) (*fixture, user.Principal, string, user.TokenResult) {
	t.Helper()
	f := newFixture(t)
	login := f.signIn(t, kind, old, dev1)
	p, err := f.svc.Authenticate(context.Background(), login.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	list, err := f.svc.ListIdentities(context.Background(), p.UserID)
	if err != nil || len(list) != 1 {
		t.Fatalf("identities: %+v %v", list, err)
	}
	return f, p, list[0].ID, login
}

func replacementCode(t *testing.T, f *fixture, p user.Principal, kind enum.IdentityKind, target string) string {
	t.Helper()
	if err := f.sendBindCode(context.Background(), p, kind, target, meta1); err != nil {
		t.Fatal(err)
	}
	return f.sent.code(normalizedTarget(kind, target))
}

func TestReplaceIdentity(t *testing.T) {
	for _, tc := range []struct {
		name        string
		kind        enum.IdentityKind
		old, target string
	}{
		{"phone", enum.IdentityPhone, phone1, phone2},
		{"email", enum.IdentityEmail, email1, "replacement@example.test"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, p, iid, current := replacementSetup(t, tc.kind, tc.old)
			ctx := context.Background()
			other := f.signIn(t, tc.kind, tc.old, user.Device{ID: "replacement-other"})
			rotated, err := f.svc.Refresh(ctx, other.RefreshToken, meta1)
			if err != nil {
				t.Fatal(err)
			}
			plain := replacementCode(t, f, p, tc.kind, tc.target)
			out, err := f.svc.ReplaceIdentity(ctx, p, iid, f.credential(enum.PurposeBind, tc.kind, tc.target, plain), meta1)
			if err != nil || out.ID == iid || !ids.Valid(ids.Identity, out.ID) || out.Kind != tc.kind || out.MaskedSubject == "" || out.MaskedSubject == tc.target {
				t.Fatalf("replacement: %+v %v", out, err)
			}
			list, err := f.svc.ListIdentities(ctx, p.UserID)
			if err != nil || len(list) != 1 || list[0].ID != out.ID {
				t.Fatalf("final identities: %+v %v", list, err)
			}
			var deleted bool
			if err := f.pool.QueryRow(ctx, "SELECT delete_time IS NOT NULL FROM identity WHERE id=$1", iid).Scan(&deleted); err != nil || !deleted {
				t.Fatalf("soft deletion: %v %v", deleted, err)
			}
			if _, err := f.svc.ReplaceIdentity(ctx, p, iid, f.credential(enum.PurposeBind, tc.kind, tc.target, plain), meta1); !errors.Is(err, user.ErrNotFound) {
				t.Fatalf("retry: %v", err)
			}
			refreshed, err := f.svc.Refresh(ctx, current.RefreshToken, meta1)
			if err != nil {
				t.Fatalf("current refresh: %v", err)
			}
			fresh, err := f.svc.Authenticate(ctx, refreshed.AccessToken)
			if err != nil || fresh.SessionID != p.SessionID || !fresh.AuthTime.Equal(p.AuthTime) {
				t.Fatalf("preserved session/auth_time: %+v %v", fresh, err)
			}
			for _, token := range []string{other.RefreshToken, rotated.RefreshToken} {
				if _, err := f.svc.Refresh(ctx, token, meta1); !errors.Is(err, user.ErrInvalidGrant) {
					t.Fatalf("other refresh: %v", err)
				}
			}
			if _, err := f.svc.Authenticate(ctx, other.AccessToken); !errors.Is(err, user.ErrInvalidToken) {
				t.Fatalf("other access: %v", err)
			}
			sess, err := f.repo.Q().GetSessionByRefreshHash(ctx, sha256Of(rotated.RefreshToken))
			if err != nil || sess.RevokeReason == nil || *sess.RevokeReason != enum.RevokeIdentityReplaced {
				t.Fatalf("revoke reason: %+v %v", sess, err)
			}
			for _, typ := range []enum.EventType{enum.EventIdentityUnbound, enum.EventIdentityBound, enum.EventSessionRevoked} {
				if !hasEventReason(f.audit, typ, enum.ResultSuccess, "IDENTITY_REPLACED") {
					t.Fatalf("missing replacement audit: %s", typ)
				}
			}
			for _, e := range f.audit.Events() {
				if e.Reason == "IDENTITY_REPLACED" && (e.RequestID != meta1.RequestID || strings.Contains(e.SubjectHint, tc.target) || strings.Contains(e.Reason, plain)) {
					t.Fatalf("unsafe/uncorrelated audit: %+v", e)
				}
			}
		})
	}
}

func TestReplaceIdentityPreflight(t *testing.T) {
	cases := []struct {
		name   string
		change func(*testing.T, *fixture, *user.Principal, *string)
		kind   enum.IdentityKind
		target string
		want   error
	}{
		{"scope", func(_ *testing.T, _ *fixture, p *user.Principal, _ *string) { p.Scope = user.ScopeBind }, enum.IdentityPhone, phone2, user.ErrInsufficientScope},
		{"missing-auth", func(_ *testing.T, _ *fixture, p *user.Principal, _ *string) { p.AuthTime = time.Time{} }, enum.IdentityPhone, phone2, user.ErrReauthenticationRequired},
		{"stale", func(_ *testing.T, f *fixture, p *user.Principal, _ *string) {
			p.AuthTime = f.clock.Add(-5*time.Minute - time.Second)
		}, enum.IdentityPhone, phone2, user.ErrReauthenticationRequired},
		{"bad-id", func(_ *testing.T, _ *fixture, _ *user.Principal, id *string) { *id = "invalid" }, enum.IdentityPhone, phone2, user.ErrInvalidArgument},
		{"missing-id", func(_ *testing.T, _ *fixture, _ *user.Principal, id *string) { *id = "i_0000000000000" }, enum.IdentityPhone, phone2, user.ErrNotFound},
		{"foreign-id", func(t *testing.T, f *fixture, _ *user.Principal, id *string) {
			r := f.signIn(t, enum.IdentityPhone, "+8613900000009", user.Device{ID: "foreign"})
			list, _ := f.svc.ListIdentities(context.Background(), r.UserID)
			*id = list[0].ID
		}, enum.IdentityPhone, phone2, user.ErrNotFound},
		{"deleted-id", func(t *testing.T, f *fixture, _ *user.Principal, id *string) {
			mustExec(t, f, "UPDATE identity SET delete_time=now() WHERE id=$1", *id)
		}, enum.IdentityPhone, phone2, user.ErrNotFound},
		{"wrong-kind", nil, enum.IdentityEmail, "new@example.test", user.ErrInvalidArgument},
		{"provider-kind", nil, enum.IdentityApple, "subject", user.ErrInvalidArgument},
		{"same-normalized", nil, enum.IdentityPhone, "13812341234", user.ErrIdentityUnchanged},
		{"invalid-target", nil, enum.IdentityPhone, "bad-phone", user.ErrInvalidTarget},
		{"frozen", func(t *testing.T, f *fixture, p *user.Principal, _ *string) {
			mustExec(t, f, "UPDATE user_account SET state=2 WHERE id=$1", p.UserID)
		}, enum.IdentityPhone, phone2, user.ErrUserFrozen},
		{"pending", func(t *testing.T, f *fixture, p *user.Principal, _ *string) {
			mustExec(t, f, "UPDATE user_account SET state=3 WHERE id=$1", p.UserID)
		}, enum.IdentityPhone, phone2, user.ErrInvalidToken},
		{"deleted-user", func(t *testing.T, f *fixture, p *user.Principal, _ *string) {
			mustExec(t, f, "UPDATE user_account SET state=4 WHERE id=$1", p.UserID)
		}, enum.IdentityPhone, phone2, user.ErrInvalidToken},
		{"missing-session", func(_ *testing.T, _ *fixture, p *user.Principal, _ *string) { p.SessionID = "s_0000000000000" }, enum.IdentityPhone, phone2, user.ErrInvalidToken},
		{"foreign-session", func(t *testing.T, f *fixture, p *user.Principal, _ *string) {
			r := f.signIn(t, enum.IdentityPhone, "+8613900000009", user.Device{ID: "foreign"})
			q, _ := f.svc.Authenticate(context.Background(), r.AccessToken)
			p.SessionID = q.SessionID
		}, enum.IdentityPhone, phone2, user.ErrInvalidToken},
		{"revoked-session", func(t *testing.T, f *fixture, p *user.Principal, _ *string) {
			mustExec(t, f, "UPDATE session SET revoke_time=now() WHERE id=$1", p.SessionID)
		}, enum.IdentityPhone, phone2, user.ErrInvalidToken},
		{"expired-session", func(t *testing.T, f *fixture, p *user.Principal, _ *string) {
			mustExec(t, f, "UPDATE session SET refresh_expire_time=$2 WHERE id=$1", p.SessionID, *f.clock)
		}, enum.IdentityPhone, phone2, user.ErrInvalidToken},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, p, id, _ := replacementSetup(t, enum.IdentityPhone, phone1)
			plain := replacementCode(t, f, p, enum.IdentityPhone, phone2)
			if tc.change != nil {
				tc.change(t, f, &p, &id)
			}
			if _, err := f.svc.ReplaceIdentity(context.Background(), p, id, f.credential(enum.PurposeBind, tc.kind, tc.target, plain), meta1); !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
			// 即使预检失败，原 BIND 证明也仍可被消费。
			if err := f.deps.Codes.VerifyChallenge(context.Background(), f.storeCredential(enum.PurposeBind, enum.IdentityPhone, phone2, plain, p)); err != nil {
				t.Fatalf("preflight consumed code: %v", err)
			}
			if !hasEvent(f.audit, enum.EventIdentityReplaceRejected, enum.ResultFailure) {
				t.Fatal("missing rejection audit")
			}
		})
	}
}

func TestReplaceIdentityConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name    string
		age     time.Duration
		enabled *bool
		authAge time.Duration
		want    error
	}{
		{"default-boundary", 0, nil, 5 * time.Minute, nil},
		{"default-expired", 0, nil, 5*time.Minute + time.Nanosecond, user.ErrReauthenticationRequired},
		{"custom-boundary", 2 * time.Minute, nil, 2 * time.Minute, nil},
		{"custom-expired", 2 * time.Minute, nil, 2*time.Minute + time.Nanosecond, user.ErrReauthenticationRequired},
		{"disabled", 0, replacementBool(false), time.Hour, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, p, id, _ := replacementSetup(t, enum.IdentityEmail, email1)
			deps := f.deps
			deps.ReauthMaxAge = tc.age
			deps.SensitiveOpVerification = tc.enabled
			var err error
			f.svc, err = user.NewService(deps)
			if err != nil {
				t.Fatal(err)
			}
			p.AuthTime = f.clock.Add(-tc.authAge)
			plain := replacementCode(t, f, p, enum.IdentityEmail, "new@example.test")
			_, err = f.svc.ReplaceIdentity(context.Background(), p, id, f.credential(enum.PurposeBind, enum.IdentityEmail, "new@example.test", plain), meta1)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
	f := newFixture(t)
	deps := f.deps
	deps.ReauthMaxAge = -time.Second
	if _, err := user.NewService(deps); err == nil {
		t.Fatal("negative reauth age accepted")
	}
	t.Run("disabled-still-checks-session-and-code", func(t *testing.T) {
		f, p, id, _ := replacementSetup(t, enum.IdentityPhone, phone1)
		deps := f.deps
		deps.SensitiveOpVerification = replacementBool(false)
		var err error
		f.svc, err = user.NewService(deps)
		if err != nil {
			t.Fatal(err)
		}
		p.AuthTime = time.Time{}
		plain := replacementCode(t, f, p, enum.IdentityPhone, phone2)
		wrong := "000000"
		if plain == wrong {
			wrong = "999999"
		}
		if _, err := f.svc.ReplaceIdentity(context.Background(), p, id, f.credential(enum.PurposeBind, enum.IdentityPhone, phone2, wrong), meta1); !errors.Is(err, code.ErrInvalid) {
			t.Fatalf("disabled freshness bypassed proof: %v", err)
		}
		mustExec(t, f, "UPDATE session SET revoke_time=now() WHERE id=$1", p.SessionID)
		if _, err := f.svc.ReplaceIdentity(context.Background(), p, id, f.credential(enum.PurposeBind, enum.IdentityPhone, phone2, plain), meta1); !errors.Is(err, user.ErrInvalidToken) {
			t.Fatalf("disabled freshness bypassed session: %v", err)
		}
	})
}

func TestReplaceIdentityNewTargetProof(t *testing.T) {
	for _, purpose := range []enum.CodePurpose{enum.PurposeSignIn, enum.PurposeReauth} {
		t.Run(purpose.String(), func(t *testing.T) {
			f, p, id, _ := replacementSetup(t, enum.IdentityPhone, phone1)
			issued, err := f.deps.Codes.IssueChallenge(context.Background(), enum.IdentityPhone, purpose, phone2, meta1.IP, code.Binding{UserID: p.UserID, SessionID: p.SessionID})
			plain := issued.Code
			f.challenges.Store(challengeKey(enum.PurposeBind, enum.IdentityPhone, phone2), issued.CodeID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.svc.ReplaceIdentity(context.Background(), p, id, f.credential(enum.PurposeBind, enum.IdentityPhone, phone2, plain), meta1); !errors.Is(err, code.ErrExpired) {
				t.Fatalf("wrong purpose: %v", err)
			}
		})
	}
	f, p, id, _ := replacementSetup(t, enum.IdentityPhone, phone1)
	plain := replacementCode(t, f, p, enum.IdentityPhone, phone2)
	wrong := "000000"
	if plain == wrong {
		wrong = "999999"
	}
	for range 5 {
		if _, err := f.svc.ReplaceIdentity(context.Background(), p, id, f.credential(enum.PurposeBind, enum.IdentityPhone, phone2, wrong), meta1); !errors.Is(err, code.ErrInvalid) && !errors.Is(err, code.ErrExhausted) {
			t.Fatalf("bad attempt: %v", err)
		}
	}
	if _, err := f.svc.ReplaceIdentity(context.Background(), p, id, f.credential(enum.PurposeBind, enum.IdentityPhone, phone2, plain), meta1); err == nil {
		t.Fatal("exhausted code accepted")
	}
	list, _ := f.svc.ListIdentities(context.Background(), p.UserID)
	if len(list) != 1 || list[0].ID != id {
		t.Fatalf("bad proof mutated identities: %+v", list)
	}
}

func replacementBool(v bool) *bool { return &v }
