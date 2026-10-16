package audit_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/audit/authentication"
	"github.com/bbxx111/accountkit/audit/event"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"math/rand/v2"
	"os"
	"sync"
	"testing"
	"time"
)

func testStore(t *testing.T, pool *pgxpool.Pool) *audit.Store {
	t.Helper()
	s, err := audit.NewStoreWithOptions(pool, audit.StoreOptions{Source: "test/source", Namespace: "test/ns"})
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func mapped(t *testing.T, in audit.AuthenticationEvent) audit.Event {
	t.Helper()
	e, err := authentication.Map(in, audit.AuthenticationOptions{Source: "test/source", Namespace: "test/ns"})
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func records(t *testing.T, s *audit.Store) []audit.RecordView {
	t.Helper()
	p, err := s.ListByResource(context.Background(), audit.Query{Source: "test/source", Resource: audit.Resource{Type: "USER", Namespace: "test/ns", ID: "u_0000000000001"}, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	return p.Records
}

// testPool 返回连接到随机 schema、已迁移的池；无 DSN 则跳过（与 user/repo_test.go 同形）。
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("SERVER_TEST_DB_DSN")
	if dsn == "" {
		t.Skip("SERVER_TEST_DB_DSN not set")
	}
	schema := fmt.Sprintf("authtest_%08x", rand.Uint32())
	ctx := context.Background()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	if err := migrations.Up(ctx, cfg.ConnConfig, schema); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		pool.Close()
	})
	return pool
}

func TestStoreInsertBatchFailureConcurrentPoolQueries(t *testing.T) {
	base := testPool(t)
	cfg := base.Config()
	// 单连接池让审计与业务查询不断交接同一条连接，覆盖失败批次关闭后的复用。
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx, `CREATE FUNCTION fail_audit_batch() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic audit outage'; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE TRIGGER fail_audit_batch BEFORE INSERT ON audit_event FOR EACH ROW EXECUTE FUNCTION fail_audit_batch()`); err != nil {
		t.Fatal(err)
	}
	events := make([]audit.Event, 512)
	for i := range events {
		events[i] = ev(i)
	}
	store := testStore(t, pool)
	queryDone := make(chan error, 1)
	go func() {
		for i := 0; i < 200; i++ {
			var count int
			// 不同语句模拟业务查询不断填充 statement cache。
			if err := pool.QueryRow(ctx, fmt.Sprintf("SELECT count(*) FROM user_account /* audit reuse %d */", i)).Scan(&count); err != nil {
				queryDone <- err
				return
			}
			if count != 0 {
				queryDone <- fmt.Errorf("unexpected users: %d", count)
				return
			}
		}
		queryDone <- nil
	}()
	for i := 0; i < 50; i++ {
		failed, err := store.InsertBatch(ctx, events)
		var pgErr *pgconn.PgError
		if failed.Failed != len(events) || !errors.As(err, &pgErr) || pgErr.Code != "P0001" {
			t.Errorf("batch %d: result=%+v err=%v", i, failed, err)
			break
		}
	}
	if err := <-queryDone; err != nil {
		t.Fatal(err)
	}
	if acquired := pool.Stat().AcquiredConns(); acquired != 0 {
		t.Fatalf("batch/query leaked %d connections", acquired)
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM audit_event").Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed batches must leave no rows: count=%d err=%v", count, err)
	}
}

