package admin_test

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAuditReaderCannotMutateQueryBounds(t *testing.T) {
	rows := []audit.RecordView{auditRecordAt(t, 1, testNow), auditRecordAt(t, 2, testNow), auditRecordAt(t, 3, testNow)}
	reader := &fakeAudit{listByResource: func(_ context.Context, q audit.Query) (audit.Page, error) {
		if len(q.Actions) == 0 {
			t.Error("missing action bounds")
		} else {
			q.Actions[0] = "ORDER_APPROVED"
		}
		remaining := rows
		if q.After != nil {
			if q.After.ID != rows[0].ID {
				t.Errorf("mutated cursor reached reader: %+v", q.After)
			}
			remaining = rows[1:]
			q.After.Time = testNow.Add(time.Hour)
		}
		return audit.Page{Records: slices.Clone(remaining[:2])}, nil
	}}
	h := auditHandler(t, reader, nil)
	rec := do(t, h, call{method: "GET", path: "/users/" + uid + "/auditEvents?page_size=1", headers: operator})
	if rec.Code != 200 {
		t.Fatalf("first page: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Token string `json:"next_page_token"`
	}
	decode(t, rec, &out)
	if out.Token == "" {
		t.Fatal("missing cursor")
	}
	rec = do(t, h, call{method: "GET", path: "/users/" + uid + "/auditEvents?page_size=1&page_token=" + out.Token, headers: operator})
	if rec.Code != 200 {
		t.Fatalf("reader mutation changed trusted bounds: %d %s", rec.Code, rec.Body)
	}
}

func TestAuditReaderReceivesActionFilter(t *testing.T) {
	called := false
	reader := &fakeAudit{listByResource: func(_ context.Context, q audit.Query) (audit.Page, error) {
		called = true
		seen := map[string]bool{}
		for _, action := range q.Actions {
			if _, err := enum.ParseEventType(action); err != nil || seen[action] {
				t.Errorf("unknown/duplicate action %q", action)
			}
			seen[action] = true
		}
		for typ := enum.EventCodeSent; typ.Valid(); typ++ {
			if !seen[typ.String()] {
				t.Errorf("missing filter for %s", typ)
			}
		}
		return audit.Page{}, nil
	}}
	rec := do(t, auditHandler(t, reader, nil), call{method: "GET", path: "/users/" + uid + "/auditEvents", headers: operator})
	if rec.Code != 200 || !called {
		t.Fatalf("reader call: %d %s", rec.Code, rec.Body)
	}
}

func mixedAuditStore(t *testing.T) *audit.Store {
	t.Helper()
	dsn := os.Getenv("SERVER_TEST_DB_DSN")
	if dsn == "" {
		t.Skip("SERVER_TEST_DB_DSN not set")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("auditmixed_%08x", rand.Uint32())
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	if err := migrations.Up(context.Background(), cfg.ConnConfig, schema); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); pool.Close() })
	s, err := audit.NewStoreWithOptions(pool, audit.StoreOptions{Source: "test/source", Namespace: "test/ns"})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// A generic business action must not occupy an authentication page or its lookahead.
func TestAuditLocalMixedActionPagination(t *testing.T) {
	for _, sameTime := range []bool{false, true} {
		t.Run(fmt.Sprint("same_time=", sameTime), func(t *testing.T) {
			s := mixedAuditStore(t)
			var events []audit.Event
			for i := 1; i <= 7; i++ {
				at := testNow
				if !sameTime {
					at = at.Add(time.Duration(i) * time.Second)
				}
				e := auditRecordAt(t, i, at).Event
				if i%2 == 1 {
					e.Action = "ORDER_APPROVED"
				}
				events = append(events, e)
			}
			// Deliberately insert in descending order; ORDER BY must break UUID ties.
			for i := len(events) - 1; i >= 0; i-- {
				if r, err := s.InsertBatch(context.Background(), events[i:i+1]); err != nil || r.Written != 1 {
					t.Fatalf("insert: %+v %v", r, err)
				}
			}
			q := audit.Query{Source: "test/source", Resource: audit.Resource{Type: "USER", Namespace: "test/ns", ID: uid}, Limit: 100}
			p, err := s.ListByResource(context.Background(), q)
			if err != nil || len(p.Records) != 7 || p.Records[0].Action != "ORDER_APPROVED" {
				t.Fatalf("generic read: %+v %v", p, err)
			}
			h := auditHandler(t, s, nil)
			token := ""
			for page, index := range []int{1, 3, 5} {
				rec := do(t, h, call{method: "GET", path: "/users/" + uid + "/auditEvents?page_size=1&page_token=" + token, headers: operator})
				if rec.Code != 200 {
					t.Fatalf("page %d: %d %s", page, rec.Code, rec.Body)
				}
				var out struct {
					Events []struct {
						Name string `json:"name"`
					} `json:"audit_events"`
					Token string `json:"next_page_token"`
				}
				decode(t, rec, &out)
				if len(out.Events) != 1 || out.Events[0].Name != "users/"+uid+"/auditEvents/"+events[index].ID.String() {
					t.Fatalf("page %d missing/duplicate: %+v", page, out)
				}
				if (page < 2) != (out.Token != "") {
					t.Fatalf("premature/extra page: %+v", out)
				}
				token = out.Token
			}
		})
	}
}

func TestAuditLocalBusinessOnlyEmptyPage(t *testing.T) {
	s := mixedAuditStore(t)
	e := auditRecordAt(t, 1, testNow).Event
	e.Action = "ORDER_APPROVED"
	if r, err := s.InsertBatch(context.Background(), []audit.Event{e}); err != nil || r.Written != 1 {
		t.Fatalf("insert: %+v %v", r, err)
	}
	rec := do(t, auditHandler(t, s, nil), call{method: "GET", path: "/users/" + uid + "/auditEvents?page_size=1", headers: operator})
	if rec.Code != 200 {
		t.Fatalf("business-only: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Events []any  `json:"audit_events"`
		Token  string `json:"next_page_token"`
	}
	decode(t, rec, &out)
	if out.Events == nil || len(out.Events) != 0 || out.Token != "" {
		t.Fatalf("expected empty page: %+v", out)
	}
}
