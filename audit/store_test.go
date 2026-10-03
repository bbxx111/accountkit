package audit_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/ids"
	"github.com/bbxx111/accountkit/migrations"
)

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

func TestStoreInsertBatchMapsFieldsAndNulls(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	s := audit.NewStore(pool)
	now := time.Now().Truncate(time.Microsecond)
	full := audit.Event{
		Type: enum.EventSignIn, Actor: enum.ActorUser, Result: enum.ResultSuccess, Reason: "NEW_USER",
		UserID: "u_0000000000001", SessionID: "s_0000000000001", IdentityKind: enum.IdentityPhone, SubjectHint: "abcdefgh",
		IP: "203.0.113.5", DeviceID: "dev-1", RequestID: "req-1",
		AdminIssuer: "https://kc/realms/x", AdminSubject: "admin-sub", AdminUsername: "ops",
		OccurTime: now,
	}
	sparse := audit.Event{Type: enum.EventUserPurged, Actor: enum.ActorSystem, Result: enum.ResultSuccess, UserID: "u_0000000000001", OccurTime: now.Add(time.Second)}
	badIP := audit.Event{Type: enum.EventCodeSent, Actor: enum.ActorUser, Result: enum.ResultSuccess, IP: "not-an-ip", OccurTime: now.Add(2 * time.Second)}
	failed, err := s.InsertBatch(ctx, []audit.Event{full, sparse, badIP})
	if err != nil || failed != 0 {
		t.Fatalf("insert: failed=%d err=%v", failed, err)
	}
	var (
		count                                                              int
		id, reason, userID, sessionID, hint, deviceID, requestID, adminSub string
		kind                                                               *int16
		ip                                                                 *string
	)
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_event`).Scan(&count); err != nil || count != 3 {
		t.Fatalf("rows: %d %v", count, err)
	}
	if err := pool.QueryRow(ctx, `SELECT id, reason, user_id, session_id, identity_kind, subject_hint, host(ip), device_id, request_id, admin_subject FROM audit_event WHERE event_type = $1`, int16(enum.EventSignIn)).
		Scan(&id, &reason, &userID, &sessionID, &kind, &hint, &ip, &deviceID, &requestID, &adminSub); err != nil {
		t.Fatal(err)
	}
	if len(id) != 15 || id[:2] != "e_" || reason != "NEW_USER" || userID != full.UserID || sessionID != full.SessionID || kind == nil || *kind != int16(enum.IdentityPhone) || hint != "abcdefgh" || ip == nil || *ip != "203.0.113.5" || deviceID != "dev-1" || requestID != "req-1" || adminSub != "admin-sub" {
		t.Fatalf("full row mapping: id=%q reason=%q user=%q sess=%q kind=%v hint=%q ip=%v dev=%q req=%q admin=%q", id, reason, userID, sessionID, kind, hint, ip, deviceID, requestID, adminSub)
	}
	// 稀疏事件：空串 → NULL，零值 identity_kind → NULL，actor 原样
	var nulls int
	var actor int16
	if err := pool.QueryRow(ctx, `SELECT (session_id IS NULL)::int + (identity_kind IS NULL)::int + (subject_hint IS NULL)::int + (reason IS NULL)::int + (ip IS NULL)::int + (device_id IS NULL)::int + (request_id IS NULL)::int + (admin_issuer IS NULL)::int, actor_kind FROM audit_event WHERE event_type = $1`, int16(enum.EventUserPurged)).Scan(&nulls, &actor); err != nil || nulls != 8 || actor != int16(enum.ActorSystem) {
		t.Fatalf("sparse row: nulls=%d actor=%d err=%v", nulls, actor, err)
	}
	// 非法 IP → NULL，而不是整批失败
	var ipNull bool
	if err := pool.QueryRow(ctx, `SELECT ip IS NULL FROM audit_event WHERE event_type = $1`, int16(enum.EventCodeSent)).Scan(&ipNull); err != nil || !ipNull {
		t.Fatalf("bad ip must be stored as NULL: %v %v", ipNull, err)
	}
	// 空批：no-op
	if failed, err := s.InsertBatch(ctx, nil); err != nil || failed != 0 {
		t.Fatalf("empty batch: %d %v", failed, err)
	}
}

func TestStoreInsertBatchIsAllOrNothing(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	s := audit.NewStore(pool)
	now := time.Now()
	// 最稳定的失败来源是主键冲突：先插一行，再经 InsertBatchWithIDs 注入同一个 id。
	if _, err := pool.Exec(ctx, `INSERT INTO audit_event (id, event_type, actor_kind, result, occur_time) VALUES ('e_0000000000dvp', 1, 1, 1, now())`); err != nil {
		t.Fatal(err)
	}
	failed, err := audit.InsertBatchWithIDs(ctx, s, []string{"e_0000000000dvp", "e_0000000000pk0"}, []audit.Event{
		{Type: enum.EventCodeSent, Actor: enum.ActorUser, Result: enum.ResultSuccess, OccurTime: now},
		{Type: enum.EventCodeSent, Actor: enum.ActorUser, Result: enum.ResultSuccess, OccurTime: now},
	})
	if err == nil || failed != 2 {
		t.Fatalf("a duplicate id fails the whole batch (implicit transaction): failed=%d err=%v", failed, err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_event`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("batch must be all-or-nothing; only the pre-inserted row may exist: count=%d err=%v", count, err)
	}
	// 连接关闭后整批失败：failed == len
	pool.Close()
	failed, err = s.InsertBatch(ctx, []audit.Event{{Type: enum.EventCodeSent, Actor: enum.ActorUser, Result: enum.ResultSuccess, OccurTime: now}, {Type: enum.EventCodeSent, Actor: enum.ActorUser, Result: enum.ResultSuccess, OccurTime: now}})
	if err == nil || failed != 2 {
		t.Fatalf("closed pool: failed=%d err=%v", failed, err)
	}
	if errors.Is(err, context.Canceled) {
		t.Fatal("error must be the pool error, not a ctx error")
	}
}

