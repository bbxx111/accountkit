package user_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/session/revocation"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/code"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/redis/go-redis/v9"
)

type completionCodeHook struct {
	before func()
	once   sync.Once
}

func (h *completionCodeHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *completionCodeHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h *completionCodeHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if h.before != nil && (cmd.Name() == "evalsha" || cmd.Name() == "eval") {
			h.once.Do(h.before)
		}
		return next(ctx, cmd)
	}
}

func TestSignInCompletionSendCodeRechecksRedisDeadline(t *testing.T) {
	testCompletionRedisDeadline(t, false)
}

func TestSignInCompletionVerifyCodeRechecksRedisDeadline(t *testing.T) {
	testCompletionRedisDeadline(t, true)
}

func testCompletionRedisDeadline(t *testing.T, verify bool) {
	t.Helper()
	for _, flowExpires := range []bool{true, false} {
		name := "flow expires"
		if !flowExpires {
			name = "only code expires"
		}
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			f.mr.SetTime(*f.clock)
			rdb := redis.NewClient(&redis.Options{Addr: f.mr.Addr()})
			t.Cleanup(func() { _ = rdb.Close() })
			hook := &completionCodeHook{}
			rdb.AddHook(hook)
			ttl := time.Minute
			if flowExpires {
				ttl = 5 * time.Minute
			}
			newServiceWith(t, f, func(d *user.Deps) {
				d.CodeTTL = ttl
				d.Codes = code.NewStore(rdb, "t:", f.dig, code.Options{FailureLimitPerTarget: 10, FailureWindow: 15 * time.Minute, TTL: ttl, Cooldown: time.Minute, MaxAttempts: 5, DailyLimitPerTarget: 10, DailyLimitPerIP: 100, Now: func() time.Time { return *f.clock }})
			})
			p := pendingFlow(t, f, "redis-code-deadline")
			var cred user.CodeCredential
			if verify {
				cred = completionCredential(t, f, p, enum.IdentityEmail, email1)
			}
			hook.before = func() {
				// Cross the deadline after the service's initial SQL read and the
				// store's clock read, but before Redis executes the script.
				f.advance(ttl)
				f.mr.SetTime(*f.clock)
			}
			meta := user.Meta{IP: meta1.IP, RequestID: "redis-deadline-rejection"}
			var err error
			if verify {
				var result user.SignInResult
				result, err = f.svc.CompleteSignIn(ctx, p.SignInCompletionToken, cred, user.Device{}, meta)
				if result.Tokens != nil || result.Pending != nil {
					t.Fatal("expired verification returned success")
				}
			} else {
				_, err = f.svc.SendSignInCompletionCode(ctx, p.SignInCompletionToken, enum.IdentityEmail, email1, user.Device{}, meta)
				if f.sent.code(email1) != "" || len(f.mr.Keys()) != 0 {
					t.Fatal("expired issue sent a code or spent quota")
				}
			}
			want, reason := code.ErrExpired, "CODE_EXPIRED"
			if flowExpires {
				want, reason = user.ErrSignInCompletionInvalid, "SIGN_IN_COMPLETION_INVALID"
			}
			if !errors.Is(err, want) {
				t.Errorf("Redis crossed deadline: got %v, want %v", err, want)
			}
			var rejected bool
			for _, event := range f.audit.Events() {
				if event.RequestID == meta.RequestID && event.Type == enum.EventSignInCompletionRejected {
					rejected = true
					if event.Reason != reason || event.FlowID == "" {
						t.Errorf("rejection audit: reason=%s flow=%s, want %s", event.Reason, event.FlowID, reason)
					}
				}
				if event.Type == enum.EventSignInCompletionProofVerified {
					t.Fatal("expired code persisted a proof")
				}
			}
			if !rejected {
				t.Fatal("missing rejection audit")
			}
			for _, table := range []string{"user_account", "identity", "session"} {
				var count int
				if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
					t.Fatalf("%s changed: count=%d err=%v", table, count, err)
				}
			}
			for _, key := range f.mr.Keys() {
				if strings.Contains(key, "verify_failure:") {
					t.Fatal("expired verification spent failure budget")
				}
			}
		})
	}
}

