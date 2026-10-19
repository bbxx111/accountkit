package user_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/anonymize"
	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/audit/authentication"
	"github.com/bbxx111/accountkit/audit/event"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func auditPrivacyFixture(t *testing.T) (*fixture, string, audit.Event) {
	t.Helper()
	f := newFixture(t)
	p := pendingFlow(t, f, "privacy-lock")
	cred := completionCredential(t, f, p, enum.IdentityEmail, email1)
	r, err := f.svc.CompleteSignIn(context.Background(), p.SignInCompletionToken, cred, dev1, meta1)
	if err != nil {
		t.Fatal(err)
	}
	uid := principalOf(t, f, requireSignInTokens(t, r)).UserID
	var e audit.Event
	for _, candidate := range f.audit.RawEvents() {
		if candidate.Action == "SIGN_IN_COMPLETION_COMPLETED" {
			e = candidate
			break
		}
	}
	if e.ID == [16]byte{} {
		t.Fatal("missing completion event")
	}
	e.Actor.Name = "private actor"
	e.Context.ClientIP = "203.0.113.22"
	e.Context.DeviceID = "private-device"
	e.Context.UserAgent = "private agent"
	for i := range e.Targets {
		e.Targets[i].Name = "private target"
	}
	for i := range e.Context.RelatedResources {
		e.Context.RelatedResources[i].Name = "private related"
	}
	return f, uid, e
}

func auditPrivacyDue(t *testing.T, f *fixture, uid string) {
	t.Helper()
	mustExec(t, f, `UPDATE user_account SET state=$2,purge_time=$3 WHERE id=$1`, uid, enum.UserPendingDeletion, f.deps.Now().Add(-time.Hour))
}

func auditPrivacyAssertScrubbed(t *testing.T, f *fixture, e audit.Event) {
	t.Helper()
	var a, targets, ctx, data []byte
	if err := f.pool.QueryRow(context.Background(), `SELECT actor,targets,context,data FROM audit_event WHERE id=$1`, e.ID).Scan(&a, &targets, &ctx, &data); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_event WHERE id=$1 AND (actor ? 'name' OR EXISTS(SELECT 1 FROM jsonb_array_elements(targets || COALESCE(context->'related_resources','[]')) r WHERE r ? 'name') OR context ?| ARRAY['client_ip','device_id','user_agent'] OR data#>'{accountkit}' ? 'subject_hint')`, e.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("privacy remains: %d %v", n, err)
	}
	rows, err := audit.NewStore(f.pool).ListByUser(context.Background(), authenticationUser(t, e), nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.ID == e.ID {
			if !event.Equivalent(r.Event, authentication.Scrub(e)) {
				t.Fatal("scrub changed stable facts")
			}
			return
		}
	}
	t.Fatal("scrubbed event missing")
}

func authenticationUser(t *testing.T, e audit.Event) string {
	t.Helper()
	p, err := authentication.Projection(e)
	if err != nil {
		t.Fatal(err)
	}
	return p.UserID
}

func TestAuditFirstInsertAfterPurge(t *testing.T) {
	f, uid, e := auditPrivacyFixture(t)
	auditPrivacyDue(t, f, uid)
	if n, err := f.svc.PurgeDueUsers(context.Background()); err != nil || n != 1 {
		t.Fatalf("purge %d %v", n, err)
	}
	if _, err := audit.NewStore(f.pool).InsertBatch(context.Background(), []audit.Event{e}); err != nil {
		t.Fatal(err)
	}
	auditPrivacyAssertScrubbed(t, f, e)
}

