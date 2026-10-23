package user_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/idp"
)

// QQ 复用合成提供方：领域补填只按 kind 判定，网络细节由 idp 包测试覆盖。
func qqCred(name, picture string) user.IdpCredential {
	return providerCred(enum.IdentityQQ, map[string]string{"sub": "qq-person", "app": "1100000001", "openid": "QQ-OPEN", "name": name, "picture": picture})
}

func useQQ(t *testing.T, f *fixture, calls *atomic.Int32, required []enum.IdentityKind) {
	t.Helper()
	newServiceWith(t, f, func(d *user.Deps) {
		*d = withProvider(*d, alipayProvider(enum.IdentityQQ, calls))
		d.RequiredIdentityKinds = required
	})
}

func TestQQLaterLoginBackfillsOnlyEmptyFields(t *testing.T) {
	for _, tc := range []struct{ name, avatar, wantName, wantAvatar string }{
		{"", "", "QQ Name", "https://thirdqq.example.test/qq.png"},
		{"Mine", "", "Mine", "https://thirdqq.example.test/qq.png"},
		{"", "https://example.test/mine.png", "QQ Name", "https://example.test/mine.png"},
		{"Mine", "https://example.test/mine.png", "Mine", "https://example.test/mine.png"},
	} {
		t.Run(tc.name+tc.avatar, func(t *testing.T) {
			f := newFixture(t)
			useQQ(t, f, nil, []enum.IdentityKind{})
			first := requireSignInTokens(t, mustSignInWithIdp(t, f, qqCred("", "")))
			if _, err := f.svc.UpdateProfile(context.Background(), first.UserID, user.ProfilePatch{DisplayName: &tc.name, AvatarURL: &tc.avatar}); err != nil {
				t.Fatal(err)
			}
			f.advance(time.Second)
			again := requireSignInTokens(t, mustSignInWithIdp(t, f, qqCred("QQ Name", "https://thirdqq.example.test/qq.png")))
			if again.UserID != first.UserID || again.IsNewUser {
				t.Fatal("later sign-in changed user")
			}
			assertAlipayProfile(t, f, first.UserID, tc.wantName, tc.wantAvatar)
		})
	}
}

func TestQQSameCredentialTwiceSignsInSameIdentity(t *testing.T) {
	f := newFixture(t)
	var calls atomic.Int32
	useQQ(t, f, &calls, []enum.IdentityKind{})
	a := requireSignInTokens(t, mustSignInWithIdp(t, f, qqCred("", "")))
	b := requireSignInTokens(t, mustSignInWithIdp(t, f, qqCred("", "")))
	if a.UserID != b.UserID || calls.Load() != 2 {
		t.Fatalf("repeat token: %s %s calls=%d", a.UserID, b.UserID, calls.Load())
	}
}

func TestQQPendingAlreadyOwnedProofBackfills(t *testing.T) {
	f := newFixture(t)
	var calls atomic.Int32
	useQQ(t, f, &calls, nil)
	owner := f.signIn(t, enum.IdentityEmail, email1, dev2)
	pending := mustSignInWithIdp(t, f, qqCred("Pending Owner", "https://example.test/pending.png"))
	if pending.Pending == nil {
		t.Fatal("expected pending")
	}
	bound, _, err := f.svc.BindWithIdp(context.Background(), principalOf(t, f, owner), qqCred("", ""), meta1)
	if err != nil {
		t.Fatal(err)
	}
	alipayPendingPayload(t, f, func(p map[string]any) {
		proof := p["Proofs"].([]any)[0].(map[string]any)
		proof["UserID"] = owner.UserID
		proof["IdentityID"] = bound.ID
	})
	cred := completionCredential(t, f, pending.Pending, enum.IdentityEmail, email1)
	done, err := f.svc.CompleteSignIn(context.Background(), pending.Pending.SignInCompletionToken, cred, dev1, meta1)
	if err != nil || requireSignInTokens(t, done).UserID != owner.UserID {
		t.Fatal(err)
	}
	assertAlipayProfile(t, f, owner.UserID, "Pending Owner", "https://example.test/pending.png")
	if calls.Load() != 2 {
		t.Fatal("completion re-verified provider")
	}
}