func TestSignInCompletionCodeFailurePreservesFreshFlowError(t *testing.T) {
	for _, storageFailure := range []bool{false, true} {
		name := "flow removed"
		if storageFailure {
			name = "flow storage failure"
		}
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			p := pendingFlow(t, f, "fresh-flow-error")
			cred := completionCredential(t, f, p, enum.IdentityEmail, email1)
			rdb := redis.NewClient(&redis.Options{Addr: f.mr.Addr()})
			t.Cleanup(func() { _ = rdb.Close() })
			rdb.AddHook(&completionCodeHook{before: func() {
				if storageFailure {
					mustExec(t, f, "ALTER TABLE identity_flow RENAME TO unavailable_identity_flow")
				} else {
					mustExec(t, f, "DELETE FROM identity_flow")
				}
			}})
			newServiceWith(t, f, func(d *user.Deps) {
				d.Codes = code.NewStore(rdb, "t:", f.dig, code.Options{FailureLimitPerTarget: 10, FailureWindow: 15 * time.Minute, TTL: 5 * time.Minute, Cooldown: time.Minute, MaxAttempts: 5, DailyLimitPerTarget: 10, DailyLimitPerIP: 100, Now: func() time.Time { return *f.clock }})
			})
			wrong := cred
			wrong.Code = "000000"
			if wrong.Code == cred.Code {
				wrong.Code = "999999"
			}
			meta := user.Meta{IP: meta1.IP, RequestID: "fresh-flow-error"}
			_, err := f.svc.CompleteSignIn(ctx, p.SignInCompletionToken, wrong, user.Device{}, meta)
			reason := "SIGN_IN_COMPLETION_INVALID"
			if storageFailure {
				var pgerr *pgconn.PgError
				if !errors.As(err, &pgerr) || pgerr.Code != "42P01" {
					t.Errorf("fresh storage error discarded: %v", err)
				}
				reason = "INTERNAL"
				mustExec(t, f, "ALTER TABLE unavailable_identity_flow RENAME TO identity_flow")
			} else if !errors.Is(err, user.ErrSignInCompletionInvalid) {
				t.Errorf("removed flow error discarded: %v", err)
			}
			events := f.audit.Events()
			last := events[len(events)-1]
			if last.RequestID != meta.RequestID || last.Type != enum.EventSignInCompletionRejected || last.Reason != reason {
				t.Errorf("fresh flow rejection audit: %+v, want %s", last, reason)
			}
			var users int
			if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM user_account").Scan(&users); err != nil || users != 0 {
				t.Fatalf("unexpected user commit: %d %v", users, err)
			}
		})
	}
}

type completionRevocationHook struct{ advance func() }

func (h completionRevocationHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h completionRevocationHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h completionRevocationHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		err := next(ctx, cmd)
		if cmd.Name() == "exists" {
			h.advance()
		}
		return err
	}
}

func TestSignInCompletionReplayRechecksExpiryAfterRedis(t *testing.T) {
	for _, mode := range []string{"access", "session", "flow"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			if mode == "access" {
				newServiceWith(t, f, func(d *user.Deps) { d.AccessTTL = 10 * time.Second })
			}
			p := pendingFlow(t, f, "redis-expiry")
			cred := completionCredential(t, f, p, enum.IdentityEmail, email1)
			r, err := f.svc.CompleteSignIn(ctx, p.SignInCompletionToken, cred, user.Device{}, meta1)
			if err != nil {
				t.Fatal(err)
			}
			pair := requireSignInTokens(t, r)
			if mode == "session" {
				principal := principalOf(t, f, pair)
				mustExec(t, f, "UPDATE session SET refresh_expire_time=$2 WHERE id=$1", principal.SessionID, f.clock.Add(10*time.Second))
			}
			delay := 11 * time.Second
			want := user.ErrSignInAlreadyCompleted
			if mode == "flow" {
				delay = 5 * time.Minute
				want = user.ErrSignInCompletionInvalid
			}
			rdb := redis.NewClient(&redis.Options{Addr: f.mr.Addr()})
			defer rdb.Close()
			rdb.AddHook(completionRevocationHook{advance: func() { f.advance(delay) }})
			newServiceWith(t, f, func(d *user.Deps) { d.Revocation = revocation.NewSet(rdb, "t:") })
			if _, err = f.svc.CompleteSignIn(ctx, p.SignInCompletionToken, cred, user.Device{}, meta1); !errors.Is(err, want) {
				t.Fatalf("Redis crossed %s expiry: got %v want %v", mode, err, want)
			}
		})
	}
}

