package audit_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/audit"
	"github.com/jackc/pgx/v5"
)

// Observe completed real snapshots, then let a separate committed writer expand
// ownership before Store takes its locks. No query results or errors are mocked.
type privacySnapshotBarrier struct {
	reached chan struct{}
	resume  chan struct{}
	count   atomic.Int32
}

type privacySnapshotKey struct{}

func (b *privacySnapshotBarrier) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, privacySnapshotKey{}, strings.HasPrefix(d.SQL, "-- name: SnapshotAuditFlows"))
}

func (b *privacySnapshotBarrier) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryEndData) {
	if snapshot, _ := ctx.Value(privacySnapshotKey{}).(bool); !snapshot || d.Err != nil {
		return
	}
	if b.count.Add(1) > 4 {
		return
	}
	select {
	case b.reached <- struct{}{}:
	case <-ctx.Done():
		return
	}
	select {
	case <-b.resume:
	case <-ctx.Done():
	}
}

func TestAuditOwnershipExpansionExhaustsRetries(t *testing.T) {
	for _, mode := range []string{"live_owner", "historical_reference"} {
		t.Run(mode, func(t *testing.T) {
			p := testPool(t)
			privacyUsers(t, p)
			privacyFlowRow(t, p, []string{})
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			barrier := &privacySnapshotBarrier{reached: make(chan struct{}), resume: make(chan struct{})}
			cfg := p.Config()
			cfg.ConnConfig.Tracer = barrier
			store, pid := privacyWorkerConfig(t, cfg)
			batch := []audit.Event{historicalGeneric(t, "", privacyFlow), privateEvent(t, privacyA, "")}
			type outcome struct {
				result audit.BatchResult
				err    error
			}
			done := make(chan outcome, 1)
			go func() { r, err := store.InsertBatch(ctx, batch); done <- outcome{r, err} }()
			transactions := map[string]bool{}
			for attempt := 0; attempt < 4; attempt++ {
				select {
				case <-barrier.reached:
				case result := <-done:
					t.Fatalf("stopped before expansion %d: %+v", attempt+1, result)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				var transaction string
				if err := p.QueryRow(ctx, `SELECT virtualtransaction FROM pg_locks WHERE pid=$1 AND locktype='virtualxid'`, pid).Scan(&transaction); err != nil {
					t.Fatal(err)
				}
				if transactions[transaction] {
					t.Fatal("ownership expansion reused the previous transaction")
				}
				transactions[transaction] = true
				owner := fmt.Sprintf("u_%013d", attempt+3)
				if _, err := p.Exec(ctx, `INSERT INTO user_account(id,state) VALUES ($1,1)`, owner); err != nil {
					t.Fatal(err)
				}
				if mode == "live_owner" {
					change := privacyLock(t, p, `SELECT id FROM user_account WHERE id=$1 FOR UPDATE`, owner)
					if _, err := change.Exec(ctx, `UPDATE identity_flow SET proof_user_ids=array_append(proof_user_ids,$2::text) WHERE id=$1`, privacyFlow, owner); err != nil {
						t.Fatal(err)
					}
					if err := change.Commit(ctx); err != nil {
						t.Fatal(err)
					}
				} else {
					if r, err := testStore(t, p).InsertBatch(ctx, []audit.Event{historicalGeneric(t, owner, privacyFlow)}); err != nil || r.Written != 1 {
						t.Fatalf("commit historical expansion: %+v %v", r, err)
					}
				}
				select {
				case barrier.resume <- struct{}{}:
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			select {
			case out := <-done:
				if out.err == nil || errors.Is(out.err, context.DeadlineExceeded) || out.result != (audit.BatchResult{Failed: 2}) {
					t.Fatalf("retry exhaustion must fail the entire batch: %+v %v", out.result, out.err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if got := barrier.count.Load(); got != 4 {
				t.Fatalf("snapshot attempts = %d, want four", got)
			}
			var rows, locks int
			if err := p.QueryRow(ctx, `SELECT count(*) FROM audit_event WHERE id=$1 OR id=$2`, batch[0].ID, batch[1].ID).Scan(&rows); err != nil || rows != 0 {
				t.Fatalf("failed batch persisted rows: %d %v", rows, err)
			}
			if err := p.QueryRow(ctx, `SELECT count(*) FROM pg_locks WHERE pid=$1 AND locktype IN ('virtualxid','transactionid','advisory')`, pid).Scan(&locks); err != nil || locks != 0 {
				t.Fatalf("failed batch retained transaction locks: %d %v", locks, err)
			}
			if r, err := store.InsertBatch(ctx, batch); err != nil || r.Written != 2 {
				t.Fatalf("stable ownership must allow a subsequent batch: %+v %v", r, err)
			}
		})
	}
}
