package user_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
)

type profileAttributesUpperResult struct {
	view    user.ProfileAttributesView
	err     error
	elapsed time.Duration
}

func upperBoundProfileAttributesFixture(t *testing.T) (*fixture, string) {
	t.Helper()
	f := newFixture(t)
	newServiceWith(t, f, func(d *user.Deps) {
		d.ProfileAttributes = &user.ProfileAttributesConfig{SchemaVersion: "upper-v1", Schema: []byte(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"payload":{"type":"string"}},"additionalProperties":false}`)}
	})
	return f, f.signIn(t, enum.IdentityPhone, phone1, dev1).UserID
}

func upperBoundProfileAttributesDocument(ch string) []byte {
	return []byte(`{"payload":"` + strings.Repeat(ch, 16384-len(`{"payload":""}`)) + `"}`)
}

func startProfileAttributesUpperWrite(ctx context.Context, f *fixture, id string, document []byte) <-chan profileAttributesUpperResult {
	done := make(chan profileAttributesUpperResult, 1)
	go func() {
		started := time.Now()
		v, err := f.svc.PatchProfileAttributes(ctx, id, user.ProfileAttributesPatch{Document: document}, meta1)
		done <- profileAttributesUpperResult{view: v, err: err, elapsed: time.Since(started)}
	}()
	return done
}

func finishProfileAttributesUpper(t *testing.T, ctx context.Context, done <-chan profileAttributesUpperResult) profileAttributesUpperResult {
	t.Helper()
	select {
	case result := <-done:
		return result
	case <-ctx.Done():
		t.Fatal("upper-bound write did not finish:", ctx.Err())
		return profileAttributesUpperResult{}
	}
}

// PostgreSQL can queue a later row waiter behind either the lock holder or an
// earlier waiter. Follow the actual blocking graph, rather than assuming a PID
// order or using a sleep to guess whether both writes have reached the lock.
func waitProfileAttributesUpperBlocked(t *testing.T, ctx context.Context, f *fixture, root uint32, want int, operations ...<-chan profileAttributesUpperResult) {
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

func TestProfileAttributesUpperBoundConcurrency(t *testing.T) {
	t.Run("same account serialized replacement", func(t *testing.T) {
		f, id := upperBoundProfileAttributesFixture(t)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		lock := identityRaceLock(t, ctx, f, id, "")
		a := startProfileAttributesUpperWrite(ctx, f, id, upperBoundProfileAttributesDocument("a"))
		b := startProfileAttributesUpperWrite(ctx, f, id, upperBoundProfileAttributesDocument("b"))
		waitProfileAttributesUpperBlocked(t, ctx, f, lock.Conn().PgConn().PID(), 2, a, b)
		if err := lock.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		ra, rb := finishProfileAttributesUpper(t, ctx, a), finishProfileAttributesUpper(t, ctx, b)
		if ra.err != nil || rb.err != nil {
			t.Fatalf("serialized writes: %v / %v", ra.err, rb.err)
		}
		got, err := f.svc.GetProfileAttributes(ctx, id)
		if err != nil || len(got.Data) != 16384 || (!bytes.Equal(got.Data, ra.view.Data) && !bytes.Equal(got.Data, rb.view.Data)) {
			t.Fatalf("upper-bound serialized result not preserved: bytes=%d err=%v", len(got.Data), err)
		}
		t.Logf("same-account, two observed lock waiters: a=%s b=%s; both successful; bytes=%d", ra.elapsed, rb.elapsed, len(got.Data))
	})
	t.Run("different account does not wait on held account", func(t *testing.T) {
		f, id := upperBoundProfileAttributesFixture(t)
		other := f.signIn(t, enum.IdentityPhone, phone2, dev1).UserID
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		lock := identityRaceLock(t, ctx, f, id, "")
		a := startProfileAttributesUpperWrite(ctx, f, id, upperBoundProfileAttributesDocument("a"))
		waitProfileAttributesUpperBlocked(t, ctx, f, lock.Conn().PgConn().PID(), 1, a)
		b := startProfileAttributesUpperWrite(ctx, f, other, upperBoundProfileAttributesDocument("b"))
		rb := finishProfileAttributesUpper(t, ctx, b)
		if rb.err != nil || len(rb.view.Data) != 16384 {
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
		ra := finishProfileAttributesUpper(t, ctx, a)
		if ra.err != nil || len(ra.view.Data) != 16384 {
			t.Fatal("held account did not complete after release:", ra.err)
		}
		for _, uid := range []string{id, other} {
			got, err := f.svc.GetProfileAttributes(ctx, uid)
			if err != nil || len(got.Data) != 16384 {
				t.Fatal("independent write lost:", err)
			}
		}
		t.Logf("different-account: b=%s completed before releasing a; a=%s; both bytes=16384 initialized metadata", rb.elapsed, ra.elapsed)
	})
}
