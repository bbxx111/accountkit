package user_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
)

type extensionsUpperResult struct {
	view    user.ExtensionsView
	err     error
	elapsed time.Duration
}

func upperBoundExtensionsFixture(t *testing.T) (*fixture, string) {
	t.Helper()
	f := newFixture(t)
	newServiceWith(t, f, func(d *user.Deps) {
		d.UserExtensions = &user.ExtensionsConfig{SchemaVersion: "upper-v1", Schema: []byte(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"payload":{"type":"string"}},"additionalProperties":false}`)}
	})
	return f, f.signIn(t, enum.IdentityPhone, phone1, dev1).UserID
}

func upperBoundExtensionsDocument(ch string) []byte {
	return []byte(`{"payload":"` + strings.Repeat(ch, 16384-len(`{"payload":""}`)) + `"}`)
}

func startExtensionsUpperWrite(ctx context.Context, f *fixture, id string, document []byte) <-chan extensionsUpperResult {
	done := make(chan extensionsUpperResult, 1)
	go func() {
		started := time.Now()
		z := int64(0)
		v, err := f.svc.PatchExtensions(ctx, id, user.ExtensionsPatch{Document: document, ExpectedRevision: &z}, meta1)
		done <- extensionsUpperResult{view: v, err: err, elapsed: time.Since(started)}
	}()
	return done
}

func finishExtensionsUpper(t *testing.T, ctx context.Context, done <-chan extensionsUpperResult) extensionsUpperResult {
	t.Helper()
	select {
	case result := <-done:
		return result
	case <-ctx.Done():
		t.Fatal("upper-bound write did not finish:", ctx.Err())
		return extensionsUpperResult{}
	}
}

// PostgreSQL can queue a later row waiter behind either the lock holder or an
// earlier waiter. Follow the actual blocking graph, rather than assuming a PID
// order or using a sleep to guess whether both writes have reached the lock.
func waitExtensionsUpperBlocked(t *testing.T, ctx context.Context, f *fixture, root uint32, want int, operations ...<-chan extensionsUpperResult) {
	t.Helper()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		var count int
		if err := f.pool.QueryRow(ctx, `WITH RECURSIVE blocked(pid) AS (
 SELECT pid FROM pg_stat_activity WHERE $1::int=ANY(pg_blocking_pids(pid))
 UNION SELECT a.pid FROM pg_stat_activity a JOIN blocked b ON b.pid=ANY(pg_blocking_pids(a.pid))
) SELECT count(*) FROM blocked`, root).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count >= want {
			return
		}
		for _, done := range operations {
			select {
			case r := <-done:
				t.Fatalf("write finished before observed lock wait: %v", r.err)
			default:
			}
		}
		select {
		case <-ctx.Done():
			t.Fatal("upper-bound lock queue not observed:", ctx.Err())
		case <-tick.C:
		}
	}
}

func TestUserExtensionsUpperBoundConcurrency(t *testing.T) {
	t.Run("same account revision conflict", func(t *testing.T) {
		f, id := upperBoundExtensionsFixture(t)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		lock := identityRaceLock(t, ctx, f, id, "")
		a := startExtensionsUpperWrite(ctx, f, id, upperBoundExtensionsDocument("a"))
		b := startExtensionsUpperWrite(ctx, f, id, upperBoundExtensionsDocument("b"))
		waitExtensionsUpperBlocked(t, ctx, f, lock.Conn().PgConn().PID(), 2, a, b)
		if err := lock.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		ra, rb := finishExtensionsUpper(t, ctx, a), finishExtensionsUpper(t, ctx, b)
		winner := ra
		if ra.err != nil {
			winner = rb
		}
		if !((ra.err == nil && errors.Is(rb.err, user.ErrExtensionsConflict)) || (rb.err == nil && errors.Is(ra.err, user.ErrExtensionsConflict))) {
			t.Fatalf("expected one success and one conflict: %v / %v", ra.err, rb.err)
		}
		got, err := f.svc.GetExtensions(ctx, id)
		if err != nil || got.Revision != 1 || len(got.Data) != 16384 || !bytes.Equal(got.Data, winner.view.Data) {
			t.Fatalf("upper-bound winner/revision not preserved: revision=%d bytes=%d err=%v", got.Revision, len(got.Data), err)
		}
		t.Logf("same-account, two observed lock waiters: a=%s b=%s; one success, one revision conflict; bytes=%d revision=%d", ra.elapsed, rb.elapsed, len(got.Data), got.Revision)
	})
	t.Run("different account does not wait on held account", func(t *testing.T) {
		f, id := upperBoundExtensionsFixture(t)
		other := f.signIn(t, enum.IdentityPhone, phone2, dev1).UserID
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		lock := identityRaceLock(t, ctx, f, id, "")
		a := startExtensionsUpperWrite(ctx, f, id, upperBoundExtensionsDocument("a"))
		waitExtensionsUpperBlocked(t, ctx, f, lock.Conn().PgConn().PID(), 1, a)
		b := startExtensionsUpperWrite(ctx, f, other, upperBoundExtensionsDocument("b"))
		rb := finishExtensionsUpper(t, ctx, b)
		if rb.err != nil || rb.view.Revision != 1 || len(rb.view.Data) != 16384 {
			t.Fatalf("other account failed while first remained locked: %v", rb.err)
		}
		select {
		case ra := <-a:
			t.Fatalf("held account completed before lock release: %v", ra.err)
		default:
		}
		if err := lock.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		ra := finishExtensionsUpper(t, ctx, a)
		if ra.err != nil || ra.view.Revision != 1 || len(ra.view.Data) != 16384 {
			t.Fatal("held account did not complete after release:", ra.err)
		}
		for _, uid := range []string{id, other} {
			got, err := f.svc.GetExtensions(ctx, uid)
			if err != nil || got.Revision != 1 || len(got.Data) != 16384 {
				t.Fatal("independent write lost:", err)
			}
		}
		t.Logf("different-account: b=%s completed before releasing a; a=%s; both bytes=16384 revision=1", rb.elapsed, ra.elapsed)
	})
}