func TestStoreInsertBatchSurvivesSameMillisecondIDCollisions(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	s := audit.NewStore(pool)
	now := time.Now()
	batchOf := func(n int) []audit.Event {
		es := make([]audit.Event, 0, n)
		for i := 0; i < n; i++ {
			es = append(es, audit.Event{Type: enum.EventCodeSent, Actor: enum.ActorUser, Result: enum.ResultSuccess, OccurTime: now})
		}
		return es
	}
	// 两次独立调用，制造批内碰撞（同一调用生成的 3000 个 id 挤在同一毫秒）与跨批碰撞
	// （两次调用各自生成的 id 撞见同一个值）两种场景；两者都必须被吸收，不能丢批。
	if failed, err := s.InsertBatch(ctx, batchOf(3000)); err != nil || failed != 0 {
		t.Fatalf("first batch of 3000: failed=%d err=%v", failed, err)
	}
	if failed, err := s.InsertBatch(ctx, batchOf(3000)); err != nil || failed != 0 {
		t.Fatalf("second batch of 3000: failed=%d err=%v", failed, err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_event`).Scan(&count); err != nil || count != 6000 {
		t.Fatalf("count: %d %v", count, err)
	}
}

// TestStoreInsertBatchRetriesOncePrimaryKeyConflict 用一个可控的 id 生成器（audit.SetNewIDForTest）
// 确定性地制造跨批 id 碰撞，验证 InsertBatch 的重试语义：碰撞可恢复时重试一次即成功；
// 碰撞持续存在时只重试一次就放弃，返回的错误必须能 errors.As 出 *pgconn.PgError(23505)。
func TestStoreInsertBatchRetriesOncePrimaryKeyConflict(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	s := audit.NewStore(pool)
	const preID = "e_0000000000pk1" // 合法 id：Crockford 字母表剔除 i/l/o/u，p/k/1 都在字母表内
	if _, err := pool.Exec(ctx, `INSERT INTO audit_event (id, event_type, actor_kind, result, occur_time) VALUES ($1, 1, 1, 1, now())`, preID); err != nil {
		t.Fatal(err)
	}
	newEvents := func() []audit.Event {
		now := time.Now()
		return []audit.Event{
			{Type: enum.EventCodeSent, Actor: enum.ActorUser, Result: enum.ResultSuccess, OccurTime: now},
			{Type: enum.EventCodeSent, Actor: enum.ActorUser, Result: enum.ResultSuccess, OccurTime: now},
			{Type: enum.EventCodeSent, Actor: enum.ActorUser, Result: enum.ResultSuccess, OccurTime: now},
		}
	}

	t.Run("gives up after exactly one retry", func(t *testing.T) {
		// 生成器在两次尝试（初次 + 1 次重试，每次 3 个事件）里都让批内第一个 id 撞上
		// 已存在的 preID（批内其余 id 各不相同，避免先触发 uniqueID 自身的批内去重），
		// 模拟两次 InsertBatch 调用之间持续撞见同一个 id 的最坏情况。
		var calls atomic.Int32
		restore := audit.SetNewIDForTest(func(k ids.Kind) (string, error) {
			n := calls.Add(1)
			if n == 1 || n == 4 {
				return preID, nil
			}
			return ids.New(k)
		})
		t.Cleanup(restore)
		failed, err := s.InsertBatch(ctx, newEvents())
		var pgErr *pgconn.PgError
		if failed != 3 || err == nil || !errors.As(err, &pgErr) || pgErr.Code != "23505" {
			t.Fatalf("persistent id collision must retry exactly once then give up with a pg error: failed=%d err=%v", failed, err)
		}
		var count int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_event`).Scan(&count); err != nil || count != 1 {
			t.Fatalf("a failed batch must not partially commit: count=%d err=%v", count, err)
		}
	})

	t.Run("succeeds after one retry", func(t *testing.T) {
		// 生成器只在全局第一次调用时撞见 preID，之后（含本次重试）都委托给真正的
		// ids.New：一次重试即可摆脱冲突，批次整体成功。
		var calls atomic.Int32
		restore := audit.SetNewIDForTest(func(k ids.Kind) (string, error) {
			if calls.Add(1) == 1 {
				return preID, nil
			}
			return ids.New(k)
		})
		t.Cleanup(restore)
		failed, err := s.InsertBatch(ctx, newEvents())
		if failed != 0 || err != nil {
			t.Fatalf("one retry must be enough to escape a single collision: failed=%d err=%v", failed, err)
		}
		var count int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_event`).Scan(&count); err != nil || count != 4 {
			t.Fatalf("count=%d err=%v", count, err)
		}
	})
}

func TestStoreDeleteOlderThanBatches(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	s := audit.NewStore(pool)
	now := time.Now()
	old := make([]audit.Event, 0, audit.RetentionBatchSize+5)
	for i := 0; i < audit.RetentionBatchSize+5; i++ {
		old = append(old, audit.Event{Type: enum.EventCodeSent, Actor: enum.ActorUser, Result: enum.ResultSuccess, OccurTime: now.Add(-200 * 24 * time.Hour)})
	}
	fresh := []audit.Event{{Type: enum.EventCodeSent, Actor: enum.ActorUser, Result: enum.ResultSuccess, OccurTime: now}}
	if failed, err := s.InsertBatch(ctx, append(old, fresh...)); err != nil || failed != 0 {
		t.Fatal(err)
	}
	n, err := s.DeleteOlderThan(ctx, now.Add(-180*24*time.Hour))
	if err != nil || n != int64(audit.RetentionBatchSize+5) {
		t.Fatalf("delete: n=%d err=%v", n, err)
	}
	var left int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_event`).Scan(&left); err != nil || left != 1 {
		t.Fatalf("fresh row must survive: %d %v", left, err)
	}
	if n, err := s.DeleteOlderThan(ctx, now.Add(-180*24*time.Hour)); err != nil || n != 0 {
		t.Fatalf("second run: %d %v", n, err)
	}
}

