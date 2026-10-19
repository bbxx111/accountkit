package accountkit_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/code"
	"github.com/jackc/pgx/v5/pgxpool"
)

type recoveryState struct {
	Fixture                      sourceFixture
	Access, Snapshot             string
	PendingToken, CompletedToken string
	PendingCode, CompletedCode   user.CodeCredential
	CompletedPair                user.TokenResult
	FlowDeadline                 time.Time
	CompletedSessionID           string
	CompletedIdentityIDs         []string
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
	a, err := accountkit.New(externalAuditConfig(cfg), accountkit.Deps{Pool: pool, Redis: testRedis(t), SMSSender: captured, EmailSender: captured, Audit: audit.Noop{}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if mode == "seed" {
		f, access := seedFormatFixture(t, pool, a.Config())
		if err := a.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		state := recoveryState{Fixture: f, Access: access}
		meta := user.Meta{IP: "127.0.0.1", ClientInfo: &user.ClientInfo{ClientType: enum.ClientNative, AppName: "Recovery App", AppVersion: "1.0", OSName: "Android", DeviceType: enum.DevicePhone}}
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
		completionMeta := meta
		completionMeta.ClientInfo = &user.ClientInfo{ClientType: enum.ClientNative, AppName: "Recovery App", AppVersion: "2.0"}
		result, err := a.Users().CompleteSignIn(ctx, state.CompletedToken, state.CompletedCode, user.Device{}, completionMeta)
		if err != nil || result.Tokens == nil {
			t.Fatalf("recovery completed fixture: %v", err)
		}
		state.CompletedPair = *result.Tokens
		principal, err := a.Users().Authenticate(ctx, state.CompletedPair.AccessToken)
		if err != nil {
			t.Fatal(err)
		}
		state.CompletedSessionID = principal.SessionID
		rows, err := pool.Query(ctx, "SELECT identity_id FROM session_authentication_identity WHERE session_id=$1 ORDER BY identity_id", state.CompletedSessionID)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			state.CompletedIdentityIDs = append(state.CompletedIdentityIDs, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		assertRecoverySessionContext(t, a, state)
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
	assertRecoverySessionContext(t, a, state)
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
	if e := pool.QueryRow(ctx, "SELECT expire_time FROM sign_in_completion_flow WHERE state=$1", enum.FlowCompleted).Scan(&completedDeadline); e != nil {
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
	if err := pool.QueryRow(context.Background(), "SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY id),'[]'::jsonb)::text FROM sign_in_completion_flow t").Scan(&flows); err != nil {
		t.Fatal(err)
	}
	var contextSnapshot string
	if err := pool.QueryRow(context.Background(), `SELECT jsonb_build_object(
      'sources',(SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY session_id,identity_id),'[]'::jsonb) FROM session_authentication_identity t),
      'client_info',(SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY session_id,snapshot_kind),'[]'::jsonb) FROM session_client_info t))::text`).Scan(&contextSnapshot); err != nil {
		t.Fatal(err)
	}
	return snapshotSource(t, pool) + "\n" + flows + "\n" + contextSnapshot
}

func assertRecoveryFlows(t *testing.T, pool *pgxpool.Pool, state recoveryState) {
	t.Helper()
	rows, err := pool.Query(context.Background(), "SELECT state,payload,cipher_version,expire_time,user_id,session_id FROM sign_in_completion_flow ORDER BY state")
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

// Detects recovery/source-preservation snapshots omitting either session context table.
func TestRecoverySessionContextSnapshot(t *testing.T) {
	a, pool, _ := integrationInstance(t, minimal(), testRedis(t))
	f, _ := seedFormatFixture(t, pool, a.Config())
	ctx := context.Background()
	before := snapshotRecovery(t, pool)
	if _, err := pool.Exec(ctx, "INSERT INTO session_authentication_identity(session_id,identity_id) VALUES($1,$2)", f.SessionID, f.IdentityID); err != nil {
		t.Fatal(err)
	}
	afterSource := snapshotRecovery(t, pool)
	if afterSource == before {
		t.Fatal("recovery snapshot omitted authentication identity associations")
	}
	if _, err := pool.Exec(ctx, "INSERT INTO session_client_info(session_id,snapshot_kind,client_type,app_name,app_version,observe_time,update_time) VALUES($1,$2,$3,'Recovery App','1.0',now(),now())", f.SessionID, enum.ClientSnapshotInitial, enum.ClientNative); err != nil {
		t.Fatal(err)
	}
	afterInitial := snapshotRecovery(t, pool)
	if afterInitial == afterSource {
		t.Fatal("recovery snapshot omitted INITIAL client info")
	}
	if _, err := pool.Exec(ctx, "INSERT INTO session_client_info(session_id,snapshot_kind,client_type,app_name,app_version,observe_time,update_time) VALUES($1,$2,$3,'Recovery App','2.0',now(),now())", f.SessionID, enum.ClientSnapshotLatest, enum.ClientNative); err != nil {
		t.Fatal(err)
	}
	afterLatest := snapshotRecovery(t, pool)
	if afterLatest == afterInitial {
		t.Fatal("recovery snapshot omitted LATEST client info")
	}
	if _, err := pool.Exec(ctx, "UPDATE session_client_info SET app_version='3.0' WHERE session_id=$1 AND snapshot_kind=$2", f.SessionID, enum.ClientSnapshotLatest); err != nil {
		t.Fatal(err)
	}
	if snapshotRecovery(t, pool) == afterLatest {
		t.Fatal("source-unchanged snapshot missed changed client info")
	}
}

func assertRecoverySessionContext(t *testing.T, a *accountkit.Auth, state recoveryState) {
	t.Helper()
	ctx := context.Background()
	known, err := a.Users().GetCurrentSession(ctx, user.Principal{UserID: state.CompletedPair.UserID, SessionID: state.CompletedSessionID, Scope: user.ScopeUser})
	if err != nil {
		t.Fatalf("restored known session context: %v", err)
	}
	if !known.AuthenticationIdentitiesKnown || len(known.AuthenticationIdentities) != 2 || len(state.CompletedIdentityIDs) != 2 {
		t.Fatal("completed session lost known two-identity authentication context")
	}
	kinds := map[enum.IdentityKind]bool{}
	for n, identity := range known.AuthenticationIdentities {
		if identity.ID != state.CompletedIdentityIDs[n] {
			t.Fatal("restored authentication identity differs from source")
		}
		kinds[identity.Kind] = true
	}
	if !kinds[enum.IdentityPhone] || !kinds[enum.IdentityEmail] {
		t.Fatal("restored completed session lost PHONE/EMAIL source")
	}
	if known.InitialClientInfo == nil || known.ClientInfo == nil || known.ClientInfoUpdateTime == nil || known.ClientInfoUpdateTime.IsZero() {
		t.Fatal("restored session lost client snapshots/observation time")
	}
	initial, latest := known.InitialClientInfo, known.ClientInfo
	if initial.ClientType != enum.ClientNative || initial.AppName != "Recovery App" || initial.AppVersion != "1.0" || initial.OSName != "Android" || initial.DeviceType != enum.DevicePhone || latest.ClientType != enum.ClientNative || latest.AppName != "Recovery App" || latest.AppVersion != "2.0" || latest.OSName != "Android" || latest.DeviceType != enum.DevicePhone {
		t.Fatal("restored INITIAL/LATEST client info differs")
	}
	unknown, err := a.Users().GetCurrentSession(ctx, user.Principal{UserID: state.Fixture.UserID, SessionID: state.Fixture.SessionID, Scope: user.ScopeUser})
	if err != nil {
		t.Fatalf("restored legacy session context: %v", err)
	}
	if unknown.AuthenticationIdentitiesKnown || len(unknown.AuthenticationIdentities) != 0 || unknown.InitialClientInfo != nil || unknown.ClientInfo != nil || unknown.ClientInfoUpdateTime != nil {
		t.Fatal("legacy unknown session acquired fabricated sources or snapshots")
	}
}
