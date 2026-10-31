package user_test

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/internal/userdb"
	"github.com/bbxx111/accountkit/user"
)

func authenticationSources(t *testing.T, f *fixture, p user.Principal, want ...string) {
	t.Helper()
	rows, err := user.TestRepoQueries(f.repo).ListSessionAuthenticationIdentities(context.Background(), db.ListSessionAuthenticationIdentitiesParams{UserID: p.UserID, SessionID: p.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(rows))
	for _, row := range rows {
		got = append(got, row.IdentityID)
	}
	sort.Strings(want)
	if len(got) != len(want) || (len(want) > 0 && !reflect.DeepEqual(got, want)) {
		t.Fatalf("authentication source IDs=%v want IDs=%v", got, want)
	}
}

func contextBindEmail(t *testing.T, f *fixture, p user.Principal, target string) string {
	t.Helper()
	ctx := context.Background()
	if err := f.sendBindCode(ctx, p, enum.IdentityEmail, target, meta1); err != nil {
		t.Fatal(err)
	}
	ident, _, err := f.svc.BindWithCode(ctx, p, f.credential(enum.PurposeBind, enum.IdentityEmail, target, f.sent.code(target)), meta1)
	if err != nil {
		t.Fatal(err)
	}
	return ident.ID
}

func TestSessionAuthenticationSources(t *testing.T) {
	t.Run("ordinary precise identity binding and refresh do not extend sources", func(t *testing.T) {
		f := newFixture(t)
		ctx := context.Background()
		first := f.signIn(t, enum.IdentityPhone, phone1, dev1)
		p := principalOf(t, f, first)
		ids, err := user.TestRepoQueries(f.repo).ListActiveIdentitiesByUser(ctx, p.UserID)
		if err != nil {
			t.Fatal(err)
		}
		authenticationSources(t, f, p, ids[0].ID)
		emailID := contextBindEmail(t, f, p, email1)
		authenticationSources(t, f, p, ids[0].ID)
		refreshed, err := f.svc.Refresh(ctx, first.RefreshToken, meta1)
		if err != nil {
			t.Fatal(err)
		}
		retry, err := f.svc.Refresh(ctx, first.RefreshToken, meta1)
		if err != nil || retry != refreshed {
			t.Fatalf("grace: %v", err)
		}
		authenticationSources(t, f, p, ids[0].ID)
		f.advance(61 * time.Second)
		login := f.signIn(t, enum.IdentityEmail, email1, dev2)
		authenticationSources(t, f, principalOf(t, f, login), emailID)
	})
	t.Run("pending records all distinct concrete proofs including same kind", func(t *testing.T) {
		f := newFixture(t)
		ctx := context.Background()
		newServiceWith(t, f, func(d *user.Deps) {
			d.RequiredIdentityKinds = []enum.IdentityKind{enum.IdentityPhone}
			d.MaxIdentitiesPerKind = 2
		})
		pending := pendingFlow(t, f, "source-pending")
		for _, target := range []string{email1, "second@example.com"} {
			cred := completionCredential(t, f, pending, enum.IdentityEmail, target)
			for i := 0; i < 2; i++ {
				r, err := f.svc.CompleteSignIn(ctx, pending.SignInCompletionToken, cred, user.Device{}, meta1)
				if err != nil || r.Pending == nil {
					t.Fatalf("intermediate: %+v %v", r, err)
				}
			}
		}
		cred := completionCredential(t, f, pending, enum.IdentityPhone, phone1)
		result, err := f.svc.CompleteSignIn(ctx, pending.SignInCompletionToken, cred, user.Device{}, meta1)
		if err != nil {
			t.Fatal(err)
		}
		pair := requireSignInTokens(t, result)
		p := principalOf(t, f, pair)
		ids, err := user.TestRepoQueries(f.repo).ListActiveIdentitiesByUser(ctx, p.UserID)
		if err != nil || len(ids) != 4 {
			t.Fatalf("proof identities %d %v", len(ids), err)
		}
		want := []string{}
		for _, id := range ids {
			want = append(want, id.ID)
		}
		authenticationSources(t, f, p, want...)
	})
}

func TestReauthenticationReplacesSources(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "multiple to one", true: "missing sources rejected"}[missing], func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			pending := pendingFlow(t, f, "reauth-source")
			cred := completionCredential(t, f, pending, enum.IdentityEmail, email1)
			result, err := f.svc.CompleteSignIn(ctx, pending.SignInCompletionToken, cred, user.Device{}, meta1)
			if err != nil {
				t.Fatal(err)
			}
			pair := requireSignInTokens(t, result)
			p := principalOf(t, f, pair)
			ids, _ := user.TestRepoQueries(f.repo).ListActiveIdentitiesByUser(ctx, p.UserID)
			var emailID string
			for _, id := range ids {
				if id.Kind == enum.IdentityEmail {
					emailID = id.ID
				}
			}
			want := []string{}
			for _, id := range ids {
				want = append(want, id.ID)
			}
			authenticationSources(t, f, p, want...)
			if missing {
				mustExec(t, f, `DELETE FROM session_authentication_identity WHERE session_id=$1`, p.SessionID)
			}
			f.advance(2 * time.Minute)
			if err = f.sendReauthenticationCode(ctx, p, enum.IdentityEmail, email1, meta1); err != nil {
				t.Fatal(err)
			}
			out, err := f.svc.Reauthenticate(ctx, p, f.credential(enum.PurposeReauth, enum.IdentityEmail, email1, f.sent.code(email1)), meta1)
			if missing {
				if err == nil || errors.Is(err, user.ErrInvalidToken) || errors.Is(err, user.ErrUnavailable) {
					t.Fatalf("missing sources must reject reauthentication as invariant error: %v", err)
				}
				authenticationSources(t, f, p)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			p2 := principalOf(t, f, out)
			if p2.SessionID != p.SessionID || !p2.AuthTime.After(p.AuthTime) || out.RefreshToken != "" {
				t.Fatalf("reauth: %+v", out)
			}
			authenticationSources(t, f, p, emailID)
			if _, err = f.svc.Refresh(ctx, pair.RefreshToken, meta1); err != nil {
				t.Fatal("original refresh lost:", err)
			}
		})
	}
}

