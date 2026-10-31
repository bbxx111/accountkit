package user_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/anonymize"
	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/audit/authentication"
	"github.com/bbxx111/accountkit/audit/event"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/internal/userdb"
	"github.com/bbxx111/accountkit/user"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// F belongs only to B. A's relationship is an ordinary public audit reference,
// never a production proof owner or a specially approved authentication action.
func historicalPrivacyFixture(t *testing.T) (*fixture, string, string, audit.Event, audit.Event) {
	t.Helper()
	f, a, template := auditPrivacyFixture(t)
	b := principalOf(t, f, f.signIn(t, enum.IdentityPhone, phone1, dev1)).UserID
	projection, err := authentication.Projection(template)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, f, `UPDATE sign_in_completion_flow SET user_id=$2,proof_user_ids=ARRAY[$2::text] WHERE id=$1`, projection.FlowID, b)
	flow := audit.Resource{Type: "IDENTITY_FLOW", Namespace: template.Actor.Namespace, ID: projection.FlowID, Name: "private flow name"}
	makeEvent := func(uid, action string) audit.Event {
		return audit.Event{ID: uuid.Must(uuid.NewV7()), Source: template.Source, Action: action, Actor: audit.Actor{Type: audit.ActorUser, Namespace: flow.Namespace, ID: uid, Name: "private actor"}, Targets: []audit.Resource{flow}, Context: event.EventContext{ClientIP: "203.0.113.26", DeviceID: "private device", UserAgent: "private agent", RequestID: "kept-request"}, Data: []byte(`{"business":{"keep":7},"accountkit":{"subject_hint":"business-owned"}}`), OccurTime: time.Now()}
	}
	return f, a, b, makeEvent(a, "RESOURCE_LINKED"), makeEvent(b, "BUSINESS_OBSERVED")
}

