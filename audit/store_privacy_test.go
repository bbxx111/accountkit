package audit_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/audit/authentication"
	"github.com/bbxx111/accountkit/audit/event"
	"github.com/bbxx111/accountkit/enum"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const privacyA = "u_0000000000001"
const privacyB = "u_0000000000002"
const privacyFlow = "f_0000000000001"

func privacyUsers(t *testing.T, p *pgxpool.Pool) {
	t.Helper()
	if _, err := p.Exec(context.Background(), `INSERT INTO user_account(id,state) VALUES ($1,1),($2,1)`, privacyA, privacyB); err != nil {
		t.Fatal(err)
	}
}

func privacyFlowRow(t *testing.T, p *pgxpool.Pool, owners []string) {
	t.Helper()
	if owners == nil {
		owners = []string{}
	}
	if _, err := p.Exec(context.Background(), `INSERT INTO sign_in_completion_flow(id,token_digest,state,policy_fingerprint,payload,cipher_version,proof_user_ids,create_time,update_time,expire_time) VALUES ($1,$1,1,'test','test',1,$2,now(),now(),now()+interval '1 hour')`, privacyFlow, owners); err != nil {
		t.Fatal(err)
	}
}

func privateEvent(t *testing.T, user, flow string) audit.Event {
	t.Helper()
	e := mapped(t, audit.AuthenticationEvent{Type: enum.EventSignIn, Actor: enum.ActorUser, Result: enum.ResultSuccess, UserID: user, FlowID: flow, IP: "203.0.113.10", DeviceID: "private-device", SubjectHint: "abcdefgh", RequestID: "kept-request", Reason: "KNOWN_USER", OccurTime: time.Now()})
	e.Actor.Name = "private actor"
	for i := range e.Targets {
		e.Targets[i].Name = "private target"
	}
	for i := range e.Context.RelatedResources {
		e.Context.RelatedResources[i].Name = "private related"
	}
	e.Context.UserAgent = "private agent"
	return e
}