func TestSessionAuthenticationSourceWriteRollback(t *testing.T) {
	for _, pending := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "pending"}[pending], func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			mustExec(t, f, `CREATE FUNCTION reject_source() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic source failure'; END $$`)
			mustExec(t, f, `CREATE TRIGGER reject_source BEFORE INSERT ON session_authentication_identity FOR EACH ROW EXECUTE FUNCTION reject_source()`)
			if pending {
				p := pendingFlow(t, f, "rollback-source")
				cred := completionCredential(t, f, p, enum.IdentityEmail, email1)
				for attempt := 0; attempt < 2; attempt++ {
					result, err := f.svc.CompleteSignIn(ctx, p.SignInCompletionToken, cred, user.Device{}, meta1)
					if err == nil {
						t.Fatal("source write failure ignored")
					}
					if result.Tokens != nil || result.Pending != nil {
						t.Fatal("rolled-back completion returned a result")
					}
					var state enum.FlowState
					var owner, sessionID *string
					if err := f.pool.QueryRow(ctx, `SELECT state,user_id,session_id FROM sign_in_completion_flow`).Scan(&state, &owner, &sessionID); err != nil {
						t.Fatal(err)
					}
					if state != enum.FlowVerified || owner != nil || sessionID != nil {
						t.Fatalf("completion/replay result partially committed: state=%v owner_present=%v session_present=%v", state, owner != nil, sessionID != nil)
					}
				}
			} else {
				if err := f.sendSignInCode(ctx, enum.IdentityPhone, phone1, meta1); err != nil {
					t.Fatal(err)
				}
				if _, err := f.svc.SignInWithCode(ctx, f.credential(enum.PurposeSignIn, enum.IdentityPhone, phone1, f.sent.code(phone1)), dev1, meta1); err == nil {
					t.Fatal("source write failure ignored")
				}
			}
			for _, table := range []string{"user_account", "identity", "session", "session_authentication_identity"} {
				var n int
				if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != 0 {
					t.Fatalf("partial %s=%d err=%v", table, n, err)
				}
			}
		})
	}
}

