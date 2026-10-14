package user_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/session/revocation"
	"github.com/bbxx111/accountkit/user"
	"github.com/redis/go-redis/v9"
)

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