func storedPrivacyEvent(t *testing.T, p *pgxpool.Pool, e audit.Event) audit.Event {
	t.Helper()
	var got audit.Event
	var raw []byte
	if err := p.QueryRow(context.Background(), `SELECT jsonb_build_object('id',id,'source',source,'action',action,'result',CASE result WHEN 1 THEN 'SUCCESS' WHEN 2 THEN 'FAILURE' END,'actor',actor,'targets',targets,'context',context,'data',data,'occur_time',occur_time) FROM audit_event WHERE id=$1`, e.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err := got.UnmarshalJSON(raw); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestAuditMissingFlowLateEvent(t *testing.T) {
	for _, removed := range []bool{false, true} {
		t.Run(fmt.Sprint(removed), func(t *testing.T) {
			p := testPool(t)
			if removed {
				privacyFlowRow(t, p, nil)
				if _, err := p.Exec(context.Background(), `DELETE FROM sign_in_completion_flow WHERE id=$1`, privacyFlow); err != nil {
					t.Fatal(err)
				}
			}
			e := privateEvent(t, "", privacyFlow)
			if _, err := testStore(t, p).InsertBatch(context.Background(), []audit.Event{e}); err != nil {
				t.Fatal(err)
			}
			if !event.Equivalent(storedPrivacyEvent(t, p, e), authentication.Scrub(e)) {
				t.Fatal("late first insert restored missing-flow privacy or changed stable facts")
			}
		})
	}
}

func TestAuditExpiredFlowLateEvent(t *testing.T) {
	p := testPool(t)
	privacyFlowRow(t, p, []string{})
	if _, err := p.Exec(context.Background(), `UPDATE sign_in_completion_flow SET expire_time=now()-interval '1 second' WHERE id=$1`, privacyFlow); err != nil {
		t.Fatal(err)
	}
	e := privateEvent(t, "", privacyFlow)
	if _, err := testStore(t, p).InsertBatch(context.Background(), []audit.Event{e}); err != nil {
		t.Fatal(err)
	}
	if !event.Equivalent(storedPrivacyEvent(t, p, e), authentication.Scrub(e)) {
		t.Fatal("expired flow first insert retained privacy")
	}
}

func TestAuditLateAuthenticationHintWithoutHTTPProjection(t *testing.T) {
	p := testPool(t)
	e := privateEvent(t, privacyA, "")
	e.Data = []byte(`{"accountkit":{"subject_hint":"abcdefgh","kept":7}}`)
	if _, err := testStore(t, p).InsertBatch(context.Background(), []audit.Event{e}); err != nil {
		t.Fatal(err)
	}
	if !event.Equivalent(storedPrivacyEvent(t, p, e), authentication.Scrub(e)) {
		t.Fatal("recognized authentication action retained hint because projection was incomplete")
	}
}

func TestAuditLateUsersAreScopedPerEvent(t *testing.T) {
	p := testPool(t)
	privacyUsers(t, p)
	if _, err := p.Exec(context.Background(), `UPDATE user_account SET state=$2 WHERE id=$1`, privacyA, enum.UserDeleted); err != nil {
		t.Fatal(err)
	}
	deleted := privateEvent(t, privacyA, "")
	active := privateEvent(t, privacyB, "")
	missing := privateEvent(t, "u_0000000000003", "")
	anonymous := privateEvent(t, "", "")
	external := privateEvent(t, "", "")
	external.Actor = audit.Actor{Type: audit.ActorUser, Namespace: "external/admin", ID: privacyA, Name: "external actor"}
	business := privateEvent(t, privacyA, "")
	business.Action = "ORDER_UPDATED"
	business.Data = []byte(`{"business":{"keep":true},"accountkit":{"subject_hint":"business-owned"}}`)
	es := []audit.Event{deleted, active, missing, anonymous, external, business}
	if _, err := testStore(t, p).InsertBatch(context.Background(), es); err != nil {
		t.Fatal(err)
	}
	for i, e := range es {
		want := e
		if i == 0 || i == 2 {
			want = authentication.Scrub(e)
		}
		if i == 5 {
			want = event.Scrub(e)
		}
		if !event.Equivalent(storedPrivacyEvent(t, p, e), want) {
			t.Errorf("event %d privacy scope or facts changed", i)
		}
	}
	if r, err := testStore(t, p).InsertBatch(context.Background(), es); err != nil || r.Duplicate != len(es) {
		t.Fatalf("scrubbed batch replay %+v %v", r, err)
	}
	if _, err := p.Exec(context.Background(), `UPDATE user_account SET state=$2 WHERE id=$1`, privacyB, enum.UserPendingDeletion); err != nil {
		t.Fatal(err)
	}
	pending := privateEvent(t, privacyB, "")
	if _, err := testStore(t, p).InsertBatch(context.Background(), []audit.Event{pending}); err != nil {
		t.Fatal(err)
	}
	if !event.Equivalent(storedPrivacyEvent(t, p, pending), pending) {
		t.Fatal("pending non-flow event scrubbed")
	}
}

func TestAuditPrivacyUsesOnlyCompleteLocalReferences(t *testing.T) {
	p := testPool(t)
	privacyUsers(t, p)
	if _, err := p.Exec(context.Background(), `UPDATE user_account SET state=$2 WHERE id=$1`, privacyA, enum.UserDeleted); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"actor", "target", "related", "foreign", "data-only"} {
		t.Run(kind, func(t *testing.T) {
			e := privateEvent(t, "", "")
			e.Action = "ORDER_UPDATED"
			e.Data = []byte(`{"business":"kept"}`)
			e.Actor = audit.Actor{Type: audit.ActorAnonymous, Name: "private actor"}
			e.Targets = nil
			e.Context.RelatedResources = nil
			r := audit.Resource{Type: "USER", Namespace: "test/ns", ID: privacyA, Name: "private resource"}
			switch kind {
			case "actor":
				e.Actor = audit.Actor{Type: audit.ActorUser, Namespace: r.Namespace, ID: r.ID, Name: "private actor"}
			case "target":
				e.Targets = []audit.Resource{r}
			case "related":
				e.Context.RelatedResources = []audit.Resource{r}
			case "foreign":
				r.Namespace = "foreign/ns"
				e.Targets = []audit.Resource{r, {Type: "IDENTITY_FLOW", Namespace: r.Namespace, ID: privacyFlow}}
			case "data-only":
				e.Data = []byte(`{"accountkit":{"user_id":"u_0000000000001","flow_id":"f_0000000000001"}}`)
			}
			if _, err := testStore(t, p).InsertBatch(context.Background(), []audit.Event{e}); err != nil {
				t.Fatal(err)
			}
			want := e
			if kind == "actor" || kind == "target" || kind == "related" {
				want = event.Scrub(e)
			}
			if !event.Equivalent(storedPrivacyEvent(t, p, e), want) {
				t.Fatal("privacy was inferred from incomplete/foreign references or failed to honor a complete reference")
			}
		})
	}
}

// A single-connection worker pool pins its backend PID; the base pool observes
// pg_blocking_pids, so observing cannot starve behind the blocked transaction.
func privacyWorker(t *testing.T, p *pgxpool.Pool, isolation ...string) (*audit.Store, uint32) {
	t.Helper()
	cfg := p.Config()
	if len(isolation) > 0 {
		cfg.ConnConfig.RuntimeParams["default_transaction_isolation"] = isolation[0]
	}
	return privacyWorkerConfig(t, cfg)
}

func privacyWorkerConfig(t *testing.T, cfg *pgxpool.Config) (*audit.Store, uint32) {
	t.Helper()
	cfg.MaxConns = 1
	w, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Close)
	c, err := w.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	pid := c.Conn().PgConn().PID()
	c.Release()
	return testStore(t, w), pid
}