type currentSessionReader interface {
	GetCurrentSession(context.Context, user.Principal) (user.CurrentSessionInfo, error)
}

func currentReader(t *testing.T, f *fixture) currentSessionReader {
	t.Helper()
	r, ok := any(f.svc).(currentSessionReader)
	if !ok {
		t.Fatal("Service missing GetCurrentSession")
	}
	return r
}

func TestCurrentSessionContextDomain(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	pair := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	p := principalOf(t, f, pair)
	r := currentReader(t, f)
	ids, _ := user.TestRepoQueries(f.repo).ListActiveIdentitiesByUser(ctx, p.UserID)
	info, err := r.GetCurrentSession(ctx, p)
	if err != nil || info.ID != p.SessionID || !info.IsCurrent || info.DeviceID != dev1.ID || info.DeviceName != dev1.Name || !info.AuthTime.Equal(p.AuthTime) || len(info.AuthenticationIdentities) != 1 || info.AuthenticationIdentities[0].ID != ids[0].ID || info.AuthenticationIdentities[0].Kind != enum.IdentityPhone {
		t.Fatalf("current: %+v %v", info, err)
	}
	for _, scope := range []string{user.ScopeUser, user.ScopeBind} {
		pp := p
		pp.Scope = scope
		if _, err := r.GetCurrentSession(ctx, pp); err != nil {
			t.Fatalf("scope %s: %v", scope, err)
		}
	}
	for _, scope := range []string{user.ScopeUndelete, "", "unknown"} {
		pp := p
		pp.Scope = scope
		if _, err := r.GetCurrentSession(ctx, pp); !errors.Is(err, user.ErrInsufficientScope) {
			t.Fatalf("scope %s: %v", scope, err)
		}
	}
	for _, pp := range []user.Principal{{UserID: "u_0000000000000", SessionID: p.SessionID, Scope: user.ScopeUser}, {UserID: p.UserID, SessionID: "s_0000000000000", Scope: user.ScopeUser}} {
		if _, err := r.GetCurrentSession(ctx, pp); !errors.Is(err, user.ErrInvalidToken) {
			t.Fatalf("ownership/missing: %v", err)
		}
	}
	mustExec(t, f, `DELETE FROM session_authentication_identity WHERE session_id=$1`, p.SessionID)
	if _, err := r.GetCurrentSession(ctx, p); err == nil || errors.Is(err, user.ErrInvalidToken) || errors.Is(err, user.ErrUnavailable) {
		t.Fatalf("missing authentication identities is invariant error: %v", err)
	}
	mustExec(t, f, `INSERT INTO session_authentication_identity(session_id,identity_id) VALUES ($1,$2)`, p.SessionID, ids[0].ID)
	// 存储层格式 CHECK 阻止非法类型落库，GetCurrentSession 的格式防御不再可从数据库触发。
	if _, err := f.pool.Exec(ctx, `UPDATE identity SET kind='99' WHERE id=$1`, ids[0].ID); err == nil {
		t.Fatal("malformed persisted kind accepted")
	}
	for _, offset := range []time.Duration{-time.Microsecond, 0, time.Microsecond} {
		mustExec(t, f, `UPDATE session SET refresh_expire_time=$2 WHERE id=$1`, p.SessionID, f.clock.Add(offset))
		_, err := r.GetCurrentSession(ctx, p)
		if offset <= 0 && !errors.Is(err, user.ErrInvalidToken) || offset > 0 && err != nil {
			t.Fatalf("expiry offset %v: %v", offset, err)
		}
	}
	mustExec(t, f, `UPDATE session SET refresh_expire_time=$2,revoke_time=$3 WHERE id=$1`, p.SessionID, f.clock.Add(time.Hour), *f.clock)
	if _, err := r.GetCurrentSession(ctx, p); !errors.Is(err, user.ErrInvalidToken) {
		t.Fatalf("revoked: %v", err)
	}
}