func TestSignInCompletionAuditLifecycleExists(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	initial, err := f.svc.SignInWithIdp(ctx, user.IdpCredential{Kind: enum.IdentityWeChat, AppID: "wx1", Code: "union:audit-life@openid"}, dev1, user.Meta{IP: meta1.IP, RequestID: "start-request"})
	if err != nil || initial.Pending == nil {
		t.Fatal(err)
	}
	p := initial.Pending
	challenge, err := f.svc.SendSignInCompletionCode(ctx, p.SignInCompletionToken, enum.IdentityEmail, email1, user.Device{}, user.Meta{IP: meta1.IP, RequestID: "send-request"})
	if err != nil {
		t.Fatal(err)
	}
	cred := user.CodeCredential{Channel: enum.IdentityEmail, Target: email1, CodeID: challenge.CodeID, Code: f.sent.code(email1)}
	if _, err = f.svc.SendSignInCompletionCode(ctx, p.SignInCompletionToken, enum.IdentityEmail, email1, user.Device{}, user.Meta{IP: meta1.IP, RequestID: "send-rejected"}); err == nil {
		t.Fatal("cooldown bypassed")
	}
	wrong := cred
	wrong.Code = "000000"
	if wrong.Code == cred.Code {
		wrong.Code = "999999"
	}
	if _, err = f.svc.CompleteSignIn(ctx, p.SignInCompletionToken, wrong, user.Device{}, user.Meta{IP: meta1.IP, RequestID: "wrong-request"}); err == nil {
		t.Fatal("wrong code accepted")
	}
	if _, err = f.svc.CompleteSignIn(ctx, "not-a-ticket", cred, user.Device{}, user.Meta{IP: meta1.IP, RequestID: "unknown-request"}); err == nil {
		t.Fatal("invalid token accepted")
	}
	if _, err := f.svc.CompleteSignIn(ctx, p.SignInCompletionToken, cred, user.Device{}, user.Meta{IP: meta1.IP, RequestID: "complete-request"}); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"SIGN_IN_COMPLETION_STARTED": false, "SIGN_IN_COMPLETION_PROOF_VERIFIED": false, "SIGN_IN_COMPLETION_COMPLETED": false}
	var flowID string
	var linked int
	for _, event := range f.audit.Events() {
		if _, ok := want[event.Type.String()]; ok {
			want[event.Type.String()] = true
		}
		if event.RequestID == "unknown-request" {
			if event.FlowID != "" || event.Type != enum.EventSignInCompletionRejected || event.Reason != "SIGN_IN_COMPLETION_INVALID" {
				t.Fatal("unknown ticket must not invent flow")
			}
			continue
		}
		if event.RequestID == "wrong-request" && (event.Type != enum.EventSignInCompletionRejected || event.Reason != "CODE_INVALID") {
			t.Fatal("wrong code lacks safe rejection audit")
		}
		if flowID == "" {
			flowID = event.FlowID
		}
		if !strings.HasPrefix(flowID, "f_") || event.FlowID != flowID {
			t.Fatalf("flow correlation lost: %+v", event)
		}
		linked++
	}
	for event, found := range want {
		if !found {
			t.Errorf("missing committed lifecycle event %s", event)
		}
	}
	store := audit.NewStore(f.pool)
	if n, err := store.InsertBatch(ctx, f.audit.Events()); err != nil || n != 0 {
		t.Fatalf("store audit %d %v", n, err)
	}
	var stored, requests int
	if err = f.pool.QueryRow(ctx, "SELECT count(*),count(DISTINCT request_id) FROM audit_event WHERE flow_id=$1", flowID).Scan(&stored, &requests); err != nil {
		t.Fatal(err)
	}
	if stored != linked || requests != 5 {
		t.Fatalf("persisted correlation: events=%d requests=%d", stored, requests)
	}
	raw, _ := json.Marshal(f.audit.Events())
	digest, _ := f.dig.Digest(email1)
	for _, secret := range []string{p.SignInCompletionToken, cred.CodeID, email1, "union:audit-life@openid", digest, "\"" + cred.Code + "\""} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatalf("audit leaked credential/PII")
		}
	}
}

