package audit_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/audit/authentication"
	"github.com/bbxx111/accountkit/enum"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestStoreFlowEvidenceIsPageScoped(t *testing.T) {
	pool := testPool(t)
	store := testStore(t, pool)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	const uid = "u_0000000000001"
	const flow = "f_0000000000001"
	const otherFlow = "f_0000000000002"
	early := mapped(t, audit.AuthenticationEvent{Type: enum.EventSignInCompletionStarted, FlowID: flow, OccurTime: now})
	early.Context.RelatedResources = append(early.Context.RelatedResources, early.Targets[0])
	proof := mapped(t, audit.AuthenticationEvent{Type: enum.EventSignInCompletionProofVerified, UserID: uid, FlowID: flow, VerifiedSubject: true, Result: enum.ResultSuccess, OccurTime: now.Add(time.Hour)})
	invalid := proof
	invalid.ID = uuid.Must(uuid.NewV7())
	invalid.OccurTime = now.Add(-time.Hour)
	invalid.Data = json.RawMessage(`{}`)
	events := []audit.Event{early, invalid}
	for range 200 {
		e := proof
		e.ID = uuid.Must(uuid.NewV7())
		events = append(events, e)
	}
	unrelated := mapped(t, audit.AuthenticationEvent{Type: enum.EventSignInCompletionProofVerified, UserID: uid, FlowID: otherFlow, VerifiedSubject: true, Result: enum.ResultSuccess, OccurTime: now.Add(time.Hour)})
	events = append(events, unrelated)
	if _, err := store.InsertBatch(ctx, events); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO sign_in_completion_flow(id,token_digest,state,policy_fingerprint,payload,cipher_version,proof_user_ids,create_time,update_time,expire_time)
VALUES($1,$1,1,'policy','\x1234',1,ARRAY[$2::text],now(),now(),now()+interval '5 minutes')`, flow, uid); err != nil {
		t.Fatal(err)
	}
	query := audit.Query{Source: "test/source", Resource: audit.Resource{Type: "USER", Namespace: "test/ns", ID: uid}, Actions: []string{"SIGN_IN_COMPLETION_STARTED"}, After: &audit.EventCursor{Time: now.Add(-time.Minute), ID: uuid.Nil}, Limit: 1}
	page, err := store.ListByResource(ctx, query)
	if err != nil || len(page.Records) != 1 || page.Records[0].ID != early.ID {
		t.Fatalf("early page: records=%d err=%v", len(page.Records), err)
	}
	if len(page.FlowLinks) != 1 {
		t.Fatalf("one page flow needs one deduplicated proof, got %d", len(page.FlowLinks))
	}
	link := page.FlowLinks[0]
	if link.Flow.ID != flow || link.User.ID != uid || link.Live || link.Evidence.ID == uuid.Nil || !link.Evidence.OccurTime.After(early.OccurTime) {
		t.Fatalf("wrong page evidence: %+v", link)
	}
	if _, err := authentication.Projection(link.Evidence.Event); err != nil {
		t.Fatalf("selected evidence cannot authorize the HTTP page: %v", err)
	}
	query.After = &audit.EventCursor{Time: now.Add(2 * time.Hour), ID: uuid.Nil}
	page, err = store.ListByResource(ctx, query)
	if err != nil || len(page.Records) != 0 || len(page.FlowLinks) != 0 {
		t.Fatalf("empty page must have no evidence: records=%d links=%d err=%v", len(page.Records), len(page.FlowLinks), err)
	}
}

func TestStoreFlowPaginationDeduplicatesBeforeLimit(t *testing.T) {
	pool := testPool(t)
	store := testStore(t, pool)
	ctx := context.Background()
	const uid = "u_0000000000001"
	refs := []audit.Resource{
		{Type: "IDENTITY_FLOW", Namespace: "test/ns", ID: "f_0000000000001"},
		{Type: "IDENTITY_FLOW", Namespace: "test/ns", ID: "f_0000000000002"},
		{Type: "IDENTITY_FLOW", Namespace: "test/ns", ID: "f_0000000000003"},
	}
	for _, ref := range refs {
		if _, err := pool.Exec(ctx, `INSERT INTO sign_in_completion_flow(id,token_digest,state,policy_fingerprint,payload,cipher_version,proof_user_ids,create_time,update_time,expire_time)
VALUES($1,$1,1,'policy','\x1234',1,ARRAY[$2::text],now(),now(),now()+interval '5 minutes')`, ref.ID, uid); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	var events []audit.Event
	for range 5 {
		e := genericEvent()
		e.Actor = audit.Actor{Type: audit.ActorAnonymous}
		e.Targets = refs
		e.OccurTime = now
		events = append(events, e)
	}
	if _, err := store.InsertBatch(ctx, events); err != nil {
		t.Fatal(err)
	}
	q := audit.Query{Source: "test/source", Resource: audit.Resource{Type: "USER", Namespace: "test/ns", ID: uid}, Limit: 2}
	seen := map[uuid.UUID]bool{}
	for _, want := range []int{2, 2, 1} {
		page, err := store.ListByResource(ctx, q)
		if err != nil || len(page.Records) != want || len(page.FlowLinks) != 3 {
			t.Fatalf("overlapping flow page: records=%d want=%d links=%d err=%v", len(page.Records), want, len(page.FlowLinks), err)
		}
		for _, record := range page.Records {
			if seen[record.ID] {
				t.Fatal("duplicate record across pages")
			}
			seen[record.ID] = true
		}
		last := page.Records[len(page.Records)-1]
		q.After = &audit.EventCursor{Time: last.OccurTime, ID: last.ID}
	}
}

type paginationQueryTrace struct {
	mu           sync.Mutex
	sql          string
	args         []any
	logf         func(string, ...any)
	evidenceSQL  string
	evidenceArgs []any
}

type auditQueryTimingKey struct{}
type auditQueryTiming struct {
	start time.Time
	name  string
}

func (tr *paginationQueryTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	if strings.HasPrefix(d.SQL, "-- name: ListAuditEventsByResource ") {
		tr.mu.Lock()
		tr.sql, tr.args = d.SQL, append([]any(nil), d.Args...)
		tr.mu.Unlock()
	}
	if strings.HasPrefix(d.SQL, "-- name: ListAuditFlowEvidence ") {
		tr.mu.Lock()
		tr.evidenceSQL, tr.evidenceArgs = d.SQL, append([]any(nil), d.Args...)
		tr.mu.Unlock()
	}
	if tr.logf != nil && strings.HasPrefix(d.SQL, "-- name: List") {
		return context.WithValue(ctx, auditQueryTimingKey{}, auditQueryTiming{start: time.Now(), name: strings.SplitN(d.SQL, "\n", 2)[0]})
	}
	return ctx
}
func (tr *paginationQueryTrace) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryEndData) {
	if timing, ok := ctx.Value(auditQueryTimingKey{}).(auditQueryTiming); ok && tr.logf != nil {
		tr.logf("%s duration=%s rows=%d err=%v", timing.name, time.Since(timing.start), d.CommandTag.RowsAffected(), d.Err)
	}
}

// This catches unbounded full-JSON UNION/materialization, not a timing threshold:
// a plan may scan history, but no node producing full event data may exceed the page.
func TestStoreResourcePaginationAtScale(t *testing.T) {
	base := testPool(t)
	cfg := base.Config()
	tr := &paginationQueryTrace{}
	cfg.ConnConfig.Tracer = tr
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	ctx := context.Background()
	_, err = pool.Exec(ctx, `INSERT INTO audit_event(id,source,action,result,actor,targets,context,data,occur_time)
SELECT ('01900000-0000-7000-8000-' || lpad(to_hex(n),12,'0'))::uuid,
'test/source','SCALE_EVENT',1,jsonb_build_object('type','SYSTEM','namespace','test/ns','id','system-' || n),
jsonb_build_array(jsonb_build_object('type','USER','namespace','test/ns','id',
CASE WHEN n <= 100000 THEN 'u_0000000000001' ELSE 'u_0000000000002' END)),
'{}',jsonb_build_object('padding',repeat(md5(n::text),32)),
'2026-01-01'::timestamptz + n * interval '1 microsecond'
FROM generate_series(1,100003) n`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "ANALYZE audit_event"); err != nil {
		t.Fatal(err)
	}
	store := testStore(t, pool)
	for _, distribution := range []string{"many_actors", "one_actor"} {
		if distribution == "one_actor" {
			if _, err := pool.Exec(ctx, `UPDATE audit_event SET actor='{"type":"SYSTEM","namespace":"test/ns","id":"system"}'`); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, "VACUUM ANALYZE audit_event"); err != nil {
				t.Fatal(err)
			}
		}
		for _, mode := range []string{"force_custom_plan", "force_generic_plan"} {
			for _, fixture := range []struct {
				name, uid string
				want      int
			}{
				{"dense", "u_0000000000001", 51},
				{"sparse", "u_0000000000002", 3},
			} {
				t.Run(distribution+"/"+mode+"/"+fixture.name, func(t *testing.T) {
					page, err := store.ListByResource(ctx, audit.Query{Source: "test/source", Resource: audit.Resource{Type: "USER", Namespace: "test/ns", ID: fixture.uid}, Limit: 51})
					if err != nil || len(page.Records) != fixture.want {
						t.Fatalf("page=%d err=%v", len(page.Records), err)
					}
					conn, err := pool.Acquire(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Release()
					if _, err := conn.Exec(ctx, "SET plan_cache_mode = "+mode); err != nil {
						t.Fatal(err)
					}
					defer conn.Exec(ctx, "RESET plan_cache_mode")
					if _, err := conn.Exec(ctx, "SET jit = off"); err != nil {
						t.Fatal(err)
					}
					defer conn.Exec(ctx, "RESET jit")
					prepared, err := conn.Conn().Prepare(ctx, "audit_scale_plan", tr.sql)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Conn().Deallocate(ctx, "audit_scale_plan")
					literals := make([]string, len(tr.args))
					for i, a := range tr.args {
						encoded, err := conn.Conn().TypeMap().Encode(prepared.ParamOIDs[i], pgx.TextFormatCode, a, nil)
						if err != nil {
							t.Fatal(err)
						}
						literals[i] = "NULL"
						if encoded != nil {
							literals[i] = "'" + strings.ReplaceAll(string(encoded), "'", "''") + "'"
						}
					}
					var raw []byte
					if err := conn.QueryRow(ctx, "EXPLAIN (ANALYZE, BUFFERS, VERBOSE, FORMAT JSON) EXECUTE audit_scale_plan("+strings.Join(literals, ",")+")").Scan(&raw); err != nil {
						t.Fatal(err)
					}
					var plan []map[string]any
					if err := json.Unmarshal(raw, &plan); err != nil {
						t.Fatal(err)
					}
					t.Logf("plan=%s", raw)
					checkAuditPagePlan(t, plan[0]["Plan"].(map[string]any), 51, fixture.name == "sparse" && distribution == "many_actors")
				})
			}
		}
	}
}

func checkAuditPagePlan(t *testing.T, node map[string]any, limit float64, sparse bool) {
	t.Helper()
	rows, _ := node["Actual Rows"].(float64)
	loops, _ := node["Actual Loops"].(float64)
	if output, ok := node["Output"].([]any); ok {
		for _, value := range output {
			if text, ok := value.(string); ok && (text == "data" || strings.HasSuffix(text, ".data")) && rows*loops > limit {
				t.Errorf("full event node %s emitted %.0f rows, limit %.0f", node["Node Type"], rows*loops, limit)
			}
		}
	}
	if sparse && node["Relation Name"] == "audit_event" {
		removed, _ := node["Rows Removed by Filter"].(float64)
		if (rows+removed)*loops > 1000 {
			t.Errorf("sparse membership scanned %.0f rows via %v/%v", (rows+removed)*loops, node["Node Type"], node["Index Name"])
		}
	}
	children, _ := node["Plans"].([]any)
	for _, child := range children {
		checkAuditPagePlan(t, child.(map[string]any), limit, sparse)
	}
}

// Exercise historical flow closure at scale: none of these flows is live, and
// the page's action excludes every ownership proof. The early events can only
// be reached through the nonempty owned_flows/flow_matches path.
func TestStoreHistoricalFlowPaginationAtScale(t *testing.T) {
	base := testPool(t)
	cfg := base.Config()
	trace := &paginationQueryTrace{logf: t.Logf}
	cfg.ConnConfig.Tracer = trace
	cfg.ConnConfig.RuntimeParams["jit"] = "off"
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	ctx := context.Background()
	store := testStore(t, pool)
	var proofs []audit.Event
	for i := 1; i <= 201; i++ {
		uid := "u_0000000000001"
		if i == 201 {
			uid = "u_0000000000002"
		}
		proofs = append(proofs, mapped(t, audit.AuthenticationEvent{
			Type: enum.EventSignInCompletionProofVerified, UserID: uid,
			FlowID: fmt.Sprintf("f_%013x", i), VerifiedSubject: true, Result: enum.ResultSuccess,
			OccurTime: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
		}))
	}
	if _, err := store.InsertBatch(ctx, proofs); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO audit_event(id,source,action,result,actor,targets,context,data,occur_time)
SELECT ('01900000-0000-7000-8000-' || lpad(to_hex(n),12,'0'))::uuid,
'test/source','SCALE_FLOW_EVENT',1,jsonb_build_object('type','SYSTEM','namespace','test/ns','id','system-' || n),
jsonb_build_array(jsonb_build_object('type','IDENTITY_FLOW','namespace','test/ns','id',
'f_' || lpad(to_hex(CASE WHEN n <= 100000 THEN ((n-1) % 200)+1 ELSE 201 END),13,'0'))),
'{}',jsonb_build_object('padding',repeat(md5(n::text),32)), '2026-01-01'::timestamptz
FROM generate_series(1,100003) n`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "ANALYZE audit_event"); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"force_custom_plan", "force_generic_plan"} {
		for _, fixture := range []struct {
			name, uid          string
			want, first, owned int
		}{
			{"dense", "u_0000000000001", 51, 1, 200},
			{"sparse", "u_0000000000002", 3, 100001, 1},
		} {
			t.Run(mode+"/"+fixture.name, func(t *testing.T) {
				modeConfig := pool.Config()
				modeConfig.ConnConfig.RuntimeParams["plan_cache_mode"] = mode
				modePool, err := pgxpool.NewWithConfig(ctx, modeConfig)
				if err != nil {
					t.Fatal(err)
				}
				defer modePool.Close()
				store := testStore(t, modePool)
				query := audit.Query{
					Source: "test/source", Resource: audit.Resource{Type: "USER", Namespace: "test/ns", ID: fixture.uid},
					Actions: []string{"SCALE_FLOW_EVENT"}, Limit: 51,
				}
				page, err := store.ListByResource(ctx, query)
				if err != nil || len(page.Records) != fixture.want {
					t.Fatalf("historical page: records=%d want=%d err=%v", len(page.Records), fixture.want, err)
				}
				for i, record := range page.Records {
					want := uuid.MustParse(fmt.Sprintf("01900000-0000-7000-8000-%012x", fixture.first+i))
					if record.ID != want {
						t.Fatalf("record[%d]=%s want=%s", i, record.ID, want)
					}
				}
				wantLinks := fixture.want
				if fixture.name == "sparse" {
					wantLinks = 1
				}
				if len(page.FlowLinks) != wantLinks {
					t.Fatalf("links=%d want=%d", len(page.FlowLinks), wantLinks)
				}
				for _, link := range page.FlowLinks {
					if link.Live || link.User.ID != fixture.uid || link.Evidence.ID == uuid.Nil {
						t.Fatalf("invalid historical link: %+v", link)
					}
					if _, err := authentication.Projection(link.Evidence.Event); err != nil {
						t.Fatal(err)
					}
				}
				plan := explainAuditPage(t, modePool, trace, mode)
				// PostgreSQL may list all base tuple fields below a Result that
				// projects only ownership references. Check complete event
				// materialization and final PK lookup, not unused heap fields.
				checkAuditPageMaterialization(t, plan, 51)
				var owned, matched, lookups float64
				walkAuditPlan(plan, func(node map[string]any) {
					rows, _ := node["Actual Rows"].(float64)
					loops, _ := node["Actual Loops"].(float64)
					switch node["Subplan Name"] {
					case "CTE owned_flows":
						owned = rows * loops
					case "CTE flow_matches":
						matched = rows * loops
					}
					if node["Index Name"] == "audit_event_pkey" {
						lookups += rows * loops
					}
				})
				if owned != float64(fixture.owned) || matched < float64(fixture.want) || lookups != float64(fixture.want) {
					t.Fatalf("nonempty flow plan: owned=%.0f matched=%.0f full lookups=%.0f", owned, matched, lookups)
				}
				last := page.Records[len(page.Records)-1]
				query.After = &audit.EventCursor{Time: last.OccurTime, ID: last.ID}
				afterTrace := &paginationQueryTrace{sql: trace.sql, args: append([]any(nil), trace.args...)}
				for i, arg := range afterTrace.args {
					switch arg.(type) {
					case *time.Time:
						afterTrace.args[i] = &last.OccurTime
					case *uuid.UUID:
						afterTrace.args[i] = &last.ID
					}
				}
				t.Log("second page estimated plan")
				estimated := explainAuditPage(t, modePool, afterTrace, mode, false)
				walkAuditPlan(estimated, func(node map[string]any) {
					if node["Subplan Name"] != "CTE flow_matches" {
						return
					}
					walkAuditPlan(node, func(scan map[string]any) {
						if scan["Index Name"] == "idx_audit_event_source_occur_time_id" {
							t.Fatal("second page flow matching plans a source-timeline walk for each owned flow")
						}
					})
				})
				t.Log("second page plan")
				afterPlan := explainAuditPage(t, modePool, afterTrace, mode)
				checkAuditPageMaterialization(t, afterPlan, 51)
				walkAuditPlan(afterPlan, func(node map[string]any) {
					if node["Relation Name"] != "audit_event" {
						return
					}
					rows, _ := node["Actual Rows"].(float64)
					removed, _ := node["Rows Removed by Filter"].(float64)
					loops, _ := node["Actual Loops"].(float64)
					// Every flow may scan its own historical candidates. It
					// must not scan the complete source timeline per flow.
					if (rows+removed)*loops > 2*100204 {
						t.Fatalf("second page repeatedly scans history: %v rows=%.0f loops=%.0f removed=%.0f", node["Index Name"], rows, loops, removed)
					}
				})
				next, err := store.ListByResource(ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				wantNext := 51
				if fixture.name == "sparse" {
					wantNext = 0
				}
				if len(next.Records) != wantNext {
					t.Fatalf("second historical page=%d want=%d", len(next.Records), wantNext)
				}
				if wantNext > 0 {
					want := uuid.MustParse("01900000-0000-7000-8000-000000000034")
					if next.Records[0].ID != want {
						t.Fatalf("second page first=%s want=%s", next.Records[0].ID, want)
					}
				}
			})
		}
	}
}

