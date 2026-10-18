package user_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/anonymize"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/pii"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/code"
	"github.com/bbxx111/accountkit/user/idp"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSignInCompletionDefersUnknownUser(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	result, err := f.svc.SignInWithIdp(ctx, user.IdpCredential{Kind: enum.IdentityWeChat, AppID: "wx1", Code: "union:pending-source@openid"}, dev1, meta1)
	if err != nil {
		t.Fatal(err)
	}
	if result.Pending == nil || result.Tokens != nil || result.Pending.SignInCompletionToken == "" {
		t.Fatalf("expected pending union: %+v", result)
	}
	for _, table := range []string{"user_account", "identity", "session"} {
		var n int
		if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("%s: got %d rows before completion, want zero", table, n)
		}
	}
}

func pendingFlow(t *testing.T, f *fixture, subject string) *user.PendingSignIn {
	t.Helper()
	r, err := f.svc.SignInWithIdp(context.Background(), user.IdpCredential{Kind: enum.IdentityWeChat, AppID: "wx1", Code: "union:" + subject + "@openid"}, dev1, meta1)
	if err != nil || r.Pending == nil || r.Tokens != nil {
		t.Fatalf("pending: %+v %v", r, err)
	}
	return r.Pending
}

func completionCredential(t *testing.T, f *fixture, p *user.PendingSignIn, kind enum.IdentityKind, target string) user.CodeCredential {
	t.Helper()
	c, err := f.svc.SendSignInCompletionCode(context.Background(), p.SignInCompletionToken, kind, target, user.Device{}, meta1)
	if err != nil {
		t.Fatal(err)
	}
	if c.ExpireTime.After(p.ExpireTime) {
		t.Fatal("code outlived flow")
	}
	return user.CodeCredential{Channel: kind, Target: target, CodeID: c.CodeID, Code: f.sent.code(normalizedTarget(kind, target))}
}

func TestSignInCompletionAssociatesOrCreatesAndReplays(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprint(existing), func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			var wantUser string
			if existing {
				wantUser = f.signIn(t, enum.IdentityEmail, email1, dev2).UserID
			}
			p := pendingFlow(t, f, "association")
			cred := completionCredential(t, f, p, enum.IdentityEmail, email1)
			result, err := f.svc.CompleteSignIn(ctx, p.SignInCompletionToken, cred, user.Device{ID: dev1.ID}, meta1)
			if err != nil {
				t.Fatal(err)
			}
			pair := requireSignInTokens(t, result)
			if pair.IsNewUser == existing || pair.Scope != user.ScopeUser || (existing && pair.UserID != wantUser) {
				t.Fatalf("wrong completion: %+v", pair)
			}
			rows, err := f.repo.Q().ListActiveIdentitiesByUser(ctx, pair.UserID)
			if err != nil || len(rows) != 2 {
				t.Fatalf("identities: %d %v", len(rows), err)
			}
			replay, err := f.svc.CompleteSignIn(ctx, p.SignInCompletionToken, cred, user.Device{}, meta1)
			if err != nil || requireSignInTokens(t, replay) != pair {
				t.Fatalf("replay not original pair: %v", err)
			}
			var users, sessions int
			_ = f.pool.QueryRow(ctx, "SELECT count(*) FROM user_account").Scan(&users)
			_ = f.pool.QueryRow(ctx, "SELECT count(*) FROM session").Scan(&sessions)
			if users != 1 || sessions != 1+map[bool]int{true: 1}[existing] {
				t.Fatalf("duplicates: users=%d sessions=%d", users, sessions)
			}
			if _, err = f.svc.Refresh(ctx, pair.RefreshToken, meta1); err != nil {
				t.Fatal(err)
			}
			if _, err = f.svc.CompleteSignIn(ctx, p.SignInCompletionToken, cred, user.Device{}, meta1); !errors.Is(err, user.ErrSignInAlreadyCompleted) {
				t.Fatalf("rotated result: %v", err)
			}
		})
	}
}

