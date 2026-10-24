package user_test

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
	ob "github.com/bbxx111/accountkit/observability"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/idp"
)

// A synthetic verified provider keeps network outside the domain transaction.
func alipayProvider(kind enum.IdentityKind, calls *atomic.Int32) idp.Provider {
	return adaptedProvider{kind: kind, fp: "alipay-test-policy", verify: func(ctx context.Context, f map[string]string) (idp.Identity, error) {
		if calls != nil {
			calls.Add(1)
		}
		if err := ctx.Err(); err != nil {
			return idp.Identity{}, err
		}
		return idp.Identity{Kind: kind, Subject: "unionid:" + f["sub"], UnionID: f["sub"], OpenIDs: map[string]string{f["app"]: f["openid"]}, Name: f["name"], Picture: f["picture"]}, nil
	}}
}

func alipayCred(name, picture string) user.IdpCredential {
	return providerCred(enum.IdentityAlipay, map[string]string{"sub": "person", "app": "app-one", "openid": "open-one", "name": name, "picture": picture})
}

func useAlipay(t *testing.T, f *fixture, calls *atomic.Int32, required []enum.IdentityKind) {
	t.Helper()
	newServiceWith(t, f, func(d *user.Deps) {
		*d = withProvider(*d, alipayProvider(enum.IdentityAlipay, calls))
		d.RequiredIdentityKinds = required
	})
}

func assertAlipayProfile(t *testing.T, f *fixture, uid, name, avatar string) {
	t.Helper()
	me, err := f.svc.GetMe(context.Background(), uid)
	if err != nil || me.DisplayName != name || me.AvatarURL != avatar {
		t.Fatalf("profile = %+v, %v; want %q %q", me, err, name, avatar)
	}
}

func TestAlipayLaterLoginBackfillsOnlyEmptyFields(t *testing.T) {
	for _, tc := range []struct{ name, avatar, wantName, wantAvatar string }{
		{"", "", "Alipay Name", "https://example.test/alipay.png"},
		{"Mine", "", "Mine", "https://example.test/alipay.png"},
		{"", "https://example.test/mine.png", "Alipay Name", "https://example.test/mine.png"},
		{"Mine", "https://example.test/mine.png", "Mine", "https://example.test/mine.png"},
	} {
		t.Run(tc.name+tc.avatar, func(t *testing.T) {
			f := newFixture(t)
			useAlipay(t, f, nil, []enum.IdentityKind{})
			first := requireSignInTokens(t, mustSignInWithIdp(t, f, alipayCred("", "")))
			rows, err := f.repo.Q().ListActiveIdentitiesByUser(context.Background(), first.UserID)
			if err != nil || len(rows) != 1 {
				t.Fatal(err)
			}
			id := rows[0].ID
			if _, err := f.svc.UpdateProfile(context.Background(), first.UserID, user.ProfilePatch{DisplayName: &tc.name, AvatarURL: &tc.avatar}); err != nil {
				t.Fatal(err)
			}
			before, err := f.repo.Q().GetUserByID(context.Background(), first.UserID)
			if err != nil {
				t.Fatal(err)
			}
			f.advance(time.Second)
			again := requireSignInTokens(t, mustSignInWithIdp(t, f, alipayCred("Alipay Name", "https://example.test/alipay.png")))
			if again.UserID != first.UserID || again.IsNewUser {
				t.Fatal("later sign-in changed user")
			}
			assertAlipayProfile(t, f, first.UserID, tc.wantName, tc.wantAvatar)
			rows, err = f.repo.Q().ListActiveIdentitiesByUser(context.Background(), first.UserID)
			if err != nil || len(rows) != 1 || rows[0].ID != id {
				t.Fatal("later sign-in changed identity")
			}
			if tc.name != "" && tc.avatar != "" {
				after, _ := f.repo.Q().GetUserByID(context.Background(), first.UserID)
				if !after.UpdateTime.Equal(before.UpdateTime) {
					t.Fatal("nonempty profile changed update_time")
				}
			}
		})
	}
}