func TestStoreInsertBatchReportsDeferredCommitFailure(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `CREATE FUNCTION fail_audit_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic deferred audit failure'; END $$`); err != nil {
		t.Fatal(err)
	}
	// INSERT 的命令结果成功，隐式事务提交时才触发错误；必须保留最终 Close 错误。
	if _, err := pool.Exec(ctx, `CREATE CONSTRAINT TRIGGER fail_audit_commit AFTER INSERT ON audit_event DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION fail_audit_commit()`); err != nil {
		t.Fatal(err)
	}
	store := testStore(t, pool)
	events := []audit.Event{
		ev(1),
		ev(2),
	}
	failed, err := store.InsertBatch(ctx, events)
	var pgErr *pgconn.PgError
	if failed.Failed != len(events) || !errors.As(err, &pgErr) || pgErr.Code != "P0001" {
		t.Fatalf("deferred commit failure: result=%+v err=%v", failed, err)
	}
	if acquired := pool.Stat().AcquiredConns(); acquired != 0 {
		t.Fatalf("commit failure leaked %d connections", acquired)
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM audit_event").Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed commit must roll back all rows: count=%d err=%v", count, err)
	}
	if _, err := pool.Exec(ctx, "DROP TRIGGER fail_audit_commit ON audit_event"); err != nil {
		t.Fatal(err)
	}
	if failed, err := store.InsertBatch(ctx, events); err != nil || failed.Failed != 0 {
		t.Fatalf("reuse after failed commit: result=%+v err=%v", failed, err)
	}
	if acquired := pool.Stat().AcquiredConns(); acquired != 0 {
		t.Fatalf("successful batch leaked %d connections", acquired)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM audit_event").Scan(&count); err != nil || count != len(events) {
		t.Fatalf("successful commit: count=%d err=%v", count, err)
	}
}

func TestStoreInsertBatchMapsFieldsAndNulls(t *testing.T) {
	pool := testPool(t)
	privacyUsers(t, pool)
	privacyFlowRow(t, pool, []string{privacyA})
	s := testStore(t, pool)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	full := mapped(t, audit.AuthenticationEvent{Type: enum.EventSignIn, Actor: enum.ActorUser, Result: enum.ResultSuccess, Reason: "NEW_USER", UserID: "u_0000000000001", SessionID: "s_0000000000001", FlowID: "f_0000000000001", IdentityKind: enum.IdentityPhone, SubjectHint: "abcdefgh", IP: "203.0.113.5", DeviceID: "dev-1", RequestID: "req-1", OccurTime: now})
	sparse := mapped(t, audit.AuthenticationEvent{Type: enum.EventUserPurged, Actor: enum.ActorSystem, UserID: "u_0000000000001", OccurTime: now.Add(time.Second)})
	badIP := mapped(t, audit.AuthenticationEvent{Type: enum.EventCodeSent, IP: "not-an-ip", OccurTime: now.Add(2 * time.Second)})
	result, err := s.InsertBatch(ctx, []audit.Event{full, sparse, badIP})
	if err != nil || result.Written != 3 {
		t.Fatalf("insert: %+v %v", result, err)
	}
	got := records(t, s)
	if len(got) != 2 || !event.Equivalent(got[0].Event, full) || !event.Equivalent(got[1].Event, sparse) || got[0].CreateTime.IsZero() {
		t.Fatalf("mapping round trip: %+v", got)
	}
	if got[0].ID != full.ID || got[0].ID.Version() != 7 || got[1].Result != nil {
		t.Fatal("UUID or NULL result changed")
	}
	var ipNull bool
	if err := pool.QueryRow(ctx, `SELECT context->>'client_ip' IS NULL FROM audit_event WHERE id=$1`, badIP.ID).Scan(&ipNull); err != nil || !ipNull {
		t.Fatalf("bad ip: %v %v", ipNull, err)
	}
	if result, err := s.InsertBatch(ctx, nil); err != nil || result != (audit.BatchResult{}) {
		t.Fatalf("empty batch: %+v %v", result, err)
	}
}
func TestStoreInsertBatchIsAllOrNothing(t *testing.T) {
	pool := testPool(t)
	s := testStore(t, pool)
	ctx := context.Background()
	first := genericEvent()
	if _, err := s.InsertBatch(ctx, []audit.Event{first}); err != nil {
		t.Fatal(err)
	}
	conflict := first
	conflict.Action = "CHANGED"
	fresh := genericEvent()
	result, err := s.InsertBatch(ctx, []audit.Event{fresh, conflict})
	if !errors.Is(err, audit.ErrConflict) || result.Conflicts != 1 || result.Failed != 1 || result.Written != 0 {
		t.Fatalf("conflict result: %+v %v", result, err)
	}
	var n int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM audit_event").Scan(&n); err != nil || n != 1 {
		t.Fatalf("partial commit: %d %v", n, err)
	}
	pool.Close()
	result, err = s.InsertBatch(ctx, []audit.Event{fresh, genericEvent()})
	if err == nil || result.Failed != 2 || errors.Is(err, context.Canceled) {
		t.Fatalf("closed pool: %+v %v", result, err)
	}
}