func TestSignInCompletionProofPersistenceAndFinalFailure(t *testing.T) {
	for _, stage := range []string{"proof", "final"} {
		t.Run(stage, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			p := pendingFlow(t, f, stage)
			cred := completionCredential(t, f, p, enum.IdentityEmail, email1)
			condition := "NEW.state = 2"
			if stage == "final" {
				condition = "NEW.state = 3"
			}
			mustExec(t, f, "CREATE FUNCTION reject_flow() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF "+condition+" THEN RAISE EXCEPTION 'synthetic flow failure'; END IF; RETURN NEW; END $$")
			mustExec(t, f, "CREATE TRIGGER reject_flow BEFORE UPDATE ON sign_in_completion_flow FOR EACH ROW EXECUTE FUNCTION reject_flow()")
			before := len(f.audit.Events())
			if _, err := f.svc.CompleteSignIn(ctx, p.SignInCompletionToken, cred, user.Device{}, meta1); err == nil {
				t.Fatal("injected failure accepted")
			}
			proofEvents := 0
			for _, event := range f.audit.Events()[before:] {
				if event.Type == enum.EventSignInCompletionCompleted || event.Type == enum.EventSignIn || event.Type == enum.EventIdentityBound {
					t.Fatal("rolled back operation audited completion success")
				}
				if event.Type == enum.EventSignInCompletionProofVerified {
					proofEvents++
				}
			}
			wantProof := 0
			if stage == "final" {
				wantProof = 1
			}
			if proofEvents != wantProof {
				t.Fatalf("durable proof audit: got %d want %d", proofEvents, wantProof)
			}
			var n int
			_ = f.pool.QueryRow(ctx, "SELECT count(*) FROM user_account").Scan(&n)
			if n != 0 {
				t.Fatal("partial user commit")
			}
			mustExec(t, f, "DROP TRIGGER reject_flow ON sign_in_completion_flow")
			r, err := f.svc.CompleteSignIn(ctx, p.SignInCompletionToken, cred, user.Device{}, meta1)
			if stage == "proof" {
				if !errors.Is(err, code.ErrExpired) {
					t.Fatalf("consumed proof must not return: %v", err)
				}
			} else if err != nil || r.Tokens == nil {
				t.Fatalf("durable proof retry: %+v %v", r, err)
			}
		})
	}
}

func TestSignInCompletionDevicePolicyAndInstanceIsolation(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	p := pendingFlow(t, f, "isolation")
	cred := completionCredential(t, f, p, enum.IdentityEmail, email1)
	for _, dev := range []user.Device{{ID: "other"}, {Name: "other"}} {
		if _, err := f.svc.CompleteSignIn(ctx, p.SignInCompletionToken, cred, dev, meta1); !errors.Is(err, user.ErrSignInCompletionInvalid) {
			t.Fatalf("device: %v", err)
		}
	}
	original := f.svc
	for _, mutate := range []func(*user.Deps){func(d *user.Deps) { d.SignInCompletionFlowContext = "other-instance" }, func(d *user.Deps) { d.RequiredIdentityKinds = []enum.IdentityKind{enum.IdentityEmail} }, func(d *user.Deps) { d.ProviderConfiguration = "changed-apps" }} {
		d := f.deps
		mutate(&d)
		other, err := user.NewService(d)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = other.CompleteSignIn(ctx, p.SignInCompletionToken, cred, user.Device{}, meta1); !errors.Is(err, user.ErrSignInCompletionInvalid) {
			t.Fatalf("changed context: %v", err)
		}
	}
	r, err := original.CompleteSignIn(ctx, p.SignInCompletionToken, cred, user.Device{}, meta1)
	if err != nil || r.Tokens == nil {
		t.Fatalf("invalid attempts consumed proof: %v", err)
	}
	var payload []byte
	var digest string
	if err = f.pool.QueryRow(ctx, "SELECT payload,token_digest FROM sign_in_completion_flow").Scan(&payload, &digest); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(payload, []byte(email1)) || bytes.Contains(payload, []byte(r.Tokens.RefreshToken)) || bytes.Contains(payload, []byte(p.SignInCompletionToken)) {
		t.Fatal("plaintext flow secrets")
	}
	want := sha256.Sum256([]byte(p.SignInCompletionToken))
	if digest != fmt.Sprintf("%x", want) {
		t.Fatal("opaque ticket digest mismatch")
	}
}

