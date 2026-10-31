package audit_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	userdb "github.com/bbxx111/accountkit/internal/userdb"
	"github.com/google/uuid"
)

// A sparse historical closure must not re-scan the full event history for every
// unrelated flow. The generous statement deadline detects that quadratic path;
// this is not a latency SLA or a microbenchmark.
func TestAuditHistoricalCleanupAtScale(t *testing.T) {
	p := testPool(t)
	ctx := context.Background()
	_, err := p.Exec(ctx, `INSERT INTO audit_event(id,source,action,actor,targets,context,occur_time)
 SELECT ('01900000-0000-7000-8000-'||lpad(i::text,12,'0'))::uuid,
 'test/source','BUSINESS_EVENT',
 CASE WHEN i=1 THEN jsonb_build_object('type','USER','namespace','test/ns','id','u_0000000000001','name','private')
 WHEN i<=6 THEN jsonb_build_object('type','ANONYMOUS','name','private')
 ELSE jsonb_build_object('type','USER','namespace','test/ns','id','u_0000000000002','name','private') END,
 jsonb_build_array(jsonb_build_object('type','IDENTITY_FLOW','namespace','test/ns','id','f_'||lpad((CASE WHEN i IN (2,4,6) THEN i-1 ELSE i END)::text,13,'0'),'name','private'))
 || CASE WHEN i=3 THEN jsonb_build_array(jsonb_build_object('type','USER','namespace','test/ns','id','u_0000000000001')) ELSE '[]'::jsonb END,
 jsonb_build_object('client_ip','203.0.113.1','related_resources',CASE WHEN i=5 THEN jsonb_build_array(jsonb_build_object('type','USER','namespace','test/ns','id','u_0000000000001')) ELSE '[]'::jsonb END),now()
 FROM generate_series(1,20000) i;
 ANALYZE audit_event;`)
	if err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"lock", "scrub"} {
		t.Run(operation, func(t *testing.T) {
			tx, err := p.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			if _, err := tx.Exec(ctx, `SET LOCAL statement_timeout='5s'`); err != nil {
				t.Fatal(err)
			}
			q := userdb.New(tx)
			start := time.Now()
			if operation == "lock" {
				rows, err := q.LockAuditEventsForUserCleanup(ctx, userdb.LockAuditEventsForUserCleanupParams{Source: "test/source", Namespace: "test/ns", UserID: privacyA, IncludeUser: true})
				if err != nil || len(rows) != 6 {
					t.Fatalf("sparse historical closure lock: rows=%d error=%v", len(rows), err)
				}
				for i, id := range rows {
					if id != uuid.MustParse(fmt.Sprintf("01900000-0000-7000-8000-%012d", i+1)) {
						t.Fatalf("closure or UUID order changed: %v", rows)
					}
				}
			} else {
				n, err := q.ScrubAuditEventsByUser(ctx, userdb.ScrubAuditEventsByUserParams{Source: "test/source", Namespace: "test/ns", UserID: privacyA, AuthenticationActions: []string{}})
				if err != nil || n != 6 {
					t.Fatalf("sparse historical closure scrub: rows=%d error=%v", n, err)
				}
				var clean int
				if err := tx.QueryRow(ctx, `SELECT count(*) FROM audit_event WHERE NOT actor ? 'name' AND NOT context ? 'client_ip'`).Scan(&clean); err != nil || clean != 6 {
					t.Fatalf("privacy cleanup scope: rows=%d error=%v", clean, err)
				}
			}
			t.Logf("20,000 events; six-row historical closure %s: %s", operation, time.Since(start))
		})
	}
}