func TestCurrentSessionContextLatestAtomicRead(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pair := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	p := principalOf(t, f, pair)
	r := currentReader(t, f)
	emailID := contextBindEmail(t, f, p, email1)
	tx := identityRaceLock(t, ctx, f, p.UserID, p.SessionID)
	next := p.AuthTime.Add(time.Minute)
	if _, err := tx.Exec(ctx, `UPDATE session SET auth_time=$2 WHERE id=$1`, p.SessionID, next); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM session_authentication_identity WHERE session_id=$1`, p.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO session_authentication_identity(session_id,identity_id) VALUES ($1,$2)`, p.SessionID, emailID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	infos := make(chan user.CurrentSessionInfo, 1)
	go func() { info, err := r.GetCurrentSession(ctx, p); infos <- info; done <- err }()
	waitIdentityRaceBlocked(t, ctx, f, tx, done)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	info := <-infos
	if !info.AuthTime.Equal(next) || len(info.AuthenticationIdentities) != 1 || info.AuthenticationIdentities[0].ID != emailID {
		t.Fatalf("mixed/stale snapshot %+v", info)
	}
	if !p.AuthTime.Before(info.AuthTime) {
		t.Fatal("old access must read latest database auth time")
	}
}

func TestCurrentSessionContextDependencyFailure(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	pair := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	p := principalOf(t, f, pair)
	r := currentReader(t, f)
	// A statement failure with dependency SQLSTATE must remain 503, never token invalid.
	mustExec(t, f, `CREATE FUNCTION unavailable_session() RETURNS boolean LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic unavailable' USING ERRCODE='08006'; END $$`)
	mustExec(t, f, `ALTER TABLE session RENAME TO hidden_session`)
	mustExec(t, f, `CREATE VIEW session AS SELECT * FROM hidden_session WHERE unavailable_session()`)
	if _, err := r.GetCurrentSession(ctx, p); !errors.Is(err, user.ErrUnavailable) || errors.Is(err, user.ErrInvalidToken) {
		t.Fatalf("dependency: %v", err)
	}
}

func TestReauthenticationSourceRollback(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	pair := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	p := principalOf(t, f, pair)
	ids, _ := user.TestRepoQueries(f.repo).ListActiveIdentitiesByUser(ctx, p.UserID)
	contextBindEmail(t, f, p, email1)
	f.advance(2 * time.Minute)
	if err := f.sendReauthenticationCode(ctx, p, enum.IdentityEmail, email1, meta1); err != nil {
		t.Fatal(err)
	}
	cred := f.credential(enum.PurposeReauth, enum.IdentityEmail, email1, f.sent.code(email1))
	mustExec(t, f, `CREATE FUNCTION reject_source() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic source failure'; END $$`)
	mustExec(t, f, `CREATE TRIGGER reject_source BEFORE INSERT ON session_authentication_identity FOR EACH ROW EXECUTE FUNCTION reject_source()`)
	if _, err := f.svc.Reauthenticate(ctx, p, cred, meta1); err == nil {
		t.Fatal("source failure ignored")
	}
	authenticationSources(t, f, p, ids[0].ID)
	sess, err := user.TestRepoQueries(f.repo).GetSessionByRefreshHash(ctx, sha256Of(pair.RefreshToken))
	if err != nil || !sess.AuthTime.Equal(p.AuthTime) {
		t.Fatalf("partial reauth %v", err)
	}
	mustExec(t, f, `DROP TRIGGER reject_source ON session_authentication_identity`)
	if _, err := f.svc.Reauthenticate(ctx, p, cred, meta1); err == nil {
		t.Fatal("consumed code restored")
	}
}

func TestMissingSessionAuthenticationSourcesRefreshDoesNotRepair(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	pair := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	p := principalOf(t, f, pair)
	mustExec(t, f, `DELETE FROM session_authentication_identity WHERE session_id=$1`, p.SessionID)
	first, err := f.svc.Refresh(ctx, pair.RefreshToken, meta1)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := f.svc.Refresh(ctx, pair.RefreshToken, meta1)
	if err != nil || first != retry {
		t.Fatalf("refresh grace %v", err)
	}
	authenticationSources(t, f, p)
	if _, err := f.svc.GetCurrentSession(ctx, p); err == nil {
		t.Fatal("refresh silently repaired missing authentication sources")
	}
}