func TestAlipayClearedProfileUsesOnlyCurrentClaims(t *testing.T) {
	f := newFixture(t)
	useAlipay(t, f, nil, []enum.IdentityKind{})
	first := requireSignInTokens(t, mustSignInWithIdp(t, f, alipayCred("Old", "https://example.test/old.png")))
	empty := ""
	if _, err := f.svc.UpdateProfile(context.Background(), first.UserID, user.ProfilePatch{DisplayName: &empty, AvatarURL: &empty}); err != nil {
		t.Fatal(err)
	}
	mustSignInWithIdp(t, f, alipayCred("", ""))
	assertAlipayProfile(t, f, first.UserID, "", "")
	mustSignInWithIdp(t, f, alipayCred("New\n\u202e", "https://example.test/new.png"))
	assertAlipayProfile(t, f, first.UserID, "New", "https://example.test/new.png")
}

func TestAlipayLoginProfileRollbackAfterSessionFailure(t *testing.T) {
	f := newFixture(t)
	useAlipay(t, f, nil, []enum.IdentityKind{})
	first := requireSignInTokens(t, mustSignInWithIdp(t, f, alipayCred("", "")))
	mustExec(t, f, `CREATE FUNCTION reject_alipay_session() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic session failure'; END $$; CREATE TRIGGER reject_alipay_session BEFORE INSERT ON session FOR EACH ROW EXECUTE FUNCTION reject_alipay_session()`)
	if _, err := f.svc.SignInWithIdp(context.Background(), alipayCred("Rollback", "https://example.test/rollback.png"), dev2, meta1); err == nil {
		t.Fatal("session failure succeeded")
	}
	assertAlipayProfile(t, f, first.UserID, "", "")
	rows, _ := f.repo.Q().ListActiveIdentitiesByUser(context.Background(), first.UserID)
	if strings.Contains(string(rows[0].ProviderMeta), "Rollback") {
		t.Fatal("provider metadata survived rollback")
	}
	var count int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM session`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("sessions=%d %v", count, err)
	}
}

func TestAlipayLoginRechecksConcurrentProfileAndState(t *testing.T) {
	for _, state := range []enum.UserState{enum.UserActive, enum.UserFrozen, enum.UserPendingDeletion, enum.UserDeleted} {
		t.Run(state.String(), func(t *testing.T) {
			f := newFixture(t)
			useAlipay(t, f, nil, []enum.IdentityKind{})
			first := requireSignInTokens(t, mustSignInWithIdp(t, f, alipayCred("", "")))
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			tx := identityRaceLock(t, ctx, f, first.UserID, "")
			done := make(chan error, 1)
			go func() {
				_, err := f.svc.SignInWithIdp(ctx, alipayCred("Provider", "https://example.test/provider.png"), dev2, meta1)
				done <- err
			}()
			waitIdentityRaceBlocked(t, ctx, f, tx, done)
			if _, err := tx.Exec(ctx, `UPDATE user_account SET state=$2,display_name='User Value',avatar_url='https://example.test/user.png' WHERE id=$1`, first.UserID, state); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			err := <-done
			if state == enum.UserActive && err != nil {
				t.Fatal(err)
			}
			if state != enum.UserActive && err == nil {
				t.Fatal("inactive login accepted")
			}
			var name, avatar string
			if err := f.pool.QueryRow(ctx, `SELECT display_name,avatar_url FROM user_account WHERE id=$1`, first.UserID).Scan(&name, &avatar); err != nil || name != "User Value" || avatar != "https://example.test/user.png" {
				t.Fatalf("concurrent values overwritten: %q %q %v", name, avatar, err)
			}
		})
	}
}

func TestAlipayObservationUsesFinalDomainResult(t *testing.T) {
	f, r := observedFixture(t)
	f.deps = withProvider(f.deps, alipayProvider(enum.IdentityAlipay, nil))
	f.deps.RequiredIdentityKinds = []enum.IdentityKind{}
	rebuildMetricsService(t, f)
	first := requireSignInTokens(t, mustSignInWithIdp(t, f, alipayCred("", "")))
	mustExec(t, f, `UPDATE user_account SET state=$2 WHERE id=$1`, first.UserID, enum.UserFrozen)
	_, err := f.svc.SignInWithIdp(context.Background(), alipayCred("Later", ""), dev2, meta1)
	if !errors.Is(err, user.ErrUserFrozen) {
		t.Fatal(err)
	}
	deps, auth := dependencyValues(r, ob.ComponentIDP, ob.DependencyVerifyAlipay), authValues(r)
	if len(deps) != 2 || deps[0].Result != ob.ResultSuccess || deps[1].Result != ob.ResultSuccess || len(auth) != 2 || auth[0].Method != ob.MethodAlipay || auth[0].Result != ob.ResultSuccess || auth[1].Method != ob.MethodAlipay || auth[1].Result != ob.ResultRejected {
		t.Fatalf("deps=%+v auth=%+v", deps, auth)
	}
}

// Inspect/reseal the actual encrypted proof, as legacy-wire tests do. Production
// APIs cannot add a provider proof during completion; the owned-proof fixture
// explicitly exercises finalizeSignIn's defensive, already-owned branch.
func alipayPendingPayload(t *testing.T, f *fixture, mutate func(map[string]any)) map[string]any {
	t.Helper()
	ctx := context.Background()
	var id string
	var ct []byte
	var version uint16
	if err := f.pool.QueryRow(ctx, `SELECT id,payload,cipher_version FROM sign_in_completion_flow`).Scan(&id, &ct, &version); err != nil {
		t.Fatal(err)
	}
	searchPath, _, _ := strings.Cut(f.pool.Config().ConnConfig.RuntimeParams["search_path"], ",")
	searchPath = strings.Trim(strings.TrimSpace(searchPath), `"`)
	extra := f.deps.SignInCompletionFlowContext
	if extra == "" {
		extra = f.deps.IdentityFlowContext
	}
	wire, _ := json.Marshal(struct{ SearchPath, Prefix, JWT, Extra string }{searchPath, f.deps.Codes.InstancePrefix(), f.deps.Signer.ContextFingerprint(), extra})
	hash := sha256.Sum256(wire)
	aad, _ := json.Marshal([]string{"accountkit-sign-in-completion-v1", fmt.Sprintf("%x", hash), id})
	plain, err := f.ciph.DecryptWithAAD(ct, version, aad)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err = json.Unmarshal([]byte(plain), &payload); err != nil {
		t.Fatal(err)
	}
	if mutate != nil {
		mutate(payload)
		b, _ := json.Marshal(payload)
		ct, version, err = f.ciph.EncryptWithAAD(string(b), aad)
		if err != nil {
			t.Fatal(err)
		}
		mustExec(t, f, `UPDATE sign_in_completion_flow SET payload=$1,cipher_version=$2 WHERE id=$3`, ct, version, id)
	}
	return payload
}