func TestStoreListByUserPaginatesByOccurTimeAndID(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	s := audit.NewStore(pool)
	base := time.Date(2026, 9, 14, 8, 0, 0, 0, time.UTC)
	var events []audit.Event
	for i := 0; i < 5; i++ {
		// 两条同一时刻的事件（i=1,2）用 id 作 tiebreaker
		ts := base.Add(time.Duration(i) * time.Second)
		if i == 2 {
			ts = base.Add(time.Second)
		}
		events = append(events, audit.Event{Type: enum.EventSignIn, Actor: enum.ActorUser, Result: enum.ResultSuccess, UserID: "u_0000000000001", Reason: fmt.Sprintf("r%d", i), OccurTime: ts})
	}
	events = append(events, audit.Event{Type: enum.EventSignIn, Actor: enum.ActorUser, Result: enum.ResultSuccess, UserID: "u_0000000000002", OccurTime: base})
	if failed, err := s.InsertBatch(ctx, events); err != nil || failed != 0 {
		t.Fatal(err)
	}
	var all []string
	var cursor *audit.EventCursor
	pages := 0
	for {
		rows, err := s.ListByUser(ctx, "u_0000000000001", cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		for _, r := range rows {
			all = append(all, *r.Reason)
		}
		if len(rows) < 2 {
			break
		}
		last := rows[len(rows)-1]
		cursor = &audit.EventCursor{Time: last.OccurTime, ID: last.ID}
	}
	if pages != 3 || len(all) != 5 {
		t.Fatalf("pages=%d rows=%v", pages, all)
	}
	// 时间升序，同一时刻按 id（写入顺序不保证 id 顺序，所以只断言时间不回退且总集合正确）
	seen := map[string]bool{}
	for _, r := range all {
		seen[r] = true
	}
	for i := 0; i < 5; i++ {
		if !seen[fmt.Sprintf("r%d", i)] {
			t.Fatalf("missing r%d in %v", i, all)
		}
	}
	rows, _ := s.ListByUser(ctx, "u_0000000000001", nil, 100)
	for i := 1; i < len(rows); i++ {
		if rows[i].OccurTime.Before(rows[i-1].OccurTime) || (rows[i].OccurTime.Equal(rows[i-1].OccurTime) && rows[i].ID <= rows[i-1].ID) {
			t.Fatalf("order must be (occur_time, id) ascending: %v", rows)
		}
	}
	if rows, _ := s.ListByUser(ctx, "u_0000000000003", nil, 10); len(rows) != 0 {
		t.Fatal("unknown user must yield no rows")
	}
}