func TestSessionAuthenticationPendingExistingAccount(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	first := f.signIn(t, enum.IdentityPhone, phone1, dev2)
	p := principalOf(t, f, first)
	emailID := contextBindEmail(t, f, p, email1)
	f.advance(61 * time.Second)
	pending := pendingFlow(t, f, "sources-existing")
	cred := completionCredential(t, f, pending, enum.IdentityEmail, email1)
	result, err := f.svc.CompleteSignIn(ctx, pending.SignInCompletionToken, cred, user.Device{}, meta1)
	if err != nil {
		t.Fatal(err)
	}
	pair := requireSignInTokens(t, result)
	if pair.UserID != p.UserID {
		t.Fatal("pending failed to associate account")
	}
	ids, _ := user.TestRepoQueries(f.repo).ListActiveIdentitiesByUser(ctx, p.UserID)
	var wechatID string
	for _, id := range ids {
		if id.Kind == enum.IdentityWeChat {
			wechatID = id.ID
		}
	}
	authenticationSources(t, f, principalOf(t, f, pair), emailID, wechatID)
	info, err := currentReader(t, f).GetCurrentSession(ctx, principalOf(t, f, pair))
	if err != nil || len(info.AuthenticationIdentities) != 2 || info.AuthenticationIdentities[0].ID >= info.AuthenticationIdentities[1].ID {
		t.Fatalf("stable concrete summaries %+v %v", info, err)
	}
}

// A missing current source set is corrupt state, never a replacement opportunity.
func TestMissingAuthenticationSourcesRejectSensitiveActions(t *testing.T) {
	for _, action := range []string{"reauthenticate", "unbind", "replace"} {
		t.Run(action, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			pair := f.signIn(t, enum.IdentityPhone, phone1, dev1)
			p := principalOf(t, f, pair)
			emailID := contextBindEmail(t, f, p, email1)
			f.advance(61 * time.Second)
			var cred user.CodeCredential
			if action == "reauthenticate" {
				if err := f.sendReauthenticationCode(ctx, p, enum.IdentityEmail, email1, meta1); err != nil {
					t.Fatal(err)
				}
				cred = f.credential(enum.PurposeReauth, enum.IdentityEmail, email1, f.sent.code(email1))
			}
			if action == "replace" {
				target := "replacement@example.test"
				if err := f.sendBindCode(ctx, p, enum.IdentityEmail, target, meta1); err != nil {
					t.Fatal(err)
				}
				cred = f.credential(enum.PurposeBind, enum.IdentityEmail, target, f.sent.code(target))
			}
			mustExec(t, f, `DELETE FROM session_authentication_identity WHERE session_id=$1`, p.SessionID)
			var err error
			switch action {
			case "reauthenticate":
				_, err = f.svc.Reauthenticate(ctx, p, cred, meta1)
			case "unbind":
				err = f.svc.UnbindIdentity(ctx, p, emailID, meta1)
			case "replace":
				_, err = f.svc.ReplaceIdentity(ctx, p, emailID, cred, meta1)
			}
			if err == nil || errors.Is(err, user.ErrInvalidToken) || errors.Is(err, user.ErrUnavailable) {
				t.Fatalf("missing authentication sources must reject as internal invariant error: %v", err)
			}
			var sourceCount int
			if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM session_authentication_identity WHERE session_id=$1`, p.SessionID).Scan(&sourceCount); err != nil {
				t.Fatal(err)
			}
			if sourceCount != 0 {
				t.Fatal("missing authentication sources silently repaired")
			}
			identity, err := user.TestRepoQueries(f.repo).GetActiveIdentityByIDAndUser(ctx, db.GetActiveIdentityByIDAndUserParams{ID: emailID, UserID: p.UserID})
			if err != nil || identity.ID != emailID {
				t.Fatalf("identity mutated after rejection: %v", err)
			}
			sess, err := user.TestRepoQueries(f.repo).GetSessionByRefreshHash(ctx, sha256Of(pair.RefreshToken))
			if err != nil || sess.RevokeTime != nil || !sess.AuthTime.Equal(p.AuthTime) {
				t.Fatalf("session mutated after rejection: %v", err)
			}
		})
	}
}
