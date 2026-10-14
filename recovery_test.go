package accountkit_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user/code"
	"os"
	"testing"
	"time"

	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/user"
	"github.com/jackc/pgx/v5/pgxpool"
)

type recoveryState struct {
	Fixture                      sourceFixture
	Access, Snapshot             string
	PendingToken, CompletedToken string
	PendingCode, CompletedCode   user.CodeCredential
	CompletedPair                user.TokenResult
	FlowDeadline                 time.Time
}

// TestRecoveryFixture is an operational fixture, invoked in each phase by verify-recovery.sh.
// It never cleans up a database: the caller supplies separate, disposable empty databases.
func TestRecoveryFixture(t *testing.T) {
	mode := os.Getenv("ACCOUNTKIT_RECOVERY_MODE")
	if mode == "" {
		t.Skip("executed separately by scripts/verify-recovery.sh")
	}
	if mode != "seed" && mode != "verify" && mode != "source-unchanged" {
		t.Fatal("unknown recovery fixture mode")
	}
	dsn := os.Getenv("ACCOUNTKIT_RECOVERY_DSN")
	stateFile := os.Getenv("ACCOUNTKIT_RECOVERY_STATE")
	if dsn == "" || stateFile == "" {
		t.Fatal("recovery DSN and state file are required")
	}
	cfg := minimal()
	cfg.Schema = "accountkit_recovery"
	cfg.EnabledIdentityKinds = []enum.IdentityKind{enum.IdentityPhone, enum.IdentityEmail}
	cfg.RequiredIdentityKinds = []enum.IdentityKind{enum.IdentityEmail}
	cfg.SubjectCipherKeys[2] = k(4)
	if mode != "seed" {
		cfg.SubjectCipherActiveKey = 2
	}
	pc, err := accountkit.PoolConfig(dsn, cfg.Schema)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	captured := &captureSender{}
	a, err := accountkit.New(cfg, accountkit.Deps{Pool: pool, Redis: testRedis(t), SMSSender: captured, EmailSender: captured, Audit: audit.Noop{}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if mode == "seed" {
		f, access := seedSourceFixture(t, pool, a.Config())
		if err := a.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		state := recoveryState{Fixture: f, Access: access}
		meta := user.Meta{IP: "127.0.0.1"}
		createPending := func(phone, device string) user.PendingSignIn {
			challenge, err := a.Users().SendSignInCode(ctx, enum.IdentityPhone, phone, meta)
			if err != nil {
				t.Fatal(err)
			}
			result, err := a.Users().SignInWithCode(ctx, user.CodeCredential{Channel: enum.IdentityPhone, Target: phone, CodeID: challenge.CodeID, Code: captured.code(phone)}, user.Device{ID: device}, meta)
			if err != nil || result.Pending == nil {
				t.Fatalf("recovery pending fixture: %v", err)
			}
			return *result.Pending
		}
		pending := createPending("+8613811100011", "recovery-pending")
		state.PendingToken = pending.SignInCompletionToken
		state.FlowDeadline = pending.ExpireTime
		challenge, err := a.Users().SendSignInCompletionCode(ctx, state.PendingToken, enum.IdentityEmail, "pending-recovery@example.test", user.Device{}, meta)
		if err != nil {
			t.Fatal(err)
		}
		state.PendingCode = user.CodeCredential{Channel: enum.IdentityEmail, Target: "pending-recovery@example.test", CodeID: challenge.CodeID, Code: captured.code("pending-recovery@example.test")}
		completed := createPending("+8613811100012", "recovery-completed")
		state.CompletedToken = completed.SignInCompletionToken
		challenge, err = a.Users().SendSignInCompletionCode(ctx, state.CompletedToken, enum.IdentityEmail, "completed-recovery@example.test", user.Device{}, meta)
		if err != nil {
			t.Fatal(err)
		}
		state.CompletedCode = user.CodeCredential{Channel: enum.IdentityEmail, Target: "completed-recovery@example.test", CodeID: challenge.CodeID, Code: captured.code("completed-recovery@example.test")}
		result, err := a.Users().CompleteSignIn(ctx, state.CompletedToken, state.CompletedCode, user.Device{}, meta)
		if err != nil || result.Tokens == nil {
			t.Fatalf("recovery completed fixture: %v", err)
		}
		state.CompletedPair = *result.Tokens
		assertRecoveryFlows(t, pool, state)
		state.Snapshot = snapshotRecovery(t, pool)
		b, err := json.Marshal(state)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(stateFile, b, 0600); err != nil {
			t.Fatal(err)
		}
		return
	}
	b, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatal(err)
	}
	var state recoveryState
	if err := json.Unmarshal(b, &state); err != nil {
		t.Fatal(err)
	}
	if got := snapshotRecovery(t, pool); got != state.Snapshot {
		t.Fatal("restored data/version differs from the source snapshot")
	}
	if mode == "source-unchanged" {
		return
	}
	for range 2 {
		if err := a.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if snapshotRecovery(t, pool) != state.Snapshot {
		t.Fatal("Migrate changed restored data")
	}
	assertRecoveryFlows(t, pool, state)
	meta := user.Meta{IP: "127.0.0.1"}
	_, err = a.Users().CompleteSignIn(ctx, state.PendingToken, state.PendingCode, user.Device{}, meta)
	if time.Now().Before(state.FlowDeadline) {
		if !errors.Is(err, code.ErrExpired) {
			t.Fatalf("restored pending code without Redis must fail closed: %v", err)
		}
	} else if !errors.Is(err, user.ErrSignInCompletionInvalid) {
		t.Fatalf("expired restored flow accepted: %v", err)
	}
	completedResult, err := a.Users().CompleteSignIn(ctx, state.CompletedToken, state.CompletedCode, user.Device{}, meta)
	var completedDeadline time.Time
	if e := pool.QueryRow(ctx, "SELECT expire_time FROM identity_flow WHERE state=$1", enum.FlowCompleted).Scan(&completedDeadline); e != nil {
		t.Fatal(e)
	}
	if time.Now().Before(completedDeadline) {
		if err != nil || completedResult.Tokens == nil || *completedResult.Tokens != state.CompletedPair {
			t.Fatalf("restored durable result replay: %v", err)
		}
	} else if !errors.Is(err, user.ErrSignInCompletionInvalid) {
		t.Fatalf("expired completed flow accepted: %v", err)
	}
	if _, err := a.Users().Refresh(ctx, state.CompletedPair.RefreshToken, meta); err != nil {
		t.Fatalf("restored completed session refresh: %v", err)
	}
	principal, err := a.Users().Authenticate(ctx, state.Access)
	if err != nil || principal.UserID != state.Fixture.UserID {
		t.Fatalf("restored access unusable: %v", err)
	}
	identity, err := a.Users().RevealIdentity(ctx, user.Admin{}, state.Fixture.UserID, state.Fixture.IdentityID, meta)
	if err != nil || identity.Subject != state.Fixture.Phone {
		t.Fatalf("restored identity not decryptable: %v", err)
	}
	tok, err := a.Users().Refresh(ctx, state.Fixture.RefreshToken, meta)
	if err != nil || tok.UserID != state.Fixture.UserID || tok.Scope != "user:bind" || tok.RefreshToken == state.Fixture.RefreshToken {
		t.Fatalf("restored refresh unusable: %v", err)
	}
}

func snapshotRecovery(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var flows string
	if err := pool.QueryRow(context.Background(), "SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY id),'[]'::jsonb)::text FROM identity_flow t").Scan(&flows); err != nil {
		t.Fatal(err)
	}
	return snapshotSource(t, pool) + "\n" + flows
}

func assertRecoveryFlows(t *testing.T, pool *pgxpool.Pool, state recoveryState) {
	t.Helper()
	rows, err := pool.Query(context.Background(), "SELECT state,payload,cipher_version,expire_time,user_id,session_id FROM identity_flow ORDER BY state")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var status enum.FlowState
		var payload []byte
		var key int16
		var deadline time.Time
		var uid, sid *string
		if err := rows.Scan(&status, &payload, &key, &deadline, &uid, &sid); err != nil {
			t.Fatal(err)
		}
		count++
		if key != 1 || len(payload) == 0 || deadline.IsZero() {
			t.Fatal("flow encryption key/deadline was not preserved")
		}
		for _, secret := range []string{state.PendingToken, state.CompletedToken, state.PendingCode.Target, state.CompletedCode.Target, state.CompletedPair.RefreshToken} {
			if secret != "" && bytes.Contains(payload, []byte(secret)) {
				t.Fatal("flow payload contains plaintext secret")
			}
		}
		if status == enum.FlowPending && (uid != nil || sid != nil || !deadline.Equal(state.FlowDeadline)) {
			t.Fatal("pending account reference/deadline changed")
		}
		if status == enum.FlowCompleted && (uid == nil || sid == nil || *uid != state.CompletedPair.UserID) {
			t.Fatal("completed account/session reference changed")
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("recovery flows=%d want 2", count)
	}
}