func privacyBlocked(t *testing.T, ctx context.Context, p *pgxpool.Pool, pid, blocker uint32, done <-chan error) {
	t.Helper()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		var blocked bool
		if err := p.QueryRow(ctx, `SELECT $2::int = ANY(pg_blocking_pids($1::int))`, pid, blocker).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		select {
		case err := <-done:
			t.Fatalf("operation finished before required lock: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
}

func privacyLock(t *testing.T, p *pgxpool.Pool, sql string, args ...any) pgx.Tx {
	t.Helper()
	tx, err := p.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	if _, err = tx.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
	return tx
}

func TestAuditSharedFlowLockOrder(t *testing.T) {
	p := testPool(t)
	privacyUsers(t, p)
	privacyFlowRow(t, p, []string{privacyA, privacyB})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	lock := privacyLock(t, p, `SELECT id FROM user_account WHERE id=$1 FOR UPDATE`, privacyA)
	s, pid := privacyWorker(t, p)
	done := make(chan error, 1)
	e := privateEvent(t, privacyB, privacyFlow)
	go func() { _, err := s.InsertBatch(ctx, []audit.Event{e}); done <- err }()
	privacyBlocked(t, ctx, p, pid, lock.Conn().PgConn().PID(), done)
	// B must remain unlocked while the sorted first owner A is blocked.
	probe := privacyLock(t, p, `SELECT id FROM user_account WHERE id=$1 FOR UPDATE NOWAIT`, privacyB)
	_ = probe.Rollback(ctx)
	if err := lock.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestAuditLockWaitCancellationRollsBackBatch(t *testing.T) {
	p := testPool(t)
	privacyUsers(t, p)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	lock := privacyLock(t, p, `SELECT id FROM user_account WHERE id=$1 FOR UPDATE`, privacyA)
	s, pid := privacyWorker(t, p)
	done := make(chan error, 1)
	go func() {
		r, err := s.InsertBatch(ctx, []audit.Event{privateEvent(t, privacyB, ""), privateEvent(t, privacyA, "")})
		if r.Failed != 2 {
			done <- fmt.Errorf("failed count %d", r.Failed)
			return
		}
		done <- err
	}()
	privacyBlocked(t, ctx, p, pid, lock.Conn().PgConn().PID(), done)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	var n int
	if err := p.QueryRow(context.Background(), `SELECT count(*) FROM audit_event`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("partial batch %d %v", n, err)
	}
	_ = lock.Rollback(context.Background())
	if _, err := s.InsertBatch(context.Background(), []audit.Event{privateEvent(t, privacyA, "")}); err != nil {
		t.Fatal(err)
	}
}

func TestAuditFlowOwnerSnapshotMustRetry(t *testing.T) {
	p := testPool(t)
	privacyUsers(t, p)
	privacyFlowRow(t, p, []string{})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// A real ownership writer locks B before the flow, but its uncommitted
	// addition is invisible to the audit writer's initial snapshot.
	change := privacyLock(t, p, `SELECT id FROM user_account WHERE id=$1 FOR UPDATE`, privacyB)
	if _, err := change.Exec(ctx, `UPDATE sign_in_completion_flow SET proof_user_ids=ARRAY[$2] WHERE id=$1`, privacyFlow, privacyB); err != nil {
		t.Fatal(err)
	}
	if _, err := change.Exec(ctx, `UPDATE user_account SET state=$2 WHERE id=$1`, privacyB, enum.UserDeleted); err != nil {
		t.Fatal(err)
	}
	s, pid := privacyWorker(t, p)
	done := make(chan error, 1)
	e := privateEvent(t, "", privacyFlow)
	go func() { _, err := s.InsertBatch(ctx, []audit.Event{e}); done <- err }()
	privacyBlocked(t, ctx, p, pid, change.Conn().PgConn().PID(), done)
	var before string
	if err := p.QueryRow(ctx, `SELECT virtualtransaction FROM pg_locks WHERE pid=$1 AND locktype='virtualxid'`, pid).Scan(&before); err != nil {
		t.Fatal(err)
	}
	// Pause only after the writer reaches INSERT, to inspect the new transaction.
	gate := privacyLock(t, p, `SELECT pg_advisory_xact_lock(774410)`)
	if _, err := p.Exec(ctx, `CREATE FUNCTION audit_retry_gate() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(774410); RETURN NEW; END $$; CREATE TRIGGER audit_retry_gate BEFORE INSERT ON audit_event FOR EACH ROW EXECUTE FUNCTION audit_retry_gate()`); err != nil {
		t.Fatal(err)
	}
	if err := change.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	privacyBlocked(t, ctx, p, pid, gate.Conn().PgConn().PID(), done)
	var after string
	if err := p.QueryRow(ctx, `SELECT virtualtransaction FROM pg_locks WHERE pid=$1 AND locktype='virtualxid'`, pid).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Error("new flow owner did not force a complete transaction rollback/retry")
	}
	_ = gate.Commit(ctx)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !event.Equivalent(storedPrivacyEvent(t, p, e), authentication.Scrub(e)) {
		t.Fatal("new deleted owner privacy retained")
	}
}

func TestAuditReverseBatchesLockUUIDsInOrder(t *testing.T) {
	p := testPool(t)
	s, pid := privacyWorker(t, p)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	first, second := privateEvent(t, "", ""), privateEvent(t, "", "")
	// UUIDv7 creation order is deterministic for these two producer calls.
	if first.ID.String() > second.ID.String() {
		first, second = second, first
	}
	if _, err := p.Exec(ctx, fmt.Sprintf(`CREATE FUNCTION audit_uuid_gate() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.id='%s'::uuid THEN PERFORM pg_advisory_xact_lock(774411); END IF; RETURN NEW; END $$; CREATE TRIGGER audit_uuid_gate BEFORE INSERT ON audit_event FOR EACH ROW EXECUTE FUNCTION audit_uuid_gate()`, first.ID)); err != nil {
		t.Fatal(err)
	}
	gate := privacyLock(t, p, `SELECT pg_advisory_xact_lock(774411)`)
	done := make(chan error, 1)
	go func() { _, err := s.InsertBatch(ctx, []audit.Event{second, first}); done <- err }()
	privacyBlocked(t, ctx, p, pid, gate.Conn().PgConn().PID(), done)
	// The reversed writer must not have inserted second before it reaches first.
	probe, err := p.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := probe.Exec(ctx, `SET LOCAL lock_timeout='250ms'`); err != nil {
		t.Fatal(err)
	}
	_, err = probe.Exec(ctx, `INSERT INTO audit_event SELECT $1,source,action,result,actor,targets,context,data,occur_time,create_time FROM (SELECT $2::text source,'TEST'::text action,NULL::smallint result,'{"type":"ANONYMOUS"}'::jsonb actor,'[]'::jsonb targets,'{}'::jsonb context,'{}'::jsonb data,now() occur_time,now() create_time) e`, second.ID, "test/source")
	_ = probe.Rollback(ctx)
	if err != nil {
		t.Errorf("reverse batch acquired later UUID before first: %v", err)
	}
	other, otherPID := privacyWorker(t, p)
	otherDone := make(chan error, 1)
	go func() {
		r, err := other.InsertBatch(ctx, []audit.Event{first, second})
		if err == nil && r.Written+r.Duplicate != 2 {
			err = fmt.Errorf("incomplete competing batch %+v", r)
		}
		otherDone <- err
	}()
	privacyBlocked(t, ctx, p, otherPID, gate.Conn().PgConn().PID(), otherDone)
	_ = gate.Commit(ctx)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-otherDone; err != nil {
		t.Fatal(err)
	}
	r, err := s.InsertBatch(ctx, []audit.Event{first, second})
	if err != nil || r.Duplicate != 2 {
		t.Fatalf("reverse replay: %+v %v", r, err)
	}
}

func TestAuditRetentionLocksCandidatesInUUIDOrder(t *testing.T) {
	p := testPool(t)
	// Lock ordering must hold for every valid PostgreSQL plan. Force the hash
	// semi-join/heap path rather than relying on a two-row HashAggregate order.
	cfg := p.Config()
	cfg.MaxConns = 1
	for _, setting := range []string{"enable_nestloop", "enable_mergejoin", "enable_indexscan", "enable_bitmapscan"} {
		cfg.ConnConfig.RuntimeParams[setting] = "off"
	}
	worker, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(worker.Close)
	c, err := worker.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	pid := c.Conn().PgConn().PID()
	c.Release()
	s := testStore(t, worker)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	low, high := privateEvent(t, "", ""), privateEvent(t, "", "")
	if low.ID.String() > high.ID.String() {
		low, high = high, low
	}
	cutoff := time.Now()
	low.OccurTime = cutoff.Add(-time.Hour)
	high.OccurTime = cutoff.Add(-2 * time.Hour)
	// Separate arrivals put the larger UUID first both physically and in the
	// retention candidate time order. Candidate selection must stay time-based,
	// but cleanup's row-lock acquisition must use UUID order like purge.
	for _, e := range []audit.Event{high, low} {
		if _, err := s.InsertBatch(ctx, []audit.Event{e}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.Exec(ctx, `ANALYZE audit_event`); err != nil {
		t.Fatal(err)
	}
	gate := privacyLock(t, p, `SELECT id FROM audit_event WHERE id=$1 FOR UPDATE`, low.ID)
	done := make(chan error, 1)
	go func() {
		n, err := s.DeleteOlderThan(ctx, cutoff)
		if err == nil && n != 2 {
			err = fmt.Errorf("retention count %d", n)
		}
		done <- err
	}()
	privacyBlocked(t, ctx, p, pid, gate.Conn().PgConn().PID(), done)
	probe, err := p.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = probe.Exec(ctx, `SELECT id FROM audit_event WHERE id=$1 FOR UPDATE NOWAIT`, high.ID)
	_ = probe.Rollback(ctx)
	if err != nil {
		t.Errorf("retention locked later UUID before earlier UUID: %v", err)
	}
	_ = gate.Commit(ctx)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