func TestQQPendingSanitizesAndCompletesWithoutVerifyReplay(t *testing.T) {
	f := newFixture(t)
	var calls atomic.Int32
	useQQ(t, f, &calls, nil)
	pending := mustSignInWithIdp(t, f, qqCred("Pending\n‮", "http://insecure.example.test/avatar"))
	if pending.Pending == nil {
		t.Fatal("expected pending")
	}
	provider := alipayPendingPayload(t, f, nil)["Proofs"].([]any)[0].(map[string]any)["Provider"].(map[string]any)
	if provider["Name"] != "Pending" || provider["Picture"] != "" {
		t.Fatalf("unsanitized pending provider: %+v", provider)
	}
	cred := completionCredential(t, f, pending.Pending, enum.IdentityEmail, email1)
	done, err := f.svc.CompleteSignIn(context.Background(), pending.Pending.SignInCompletionToken, cred, dev1, meta1)
	if err != nil {
		t.Fatal(err)
	}
	pair := requireSignInTokens(t, done)
	assertAlipayProfile(t, f, pair.UserID, "Pending", "")
	replay, err := f.svc.CompleteSignIn(context.Background(), pending.Pending.SignInCompletionToken, cred, dev1, meta1)
	if err != nil || requireSignInTokens(t, replay) != pair || calls.Load() != 1 {
		t.Fatalf("completion replay called provider or changed result: %v calls=%d", err, calls.Load())
	}
}

func TestQQLoginProfileRollbackAfterSessionFailure(t *testing.T) {
	f := newFixture(t)
	useQQ(t, f, nil, []enum.IdentityKind{})
	first := requireSignInTokens(t, mustSignInWithIdp(t, f, qqCred("", "")))
	mustExec(t, f, `CREATE FUNCTION reject_qq_session() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic session failure'; END $$; CREATE TRIGGER reject_qq_session BEFORE INSERT ON session FOR EACH ROW EXECUTE FUNCTION reject_qq_session()`)
	if _, err := f.svc.SignInWithIdp(context.Background(), qqCred("Rollback", "https://example.test/rollback.png"), dev2, meta1); err == nil {
		t.Fatal("session failure succeeded")
	}
	assertAlipayProfile(t, f, first.UserID, "", "")
	rows, err := f.repo.Q().ListActiveIdentitiesByUser(context.Background(), first.UserID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("list identities: %d %v", len(rows), err)
	}
	if strings.Contains(string(rows[0].ProviderMeta), "Rollback") {
		t.Fatal("provider metadata survived rollback")
	}
}

func TestQQConcurrentPATCHNonemptyProfileWins(t *testing.T) {
	f := newFixture(t)
	useQQ(t, f, nil, []enum.IdentityKind{})
	first := requireSignInTokens(t, mustSignInWithIdp(t, f, qqCred("", "")))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	gate := replacementMutationGate(t, ctx, f, "user_account", "UPDATE", "NEW.display_name IS DISTINCT FROM OLD.display_name")
	patch := make(chan replacementRaceResult, 1)
	name, avatar := "PATCH Name", "https://example.test/patch.png"
	go func() {
		_, err := f.svc.UpdateProfile(ctx, first.UserID, user.ProfilePatch{DisplayName: &name, AvatarURL: &avatar})
		patch <- replacementRaceResult{err: err}
	}()
	pid := replacementWaitForPID(t, ctx, f, gate.Conn().PgConn().PID(), patch)
	login := make(chan replacementRaceResult, 1)
	go func() {
		_, err := f.svc.SignInWithIdp(ctx, qqCred("Provider", "https://example.test/provider.png"), dev2, meta1)
		login <- replacementRaceResult{err: err}
	}()
	replacementWaitForPID(t, ctx, f, pid, login)
	if err := gate.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if out := finishReplacementRace(t, ctx, patch); out.err != nil {
		t.Fatal(out.err)
	}
	if out := finishReplacementRace(t, ctx, login); out.err != nil {
		t.Fatal(out.err)
	}
	assertAlipayProfile(t, f, first.UserID, name, avatar)
}