func TestAlipayPendingSanitizesAndCompletesWithoutVerifyReplay(t *testing.T) {
	f := newFixture(t)
	var calls atomic.Int32
	useAlipay(t, f, &calls, nil)
	pending := mustSignInWithIdp(t, f, alipayCred("Pending\n\u202e", "http://insecure.example.test/avatar"))
	if pending.Pending == nil {
		t.Fatal("expected pending")
	}
	payload := alipayPendingPayload(t, f, nil)
	provider := payload["Proofs"].([]any)[0].(map[string]any)["Provider"].(map[string]any)
	if provider["Name"] != "Pending" || provider["Picture"] != "" {
		t.Fatalf("unsanitized pending provider: %+v", provider)
	}
	var n int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM user_account`).Scan(&n); err != nil || n != 0 {
		t.Fatal("pending wrote an account")
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

func TestAlipayPendingAlreadyOwnedProofBackfills(t *testing.T) {
	f := newFixture(t)
	var calls atomic.Int32
	useAlipay(t, f, &calls, nil)
	owner := f.signIn(t, enum.IdentityEmail, email1, dev2)
	pending := mustSignInWithIdp(t, f, alipayCred("Pending Owner", "https://example.test/pending.png"))
	if pending.Pending == nil {
		t.Fatal("expected pending")
	}
	bound, _, err := f.svc.BindWithIdp(context.Background(), principalOf(t, f, owner), alipayCred("", ""), meta1)
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

func TestAlipayPendingConcurrentOwnershipRejectsWithoutBackfill(t *testing.T) {
	f := newFixture(t)
	useAlipay(t, f, nil, nil)
	owner := f.signIn(t, enum.IdentityEmail, email1, dev2)
	pending := mustSignInWithIdp(t, f, alipayCred("Must Roll Back", "https://example.test/pending.png"))
	if pending.Pending == nil {
		t.Fatal("expected pending")
	}
	if _, _, err := f.svc.BindWithIdp(context.Background(), principalOf(t, f, owner), alipayCred("", ""), meta1); err != nil {
		t.Fatal(err)
	}
	cred := completionCredential(t, f, pending.Pending, enum.IdentityEmail, email1)
	if _, err := f.svc.CompleteSignIn(context.Background(), pending.Pending.SignInCompletionToken, cred, dev1, meta1); !errors.Is(err, user.ErrIdentityConflict) {
		t.Fatalf("concurrent ownership changed: %v", err)
	}
	assertAlipayProfile(t, f, owner.UserID, "", "")
}

func TestAlipayConcurrentAppsShareSingleUnionIdentity(t *testing.T) {
	f := newFixture(t)
	var calls atomic.Int32
	useAlipay(t, f, &calls, []enum.IdentityKind{})
	start := make(chan struct{})
	type outcome struct {
		result user.SignInResult
		err    error
	}
	done := make(chan outcome, 2)
	for _, app := range []string{"app-one", "app-two"} {
		go func(app string) {
			<-start
			r, err := f.svc.SignInWithIdp(context.Background(), providerCred(enum.IdentityAlipay, map[string]string{"sub": "shared", "app": app, "openid": app + "-open", "name": "Shared"}), user.Device{ID: app}, meta1)
			done <- outcome{r, err}
		}(app)
	}
	close(start)
	a, b := <-done, <-done
	if a.err != nil || b.err != nil {
		t.Fatalf("concurrent login: %v %v", a.err, b.err)
	}
	pa, pb := requireSignInTokens(t, a.result), requireSignInTokens(t, b.result)
	if pa.UserID != pb.UserID || pa.IsNewUser == pb.IsNewUser || calls.Load() != 2 {
		t.Fatal("concurrent union identity duplicated or reverified")
	}
	rows, err := f.repo.Q().ListActiveIdentitiesByUser(context.Background(), pa.UserID)
	if err != nil || len(rows) != 1 || !strings.Contains(string(rows[0].ProviderMeta), "app-one-open") || !strings.Contains(string(rows[0].ProviderMeta), "app-two-open") {
		t.Fatalf("union metadata=%+v %v", rows, err)
	}
	var n int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM user_account`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("users=%d %v", n, err)
	}
}