// Observe the actual dependency before probing the audit row. A cleanup that
// scrubs first will own this row while waiting for the flow and fail NOWAIT.
func TestAuditCleanupLocksFlowsBeforeAudit(t *testing.T) {
	for _, purge := range []bool{false, true} {
		t.Run(fmt.Sprint(purge), func(t *testing.T) {
			f, uid, e := auditPrivacyFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if _, err := audit.NewStore(f.pool).InsertBatch(ctx, []audit.Event{e}); err != nil {
				t.Fatal(err)
			}
			if purge {
				auditPrivacyDue(t, f, uid)
			}
			tx, err := f.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background())
			if _, err := tx.Exec(ctx, `SELECT id FROM sign_in_completion_flow FOR UPDATE`); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				if purge {
					_, err := f.svc.PurgeDueUsers(ctx)
					done <- err
				} else {
					_, err := f.svc.AdminDeleteUser(ctx, user.Admin{}, uid, meta1)
					done <- err
				}
			}()
			ticker := time.NewTicker(time.Millisecond)
			defer ticker.Stop()
			for {
				var blocked bool
				if err := f.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1::int=ANY(pg_blocking_pids(pid)))`, tx.Conn().PgConn().PID()).Scan(&blocked); err != nil {
					t.Fatal(err)
				}
				if blocked {
					break
				}
				select {
				case err := <-done:
					t.Fatalf("cleanup finished early %v", err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-ticker.C:
				}
			}
			probe, err := f.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			_, err = probe.Exec(ctx, `SELECT id FROM audit_event WHERE id=$1 FOR UPDATE NOWAIT`, e.ID)
			_ = probe.Rollback(ctx)
			if err != nil {
				t.Errorf("cleanup locked audit before flow: %v", err)
			}
			_ = tx.Commit(ctx)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			auditPrivacyAssertScrubbed(t, f, e)
		})
	}
}

type auditPrivacyCallback struct {
	run func(context.Context, pgx.Tx) error
}

func (*auditPrivacyCallback) Name() string     { return "privacy-test" }
func (*auditPrivacyCallback) Tables() []string { return nil }
func (a *auditPrivacyCallback) Anonymize(ctx context.Context, tx pgx.Tx, _ string) error {
	return a.run(ctx, tx)
}

func TestPurgeRollbackPreservesAudit(t *testing.T) {
	f, uid, e := auditPrivacyFixture(t)
	ctx := context.Background()
	if _, err := audit.NewStore(f.pool).InsertBatch(ctx, []audit.Event{e}); err != nil {
		t.Fatal(err)
	}
	auditPrivacyDue(t, f, uid)
	snapshot := func() string {
		var s string
		if err := f.pool.QueryRow(ctx, `SELECT jsonb_build_array((SELECT jsonb_agg(t ORDER BY id) FROM user_account t),(SELECT jsonb_agg(t ORDER BY id) FROM identity t),(SELECT jsonb_agg(t ORDER BY id) FROM sign_in_completion_flow t),(SELECT jsonb_agg(t ORDER BY id) FROM audit_event t))::text`).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	before := snapshot()
	sentinel := errors.New("synthetic rollback")
	newServiceWith(t, f, func(d *user.Deps) {
		d.Anonymizers = []anonymize.Anonymizer{&auditPrivacyCallback{run: func(context.Context, pgx.Tx) error { return sentinel }}}
	})
	if n, err := f.svc.PurgeDueUsers(ctx); n != 0 || !errors.Is(err, sentinel) {
		t.Fatalf("rollback %d %v", n, err)
	}
	if snapshot() != before {
		t.Fatal("failed callback changed account, identity, flow or audit")
	}
	newServiceWith(t, f, func(d *user.Deps) { d.Anonymizers = nil })
	if n, err := f.svc.PurgeDueUsers(ctx); n != 1 || err != nil {
		t.Fatalf("retry %d %v", n, err)
	}
	auditPrivacyAssertScrubbed(t, f, e)
}

func TestAuditPurgeCallbackCancellationRollsBack(t *testing.T) {
	f, uid, e := auditPrivacyFixture(t)
	auditPrivacyDue(t, f, uid)
	if _, err := audit.NewStore(f.pool).InsertBatch(context.Background(), []audit.Event{e}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	newServiceWith(t, f, func(d *user.Deps) {
		d.Anonymizers = []anonymize.Anonymizer{&auditPrivacyCallback{run: func(ctx context.Context, tx pgx.Tx) error { cancel(); return ctx.Err() }}}
	})
	if n, err := f.svc.PurgeDueUsers(ctx); n != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled purge %d %v", n, err)
	}
	var state enum.UserState
	var flows, private int
	if err := f.pool.QueryRow(context.Background(), `SELECT state FROM user_account WHERE id=$1`, uid).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM sign_in_completion_flow`).Scan(&flows); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_event WHERE id=$1 AND context->>'device_id'='private-device'`, e.ID).Scan(&private); err != nil {
		t.Fatal(err)
	}
	if state != enum.UserPendingDeletion || flows != 1 || private != 1 {
		t.Fatalf("cancelled transaction changed state=%v flows=%d private=%d", state, flows, private)
	}
	if f.pool.Stat().AcquiredConns() != 0 {
		t.Fatal("cancelled transaction leaked connection")
	}
}

func auditPrivacyWait(t *testing.T, ctx context.Context, p *pgxpool.Pool, pid, blocker uint32, done <-chan error) {
	t.Helper()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		var blocked bool
		query := `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE ($1::int=0 OR pid=$1::int) AND $2::int=ANY(pg_blocking_pids(pid)))`
		if err := p.QueryRow(ctx, query, pid, blocker).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		select {
		case err := <-done:
			t.Fatalf("finished before required dependency: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
}

func TestAuditInsertVersusPurgeBothOrders(t *testing.T) {
	for _, insertFirst := range []bool{true, false} {
		t.Run(fmt.Sprint(insertFirst), func(t *testing.T) {
			f, uid, e := auditPrivacyFixture(t)
			auditPrivacyDue(t, f, uid)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cfg := f.pool.Config()
			cfg.MaxConns = 1
			worker, err := pgxpool.NewWithConfig(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer worker.Close()
			c, err := worker.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			pid := c.Conn().PgConn().PID()
			c.Release()
			writeDone, purgeDone := make(chan error, 1), make(chan error, 1)
			write := func() { _, err := audit.NewStore(worker).InsertBatch(ctx, []audit.Event{e}); writeDone <- err }
			purge := func() {
				n, err := f.svc.PurgeDueUsers(ctx)
				if err == nil && n != 1 {
					err = fmt.Errorf("purged %d", n)
				}
				purgeDone <- err
			}
			if insertFirst {
				gate, err := f.pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer gate.Rollback(context.Background())
				if _, err := gate.Exec(ctx, `SELECT pg_advisory_xact_lock(774412)`); err != nil {
					t.Fatal(err)
				}
				mustExec(t, f, `CREATE FUNCTION audit_insert_gate() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(774412); RETURN NEW; END $$; CREATE TRIGGER audit_insert_gate BEFORE INSERT ON audit_event FOR EACH ROW EXECUTE FUNCTION audit_insert_gate()`)
				go write()
				auditPrivacyWait(t, ctx, f.pool, pid, gate.Conn().PgConn().PID(), writeDone)
				go purge()
				auditPrivacyWait(t, ctx, f.pool, 0, pid, purgeDone)
				if err := gate.Commit(ctx); err != nil {
					t.Fatal(err)
				}
			} else {
				entered := make(chan uint32, 1)
				release := make(chan struct{})
				defer func() {
					select {
					case <-release:
					default:
						close(release)
					}
				}()
				newServiceWith(t, f, func(d *user.Deps) {
					d.Anonymizers = []anonymize.Anonymizer{&auditPrivacyCallback{run: func(ctx context.Context, tx pgx.Tx) error {
						entered <- tx.Conn().PgConn().PID()
						select {
						case <-release:
							return nil
						case <-ctx.Done():
							return ctx.Err()
						}
					}}}
				})
				go purge()
				var purgePID uint32
				select {
				case purgePID = <-entered:
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				go write()
				auditPrivacyWait(t, ctx, f.pool, pid, purgePID, writeDone)
				close(release)
			}
			if err := <-writeDone; err != nil {
				t.Fatal(err)
			}
			if err := <-purgeDone; err != nil {
				t.Fatal(err)
			}
			auditPrivacyAssertScrubbed(t, f, e)
		})
	}
}

func TestAuditPurgeLocksWholeAuditSetInUUIDOrder(t *testing.T) {
	f, uid, flowEvent := auditPrivacyFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	plain, err := authentication.Map(audit.AuthenticationEvent{Type: enum.EventSignIn, Actor: enum.ActorUser, UserID: uid, IP: "203.0.113.25", OccurTime: time.Now()}, audit.AuthenticationOptions{Source: flowEvent.Source, Namespace: flowEvent.Actor.Namespace})
	if err != nil {
		t.Fatal(err)
	}
	plain.ID = flowEvent.ID
	flowEvent.ID = uuid.Must(uuid.NewV7())
	if _, err := audit.NewStore(f.pool).InsertBatch(ctx, []audit.Event{flowEvent, plain}); err != nil {
		t.Fatal(err)
	}
	auditPrivacyDue(t, f, uid)
	gate, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Rollback(context.Background())
	if _, err := gate.Exec(ctx, `SELECT id FROM audit_event WHERE id=$1 FOR UPDATE`, plain.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := f.svc.PurgeDueUsers(ctx); done <- err }()
	auditPrivacyWait(t, ctx, f.pool, 0, gate.Conn().PgConn().PID(), done)
	probe, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = probe.Exec(ctx, `SELECT id FROM audit_event WHERE id=$1 FOR UPDATE NOWAIT`, flowEvent.ID)
	_ = probe.Rollback(ctx)
	if err != nil {
		t.Errorf("purge locked flow event before smaller ordinary event: %v", err)
	}
	_ = gate.Commit(ctx)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	auditPrivacyAssertScrubbed(t, f, flowEvent)
	auditPrivacyAssertScrubbed(t, f, plain)
}

func TestAuditCleanupPreservesBusinessData(t *testing.T) {
	for _, purge := range []bool{false, true} {
		t.Run(fmt.Sprint(purge), func(t *testing.T) {
			f, uid, template := auditPrivacyFixture(t)
			ctx := context.Background()
			var events []audit.Event
			for _, raw := range []string{`{"accountkit":{"subject_hint":"business-owned"},"order":7}`, `{"accountkit":"business-scalar"}`, `{"accountkit":["business-array"]}`, `{"accountkit":null}`} {
				e := template
				e.ID = uuid.Must(uuid.NewV7())
				e.Action = "ORDER_UPDATED"
				e.Data = json.RawMessage(raw)
				events = append(events, e)
			}
			// A recognizable authentication action owns its hint even if the old
			// HTTP projection cannot reconstruct an actor_kind from this record.
			auth := template
			auth.ID = uuid.Must(uuid.NewV7())
			auth.Data = json.RawMessage(`{"accountkit":{"subject_hint":"abcdefgh","kept":7}}`)
			events = append(events, auth)
			if _, err := audit.NewStore(f.pool).InsertBatch(ctx, events); err != nil {
				t.Fatal(err)
			}
			if purge {
				auditPrivacyDue(t, f, uid)
				if n, err := f.svc.PurgeDueUsers(ctx); n != 1 || err != nil {
					t.Fatalf("purge %d %v", n, err)
				}
			} else {
				if _, err := f.svc.AdminDeleteUser(ctx, user.Admin{}, uid, meta1); err != nil {
					t.Fatal(err)
				}
			}
			// The real reader freezes every stored event, so this also proves
			// bulk SQL cleanup preserves the full Go event contract.
			rows, err := audit.NewStore(f.pool).ListByUser(ctx, uid, nil, 100)
			if err != nil {
				t.Fatalf("read cleaned events: %v", err)
			}
			readable := make(map[uuid.UUID]bool, len(rows))
			for _, row := range rows {
				readable[row.ID] = true
			}
			for i, e := range events {
				if !readable[e.ID] {
					t.Errorf("cleaned event %d missing from reader", i)
				}
				want := e.Data
				if i == len(events)-1 {
					want = json.RawMessage(`{"accountkit":{"kept":7}}`)
				}
				var same bool
				if err := f.pool.QueryRow(ctx, `SELECT data=$2::jsonb FROM audit_event WHERE id=$1`, e.ID, want).Scan(&same); err != nil || !same {
					t.Errorf("event %d data changed: %v %v", i, same, err)
				}
			}
		})
	}
}

func TestAuditSharedFlowPrivacyDoesNotExpandReaderScope(t *testing.T) {
	f, a, e := auditPrivacyFixture(t)
	ctx := context.Background()
	b := principalOf(t, f, f.signIn(t, enum.IdentityPhone, phone1, dev1)).UserID
	flow, err := authentication.Projection(e)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, f, `UPDATE sign_in_completion_flow SET proof_user_ids=ARRAY[$2,$3] WHERE id=$1`, flow.FlowID, a, b)
	e.Actor.ID = b
	for i := range e.Targets {
		if e.Targets[i].Type == "USER" {
			e.Targets[i].ID = b
		}
	}
	for i := range e.Context.RelatedResources {
		if e.Context.RelatedResources[i].Type == "USER" {
			e.Context.RelatedResources[i].ID = b
		}
	}
	store := audit.NewStore(f.pool)
	if _, err := store.InsertBatch(ctx, []audit.Event{e}); err != nil {
		t.Fatal(err)
	}
	rows, err := store.ListByUser(ctx, a, nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.ID == e.ID {
			t.Fatal("shared flow leaked B's explicit event to A")
		}
	}
	auditPrivacyDue(t, f, a)
	if n, err := f.svc.PurgeDueUsers(ctx); n != 1 || err != nil {
		t.Fatalf("purge %d %v", n, err)
	}
	auditPrivacyAssertScrubbed(t, f, e)
	late := e
	late.ID = uuid.Must(uuid.NewV7())
	if _, err := store.InsertBatch(ctx, []audit.Event{late}); err != nil {
		t.Fatal(err)
	}
	auditPrivacyAssertScrubbed(t, f, late)
	rows, err = store.ListByUser(ctx, a, nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.ID == e.ID || r.ID == late.ID {
			t.Fatal("privacy cleanup expanded A's read authorization")
		}
	}
}

func TestAuditFlowExpiryCleanupKeepsFlowOnlyLockOrder(t *testing.T) {
	f, _, e := auditPrivacyFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	mustExec(t, f, `UPDATE sign_in_completion_flow SET expire_time=now()-interval '1 minute'`)
	gate, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Rollback(context.Background())
	if _, err := gate.Exec(ctx, `SELECT pg_advisory_xact_lock(774413)`); err != nil {
		t.Fatal(err)
	}
	mustExec(t, f, `CREATE FUNCTION audit_expiry_gate() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(774413); RETURN NEW; END $$; CREATE TRIGGER audit_expiry_gate BEFORE INSERT ON audit_event FOR EACH ROW EXECUTE FUNCTION audit_expiry_gate()`)
	done := make(chan error, 1)
	go func() { _, err := audit.NewStore(f.pool).InsertBatch(ctx, []audit.Event{e}); done <- err }()
	auditPrivacyWait(t, ctx, f.pool, 0, gate.Conn().PgConn().PID(), done)
	if n, err := f.svc.CleanupSignInCompletionFlows(ctx); n != 0 || err != nil {
		t.Fatalf("cleanup did not skip audit-locked flow: %d %v", n, err)
	}
	_ = gate.Commit(ctx)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if n, err := f.svc.CleanupSignInCompletionFlows(ctx); n != 1 || err != nil {
		t.Fatalf("cleanup after release: %d %v", n, err)
	}
	auditPrivacyAssertScrubbed(t, f, e)
}