// Replaces obsolete TSID same-millisecond random retry tests: UUIDs are producer identities.
func TestStoreConcurrentDuplicate(t *testing.T) {
	pool := testPool(t)
	s := testStore(t, pool)
	ctx := context.Background()
	e := genericEvent()
	var wg sync.WaitGroup
	results := make(chan audit.BatchResult, 16)
	errs := make(chan error, 16)
	for range 16 {
		wg.Add(1)
		go func() { defer wg.Done(); r, err := s.InsertBatch(ctx, []audit.Event{e}); results <- r; errs <- err }()
	}
	wg.Wait()
	close(results)
	close(errs)
	written, duplicate := 0, 0
	for r := range results {
		written += r.Written
		duplicate += r.Duplicate
	}
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if written != 1 || duplicate != 15 {
		t.Fatalf("written=%d duplicate=%d", written, duplicate)
	}
	before := records(t, s)[0]
	r, err := s.InsertBatch(ctx, []audit.Event{e, e})
	if err != nil || r.Duplicate != 2 {
		t.Fatalf("replay: %+v %v", r, err)
	}
	after := records(t, s)[0]
	if before.ID != after.ID || !before.CreateTime.Equal(after.CreateTime) {
		t.Fatal("duplicate changed identity or create_time")
	}
}
func TestStoreConflict(t *testing.T) {
	pool := testPool(t)
	s := testStore(t, pool)
	ctx := context.Background()
	e := genericEvent()
	if _, err := s.InsertBatch(ctx, []audit.Event{e}); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*audit.Event){func(e *audit.Event) { e.Action = "OTHER" }, func(e *audit.Event) { e.OccurTime = e.OccurTime.Add(time.Second) }, func(e *audit.Event) { e.Targets = []audit.Resource{{Type: "USER", Namespace: "test/ns", ID: "other"}} }, func(e *audit.Event) { e.Data = []byte(`{"other":true}`) }} {
		changed, _ := event.Freeze(e)
		mutate(&changed)
		r, err := s.InsertBatch(ctx, []audit.Event{changed})
		if !errors.Is(err, audit.ErrConflict) || r.Conflicts != 1 {
			t.Fatalf("expected conflict: %+v %v", r, err)
		}
	}
	other, err := audit.NewStoreWithOptions(pool, audit.StoreOptions{Source: "other/source", Namespace: "test/ns"})
	if err != nil {
		t.Fatal(err)
	}
	e.Source = "other/source"
	r, err := other.InsertBatch(ctx, []audit.Event{e})
	if !errors.Is(err, audit.ErrConflict) || r.Conflicts != 1 {
		t.Fatalf("source conflict: %+v %v", r, err)
	}
}
func TestStoreScrubbedReplay(t *testing.T) {
	pool := testPool(t)
	s := testStore(t, pool)
	ctx := context.Background()
	e := mapped(t, audit.AuthenticationEvent{Type: enum.EventSignIn, UserID: "u_0000000000001", IP: "203.0.113.5", DeviceID: "private-device", SubjectHint: "abcdefgh", OccurTime: time.Now()})
	scrubbed := authentication.Scrub(e)
	if _, err := s.InsertBatch(ctx, []audit.Event{scrubbed}); err != nil {
		t.Fatal(err)
	}
	before := records(t, s)[0]
	r, err := s.InsertBatch(ctx, []audit.Event{e})
	if err != nil || r.Duplicate != 1 {
		t.Fatalf("scrubbed replay: %+v %v", r, err)
	}
	after := records(t, s)[0]
	if !event.Equivalent(after.Event, scrubbed) || !before.CreateTime.Equal(after.CreateTime) {
		t.Fatal("replay restored privacy")
	}
}
func TestStoreResourcePagination(t *testing.T) {
	pool := testPool(t)
	s := testStore(t, pool)
	ctx := context.Background()
	now := time.Now().Truncate(time.Microsecond)
	es := make([]audit.Event, 5)
	resource := audit.Resource{Type: "USER", Namespace: "test/ns", ID: "u_0000000000001"}
	for i := range es {
		e := genericEvent()
		e.OccurTime = now
		e.Targets = nil
		if i%3 == 0 {
			e.Actor = audit.Actor{Type: audit.ActorUser, Namespace: resource.Namespace, ID: resource.ID}
		} else if i%3 == 1 {
			e.Targets = []audit.Resource{resource}
		} else {
			e.Context.RelatedResources = []audit.Resource{resource}
		}
		es[i] = e
	}
	if _, err := s.InsertBatch(ctx, es); err != nil {
		t.Fatal(err)
	}
	otherUser := genericEvent()
	otherUser.Targets[0].ID = "u_0000000000002"
	otherNamespace := genericEvent()
	otherNamespace.Targets[0].Namespace = "foreign/namespace"
	if _, err := s.InsertBatch(ctx, []audit.Event{otherUser, otherNamespace}); err != nil {
		t.Fatal(err)
	}
	q := audit.Query{Source: "test/source", Resource: resource, Limit: 2}
	var all []audit.RecordView
	for {
		p, err := s.ListByResource(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, p.Records...)
		if len(p.Records) < 2 {
			break
		}
		last := p.Records[len(p.Records)-1]
		q.After = &audit.EventCursor{Time: last.OccurTime, ID: last.ID}
	}
	if len(all) != 5 {
		t.Fatalf("count %d", len(all))
	}
	for i := 1; i < len(all); i++ {
		if bytes.Compare(all[i-1].ID[:], all[i].ID[:]) >= 0 {
			t.Fatal("unstable equal-time order")
		}
	}
	q.After = nil
	q.Resource.ID = "u_0000000000003"
	if page, err := s.ListByResource(ctx, q); err != nil || len(page.Records) != 0 {
		t.Fatalf("unknown user returned records: %+v %v", page, err)
	}
	q.Source = "other"
	if _, err := s.ListByResource(ctx, q); err == nil {
		t.Fatal("foreign source accepted")
	}
	q.Source = "test/source"
	q.Resource.Namespace = "other"
	if _, err := s.ListByResource(ctx, q); err == nil {
		t.Fatal("foreign namespace accepted")
	}
}
func TestStoreDeleteOlderThanBatches(t *testing.T) {
	pool := testPool(t)
	s := testStore(t, pool)
	ctx := context.Background()
	cutoff := time.Now().UTC().Truncate(time.Microsecond)
	var es []audit.Event
	for i := 0; i < audit.RetentionBatchSize+5; i++ {
		e := ev(i)
		e.OccurTime = cutoff.Add(-time.Hour)
		if i%2 == 0 {
			e.Actor = audit.Actor{Type: audit.ActorUser, Namespace: "https://external-admin", ID: "operator"}
		}
		es = append(es, e)
	}
	fresh := ev(99)
	fresh.OccurTime = cutoff
	es = append(es, fresh)
	if _, err := s.InsertBatch(ctx, es); err != nil {
		t.Fatal(err)
	}
	other, _ := audit.NewStoreWithOptions(pool, audit.StoreOptions{Source: "foreign", Namespace: "test/ns"})
	foreign := ev(0)
	foreign.Source = "foreign"
	foreign.OccurTime = cutoff.Add(-time.Hour)
	if _, err := other.InsertBatch(ctx, []audit.Event{foreign}); err != nil {
		t.Fatal(err)
	}
	n, err := s.DeleteOlderThan(ctx, cutoff)
	if err != nil || n != audit.RetentionBatchSize+5 {
		t.Fatalf("retention: %d %v", n, err)
	}
	var left int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM audit_event").Scan(&left); err != nil || left != 2 {
		t.Fatalf("fresh/foreign lost: %d %v", left, err)
	}
	if n, err := s.DeleteOlderThan(ctx, cutoff); err != nil || n != 0 {
		t.Fatalf("repeat: %d %v", n, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.DeleteOlderThan(canceled, cutoff); !errors.Is(err, context.Canceled) {
		t.Fatal("cancel not honored")
	}
}

var _ = uuid.Nil

func TestStoreFlowClosureDoesNotLeakOtherProofOwners(t *testing.T) {
	pool := testPool(t)
	s := testStore(t, pool)
	ctx := context.Background()
	const a = "u_0000000000001"
	const b = "u_0000000000002"
	const flow = "f_0000000000001"
	early := mapped(t, audit.AuthenticationEvent{Type: enum.EventSignInCompletionStarted, FlowID: flow, Result: enum.ResultSuccess})
	if _, err := s.InsertBatch(ctx, []audit.Event{early}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO identity_flow(id,token_digest,state,policy_fingerprint,payload,cipher_version,proof_user_ids,create_time,update_time,expire_time) VALUES($1,'synthetic-flow',1,'policy','\x1234',1,ARRAY[$2::text],now(),now(),now()+interval '5 minutes')`, flow, a); err != nil {
		t.Fatal(err)
	}
	query := audit.Query{Source: "test/source", Resource: audit.Resource{Type: "USER", Namespace: "test/ns", ID: a}, Limit: 20}
	page, err := s.ListByResource(ctx, query)
	if err != nil || len(page.Records) != 1 || page.Records[0].ID != early.ID || len(page.FlowLinks) != 1 || !page.FlowLinks[0].Live || page.FlowLinks[0].Evidence.ID != uuid.Nil {
		t.Fatalf("live ownership: %+v %v", page, err)
	}
	proofA := mapped(t, audit.AuthenticationEvent{Type: enum.EventSignInCompletionProofVerified, FlowID: flow, UserID: a, VerifiedSubject: true, Result: enum.ResultSuccess})
	proofB := mapped(t, audit.AuthenticationEvent{Type: enum.EventSignInCompletionProofVerified, FlowID: flow, UserID: b, VerifiedSubject: true, Result: enum.ResultSuccess})
	sessionB := mapped(t, audit.AuthenticationEvent{Type: enum.EventSignIn, FlowID: flow, UserID: b, SessionID: "s_0000000000002", Result: enum.ResultSuccess})
	if _, err := s.InsertBatch(ctx, []audit.Event{proofA, proofB, sessionB}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "DELETE FROM identity_flow WHERE id=$1", flow); err != nil {
		t.Fatal(err)
	}
	page, err = s.ListByResource(ctx, query)
	if err != nil || len(page.Records) != 2 {
		t.Fatalf("historical closure: %+v %v", page, err)
	}
	for _, record := range page.Records {
		if record.ID != early.ID && record.ID != proofA.ID {
			t.Fatal("other consumer event leaked through flow")
		}
	}
	if len(page.FlowLinks) != 1 || page.FlowLinks[0].Live || page.FlowLinks[0].Evidence.ID != proofA.ID {
		t.Fatal("historical evidence is not real persisted proof")
	}
	if !event.Equivalent(page.Records[0].Event, early) {
		t.Fatal("closure mutated early event")
	}
}