func TestQQLoginRechecksAccountState(t *testing.T) {
	for _, state := range []enum.UserState{enum.UserFrozen, enum.UserPendingDeletion, enum.UserDeleted} {
		t.Run(state.String(), func(t *testing.T) {
			f := newFixture(t)
			useQQ(t, f, nil, []enum.IdentityKind{})
			first := requireSignInTokens(t, mustSignInWithIdp(t, f, qqCred("", "")))
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			tx := identityRaceLock(t, ctx, f, first.UserID, "")
			done := make(chan error, 1)
			go func() {
				_, err := f.svc.SignInWithIdp(ctx, qqCred("Provider", "https://example.test/provider.png"), dev2, meta1)
				done <- err
			}()
			waitIdentityRaceBlocked(t, ctx, f, tx, done)
			if _, err := tx.Exec(ctx, `UPDATE user_account SET state=$2 WHERE id=$1`, first.UserID, state); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err == nil {
				t.Fatal("inactive login accepted")
			}
			var name, avatar *string
			if err := f.pool.QueryRow(ctx, `SELECT display_name,avatar_url FROM user_account WHERE id=$1`, first.UserID).Scan(&name, &avatar); err != nil || name != nil && *name != "" || avatar != nil && *avatar != "" {
				t.Fatal("inactive account was backfilled")
			}
		})
	}
}

func TestQQLoginCannotRestoreProfileAfterConcurrentPurge(t *testing.T) {
	f := newFixture(t)
	useQQ(t, f, nil, []enum.IdentityKind{})
	first := requireSignInTokens(t, mustSignInWithIdp(t, f, qqCred("", "")))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := f.svc.AdminDeleteUser(ctx, admin1, first.UserID, meta1); err != nil {
		t.Fatal(err)
	}
	f.advance(360*time.Hour + time.Second)
	gate := replacementMutationGate(t, ctx, f, "user_account", "UPDATE", "NEW.state <> OLD.state")
	purge := make(chan replacementRaceResult, 1)
	go func() { _, err := f.svc.PurgeDueUsers(ctx); purge <- replacementRaceResult{err: err} }()
	pid := replacementWaitForPID(t, ctx, f, gate.Conn().PgConn().PID(), purge)
	login := make(chan replacementRaceResult, 1)
	go func() {
		_, err := f.svc.SignInWithIdp(ctx, qqCred("Must Not Restore", "https://example.test/late.png"), dev2, meta1)
		login <- replacementRaceResult{err: err}
	}()
	replacementWaitForPID(t, ctx, f, pid, login)
	if err := gate.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if out := finishReplacementRace(t, ctx, purge); out.err != nil {
		t.Fatal(out.err)
	}
	if out := finishReplacementRace(t, ctx, login); !errors.Is(out.err, idp.ErrInvalidCredential) {
		t.Fatalf("stale identity accepted after purge: %v", out.err)
	}
	row, err := f.repo.Q().GetUserByID(ctx, first.UserID)
	if err != nil || row.State != enum.UserDeleted || row.DisplayName != nil || row.AvatarUrl != nil {
		t.Fatalf("login restored anonymized profile: %+v %v", row, err)
	}
}

func TestQQConcurrentAppsShareSingleUnionIdentity(t *testing.T) {
	f := newFixture(t)
	useQQ(t, f, nil, []enum.IdentityKind{})
	start := make(chan struct{})
	type outcome struct {
		result user.SignInResult
		err    error
	}
	done := make(chan outcome, 2)
	for _, app := range []string{"1100000001", "1100000002"} {
		go func(app string) {
			<-start
			r, err := f.svc.SignInWithIdp(context.Background(), providerCred(enum.IdentityQQ, map[string]string{"sub": "shared", "app": app, "openid": app + "-open"}), user.Device{ID: app}, meta1)
			done <- outcome{r, err}
		}(app)
	}
	close(start)
	a, b := <-done, <-done
	if a.err != nil || b.err != nil {
		t.Fatalf("concurrent login: %v %v", a.err, b.err)
	}
	if requireSignInTokens(t, a.result).UserID != requireSignInTokens(t, b.result).UserID {
		t.Fatal("same UnionID across QQ apps created two accounts")
	}
	var n int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM identity WHERE kind='QQ'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("qq identities=%d %v", n, err)
	}
}

func TestQQBindUnbindAndDisabledHistory(t *testing.T) {
	f := newFixture(t)
	useQQ(t, f, nil, nil)
	phone := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	p := principalOf(t, f, phone)
	bound, created, err := f.svc.BindWithIdp(context.Background(), p, qqCred("", ""), meta1)
	if err != nil || !created {
		t.Fatal(err)
	}
	newServiceWith(t, f, func(d *user.Deps) {
		d.EnabledIdentityKinds = []enum.IdentityKind{enum.IdentityPhone, enum.IdentityEmail}
		*d = withProvider(*d, alipayProvider(enum.IdentityQQ, nil))
	})
	if _, err := f.svc.SignInWithIdp(context.Background(), qqCred("Must Not Seed", ""), dev2, meta1); !errors.Is(err, user.ErrIdentityKindNotEnabled) {
		t.Fatal(err)
	}
	rows, err := f.svc.ListIdentities(context.Background(), phone.UserID)
	if err != nil || len(rows) != 2 {
		t.Fatalf("disabled QQ history invisible: %+v %v", rows, err)
	}
	if err := f.svc.UnbindIdentity(context.Background(), p, bound.ID, meta1); err != nil {
		t.Fatal(err)
	}
	assertAlipayProfile(t, f, phone.UserID, "", "")
}