func TestSignInCompletionIncompleteProofsAndExistingBindScope(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprint(existing), func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			var uid string
			if existing {
				uid = f.signIn(t, enum.IdentityPhone, phone1, dev2).UserID
			}
			newServiceWith(t, f, func(d *user.Deps) { d.RequiredIdentityKinds = []enum.IdentityKind{enum.IdentityEmail} })
			p := pendingFlow(t, f, "incomplete")
			cred := completionCredential(t, f, p, enum.IdentityPhone, phone1)
			r, err := f.svc.CompleteSignIn(ctx, p.SignInCompletionToken, cred, user.Device{}, meta1)
			if err != nil {
				t.Fatal(err)
			}
			if existing {
				pair := requireSignInTokens(t, r)
				if pair.UserID != uid || pair.Scope != user.ScopeBind || pair.IsNewUser {
					t.Fatalf("existing: %+v", pair)
				}
				return
			}
			if r.Pending == nil || r.Pending.ExpireTime != p.ExpireTime || r.Pending.SignInCompletionToken != p.SignInCompletionToken {
				t.Fatal("incomplete must retain flow and deadline")
			}
			cred2 := completionCredential(t, f, p, enum.IdentityEmail, email1)
			r, err = f.svc.CompleteSignIn(ctx, p.SignInCompletionToken, cred2, user.Device{}, meta1)
			if err != nil || r.Tokens == nil {
				t.Fatalf("additional proof: %v", err)
			}
			idents, _ := f.repo.Q().ListActiveIdentitiesByUser(ctx, r.Tokens.UserID)
			if len(idents) != 3 {
				t.Fatalf("lost earlier proof: %d", len(idents))
			}
		})
	}
}

func TestSignInCompletionConcurrentFinalRequest(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	p := pendingFlow(t, f, "concurrent")
	cred := completionCredential(t, f, p, enum.IdentityEmail, email1)
	const count = 8
	results := make([]user.SignInResult, count)
	errs := make([]error, count)
	var wg sync.WaitGroup
	for i := range count {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = f.svc.CompleteSignIn(ctx, p.SignInCompletionToken, cred, user.Device{}, meta1)
		}()
	}
	wg.Wait()
	var first *user.TokenResult
	for i, err := range errs {
		if err != nil {
			if !errors.Is(err, code.ErrExpired) {
				t.Fatalf("request %d: %v", i, err)
			}
			continue
		}
		pair := requireSignInTokens(t, results[i])
		if first == nil {
			first = &pair
		} else if pair != *first {
			t.Fatal("different replay pair")
		}
	}
	if first == nil {
		t.Fatal("no completion succeeded")
	}
	var n int
	_ = f.pool.QueryRow(ctx, "SELECT count(*) FROM session").Scan(&n)
	if n != 1 {
		t.Fatalf("duplicate sessions %d", n)
	}
}

func TestSignInCompletionExpiryAndDeletionCleanup(t *testing.T) {
	for _, mode := range []string{"expiry", "deletion"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			p := pendingFlow(t, f, mode)
			cred := completionCredential(t, f, p, enum.IdentityEmail, email1)
			if mode == "expiry" {
				f.advance(5 * time.Minute)
				if _, err := f.svc.CompleteSignIn(ctx, p.SignInCompletionToken, cred, user.Device{}, meta1); !errors.Is(err, user.ErrSignInCompletionInvalid) {
					t.Fatal(err)
				}
				if n, err := f.svc.CleanupSignInCompletionFlows(ctx); err != nil || n != 1 {
					t.Fatalf("cleanup %d %v", n, err)
				}
				return
			}
			r, err := f.svc.CompleteSignIn(ctx, p.SignInCompletionToken, cred, user.Device{}, meta1)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.svc.DeleteMe(ctx, principalOf(t, f, requireSignInTokens(t, r)), meta1); err != nil {
				t.Fatal(err)
			}
			var n int
			_ = f.pool.QueryRow(ctx, "SELECT count(*) FROM sign_in_completion_flow").Scan(&n)
			if n != 0 {
				t.Fatal("deleted user retains encrypted flow")
			}
		})
	}
}