func explainAuditPage(t *testing.T, pool *pgxpool.Pool, trace *paginationQueryTrace, mode string, analyze ...bool) map[string]any {
	t.Helper()
	ctx := context.Background()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SET plan_cache_mode = "+mode); err != nil {
		t.Fatal(err)
	}
	defer conn.Exec(ctx, "RESET plan_cache_mode")
	if _, err := conn.Exec(ctx, "SET jit = off"); err != nil {
		t.Fatal(err)
	}
	defer conn.Exec(ctx, "RESET jit")
	prepared, err := conn.Conn().Prepare(ctx, "audit_flow_scale_plan", trace.sql)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Conn().Deallocate(ctx, "audit_flow_scale_plan")
	literals := make([]string, len(trace.args))
	for i, arg := range trace.args {
		encoded, err := conn.Conn().TypeMap().Encode(prepared.ParamOIDs[i], pgx.TextFormatCode, arg, nil)
		if err != nil {
			t.Fatal(err)
		}
		literals[i] = "NULL"
		if encoded != nil {
			literals[i] = "'" + strings.ReplaceAll(string(encoded), "'", "''") + "'"
		}
	}
	var raw []byte
	options := "ANALYZE, BUFFERS, VERBOSE, FORMAT JSON"
	if len(analyze) > 0 && !analyze[0] {
		options = "VERBOSE, FORMAT JSON"
	}
	if err := conn.QueryRow(ctx, "EXPLAIN ("+options+") EXECUTE audit_flow_scale_plan("+strings.Join(literals, ",")+")").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var result []map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	t.Logf("plan=%s", raw)
	return result[0]["Plan"].(map[string]any)
}

func walkAuditPlan(node map[string]any, visit func(map[string]any)) {
	visit(node)
	children, _ := node["Plans"].([]any)
	for _, child := range children {
		walkAuditPlan(child.(map[string]any), visit)
	}
}

func checkAuditPageMaterialization(t *testing.T, plan map[string]any, limit float64) {
	t.Helper()
	walkAuditPlan(plan, func(node map[string]any) {
		if node["Relation Name"] != nil && node["Subplan Name"] == nil {
			return
		}
		rows, _ := node["Actual Rows"].(float64)
		loops, _ := node["Actual Loops"].(float64)
		if output, ok := node["Output"].([]any); ok {
			for _, value := range output {
				if text, ok := value.(string); ok && (text == "data" || strings.HasSuffix(text, ".data")) && rows*loops > limit {
					t.Errorf("materialized complete events %v rows=%.0f > %.0f", node["Node Type"], rows*loops, limit)
				}
			}
		}
	})
}