func historicalRecord(t *testing.T, f *fixture, e audit.Event) audit.RecordView {
	t.Helper()
	r := e.Targets[0]
	r.Name = ""
	p, err := audit.NewStore(f.pool).ListByResource(context.Background(), audit.Query{Source: e.Source, Resource: r, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range p.Records {
		if record.ID == e.ID {
			return record
		}
	}
	t.Fatal("missing historical event")
	return audit.RecordView{}
}

func historicalExpect(t *testing.T, f *fixture, e audit.Event, clean bool) {
	t.Helper()
	want := e
	if clean {
		want = event.Scrub(e)
	}
	if !event.Equivalent(historicalRecord(t, f, e).Event, want) {
		t.Fatalf("historical flow privacy mismatch (clean=%v)", clean)
	}
}

func historicalPurge(t *testing.T, f *fixture, a, b string, flow string) {
	t.Helper()
	auditPrivacyDue(t, f, a)
	if n, err := f.svc.PurgeDueUsers(context.Background()); n != 1 || err != nil {
		t.Fatalf("purge %d %v", n, err)
	}
	var owner string
	if err := f.pool.QueryRow(context.Background(), `SELECT user_id FROM sign_in_completion_flow WHERE id=$1`, flow).Scan(&owner); err != nil || owner != b {
		t.Fatalf("A purge changed B's flow: owner=%s err=%v", owner, err)
	}
}

func TestAuditHistoricalLinkProtectsLateFirstInsert(t *testing.T) {
	f, a, b, link, early := historicalPrivacyFixture(t)
	s := audit.NewStore(f.pool)
	if _, err := s.InsertBatch(context.Background(), []audit.Event{link, early}); err != nil {
		t.Fatal(err)
	}
	historicalPurge(t, f, a, b, link.Targets[0].ID)
	historicalExpect(t, f, early, true)
	late := early
	late.ID = uuid.Must(uuid.NewV7())
	if _, err := s.InsertBatch(context.Background(), []audit.Event{late}); err != nil {
		t.Fatal(err)
	}
	historicalExpect(t, f, late, true)
}

func TestAuditLateLinkScrubsExistingFlowRows(t *testing.T) {
	for _, sameBatch := range []bool{false, true} {
		t.Run(fmt.Sprint(sameBatch), func(t *testing.T) {
			f, a, b, link, early := historicalPrivacyFixture(t)
			s := audit.NewStore(f.pool)
			if _, err := s.InsertBatch(context.Background(), []audit.Event{early}); err != nil {
				t.Fatal(err)
			}
			before := historicalRecord(t, f, early)
			historicalPurge(t, f, a, b, link.Targets[0].ID)
			historicalExpect(t, f, early, false)
			batch := []audit.Event{link}
			late := early
			late.ID = uuid.Must(uuid.NewV7())
			if sameBatch {
				batch = append([]audit.Event{late}, batch...)
			}
			if result, err := s.InsertBatch(context.Background(), batch); err != nil || result.Written != len(batch) || result.Duplicate != 0 {
				t.Fatalf("closure batch counts %+v %v", result, err)
			}
			historicalExpect(t, f, early, true)
			historicalExpect(t, f, link, true)
			if sameBatch {
				historicalExpect(t, f, late, true)
			}
			if !before.CreateTime.Equal(historicalRecord(t, f, early).CreateTime) {
				t.Fatal("closure expansion changed create_time")
			}
		})
	}
}

func TestAuditLateLinkConflictRollsBackCleanup(t *testing.T) {
	f, a, b, link, early := historicalPrivacyFixture(t)
	ctx := context.Background()
	s := audit.NewStore(f.pool)
	if _, err := s.InsertBatch(ctx, []audit.Event{early}); err != nil {
		t.Fatal(err)
	}
	before := historicalRecord(t, f, early)
	historicalPurge(t, f, a, b, link.Targets[0].ID)
	conflict := early
	conflict.ID = uuid.Must(uuid.NewV7())
	conflict.Actor = audit.Actor{Type: audit.ActorAnonymous}
	conflict.Targets = nil
	conflict.Action = "UNRELATED_EVENT"
	if _, err := s.InsertBatch(ctx, []audit.Event{conflict}); err != nil {
		t.Fatal(err)
	}
	link.ID = conflict.ID
	r, err := s.InsertBatch(ctx, []audit.Event{link})
	if !errors.Is(err, audit.ErrConflict) || r.Conflicts != 1 || r.Written != 0 {
		t.Fatalf("conflict %+v %v", r, err)
	}
	historicalExpect(t, f, early, false)
	if !before.CreateTime.Equal(historicalRecord(t, f, early).CreateTime) {
		t.Fatal("rollback changed create_time")
	}
}

func TestAuditHistoricalLinkPurgeBothOrders(t *testing.T) {
	for _, isolation := range []string{"read committed", "repeatable read"} {
		for _, insertFirst := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/%v", isolation, insertFirst), func(t *testing.T) {
				f, a, _, link, late := historicalPrivacyFixture(t)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				cleanupConfig := f.pool.Config()
				cleanupConfig.ConnConfig.RuntimeParams["default_transaction_isolation"] = isolation
				cleanupPool, err := pgxpool.NewWithConfig(ctx, cleanupConfig)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(cleanupPool.Close)
				newServiceWith(t, f, func(d *user.Deps) { d.Repo = user.NewRepo(cleanupPool) })
				if _, err := audit.NewStore(f.pool).InsertBatch(ctx, []audit.Event{link}); err != nil {
					t.Fatal(err)
				}
				auditPrivacyDue(t, f, a)
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
				write := func() { _, err := audit.NewStore(worker).InsertBatch(ctx, []audit.Event{late}); writeDone <- err }
				purge := func() { _, err := f.svc.PurgeDueUsers(ctx); purgeDone <- err }
				if insertFirst {
					gate, err := f.pool.Begin(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer gate.Rollback(context.Background())
					key := gate.Conn().PgConn().PID()
					if _, err := gate.Exec(ctx, `SELECT pg_advisory_xact_lock($1::int,774420)`, int32(key)); err != nil {
						t.Fatal(err)
					}
					mustExec(t, f, fmt.Sprintf(`CREATE FUNCTION audit_history_gate() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(%d,774420); RETURN NEW; END $$; CREATE TRIGGER audit_history_gate BEFORE INSERT ON audit_event FOR EACH ROW EXECUTE FUNCTION audit_history_gate()`, key))
					go write()
					auditPrivacyWait(t, ctx, f.pool, pid, key, writeDone)
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
				historicalExpect(t, f, late, true)
			})
		}
	}
}

func TestAuditSoftDeleteSeesWriterAfterRepeatableReadWait(t *testing.T) {
	f, uid, e := auditPrivacyFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cfg := f.pool.Config()
	cfg.ConnConfig.RuntimeParams["default_transaction_isolation"] = "repeatable read"
	cleanupPool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanupPool.Close)
	newServiceWith(t, f, func(d *user.Deps) { d.Repo = user.NewRepo(cleanupPool) })
	cfg = f.pool.Config()
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
	gate, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Rollback(context.Background())
	key := gate.Conn().PgConn().PID()
	if _, err := gate.Exec(ctx, `SELECT pg_advisory_xact_lock($1::int,774425)`, int32(key)); err != nil {
		t.Fatal(err)
	}
	mustExec(t, f, fmt.Sprintf(`CREATE FUNCTION delete_history_gate() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(%d,774425); RETURN NEW; END $$; CREATE TRIGGER delete_history_gate BEFORE INSERT ON audit_event FOR EACH ROW EXECUTE FUNCTION delete_history_gate()`, key))
	writeDone, deleteDone := make(chan error, 1), make(chan error, 1)
	go func() { _, err := audit.NewStore(worker).InsertBatch(ctx, []audit.Event{e}); writeDone <- err }()
	auditPrivacyWait(t, ctx, f.pool, pid, key, writeDone)
	go func() { _, err := f.svc.AdminDeleteUser(ctx, user.Admin{}, uid, meta1); deleteDone <- err }()
	auditPrivacyWait(t, ctx, f.pool, 0, pid, deleteDone)
	_ = gate.Commit(ctx)
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
	if err := <-deleteDone; err != nil {
		t.Fatal(err)
	}
	auditPrivacyAssertScrubbed(t, f, e)
}

func TestAuditCleanupIsolationDoesNotChangePublicRepoDefault(t *testing.T) {
	p := testPool(t)
	ctx := context.Background()
	cfg := p.Config()
	cfg.ConnConfig.RuntimeParams["default_transaction_isolation"] = "repeatable read"
	rp, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer rp.Close()
	var isolation string
	if err := user.TestRepoWithTxRaw(user.NewRepo(rp), ctx, func(tx pgx.Tx, _ *db.Queries) error {
		return tx.QueryRow(ctx, `SHOW transaction_isolation`).Scan(&isolation)
	}); err != nil {
		t.Fatal(err)
	}
	if isolation != "repeatable read" {
		t.Fatalf("public repo changed host isolation: %s", isolation)
	}
}