func TestSignInCompletionRefusesChangedSourceOwnerAndUnavailableTarget(t *testing.T) {
	for _, mode := range []string{"source", "frozen", "deleted", "limit"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			owner := f.signIn(t, enum.IdentityEmail, email1, dev2)
			p := pendingFlow(t, f, "later-owned")
			cred := completionCredential(t, f, p, enum.IdentityEmail, email1)
			want := user.ErrIdentityConflict
			switch mode {
			case "source":
				_, _, err := f.svc.BindWithIdp(ctx, principalOf(t, f, owner), user.IdpCredential{Kind: enum.IdentityWeChat, AppID: "wx1", Code: "union:later-owned@openid"}, meta1)
				if err != nil {
					t.Fatal(err)
				}
			case "limit":
				_, _, err := f.svc.BindWithIdp(ctx, principalOf(t, f, owner), user.IdpCredential{Kind: enum.IdentityWeChat, AppID: "wx1", Code: "union:other-owned@openid"}, meta1)
				if err != nil {
					t.Fatal(err)
				}
				want = user.ErrIdentityKindLimit
			case "frozen":
				mustExec(t, f, "UPDATE user_account SET state=2 WHERE id=$1", owner.UserID)
				want = user.ErrUserFrozen
			case "deleted":
				mustExec(t, f, "UPDATE user_account SET state=3 WHERE id=$1", owner.UserID)
				want = user.ErrUserPendingDeletion
			}
			if _, err := f.svc.CompleteSignIn(ctx, p.SignInCompletionToken, cred, user.Device{}, meta1); !errors.Is(err, want) {
				t.Fatalf("%s: got %v want %v", mode, err, want)
			}
			events := f.audit.Events()
			last := events[len(events)-1]
			reason := map[string]string{"source": "IDENTITY_ALREADY_BOUND", "frozen": "USER_FROZEN", "deleted": "USER_PENDING_DELETION", "limit": "IDENTITY_KIND_LIMIT"}[mode]
			if last.Type != enum.EventSignInCompletionRejected || last.Reason != reason || last.FlowID == "" {
				t.Fatalf("missing safe associated rejection: %+v", last)
			}
			var n int
			_ = f.pool.QueryRow(ctx, "SELECT count(*) FROM user_account").Scan(&n)
			if n != 1 {
				t.Fatal("created bypass account")
			}
		})
	}
}

func TestSignInCompletionCrossFlowAndPurpose(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	a := pendingFlow(t, f, "flow-a")
	b := pendingFlow(t, f, "flow-b")
	cred := completionCredential(t, f, a, enum.IdentityEmail, email1)
	if _, err := f.svc.CompleteSignIn(ctx, b.SignInCompletionToken, cred, user.Device{}, meta1); !errors.Is(err, code.ErrExpired) {
		t.Fatalf("cross flow: %v", err)
	}
	if _, err := f.svc.SignInWithCode(ctx, cred, dev1, meta1); !errors.Is(err, code.ErrExpired) {
		t.Fatalf("cross purpose: %v", err)
	}
	r, err := f.svc.CompleteSignIn(ctx, a.SignInCompletionToken, cred, user.Device{}, meta1)
	if err != nil || r.Tokens == nil {
		t.Fatalf("legitimate proof consumed: %v", err)
	}
}

