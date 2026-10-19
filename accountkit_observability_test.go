package accountkit_test

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/internal/obstest"
	ob "github.com/bbxx111/accountkit/observability"
	"github.com/bbxx111/accountkit/user/sender"
)

type closeAwareObserver struct {
	obstest.Recorder
	closed atomic.Int32
}

func (o *closeAwareObserver) Close() error                   { o.closed.Add(1); return nil }
func (o *closeAwareObserver) Shutdown(context.Context) error { o.closed.Add(1); return nil }
func (o *closeAwareObserver) Unregister() bool               { o.closed.Add(1); return true }

func TestObserverLifecycle(t *testing.T) {
	var typedNil *closeAwareObserver
	for _, observer := range []ob.Observer{nil, typedNil, &closeAwareObserver{}} {
		deps := baseDeps(t)
		deps.Observer = observer
		a, err := accountkit.New(minimal(), deps) // lazy pool proves construction does no I/O
		if err != nil {
			t.Fatal(err)
		}
		a.Close()
		a.Close()
		if live, ok := observer.(*closeAwareObserver); ok && live != nil && live.closed.Load() != 0 {
			t.Fatal("accountkit closed host observer")
		}
	}
}

func TestObserverSharedAcrossRootMaintenance(t *testing.T) {
	ctx := context.Background()
	dsn := dbDSN(t)
	shared := &closeAwareObserver{}
	for instance := 0; instance < 2; instance++ {
		schema := fmt.Sprintf("metricsroot_%08x", rand.Uint32())
		pc, err := accountkit.PoolConfig(dsn, schema)
		if err != nil {
			t.Fatal(err)
		}
		pool, err := pgxpool.NewWithConfig(ctx, pc)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE"); pool.Close() })
		cfg := minimal()
		cfg.Schema = schema
		a, err := accountkit.New(cfg, accountkit.Deps{Pool: pool, Redis: testRedis(t), SMSSender: sender.NewLog(nil), EmailSender: sender.NewLog(nil), Observer: shared})
		if err != nil {
			t.Fatal(err)
		}
		if err := a.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{"019a0000-0000-7000-8000-000000000001", "019a0000-0000-7000-8000-000000000002"} {
			if _, err := pool.Exec(ctx, `INSERT INTO audit_event(id,source,action,actor,occur_time) VALUES($1,$2,'ORDER_APPROVED','{"type":"ANONYMOUS"}','2024-01-01')`, id, a.Config().AuditSource); err != nil {
				t.Fatal(err)
			}
		}
		start := len(shared.Snapshot())
		if !a.RunMaintenanceOnce(ctx) {
			t.Fatal("maintenance lock not acquired")
		}
		var rounds, runs, items int
		for _, event := range shared.Snapshot()[start:] {
			m, ok := event.(ob.MaintenanceObservation)
			if !ok {
				continue
			}
			switch m.Kind {
			case ob.MaintenanceRound:
				rounds++
			case ob.MaintenanceRun:
				runs++
			case ob.MaintenanceItems:
				items++
			}
		}
		if rounds != 1 || runs != 6 || items != 1 {
			t.Fatalf("instance %d: round=%d runs=%d items=%d", instance, rounds, runs, items)
		}
		for _, event := range shared.Snapshot()[start:] {
			if m, ok := event.(ob.MaintenanceObservation); ok && m.Kind == ob.MaintenanceItems && (m.Task != ob.TaskAuditRetention || m.Count != 2) {
				t.Fatalf("audit retention counted incorrectly: %+v", m)
			}
		}
		start = len(shared.Snapshot())
		if _, err := a.Users().CleanupSessions(ctx); err != nil {
			t.Fatal(err)
		}
		if got := shared.Snapshot()[start:]; len(got) != 1 || got[0].(ob.MaintenanceObservation).Kind != ob.MaintenanceRun {
			t.Fatalf("direct maintenance terminal: %+v", got)
		}
		a.Close()
	}
	if shared.closed.Load() != 0 {
		t.Fatal("accountkit closed shared host observer")
	}
}