func TestSignInCompletionAuditFailureDoesNotChangeCommit(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	mustExec(t, f, "CREATE FUNCTION fail_completion_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic audit outage'; END $$")
	mustExec(t, f, "CREATE TRIGGER fail_completion_audit BEFORE INSERT ON audit_event FOR EACH ROW EXECUTE FUNCTION fail_completion_audit()")
	var logs bytes.Buffer
	async := audit.NewAsync(audit.NewStore(f.pool), audit.AsyncOptions{QueueSize: 64, BatchSize: 1, FlushInterval: time.Millisecond, Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	async.Start(ctx)
	defer async.Close()
	newServiceWith(t, f, func(d *user.Deps) { d.Audit = async })
	p := pendingFlow(t, f, "audit-outage")
	cred := completionCredential(t, f, p, enum.IdentityEmail, email1)
	r, err := f.svc.CompleteSignIn(ctx, p.SignInCompletionToken, cred, user.Device{}, meta1)
	if err != nil || r.Tokens == nil {
		t.Fatalf("audit failure changed committed result: %v", err)
	}
	async.Close()
	if async.Stats().Failed == 0 {
		t.Fatal("did not exercise audit database failure")
	}
	var users, sessions int
	_ = f.pool.QueryRow(ctx, "SELECT count(*) FROM user_account").Scan(&users)
	_ = f.pool.QueryRow(ctx, "SELECT count(*) FROM session").Scan(&sessions)
	if users != 1 || sessions != 1 {
		t.Fatal("business transaction not committed")
	}
	for _, secret := range []string{p.SignInCompletionToken, cred.CodeID, email1, r.Tokens.RefreshToken} {
		if strings.Contains(logs.String(), secret) {
			t.Fatal("failure log leaked secret")
		}
	}
}

func TestSignInCompletionPurgeScrubsEarlierFlowAudit(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	p := pendingFlow(t, f, "purge-audit")
	cred := completionCredential(t, f, p, enum.IdentityEmail, email1)
	r, err := f.svc.CompleteSignIn(ctx, p.SignInCompletionToken, cred, user.Device{}, meta1)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := audit.NewStore(f.pool).InsertBatch(ctx, f.audit.Events()); err != nil || n != 0 {
		t.Fatal(err)
	}
	if _, err = f.svc.DeleteMe(ctx, principalOf(t, f, requireSignInTokens(t, r)), meta1); err != nil {
		t.Fatal(err)
	}
	f.advance(361 * time.Hour)
	if n, err := f.svc.PurgeDueUsers(ctx); err != nil || n != 1 {
		t.Fatalf("purge: %d %v", n, err)
	}
	var piiRows int
	if err = f.pool.QueryRow(ctx, "SELECT count(*) FROM audit_event WHERE flow_id IS NOT NULL AND (ip IS NOT NULL OR device_id IS NOT NULL OR subject_hint IS NOT NULL)").Scan(&piiRows); err != nil {
		t.Fatal(err)
	}
	if piiRows != 0 {
		t.Fatalf("earlier anonymous flow audit retains PII after purge: %d", piiRows)
	}
}

func TestSignInCompletionPurgeScrubsFlowBeforeFinalAuditIsStored(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	p := pendingFlow(t, f, "queued-final-audit")
	cred := completionCredential(t, f, p, enum.IdentityEmail, email1)
	r, err := f.svc.CompleteSignIn(ctx, p.SignInCompletionToken, cred, user.Device{}, meta1)
	if err != nil {
		t.Fatal(err)
	}
	var early []audit.Event
	for _, event := range f.audit.Events() {
		if event.UserID == "" {
			early = append(early, event)
		}
	}
	if n, err := audit.NewStore(f.pool).InsertBatch(ctx, early); err != nil || n != 0 {
		t.Fatal(err)
	}
	var known int
	_ = f.pool.QueryRow(ctx, "SELECT count(*) FROM audit_event WHERE user_id IS NOT NULL").Scan(&known)
	if known != 0 {
		t.Fatal("final audit must still be queued")
	}
	if _, err = f.svc.DeleteMe(ctx, principalOf(t, f, requireSignInTokens(t, r)), meta1); err != nil {
		t.Fatal(err)
	}
	f.advance(361 * time.Hour)
	if n, err := f.svc.PurgeDueUsers(ctx); err != nil || n != 1 {
		t.Fatalf("purge %d %v", n, err)
	}
	var piiRows int
	_ = f.pool.QueryRow(ctx, "SELECT count(*) FROM audit_event WHERE flow_id IS NOT NULL AND (ip IS NOT NULL OR device_id IS NOT NULL OR subject_hint IS NOT NULL)").Scan(&piiRows)
	if piiRows != 0 {
		t.Fatalf("early flow PII remained without final audit: %d", piiRows)
	}
}