func TestSignInCompletionOriginalProofDeadlineAndKeys(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	newServiceWith(t, f, func(d *user.Deps) { d.RequiredIdentityKinds = []enum.IdentityKind{enum.IdentityEmail} })
	issued, err := f.svc.SendSignInCode(ctx, enum.IdentityPhone, phone1, meta1)
	if err != nil {
		t.Fatal(err)
	}
	f.advance(4 * time.Minute)
	r, err := f.svc.SignInWithCode(ctx, user.CodeCredential{Channel: enum.IdentityPhone, Target: phone1, CodeID: issued.CodeID, Code: f.sent.code(phone1)}, dev1, meta1)
	if err != nil || r.Pending == nil || !r.Pending.ExpireTime.Equal(issued.ExpireTime) {
		t.Fatalf("initial expiry extended: %+v %v", r, err)
	}
	if err := f.svc.CheckKeyVersions(ctx); err != nil {
		t.Fatal(err)
	}
	newCipher, err := pii.NewCipher(map[uint16][]byte{2: bytes.Repeat([]byte{8}, 32)}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err = user.CheckKeyVersions(ctx, f.repo, f.dig, newCipher, *f.clock); !errors.Is(err, user.ErrUnknownKeyVersion) {
		t.Fatalf("removed flow key accepted: %v", err)
	}
	f.advance(time.Minute)
	if err = user.CheckKeyVersions(ctx, f.repo, f.dig, newCipher, *f.clock); err != nil {
		t.Fatalf("expired flow retained key requirement: %v", err)
	}
}

type expiringApple struct{ expiry time.Time }

func (a expiringApple) VerifyApple(context.Context, string, string) (idp.Identity, error) {
	return idp.Identity{Kind: enum.IdentityApple, Subject: "expiry-apple", ExpireTime: a.expiry}, nil
}

func TestSignInCompletionUsesVerifiedIdpExpiry(t *testing.T) {
	f := newFixture(t)
	expiry := f.clock.Add(30 * time.Second)
	newServiceWith(t, f, func(d *user.Deps) { d.Apple = expiringApple{expiry: expiry} })
	r, err := f.svc.SignInWithIdp(context.Background(), user.IdpCredential{Kind: enum.IdentityApple, IDToken: "opaque-verified-input", Nonce: "n"}, dev1, meta1)
	if err != nil || r.Pending == nil || !r.Pending.ExpireTime.Equal(expiry) {
		t.Fatalf("verified expiry: %+v %v", r, err)
	}
}

func TestSignInCompletionDoesNotPersistProofAfterUserDeletionWhileWaiting(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	owner := f.signIn(t, enum.IdentityEmail, email1, dev2)
	p := pendingFlow(t, f, "delete-race")
	cred := completionCredential(t, f, p, enum.IdentityEmail, email1)
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT id FROM user_account WHERE id=$1 FOR UPDATE", owner.UserID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := f.svc.CompleteSignIn(ctx, p.SignInCompletionToken, cred, user.Device{}, meta1)
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var n int
		if err = f.pool.QueryRow(ctx, "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%user_account%' AND pid<>pg_backend_pid()").Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("completion did not wait on user lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err = tx.Exec(ctx, "UPDATE user_account SET state=3 WHERE id=$1", owner.UserID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; !errors.Is(err, user.ErrUserPendingDeletion) {
		t.Fatalf("deleted owner: %v", err)
	}
	var state int
	var proofs []string
	if err = f.pool.QueryRow(ctx, "SELECT state,proof_user_ids FROM sign_in_completion_flow").Scan(&state, &proofs); err != nil {
		t.Fatal(err)
	}
	if state != 1 || len(proofs) != 0 {
		t.Fatalf("late proof written after deletion: state=%d owners=%v", state, proofs)
	}
	if _, err = f.svc.CompleteSignIn(ctx, p.SignInCompletionToken, cred, user.Device{}, meta1); !errors.Is(err, code.ErrExpired) {
		t.Fatalf("consumed proof restored: %v", err)
	}
}

func TestSignInCompletionCompetingFlowsDoNotMergeAccounts(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	a := pendingFlow(t, f, "shared-source")
	b := pendingFlow(t, f, "shared-source")
	ca := completionCredential(t, f, a, enum.IdentityEmail, "a@example.test")
	cb := completionCredential(t, f, b, enum.IdentityEmail, "b@example.test")
	start := make(chan struct{})
	done := make(chan error, 2)
	for _, item := range []struct {
		p *user.PendingSignIn
		c user.CodeCredential
	}{{a, ca}, {b, cb}} {
		go func() {
			<-start
			_, err := f.svc.CompleteSignIn(ctx, item.p.SignInCompletionToken, item.c, user.Device{}, meta1)
			done <- err
		}()
	}
	close(start)
	success := 0
	for range 2 {
		err := <-done
		if err == nil {
			success++
		} else if !errors.Is(err, user.ErrIdentityConflict) {
			t.Fatalf("unexpected competing error %v", err)
		}
	}
	if success != 1 {
		t.Fatalf("success count %d", success)
	}
	for _, table := range []string{"user_account", "session"} {
		var n int
		_ = f.pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n)
		if n != 1 {
			t.Fatalf("%s duplicated: %d", table, n)
		}
	}
}

func TestSignInCompletionReplayRejectsRevokedOrExpiredResult(t *testing.T) {
	for _, mode := range []string{"revoked", "access-revoked", "access-expired", "different-request"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			if mode == "access-expired" {
				newServiceWith(t, f, func(d *user.Deps) { d.AccessTTL = 10 * time.Second })
			}
			p := pendingFlow(t, f, mode)
			cred := completionCredential(t, f, p, enum.IdentityEmail, email1)
			r, err := f.svc.CompleteSignIn(ctx, p.SignInCompletionToken, cred, user.Device{}, meta1)
			if err != nil {
				t.Fatal(err)
			}
			pair := requireSignInTokens(t, r)
			switch mode {
			case "revoked":
				if err = f.svc.Revoke(ctx, pair.RefreshToken, meta1); err != nil {
					t.Fatal(err)
				}
			case "access-revoked":
				principal := principalOf(t, f, pair)
				if err = f.deps.Revocation.Revoke(ctx, principal.SessionID, time.Minute); err != nil {
					t.Fatal(err)
				}
			case "access-expired":
				f.advance(11 * time.Second)
			case "different-request":
				cred.Code = "987654"
			}
			if _, err = f.svc.CompleteSignIn(ctx, p.SignInCompletionToken, cred, user.Device{}, meta1); !errors.Is(err, user.ErrSignInAlreadyCompleted) {
				t.Fatalf("replay: %v", err)
			}
		})
	}
}

