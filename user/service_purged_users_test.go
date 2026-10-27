package user_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/anonymize"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
)

// deletedUsers 建立 n 个已申请注销的账号，并把时钟推过冷静期。
func deletedUsers(t *testing.T, f *fixture, n int) []string {
	t.Helper()
	ctx := context.Background()
	ids := make([]string, 0, n)
	for i := range n {
		res := f.signIn(t, enum.IdentityPhone, fmt.Sprintf("+861380000%04d", i), dev1)
		p := principalOf(t, f, res)
		if _, err := f.svc.DeleteMe(ctx, p, meta1); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, p.UserID)
	}
	f.advance(360*time.Hour + time.Second)
	return ids
}

func TestPurgeWritesPurgedUserRecord(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	ids := deletedUsers(t, f, 2)
	if got, next, err := f.svc.ListPurgedUsers(ctx, 0, 10); err != nil || len(got) != 0 || next != 0 {
		t.Fatalf("before purge: %+v %d %v", got, next, err)
	}
	if n, err := f.svc.PurgeDueUsers(ctx); err != nil || n != 2 {
		t.Fatalf("purge: %d %v", n, err)
	}
	page, next, err := f.svc.ListPurgedUsers(ctx, 0, 10)
	if err != nil || len(page) != 2 || next == 0 {
		t.Fatalf("after purge: %+v %d %v", page, next, err)
	}
	seen := map[string]bool{}
	for _, p := range page {
		seen[p.UserID] = true
		if !p.PurgeTime.Equal(*f.clock) {
			t.Fatalf("purge_time %v is not the purge execution time %v", p.PurgeTime, *f.clock)
		}
	}
	for _, id := range ids {
		if !seen[id] {
			t.Fatalf("missing purged user %s in %+v", id, page)
		}
	}
	// 记录只含 user_id 与时间；再次 purge 不重复写入。
	if n, err := f.svc.PurgeDueUsers(ctx); err != nil || n != 0 {
		t.Fatalf("repeat purge: %d %v", n, err)
	}
	var count int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM purged_user`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("records = %d %v", count, err)
	}
}

func TestPurgedUserRecordRollsBackWithAnonymizer(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	mustExec(t, f, `CREATE TABLE host_note (id SERIAL PRIMARY KEY, user_id TEXT NOT NULL, body TEXT)`)
	failing := &recordingAnonymizer{name: "host_note", fail: errors.New("host failure")}
	newServiceWith(t, f, func(d *user.Deps) { d.Anonymizers = []anonymize.Anonymizer{failing} })
	deletedUsers(t, f, 1)
	if _, err := f.svc.PurgeDueUsers(ctx); err == nil {
		t.Fatal("anonymizer failure must fail the purge")
	}
	if page, _, err := f.svc.ListPurgedUsers(ctx, 0, 10); err != nil || len(page) != 0 {
		t.Fatalf("failed purge left a record: %+v %v", page, err)
	}
	failing.fail = nil
	if n, err := f.svc.PurgeDueUsers(ctx); err != nil || n != 1 {
		t.Fatalf("retry: %d %v", n, err)
	}
	if page, _, err := f.svc.ListPurgedUsers(ctx, 0, 10); err != nil || len(page) != 1 {
		t.Fatalf("retry record: %+v %v", page, err)
	}
}

func TestListPurgedUsersPaging(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	ids := deletedUsers(t, f, 5)
	if _, err := f.svc.PurgeDueUsers(ctx); err != nil {
		t.Fatal(err)
	}
	var all []user.PurgedUser
	var cursor int64
	for {
		page, next, err := f.svc.ListPurgedUsers(ctx, cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			if next != cursor {
				t.Fatalf("caught-up cursor moved: %d -> %d", cursor, next)
			}
			break
		}
		if len(page) > 2 || next <= cursor {
			t.Fatalf("page size %d, cursor %d -> %d", len(page), cursor, next)
		}
		all = append(all, page...)
		cursor = next
	}
	if len(all) != len(ids) {
		t.Fatalf("paged %d records, want %d", len(all), len(ids))
	}
	// 同一位置重复读取结果相同。
	again, next, err := f.svc.ListPurgedUsers(ctx, 0, 100)
	if err != nil || len(again) != len(ids) || next != cursor {
		t.Fatalf("re-read: %d %d %v", len(again), next, err)
	}
	for _, bad := range []int{0, -1, user.MaxPurgedUserPage + 1} {
		if _, _, err := f.svc.ListPurgedUsers(ctx, 0, bad); !errors.Is(err, user.ErrInvalidArgument) {
			t.Fatalf("limit %d: %v", bad, err)
		}
	}
	if _, _, err := f.svc.ListPurgedUsers(ctx, -1, 10); !errors.Is(err, user.ErrInvalidArgument) {
		t.Fatalf("negative cursor: %v", err)
	}
}

// 并发 purge 与持续拉取：序号顺序与提交顺序一致，拉取方最终读到全部记录、不漏读。
func TestPurgedUserFeedConcurrentPurgeNoGaps(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	ids := deletedUsers(t, f, 12)
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() { _, _ = f.svc.PurgeDueUsers(ctx) })
	}
	seen := map[string]int64{}
	var cursor int64
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	poll := func() {
		for {
			page, next, err := f.svc.ListPurgedUsers(ctx, cursor, 3)
			if err != nil {
				t.Error(err)
				return
			}
			if len(page) == 0 {
				return
			}
			for _, p := range page {
				if _, dup := seen[p.UserID]; dup {
					t.Errorf("user %s returned twice", p.UserID)
				}
				seen[p.UserID] = next
			}
			cursor = next
		}
	}
	for running := true; running; {
		select {
		case <-done:
			running = false
		default:
			poll()
		}
	}
	poll()
	if len(seen) != len(ids) {
		t.Fatalf("feed returned %d of %d purged users", len(seen), len(ids))
	}
	// 每个账号恰好一条记录。
	var count int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM purged_user`).Scan(&count); err != nil || count != len(ids) {
		t.Fatalf("records = %d %v", count, err)
	}
}
