package audit_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/audit/event"
	"github.com/bbxx111/accountkit/enum"
	"github.com/google/uuid"
)

func historicalGeneric(t *testing.T, user, flow string) audit.Event {
	t.Helper()
	e := privateEvent(t, user, flow)
	e.Action = "BUSINESS_EVENT"
	e.Data = []byte(`{"business":{"keep":true},"accountkit":{"subject_hint":"business-owned"}}`)
	return e
}

func TestAuditHistoricalSnapshotMustRetry(t *testing.T) {
	for _, isolation := range []string{"read committed", "repeatable read"} {
		t.Run(isolation, func(t *testing.T) { auditHistoricalSnapshotRetry(t, isolation) })
	}
}

func auditHistoricalSnapshotRetry(t *testing.T, isolation string) {
	p := testPool(t)
	privacyUsers(t, p)
	privacyFlowRow(t, p, []string{privacyB})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := p.Exec(ctx, `UPDATE user_account SET state=$2 WHERE id=$1`, privacyA, enum.UserDeleted); err != nil {
		t.Fatal(err)
	}
	link := historicalGeneric(t, privacyA, privacyFlow)
	late := historicalGeneric(t, privacyB, privacyFlow)
	linkGate := privacyLock(t, p, `SELECT pg_advisory_xact_lock(774421)`)
	lateGate := privacyLock(t, p, `SELECT pg_advisory_xact_lock(774422)`)
	if _, err := p.Exec(ctx, fmt.Sprintf(`CREATE FUNCTION history_retry_gate() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.id='%s'::uuid THEN PERFORM pg_advisory_xact_lock(774421); ELSE PERFORM pg_advisory_xact_lock(774422); END IF; RETURN NEW; END $$; CREATE TRIGGER history_retry_gate BEFORE INSERT ON audit_event FOR EACH ROW EXECUTE FUNCTION history_retry_gate()`, link.ID)); err != nil {
		t.Fatal(err)
	}
	ls, linkPID := privacyWorker(t, p)
	es, eventPID := privacyWorker(t, p, isolation)
	linkDone, eventDone := make(chan error, 1), make(chan error, 1)
	go func() { _, err := ls.InsertBatch(ctx, []audit.Event{link}); linkDone <- err }()
	privacyBlocked(t, ctx, p, linkPID, linkGate.Conn().PgConn().PID(), linkDone)
	go func() { _, err := es.InsertBatch(ctx, []audit.Event{late}); eventDone <- err }()
	privacyBlocked(t, ctx, p, eventPID, linkPID, eventDone)
	var before string
	if err := p.QueryRow(ctx, `SELECT virtualtransaction FROM pg_locks WHERE pid=$1 AND locktype='virtualxid'`, eventPID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	_ = linkGate.Commit(ctx)
	if err := <-linkDone; err != nil {
		t.Fatal(err)
	}
	privacyBlocked(t, ctx, p, eventPID, lateGate.Conn().PgConn().PID(), eventDone)
	var after string
	if err := p.QueryRow(ctx, `SELECT virtualtransaction FROM pg_locks WHERE pid=$1 AND locktype='virtualxid'`, eventPID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Error("new historical owner did not restart the transaction")
	}
	_ = lateGate.Commit(ctx)
	if err := <-eventDone; err != nil {
		t.Fatal(err)
	}
	if !event.Equivalent(storedPrivacyEvent(t, p, late), event.Scrub(late)) {
		t.Fatal("late event retained privacy after historical closure expanded")
	}
}

func TestAuditLateLinkMergesCleanupAndInsertUUIDOrder(t *testing.T) {
	p := testPool(t)
	privacyUsers(t, p)
	privacyFlowRow(t, p, []string{privacyB})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	link := historicalGeneric(t, privacyA, privacyFlow)
	old := historicalGeneric(t, privacyB, privacyFlow)
	if link.ID.String() > old.ID.String() {
		link.ID, old.ID = old.ID, link.ID
	}
	if _, err := testStore(t, p).InsertBatch(ctx, []audit.Event{old}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exec(ctx, `UPDATE user_account SET state=$2 WHERE id=$1`, privacyA, enum.UserDeleted); err != nil {
		t.Fatal(err)
	}
	gate := privacyLock(t, p, `SELECT pg_advisory_xact_lock(774423)`)
	if _, err := p.Exec(ctx, `CREATE FUNCTION history_insert_gate() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(774423); RETURN NEW; END $$; CREATE TRIGGER history_insert_gate BEFORE INSERT ON audit_event FOR EACH ROW EXECUTE FUNCTION history_insert_gate()`); err != nil {
		t.Fatal(err)
	}
	s, pid := privacyWorker(t, p)
	done := make(chan error, 1)
	go func() { _, err := s.InsertBatch(ctx, []audit.Event{link}); done <- err }()
	privacyBlocked(t, ctx, p, pid, gate.Conn().PgConn().PID(), done)
	probe, err := p.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = probe.Exec(ctx, `SELECT id FROM audit_event WHERE id=$1 FOR UPDATE NOWAIT`, old.ID)
	_ = probe.Rollback(ctx)
	if err != nil {
		t.Errorf("old high UUID locked before new low UUID: %v", err)
	}
	_ = gate.Commit(ctx)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !event.Equivalent(storedPrivacyEvent(t, p, old), event.Scrub(old)) {
		t.Fatal("late link did not scrub existing flow row")
	}
	// A duplicate link is harmless; it must not reset the old row's storage time.
	var before, after time.Time
	if err := p.QueryRow(ctx, `SELECT create_time FROM audit_event WHERE id=$1`, old.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if r, err := s.InsertBatch(ctx, []audit.Event{link}); err != nil || r.Duplicate != 1 {
		t.Fatalf("link replay %+v %v", r, err)
	}
	if err := p.QueryRow(ctx, `SELECT create_time FROM audit_event WHERE id=$1`, old.ID).Scan(&after); err != nil || !before.Equal(after) {
		t.Fatal("scrub replay changed create_time")
	}
}

func TestAuditHistoryBatchDoesNotCrossUnrelatedFlows(t *testing.T) {
	p := testPool(t)
	privacyUsers(t, p)
	privacyFlowRow(t, p, []string{privacyB})
	ctx := context.Background()
	const otherFlow = "f_0000000000002"
	if _, err := p.Exec(ctx, `INSERT INTO identity_flow SELECT $1,$1,state,policy_fingerprint,payload,cipher_version,revision,proof_user_ids,user_id,session_id,create_time,update_time,expire_time FROM identity_flow WHERE id=$2`, otherFlow, privacyFlow); err != nil {
		t.Fatal(err)
	}
	old := historicalGeneric(t, privacyB, privacyFlow)
	unrelated := historicalGeneric(t, privacyB, otherFlow)
	if _, err := testStore(t, p).InsertBatch(ctx, []audit.Event{old, unrelated}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exec(ctx, `UPDATE user_account SET state=$2 WHERE id=$1`, privacyA, enum.UserDeleted); err != nil {
		t.Fatal(err)
	}
	link := historicalGeneric(t, privacyA, privacyFlow)
	fresh := unrelated
	fresh.ID = uuid.Must(uuid.NewV7())
	if _, err := testStore(t, p).InsertBatch(ctx, []audit.Event{fresh, link}); err != nil {
		t.Fatal(err)
	}
	if !event.Equivalent(storedPrivacyEvent(t, p, old), event.Scrub(old)) {
		t.Fatal("affected history not scrubbed")
	}
	for _, e := range []audit.Event{unrelated, fresh} {
		if !event.Equivalent(storedPrivacyEvent(t, p, e), e) {
			t.Fatal("batch cross product scrubbed unrelated flow")
		}
	}
}

func TestAuditLateLinkAfterFlowRemovalScrubsHistory(t *testing.T) {
	p := testPool(t)
	privacyUsers(t, p)
	privacyFlowRow(t, p, []string{privacyB})
	ctx := context.Background()
	old := historicalGeneric(t, privacyB, privacyFlow)
	if _, err := testStore(t, p).InsertBatch(ctx, []audit.Event{old}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exec(ctx, `DELETE FROM identity_flow WHERE id=$1`, privacyFlow); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exec(ctx, `UPDATE user_account SET state=$2 WHERE id=$1`, privacyA, enum.UserDeleted); err != nil {
		t.Fatal(err)
	}
	link := historicalGeneric(t, privacyA, privacyFlow)
	if _, err := testStore(t, p).InsertBatch(ctx, []audit.Event{link}); err != nil {
		t.Fatal(err)
	}
	if !event.Equivalent(storedPrivacyEvent(t, p, old), event.Scrub(old)) {
		t.Fatal("late link to removed flow failed to scrub old history")
	}
}

func TestAuditHistoricalReferencesRespectSourceAndNamespace(t *testing.T) {
	for _, kind := range []string{"target", "related", "foreign-user", "foreign-flow", "foreign-source"} {
		t.Run(kind, func(t *testing.T) {
			p := testPool(t)
			privacyUsers(t, p)
			privacyFlowRow(t, p, []string{privacyB})
			ctx := context.Background()
			link := historicalGeneric(t, privacyA, privacyFlow)
			flow := audit.Resource{Type: "IDENTITY_FLOW", Namespace: "test/ns", ID: privacyFlow}
			u := audit.Resource{Type: "USER", Namespace: "test/ns", ID: privacyA}
			link.Actor = audit.Actor{Type: audit.ActorAnonymous}
			link.Targets = []audit.Resource{flow}
			link.Context.RelatedResources = nil
			s := testStore(t, p)
			switch kind {
			case "target":
				link.Targets = append(link.Targets, u)
			case "related":
				link.Context.RelatedResources = []audit.Resource{u}
			case "foreign-user":
				u.Namespace = "foreign/ns"
				link.Targets = append(link.Targets, u)
			case "foreign-flow":
				link.Targets[0].Namespace = "foreign/ns"
				link.Targets = append(link.Targets, u)
			case "foreign-source":
				link.Source = "foreign/source"
				link.Targets = append(link.Targets, u)
				var err error
				s, err = audit.NewStoreWithOptions(p, audit.StoreOptions{Source: link.Source, Namespace: "test/ns"})
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.InsertBatch(ctx, []audit.Event{link}); err != nil {
				t.Fatal(err)
			}
			if _, err := p.Exec(ctx, `UPDATE user_account SET state=$2 WHERE id=$1`, privacyA, enum.UserDeleted); err != nil {
				t.Fatal(err)
			}
			late := historicalGeneric(t, privacyB, privacyFlow)
			if _, err := testStore(t, p).InsertBatch(ctx, []audit.Event{late}); err != nil {
				t.Fatal(err)
			}
			want := late
			if kind == "target" || kind == "related" {
				want = event.Scrub(late)
			}
			if !event.Equivalent(storedPrivacyEvent(t, p, late), want) {
				t.Fatal("historical source/namespace or resource boundary mismatch")
			}
		})
	}
}

func TestAuditUnknownFlowCreationCannotOvertakeLateLink(t *testing.T) {
	for _, cancelWait := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelWait), func(t *testing.T) { auditUnknownFlowCreation(t, cancelWait) })
	}
}

func auditUnknownFlowCreation(t *testing.T, cancelWait bool) {
	p := testPool(t)
	privacyUsers(t, p)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := p.Exec(ctx, `UPDATE user_account SET state=$2 WHERE id=$1`, privacyA, enum.UserDeleted); err != nil {
		t.Fatal(err)
	}
	link := historicalGeneric(t, privacyA, privacyFlow)
	late := historicalGeneric(t, privacyB, privacyFlow)
	gate := privacyLock(t, p, `SELECT pg_advisory_xact_lock(774424)`)
	if _, err := p.Exec(ctx, fmt.Sprintf(`CREATE FUNCTION unknown_flow_gate() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.id='%s'::uuid THEN PERFORM pg_advisory_xact_lock(774424); END IF; RETURN NEW; END $$; CREATE TRIGGER unknown_flow_gate BEFORE INSERT ON audit_event FOR EACH ROW EXECUTE FUNCTION unknown_flow_gate()`, link.ID)); err != nil {
		t.Fatal(err)
	}
	ls, linkPID := privacyWorker(t, p)
	otherPath := p.Config()
	otherPath.ConnConfig.RuntimeParams["search_path"] = "pg_catalog," + otherPath.ConnConfig.RuntimeParams["search_path"]
	// Both paths resolve the same table despite different current_schema().
	es, eventPID := privacyWorkerConfig(t, otherPath)
	eventCtx, eventCancel := context.WithCancel(ctx)
	defer eventCancel()
	linkDone, eventDone := make(chan error, 1), make(chan error, 1)
	go func() { _, err := ls.InsertBatch(ctx, []audit.Event{link}); linkDone <- err }()
	privacyBlocked(t, ctx, p, linkPID, gate.Conn().PgConn().PID(), linkDone)
	// This is F's first creation, not reuse of a deleted resource ID.
	privacyFlowRow(t, p, []string{privacyB})
	go func() {
		r, err := es.InsertBatch(eventCtx, []audit.Event{late})
		if err != nil && (r.Failed != 1 || r.Written != 0) {
			err = fmt.Errorf("cancel counts %+v: %w", r, err)
		}
		eventDone <- err
	}()
	privacyBlocked(t, ctx, p, eventPID, linkPID, eventDone)
	if cancelWait {
		eventCancel()
		if err := <-eventDone; !errors.Is(err, context.Canceled) {
			t.Fatalf("scope wait cancel: %v", err)
		}
	}
	_ = gate.Commit(ctx)
	if err := <-linkDone; err != nil {
		t.Fatal(err)
	}
	if !cancelWait {
		if err := <-eventDone; err != nil {
			t.Fatal(err)
		}
	} else {
		var n int
		if err := p.QueryRow(ctx, `SELECT count(*) FROM audit_event WHERE id=$1`, late.ID).Scan(&n); err != nil || n != 0 {
			t.Fatalf("cancelled event committed: %d %v", n, err)
		}
		if r, err := es.InsertBatch(ctx, []audit.Event{late}); err != nil || r.Written != 1 {
			t.Fatalf("reuse after scope cancellation %+v %v", r, err)
		}
	}
	if !event.Equivalent(storedPrivacyEvent(t, p, late), event.Scrub(late)) {
		t.Fatal("newly created flow overtook its late privacy link")
	}
}

func TestAuditLateLinkScrubFailureRollsBackInsertedRows(t *testing.T) {
	p := testPool(t)
	privacyUsers(t, p)
	privacyFlowRow(t, p, []string{privacyB})
	ctx := context.Background()
	link := historicalGeneric(t, privacyA, privacyFlow)
	old := historicalGeneric(t, privacyB, privacyFlow)
	if link.ID.String() > old.ID.String() {
		link.ID, old.ID = old.ID, link.ID
	}
	if _, err := testStore(t, p).InsertBatch(ctx, []audit.Event{old}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exec(ctx, `UPDATE user_account SET state=$2 WHERE id=$1`, privacyA, enum.UserDeleted); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exec(ctx, `CREATE FUNCTION fail_history_scrub() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic privacy scrub failure'; END $$; CREATE TRIGGER fail_history_scrub BEFORE UPDATE ON audit_event FOR EACH ROW EXECUTE FUNCTION fail_history_scrub()`); err != nil {
		t.Fatal(err)
	}
	r, err := testStore(t, p).InsertBatch(ctx, []audit.Event{link})
	if err == nil || r.Failed != 1 || r.Written != 0 {
		t.Fatalf("scrub failure counts %+v %v", r, err)
	}
	var n int
	if err := p.QueryRow(ctx, `SELECT count(*) FROM audit_event WHERE id=$1`, link.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("link escaped rollback %d %v", n, err)
	}
	if !event.Equivalent(storedPrivacyEvent(t, p, old), old) {
		t.Fatal("failed closure transaction changed existing event")
	}
}