func TestQQClearedProfileUsesOnlyCurrentClaims(t *testing.T) {
	f := newFixture(t)
	useQQ(t, f, nil, []enum.IdentityKind{})
	first := requireSignInTokens(t, mustSignInWithIdp(t, f, qqCred("Old", "https://example.test/old.png")))
	empty := ""
	if _, err := f.svc.UpdateProfile(context.Background(), first.UserID, user.ProfilePatch{DisplayName: &empty, AvatarURL: &empty}); err != nil {
		t.Fatal(err)
	}
	mustSignInWithIdp(t, f, qqCred("", ""))
	assertAlipayProfile(t, f, first.UserID, "", "")
	mustSignInWithIdp(t, f, qqCred("New\n‮", "https://example.test/new.png"))
	assertAlipayProfile(t, f, first.UserID, "New", "https://example.test/new.png")
}

func TestQQExceptionDoesNotBackfillOtherProviders(t *testing.T) {
	for _, kind := range []enum.IdentityKind{enum.IdentityGoogle, enum.IdentityApple, enum.IdentityFacebook, corpSSO} {
		t.Run(string(kind), func(t *testing.T) {
			f := newFixture(t)
			newServiceWith(t, f, func(d *user.Deps) {
				*d = withProvider(*d, alipayProvider(kind, nil))
				d.RequiredIdentityKinds = []enum.IdentityKind{}
			})
			first := requireSignInTokens(t, mustSignInWithIdp(t, f, providerCred(kind, map[string]string{"sub": "compat"})))
			mustSignInWithIdp(t, f, providerCred(kind, map[string]string{"sub": "compat", "name": "Later", "picture": "https://example.test/later.png"}))
			assertAlipayProfile(t, f, first.UserID, "", "")
		})
	}
}

func TestQQPendingConcurrentOwnershipRejectsWithoutBackfill(t *testing.T) {
	f := newFixture(t)
	useQQ(t, f, nil, nil)
	owner := f.signIn(t, enum.IdentityEmail, email1, dev2)
	pending := mustSignInWithIdp(t, f, qqCred("Must Roll Back", "https://example.test/pending.png"))
	if pending.Pending == nil {
		t.Fatal("expected pending")
	}
	if _, _, err := f.svc.BindWithIdp(context.Background(), principalOf(t, f, owner), qqCred("", ""), meta1); err != nil {
		t.Fatal(err)
	}
	cred := completionCredential(t, f, pending.Pending, enum.IdentityEmail, email1)
	if _, err := f.svc.CompleteSignIn(context.Background(), pending.Pending.SignInCompletionToken, cred, dev1, meta1); !errors.Is(err, user.ErrIdentityConflict) {
		t.Fatalf("concurrent ownership changed: %v", err)
	}
	assertAlipayProfile(t, f, owner.UserID, "", "")
}

func TestQQRepeatBindingDoesNotBackfill(t *testing.T) {
	f := newFixture(t)
	useQQ(t, f, nil, nil)
	phone := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	p := principalOf(t, f, phone)
	if _, created, err := f.svc.BindWithIdp(context.Background(), p, qqCred("", ""), meta1); err != nil || !created {
		t.Fatal(err)
	}
	if _, created, err := f.svc.BindWithIdp(context.Background(), p, qqCred("Later", "https://example.test/later.png"), meta1); err != nil || created {
		t.Fatal(err)
	}
	assertAlipayProfile(t, f, phone.UserID, "", "")
	rows, err := f.repo.Q().ListActiveIdentitiesByUser(context.Background(), phone.UserID)
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, row := range rows {
		if row.Kind == enum.IdentityQQ {
			seen = true
			if !strings.Contains(string(row.ProviderMeta), "Later") {
				t.Fatal("repeat binding did not refresh provider metadata")
			}
		}
	}
	if !seen {
		t.Fatal("QQ identity missing")
	}
}