func TestAlipayUniqueRetryDoesNotRepeatVerification(t *testing.T) {
	f := newFixture(t)
	var calls atomic.Int32
	useAlipay(t, f, &calls, []enum.IdentityKind{})
	mustExec(t, f, `CREATE SEQUENCE alipay_first_collision; CREATE FUNCTION alipay_collision() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF nextval('alipay_first_collision')=1 THEN RAISE EXCEPTION USING ERRCODE='23505',MESSAGE='synthetic collision'; END IF; RETURN NEW; END $$; CREATE TRIGGER alipay_collision BEFORE INSERT ON session FOR EACH ROW EXECUTE FUNCTION alipay_collision()`)
	first := requireSignInTokens(t, mustSignInWithIdp(t, f, alipayCred("Retried", "https://example.test/retried.png")))
	if calls.Load() != 1 {
		t.Fatal("transaction retry called Verify again")
	}
	assertAlipayProfile(t, f, first.UserID, "Retried", "https://example.test/retried.png")
}

func TestAlipayConcurrentPATCHNonemptyProfileWins(t *testing.T) {
	f := newFixture(t)
	useAlipay(t, f, nil, []enum.IdentityKind{})
	first := requireSignInTokens(t, mustSignInWithIdp(t, f, alipayCred("", "")))
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
		_, err := f.svc.SignInWithIdp(ctx, alipayCred("Provider", "https://example.test/provider.png"), dev2, meta1)
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

func TestAlipayLoginCannotRestoreProfileAfterConcurrentPurge(t *testing.T) {
	f := newFixture(t)
	useAlipay(t, f, nil, []enum.IdentityKind{})
	first := requireSignInTokens(t, mustSignInWithIdp(t, f, alipayCred("", "")))
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
		_, err := f.svc.SignInWithIdp(ctx, alipayCred("Must Not Restore", "https://example.test/late.png"), dev2, meta1)
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

func TestAlipayBuiltInPolicyOrder(t *testing.T) {
	p, err := user.NewIdentityPolicy([]enum.IdentityKind{enum.IdentityKind("AAA_SSO"), enum.IdentityAlipay, enum.IdentityFacebook, enum.IdentityGoogle, enum.IdentityApple, enum.IdentityWeChat, enum.IdentityEmail, enum.IdentityPhone}, []enum.IdentityKind{}, enum.IdentityKind("AAA_SSO"))
	want := []enum.IdentityKind{enum.IdentityPhone, enum.IdentityEmail, enum.IdentityWeChat, enum.IdentityApple, enum.IdentityGoogle, enum.IdentityFacebook, enum.IdentityAlipay, enum.IdentityKind("AAA_SSO")}
	if err != nil || !slices.Equal(p.EnabledKinds(), want) {
		t.Fatalf("builtin policy order: %v %v", p, err)
	}
}

func TestAlipayDisabledHistoricalIdentityRemainsManageable(t *testing.T) {
	f := newFixture(t)
	useAlipay(t, f, nil, nil)
	phone := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	p := principalOf(t, f, phone)
	bound, _, err := f.svc.BindWithIdp(context.Background(), p, alipayCred("", ""), meta1)
	if err != nil {
		t.Fatal(err)
	}
	newServiceWith(t, f, func(d *user.Deps) {
		d.EnabledIdentityKinds = []enum.IdentityKind{enum.IdentityPhone, enum.IdentityEmail}
		*d = withProvider(*d, alipayProvider(enum.IdentityAlipay, nil))
	})
	if _, err := f.svc.SignInWithIdp(context.Background(), alipayCred("Must Not Seed", ""), dev2, meta1); !errors.Is(err, user.ErrIdentityKindNotEnabled) {
		t.Fatal(err)
	}
	rows, err := f.svc.ListIdentities(context.Background(), phone.UserID)
	if err != nil || len(rows) != 2 {
		t.Fatalf("disabled history invisible: %+v %v", rows, err)
	}
	if err := f.svc.UnbindIdentity(context.Background(), p, bound.ID, meta1); err != nil {
		t.Fatal(err)
	}
	assertAlipayProfile(t, f, phone.UserID, "", "")
}

func TestAlipayConflictingMetadataRollsBackProfile(t *testing.T) {
	f := newFixture(t)
	useAlipay(t, f, nil, []enum.IdentityKind{})
	first := requireSignInTokens(t, mustSignInWithIdp(t, f, alipayCred("", "")))
	cred := providerCred(enum.IdentityAlipay, map[string]string{"sub": "person", "app": "app-one", "openid": "conflicting-open", "name": "Must Not Seed", "picture": "https://example.test/conflict.png"})
	if _, err := f.svc.SignInWithIdp(context.Background(), cred, dev2, meta1); !errors.Is(err, user.ErrIdentityConflict) {
		t.Fatal(err)
	}
	assertAlipayProfile(t, f, first.UserID, "", "")
	rows, err := f.repo.Q().ListActiveIdentitiesByUser(context.Background(), first.UserID)
	if err != nil || len(rows) != 1 || strings.Contains(string(rows[0].ProviderMeta), "conflicting-open") {
		t.Fatalf("conflict modified metadata: %+v %v", rows, err)
	}
}

func TestAlipayGatewayObservationsAndFlowFingerprint(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	private := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	public := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PUBLIC KEY", Bytes: x509.MarshalPKCS1PublicKey(&key.PublicKey)}))
	signed := func(node, raw string) string {
		hash := sha256.Sum256([]byte(raw))
		sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hash[:])
		if err != nil {
			t.Fatal(err)
		}
		return fmt.Sprintf(`{%q:%s,"sign":%q}`, node, raw, base64.StdEncoding.EncodeToString(sig))
	}
	tokenResponse := signed("alipay_system_oauth_token_response", `{"union_id":"person","open_id":"open-one","access_token":"synthetic-local-only"}`)
	profileResponse := signed("alipay_user_info_share_response", `{"code":"10000","open_id":"open-one"}`)
	for _, scenario := range []string{"missing fields", "degraded", "frozen", "pending", "canceled"} {
		t.Run(scenario, func(t *testing.T) {
			f, r := observedFixture(t)
			if scenario == "frozen" {
				f.deps = withProvider(f.deps, alipayProvider(enum.IdentityAlipay, nil))
				f.deps.RequiredIdentityKinds = []enum.IdentityKind{}
				rebuildMetricsService(t, f)
				first := requireSignInTokens(t, mustSignInWithIdp(t, f, alipayCred("", "")))
				mustExec(t, f, `UPDATE user_account SET state=$2 WHERE id=$1`, first.UserID, enum.UserFrozen)
				r.Reset()
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				requests.Add(1)
				if err := req.ParseForm(); err != nil {
					w.WriteHeader(400)
					return
				}
				if req.Form.Get("method") == "alipay.system.oauth.token" {
					fmt.Fprint(w, tokenResponse)
					return
				}
				if scenario == "canceled" {
					cancel()
					<-req.Context().Done()
					return
				}
				if scenario == "degraded" {
					w.WriteHeader(503)
					return
				}
				fmt.Fprint(w, profileResponse)
			}))
			defer srv.Close()
			cfg := idp.AlipayConfig{Apps: []idp.AlipayApp{{AppID: "2026000000000001", PrivateKey: private, AlipayPublicKey: public}}, GatewayURL: srv.URL}
			provider, err := idp.NewAlipay(idp.AlipayOptions{Config: cfg, Observer: r, Logger: f.deps.Logger})
			if err != nil {
				t.Fatal(err)
			}
			f.deps = withProvider(f.deps, provider)
			f.deps.RequiredIdentityKinds = []enum.IdentityKind{}
			if scenario == "pending" {
				f.deps.RequiredIdentityKinds = nil
			}
			rebuildMetricsService(t, f)
			result, err := f.svc.SignInWithIdp(ctx, providerCred(enum.IdentityAlipay, map[string]string{"app_id": "2026000000000001", "code": "synthetic-code"}), dev1, meta1)
			want := ob.ResultSuccess
			switch scenario {
			case "frozen":
				want = ob.ResultRejected
				if !errors.Is(err, user.ErrUserFrozen) {
					t.Fatal(err)
				}
			case "canceled":
				want = ob.ResultCanceled
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			case "pending":
				want = ob.ResultPending
				if err != nil || result.Pending == nil {
					t.Fatal("expected pending", err)
				}
			default:
				if err != nil || result.Tokens == nil {
					t.Fatal("expected successful sign-in", err)
				}
			}
			deps, auth := dependencyValues(r, ob.ComponentIDP, ob.DependencyVerifyAlipay), authValues(r)
			depWant := ob.ResultSuccess
			if scenario == "canceled" {
				depWant = ob.ResultCanceled
			}
			if len(deps) != 1 || deps[0].Result != depWant || len(auth) != 1 || auth[0].Method != ob.MethodAlipay || auth[0].Result != want || requests.Load() != 2 {
				t.Fatalf("deps=%+v auth=%+v requests=%d", deps, auth, requests.Load())
			}
			var degradations int64
			for _, v := range r.Snapshot() {
				if d, ok := v.(ob.DegradationObservation); ok && d.Scenario == ob.DegradationAlipayProfileUnavailable {
					degradations += d.Count
				}
			}
			wantDegradations := int64(0)
			if scenario == "degraded" {
				wantDegradations = 1
			}
			if degradations != wantDegradations {
				t.Fatalf("degradation=%d want=%d", degradations, wantDegradations)
			}
			if scenario == "canceled" {
				var n int
				if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM user_account`).Scan(&n); err != nil || n != 0 {
					t.Fatal("canceled verification wrote an account")
				}
			}
			if scenario == "pending" {
				cfg.SubjectMode = idp.AlipaySubjectModeOpenID
				changed, err := idp.NewAlipay(idp.AlipayOptions{Config: cfg})
				if err != nil {
					t.Fatal(err)
				}
				f.deps = withProvider(f.deps, changed)
				rebuildMetricsService(t, f)
				if _, err := f.svc.SendSignInCompletionCode(context.Background(), result.Pending.SignInCompletionToken, enum.IdentityEmail, email1, dev1, meta1); !errors.Is(err, user.ErrSignInCompletionInvalid) {
					t.Fatalf("policy fingerprint change accepted: %v", err)
				}
			}
		})
	}
}