func TestSignInCompletionPurgeRollbackPreservesFlowUntilSuccessfulAnonymization(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	p := pendingFlow(t, f, "purge-rollback")
	cred := completionCredential(t, f, p, enum.IdentityEmail, email1)
	r, err := f.svc.CompleteSignIn(ctx, p.SignInCompletionToken, cred, user.Device{}, meta1)
	if err != nil {
		t.Fatal(err)
	}
	pair := requireSignInTokens(t, r)
	host := &recordingAnonymizer{name: "synthetic", fail: errors.New("synthetic failure")}
	newServiceWith(t, f, func(d *user.Deps) { d.Anonymizers = []anonymize.Anonymizer{host} })
	mustExec(t, f, "UPDATE user_account SET state=3,purge_time=$2 WHERE id=$1", pair.UserID, *f.clock)
	if _, err = f.svc.PurgeDueUsers(ctx); err == nil {
		t.Fatal("expected callback failure")
	}
	var n int
	_ = f.pool.QueryRow(ctx, "SELECT count(*) FROM sign_in_completion_flow").Scan(&n)
	if n != 1 {
		t.Fatal("rollback lost flow")
	}
	newServiceWith(t, f, func(d *user.Deps) { d.Anonymizers = nil })
	if n, err := f.svc.PurgeDueUsers(ctx); err != nil || n != 1 {
		t.Fatalf("purge retry: %d %v", n, err)
	}
	_ = f.pool.QueryRow(ctx, "SELECT count(*) FROM sign_in_completion_flow").Scan(&n)
	if n != 0 {
		t.Fatal("purged flow remains")
	}
}

func TestSignInIdpRechecksOwnershipAfterUserLock(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	owner := f.signIn(t, enum.IdentityEmail, email1, dev2)
	cred := user.IdpCredential{Kind: enum.IdentityWeChat, AppID: "wx1", Code: "union:stale-owner@openid"}
	identity, _, err := f.svc.BindWithIdp(ctx, principalOf(t, f, owner), cred, meta1)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT id FROM user_account WHERE id=$1 FOR UPDATE", owner.UserID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := f.svc.SignInWithIdp(ctx, cred, dev1, meta1); done <- err }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var n int
		err = f.pool.QueryRow(ctx, "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%user_account%' AND pid<>pg_backend_pid()").Scan(&n)
		if err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("login did not wait for user lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err = tx.Exec(ctx, "UPDATE identity SET delete_time=$2 WHERE id=$1", identity.ID, *f.clock); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; !errors.Is(err, idp.ErrInvalidCredential) {
		t.Fatalf("stale provider identity: %v", err)
	}
}

func TestSignInCompletionStorageUnavailable(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	p := pendingFlow(t, f, "storage-down")
	cfg := f.pool.Config()
	cfg.ConnConfig.Host = "127.0.0.1"
	cfg.ConnConfig.Port = 1
	cfg.ConnConfig.ConnectTimeout = time.Second
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	d := f.deps
	d.Repo = user.NewRepo(pool)
	svc, err := user.NewService(d)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.SendSignInCompletionCode(ctx, p.SignInCompletionToken, enum.IdentityEmail, email1, user.Device{}, meta1); !errors.Is(err, user.ErrUnavailable) {
		t.Fatalf("storage failure: %v", err)
	}
}

func TestSignInCompletionIgnoresTrailingSearchPath(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	p := pendingFlow(t, f, "logical-schema")
	cfg := f.pool.Config()
	schema := strings.Split(cfg.ConnConfig.RuntimeParams["search_path"], ",")[0]
	cfg.ConnConfig.RuntimeParams["search_path"] = "\"" + schema + "\", pg_catalog, public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	d := f.deps
	d.Repo = user.NewRepo(pool)
	svc, err := user.NewService(d)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.SendSignInCompletionCode(ctx, p.SignInCompletionToken, enum.IdentityEmail, email1, user.Device{}, meta1); err != nil {
		t.Fatalf("same logical schema rejected: %v", err)
	}
}
