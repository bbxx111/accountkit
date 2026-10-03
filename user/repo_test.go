package user_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/ids"
	"github.com/bbxx111/accountkit/migrations"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/db"
)

// testPool 返回连接到随机 schema、已迁移的池；无 DSN 则跳过。供本包所有集成测试复用。
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

func sha(s string) []byte { h := sha256.Sum256([]byte(s)); return h[:] }

func TestRepoRotateIsCAS(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	r := user.NewRepo(pool)
	q := r.Q()
	uid, _ := ids.New(ids.User)
	if _, err := q.CreateUser(ctx, db.CreateUserParams{ID: uid, State: enum.UserActive}); err != nil {
		t.Fatal(err)
	}
	sid, _ := ids.New(ids.Session)
	now := time.Now().Truncate(time.Microsecond)
	if _, err := q.CreateSession(ctx, db.CreateSessionParams{ID: sid, UserID: uid, DeviceID: "dev-1", DeviceName: ptr("iPhone"), AuthTime: now, RefreshTokenHash: sha("r1"), RefreshExpireTime: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	n, err := q.RotateSession(ctx, db.RotateSessionParams{ID: sid, OldHash: sha("r1"), NewHash: sha("r2"), Now: now.Add(time.Second), RefreshExpireTime: now.Add(2 * time.Hour)})
	if err != nil || n != 1 {
		t.Fatalf("first rotate: n=%d err=%v", n, err)
	}
	n, err = q.RotateSession(ctx, db.RotateSessionParams{ID: sid, OldHash: sha("r1"), NewHash: sha("r3"), Now: now.Add(2 * time.Second), RefreshExpireTime: now.Add(2 * time.Hour)})
	if err != nil || n != 0 {
		t.Fatalf("stale rotate must affect 0 rows: n=%d err=%v", n, err)
	}
	s, err := q.GetSessionByPreviousRefreshHash(ctx, sha("r1"))
	if err != nil || s.ID != sid || s.RotateTime == nil {
		t.Fatalf("previous hash lookup: %+v %v", s, err)
	}
	if _, err := q.GetSessionByRefreshHash(ctx, sha("r2")); err != nil {
		t.Fatalf("new hash lookup: %v", err)
	}
}

func TestRepoRevokeSessionsByUserExceptCurrent(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := user.NewRepo(pool).Q()
	uid, _ := ids.New(ids.User)
	_, _ = q.CreateUser(ctx, db.CreateUserParams{ID: uid, State: enum.UserActive})
	now := time.Now()
	var sids []string
	for i := 0; i < 3; i++ {
		sid, _ := ids.New(ids.Session)
		sids = append(sids, sid)
		if _, err := q.CreateSession(ctx, db.CreateSessionParams{ID: sid, UserID: uid, DeviceID: fmt.Sprintf("d%d", i), AuthTime: now, RefreshTokenHash: sha(sid), RefreshExpireTime: now.Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	keep := sids[0]
	reason := enum.RevokeUserRevokedDevice
	revoked, err := q.RevokeSessionsByUser(ctx, db.RevokeSessionsByUserParams{UserID: uid, ExceptID: &keep, Reason: &reason, Now: now})
	if err != nil || len(revoked) != 2 {
		t.Fatalf("revoked = %v err=%v", revoked, err)
	}
	active, _ := q.ListActiveSessionsByUser(ctx, db.ListActiveSessionsByUserParams{UserID: uid, Now: now})
	if len(active) != 1 || active[0].ID != keep {
		t.Fatalf("active = %+v", active)
	}
	if active[0].RevokeReason != nil {
		t.Fatal("kept session must have nil revoke_reason")
	}
}

func TestRepoIdentityDigestsAndUniqueViolation(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	r := user.NewRepo(pool)
	q := r.Q()
	uid, _ := ids.New(ids.User)
	_, _ = q.CreateUser(ctx, db.CreateUserParams{ID: uid, State: enum.UserActive})
	iid, _ := ids.New(ids.Identity)
	digest := "d1"
	if _, err := q.CreateIdentity(ctx, db.CreateIdentityParams{ID: iid, UserID: uid, Kind: enum.IdentityPhone, SubjectDigest: &digest, DigestKeyVersion: ptr16(1), SubjectCiphertext: []byte("ct"), CipherKeyVersion: ptr16(1), HintPrefix: ptr("+86138"), HintSuffix: ptr("1234")}); err != nil {
		t.Fatal(err)
	}
	got, err := q.FindActiveIdentityByDigests(ctx, db.FindActiveIdentityByDigestsParams{Kind: enum.IdentityPhone, Digests: []string{"other", "d1"}})
	if err != nil || got.ID != iid {
		t.Fatalf("find by digests: %+v %v", got, err)
	}
	if _, err := q.FindActiveIdentityByDigests(ctx, db.FindActiveIdentityByDigestsParams{Kind: enum.IdentityEmail, Digests: []string{"d1"}}); err != pgx.ErrNoRows {
		t.Fatalf("kind mismatch must be ErrNoRows: %v", err)
	}
	iid2, _ := ids.New(ids.Identity)
	_, err = q.CreateIdentity(ctx, db.CreateIdentityParams{ID: iid2, UserID: uid, Kind: enum.IdentityPhone, SubjectDigest: &digest, DigestKeyVersion: ptr16(1), SubjectCiphertext: []byte("ct"), CipherKeyVersion: ptr16(1)})
	if !user.IsUniqueViolation(err) {
		t.Fatalf("duplicate active digest must be a unique violation: %v", err)
	}
}

func TestRepoWithTxRollsBackOnError(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	r := user.NewRepo(pool)
	uid, _ := ids.New(ids.User)
	err := r.WithTx(ctx, func(q *db.Queries) error {
		if _, err := q.CreateUser(ctx, db.CreateUserParams{ID: uid, State: enum.UserActive}); err != nil {
			return err
		}
		return fmt.Errorf("boom")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if _, err := r.Q().GetUserByID(ctx, uid); err != pgx.ErrNoRows {
		t.Fatalf("user must not exist after rollback: %v", err)
	}
}

// TestRepoLockUserByIDSerializes 证明 LockUserByID 用 SELECT ... FOR UPDATE 把并发登录串行化：
// 持锁事务提交前，另一事务对同一行加锁必须阻塞，而不是立即拿到锁或报错。
func TestRepoLockUserByIDSerializes(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := user.NewRepo(pool).Q()
	uid, _ := ids.New(ids.User)
	if _, err := q.CreateUser(ctx, db.CreateUserParams{ID: uid, State: enum.UserActive}); err != nil {
		t.Fatal(err)
	}

	txA, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = txA.Rollback(context.Background()) }()
	qA := q.WithTx(txA)
	if _, err := qA.LockUserByID(ctx, uid); err != nil {
		t.Fatalf("tx A lock: %v", err)
	}

	txB, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = txB.Rollback(context.Background()) }()
	qB := q.WithTx(txB)
	ctxB, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	if _, err := qB.LockUserByID(ctxB, uid); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("tx B must block on tx A's lock until the context times out: %v", err)
	}

	if err := txA.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}

	// 锁释放后，一次全新的加锁必须立即成功。
	if _, err := q.LockUserByID(ctx, uid); err != nil {
		t.Fatalf("lock after commit: %v", err)
	}
}

func ptr(s string) *string { return &s }
func ptr16(v int16) *int16 { return &v }

func TestRepoProviderIdentityLookupAndMetaMerge(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	repo := user.NewRepo(pool)
	q := repo.Q()
	uid, _ := ids.New(ids.User)
	if _, err := q.CreateUser(ctx, db.CreateUserParams{ID: uid, State: enum.UserActive}); err != nil {
		t.Fatal(err)
	}
	iid, _ := ids.New(ids.Identity)
	subject := "union-abc"
	meta := []byte(`{"openids":{"wx1":"o1"}}`)
	if _, err := q.CreateIdentity(ctx, db.CreateIdentityParams{ID: iid, UserID: uid, Kind: enum.IdentityWeChat, ProviderSubject: &subject, ProviderMeta: meta}); err != nil {
		t.Fatal(err)
	}
	got, err := q.FindActiveIdentityByProviderSubject(ctx, db.FindActiveIdentityByProviderSubjectParams{Kind: enum.IdentityWeChat, ProviderSubject: &subject})
	// JSONB 存储会重新序列化（如冒号后加空格），故按内容而非逐字节比较 provider_meta。
	if err != nil || got.ID != iid || got.UserID != uid || !strings.Contains(string(got.ProviderMeta), `"wx1"`) || !strings.Contains(string(got.ProviderMeta), `"o1"`) {
		t.Fatalf("find: %+v %v", got, err)
	}
	// 另一 kind 同 subject 不命中
	if _, err := q.FindActiveIdentityByProviderSubject(ctx, db.FindActiveIdentityByProviderSubjectParams{Kind: enum.IdentityApple, ProviderSubject: &subject}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("kind mismatch must miss: %v", err)
	}
	// 合并 meta
	now := time.Now()
	n, err := q.UpdateIdentityProviderMeta(ctx, db.UpdateIdentityProviderMetaParams{ID: iid, ProviderMeta: []byte(`{"openids":{"wx1":"o1","wx2":"o2"}}`), UpdateTime: now})
	if err != nil || n != 1 {
		t.Fatalf("update meta: %d %v", n, err)
	}
	got, _ = q.FindActiveIdentityByProviderSubject(ctx, db.FindActiveIdentityByProviderSubjectParams{Kind: enum.IdentityWeChat, ProviderSubject: &subject})
	if !strings.Contains(string(got.ProviderMeta), `"wx2"`) || got.UpdateTime.Before(now.Add(-time.Second)) {
		t.Fatalf("meta not merged: %s", got.ProviderMeta)
	}
	// 同 (kind, provider_subject) 第二条活动身份 → 唯一冲突
	iid2, _ := ids.New(ids.Identity)
	_, err = q.CreateIdentity(ctx, db.CreateIdentityParams{ID: iid2, UserID: uid, Kind: enum.IdentityWeChat, ProviderSubject: &subject})
	if !user.IsUniqueViolation(err) {
		t.Fatalf("duplicate provider subject must be a unique violation: %v", err)
	}
}

func TestRepoGetAndSoftDeleteIdentity(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := user.NewRepo(pool).Q()
	uid, _ := ids.New(ids.User)
	other, _ := ids.New(ids.User)
	for _, id := range []string{uid, other} {
		if _, err := q.CreateUser(ctx, db.CreateUserParams{ID: id, State: enum.UserActive}); err != nil {
			t.Fatal(err)
		}
	}
	iid, _ := ids.New(ids.Identity)
	digest := "digest-unbind-1"
	if _, err := q.CreateIdentity(ctx, db.CreateIdentityParams{ID: iid, UserID: uid, Kind: enum.IdentityPhone, SubjectDigest: &digest, DigestKeyVersion: ptr16(1), SubjectCiphertext: []byte("ct"), CipherKeyVersion: ptr16(1), HintPrefix: ptr("+86138"), HintSuffix: ptr("1234")}); err != nil {
		t.Fatal(err)
	}
	got, err := q.GetActiveIdentityByIDAndUser(ctx, db.GetActiveIdentityByIDAndUserParams{ID: iid, UserID: uid})
	if err != nil || got.ID != iid {
		t.Fatalf("get: %+v %v", got, err)
	}
	// 非本人 → 无行
	if _, err := q.GetActiveIdentityByIDAndUser(ctx, db.GetActiveIdentityByIDAndUserParams{ID: iid, UserID: other}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("foreign must miss: %v", err)
	}
	// 非本人不能软删
	n, err := q.SoftDeleteIdentity(ctx, db.SoftDeleteIdentityParams{ID: iid, UserID: other, Now: time.Now()})
	if err != nil || n != 0 {
		t.Fatalf("foreign delete: %d %v", n, err)
	}
	n, err = q.SoftDeleteIdentity(ctx, db.SoftDeleteIdentityParams{ID: iid, UserID: uid, Now: time.Now()})
	if err != nil || n != 1 {
		t.Fatalf("delete: %d %v", n, err)
	}
	// 软删后：查不到、列表不含、再删 0 行、同 subject 可再建（部分唯一索引）
	if _, err := q.GetActiveIdentityByIDAndUser(ctx, db.GetActiveIdentityByIDAndUserParams{ID: iid, UserID: uid}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("deleted identity must not be found")
	}
	if list, _ := q.ListActiveIdentitiesByUser(ctx, uid); len(list) != 0 {
		t.Fatalf("list after delete: %d", len(list))
	}
	if n, _ := q.SoftDeleteIdentity(ctx, db.SoftDeleteIdentityParams{ID: iid, UserID: uid, Now: time.Now()}); n != 0 {
		t.Fatal("second delete must affect 0 rows")
	}
	iid2, _ := ids.New(ids.Identity)
	if _, err := q.CreateIdentity(ctx, db.CreateIdentityParams{ID: iid2, UserID: other, Kind: enum.IdentityPhone, SubjectDigest: &digest, DigestKeyVersion: ptr16(1), SubjectCiphertext: []byte("ct"), CipherKeyVersion: ptr16(1)}); err != nil {
		t.Fatalf("rebind after soft delete must succeed: %v", err)
	}
}

func TestRepoSoftDeleteUndeletePurgeUser(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := user.NewRepo(pool).Q()
	uid, _ := ids.New(ids.User)
	if _, err := q.CreateUser(ctx, db.CreateUserParams{ID: uid, State: enum.UserActive}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Microsecond)
	purgeAt := now.Add(360 * time.Hour)
	u, err := q.SoftDeleteUser(ctx, db.SoftDeleteUserParams{ID: uid, State: enum.UserPendingDeletion, Now: now, PurgeTime: purgeAt})
	if err != nil || u.State != enum.UserPendingDeletion || u.DeleteTime == nil || !u.DeleteTime.Equal(now) || u.PurgeTime == nil || !u.PurgeTime.Equal(purgeAt) {
		t.Fatalf("soft delete: %+v %v", u, err)
	}
	// 未到期：不在批内；到期：在批内且按 purge_time, id 排序
	due, err := q.ListUsersDueForPurge(ctx, db.ListUsersDueForPurgeParams{Now: now, BatchSize: 10})
	if err != nil || len(due) != 0 {
		t.Fatalf("not due yet: %v %v", due, err)
	}
	due, err = q.ListUsersDueForPurge(ctx, db.ListUsersDueForPurgeParams{Now: purgeAt, BatchSize: 10})
	if err != nil || len(due) != 1 || due[0] != uid {
		t.Fatalf("due: %v %v", due, err)
	}
	// undelete 清空两个时间
	u, err = q.UndeleteUser(ctx, db.UndeleteUserParams{ID: uid, State: enum.UserActive, Now: now})
	if err != nil || u.State != enum.UserActive || u.DeleteTime != nil || u.PurgeTime != nil {
		t.Fatalf("undelete: %+v %v", u, err)
	}
	// ACTIVE 行不会被 PurgeUser 改写（state 条件）
	n, err := q.PurgeUser(ctx, db.PurgeUserParams{ID: uid, DeletedState: enum.UserDeleted, PendingState: enum.UserPendingDeletion, Now: now})
	if err != nil || n != 0 {
		t.Fatalf("purge active must be a no-op: %d %v", n, err)
	}
	// 再软删 + 冻结快照，purge 清空快照与展示名、改状态与 purge_time、保留 delete_time
	if _, err := q.SoftDeleteUser(ctx, db.SoftDeleteUserParams{ID: uid, State: enum.UserPendingDeletion, Now: now, PurgeTime: purgeAt}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE user_account SET display_name = 'n', freeze_time = now(), freeze_reason = 'r', freeze_actor_subject = 's', freeze_actor_username = 'u' WHERE id = $1`, uid); err != nil {
		t.Fatal(err)
	}
	later := purgeAt.Add(time.Hour)
	n, err = q.PurgeUser(ctx, db.PurgeUserParams{ID: uid, DeletedState: enum.UserDeleted, PendingState: enum.UserPendingDeletion, Now: later})
	if err != nil || n != 1 {
		t.Fatalf("purge: %d %v", n, err)
	}
	u, _ = q.GetUserByID(ctx, uid)
	if u.State != enum.UserDeleted || u.DisplayName != nil || u.FreezeTime != nil || u.FreezeReason != nil || u.FreezeActorSubject != nil || u.FreezeActorUsername != nil || u.PurgeTime == nil || !u.PurgeTime.Equal(later) || u.DeleteTime == nil {
		t.Fatalf("purged row: %+v", u)
	}
	if due, _ := q.ListUsersDueForPurge(ctx, db.ListUsersDueForPurgeParams{Now: later.Add(time.Hour), BatchSize: 10}); len(due) != 0 {
		t.Fatal("DELETED rows must not be listed again")
	}
}

func TestRepoAnonymizeIdentityKeepsExactlyOneSubjectAndCoversSoftDeleted(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := user.NewRepo(pool).Q()
	uid, _ := ids.New(ids.User)
	if _, err := q.CreateUser(ctx, db.CreateUserParams{ID: uid, State: enum.UserPendingDeletion}); err != nil {
		t.Fatal(err)
	}
	anchorID, _ := ids.New(ids.Identity)
	providerID, _ := ids.New(ids.Identity)
	goneID, _ := ids.New(ids.Identity)
	digest, sub := "digest-anon-1", "union-anon-1"
	for _, p := range []db.CreateIdentityParams{
		{ID: anchorID, UserID: uid, Kind: enum.IdentityPhone, SubjectDigest: ptr(digest), DigestKeyVersion: ptr16(1), SubjectCiphertext: []byte("ct"), CipherKeyVersion: ptr16(1), HintPrefix: ptr("+86138"), HintSuffix: ptr("1234")},
		{ID: providerID, UserID: uid, Kind: enum.IdentityWeChat, ProviderSubject: ptr(sub), ProviderMeta: []byte(`{"openids":{"wx1":"o1"}}`)},
		{ID: goneID, UserID: uid, Kind: enum.IdentityEmail, SubjectDigest: ptr("digest-anon-gone"), DigestKeyVersion: ptr16(1), SubjectCiphertext: []byte("ct2"), CipherKeyVersion: ptr16(1), HintPrefix: ptr("ba"), HintSuffix: ptr("shifang.co")},
	} {
		if _, err := q.CreateIdentity(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	earlier := time.Now().Add(-time.Hour).Truncate(time.Microsecond)
	if n, err := q.SoftDeleteIdentity(ctx, db.SoftDeleteIdentityParams{ID: goneID, UserID: uid, Now: earlier}); err != nil || n != 1 {
		t.Fatalf("pre-soft-delete: %d %v", n, err)
	}
	all, err := q.ListIdentitiesByUserIncludingDeleted(ctx, uid)
	if err != nil || len(all) != 3 {
		t.Fatalf("including deleted: %d %v", len(all), err)
	}
	if active, _ := q.ListActiveIdentitiesByUser(ctx, uid); len(active) != 2 {
		t.Fatalf("active before purge: %d", len(active))
	}
	now := time.Now().Truncate(time.Microsecond)
	for i, row := range all {
		repl := fmt.Sprintf("%064d", i+1)
		n, err := q.AnonymizeIdentity(ctx, db.AnonymizeIdentityParams{ID: row.ID, UserID: uid, Replacement: repl, Now: now})
		if err != nil || n != 1 {
			t.Fatalf("anonymize %s: %d %v", row.ID, n, err)
		}
	}
	after, _ := q.ListIdentitiesByUserIncludingDeleted(ctx, uid)
	for _, row := range after {
		if row.DeleteTime == nil || row.SubjectCiphertext != nil || row.ProviderMeta != nil || row.HintPrefix != nil || row.HintSuffix != nil || row.DigestKeyVersion != nil || row.CipherKeyVersion != nil {
			t.Fatalf("pii left on %s: %+v", row.ID, row)
		}
		// 恰好一个 subject 列非空，且不再是原值
		if (row.SubjectDigest == nil) == (row.ProviderSubject == nil) {
			t.Fatalf("exactly-one violated on %s: %+v", row.ID, row)
		}
		switch row.ID {
		case anchorID:
			if row.SubjectDigest == nil || *row.SubjectDigest == digest || row.ProviderSubject != nil {
				t.Fatalf("anchor: %+v", row)
			}
		case providerID:
			if row.ProviderSubject == nil || *row.ProviderSubject == sub || row.SubjectDigest != nil {
				t.Fatalf("provider: %+v", row)
			}
		case goneID:
			if !row.DeleteTime.Equal(earlier) {
				t.Fatalf("already soft-deleted row must keep its delete_time: %+v", row)
			}
		}
	}
	if active, _ := q.ListActiveIdentitiesByUser(ctx, uid); len(active) != 0 {
		t.Fatalf("active after purge: %d", len(active))
	}
	// 原 subject 可再次注册（唯一索引只看活动行，且旧行的 digest 已被替换）
	againID, _ := ids.New(ids.Identity)
	if _, err := q.CreateIdentity(ctx, db.CreateIdentityParams{ID: againID, UserID: uid, Kind: enum.IdentityPhone, SubjectDigest: ptr(digest), DigestKeyVersion: ptr16(1), SubjectCiphertext: []byte("ct"), CipherKeyVersion: ptr16(1), HintPrefix: ptr("+86138"), HintSuffix: ptr("1234")}); err != nil {
		t.Fatalf("re-register original subject: %v", err)
	}
	// 非本人的 user_id 不匹配 → 0 行
	other, _ := ids.New(ids.User)
	if n, _ := q.AnonymizeIdentity(ctx, db.AnonymizeIdentityParams{ID: againID, UserID: other, Replacement: "x", Now: now}); n != 0 {
		t.Fatal("foreign user_id must not match")
	}
}

func TestRepoScrubAuditEventsAndDeleteStaleSessions(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := user.NewRepo(pool).Q()
	uid, _ := ids.New(ids.User)
	other, _ := ids.New(ids.User)
	for _, id := range []string{uid, other} {
		if _, err := q.CreateUser(ctx, db.CreateUserParams{ID: id, State: enum.UserActive}); err != nil {
			t.Fatal(err)
		}
	}
	// 审计：本人两行（一行已无可清字段）、他人一行
	for _, row := range []struct{ id, user, ip string }{{"e_0000000000001", uid, "203.0.113.5"}, {"e_0000000000002", uid, ""}, {"e_0000000000003", other, "203.0.113.6"}} {
		var err error
		if row.ip == "" {
			_, err = pool.Exec(ctx, `INSERT INTO audit_event (id, event_type, actor_kind, user_id, result) VALUES ($1, 4, 1, $2, 1)`, row.id, row.user)
		} else {
			_, err = pool.Exec(ctx, `INSERT INTO audit_event (id, event_type, actor_kind, user_id, result, ip, device_id, subject_hint) VALUES ($1, 4, 1, $2, 1, $3::inet, 'dev', 'abcdefgh')`, row.id, row.user, row.ip)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	n, err := q.ScrubAuditEventsByUser(ctx, uid)
	if err != nil || n != 1 {
		t.Fatalf("scrub: %d %v", n, err)
	}
	var scrubbed, intact int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_event WHERE user_id = $1 AND ip IS NULL AND device_id IS NULL AND subject_hint IS NULL`, uid).Scan(&scrubbed); err != nil || scrubbed != 2 {
		t.Fatalf("scrubbed rows: %d %v", scrubbed, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_event WHERE user_id = $1 AND ip IS NOT NULL`, other).Scan(&intact); err != nil || intact != 1 {
		t.Fatalf("other user's rows must be untouched: %d %v", intact, err)
	}

	// 会话：吊销 31 天 / 吊销 29 天 / 未吊销但过期 31 天 / 活跃
	now := time.Now()
	mk := func(name string, revokeAt *time.Time, expireAt time.Time) string {
		sid, _ := ids.New(ids.Session)
		if _, err := q.CreateSession(ctx, db.CreateSessionParams{ID: sid, UserID: uid, DeviceID: name, AuthTime: now, RefreshTokenHash: sha("rt-" + name), RefreshExpireTime: expireAt}); err != nil {
			t.Fatal(err)
		}
		if revokeAt != nil {
			if _, err := pool.Exec(ctx, `UPDATE session SET revoke_time = $2, revoke_reason = 1 WHERE id = $1`, sid, *revokeAt); err != nil {
				t.Fatal(err)
			}
		}
		return sid
	}
	d := 24 * time.Hour
	t31, t29 := now.Add(-31*d), now.Add(-29*d)
	oldRevoked := mk("old-revoked", &t31, now.Add(30*d))
	recentRevoked := mk("recent-revoked", &t29, now.Add(30*d))
	oldExpired := mk("old-expired", nil, now.Add(-31*d))
	live := mk("live", nil, now.Add(30*d))
	before := now.Add(-30 * d)
	if n, err := q.DeleteStaleSessions(ctx, db.DeleteStaleSessionsParams{Before: before, BatchSize: 1}); err != nil || n != 1 {
		t.Fatalf("batch of 1: %d %v", n, err)
	}
	if n, err := q.DeleteStaleSessions(ctx, db.DeleteStaleSessionsParams{Before: before, BatchSize: 100}); err != nil || n != 1 {
		t.Fatalf("rest: %d %v", n, err)
	}
	for sid, want := range map[string]bool{oldRevoked: false, recentRevoked: true, oldExpired: false, live: true} {
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM session WHERE id = $1)`, sid).Scan(&exists); err != nil || exists != want {
			t.Fatalf("session %s exists=%v want %v (%v)", sid, exists, want, err)
		}
	}
}

func TestRepoKeyVersionQueries(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := user.NewRepo(pool).Q()
	uid, _ := ids.New(ids.User)
	if _, err := q.CreateUser(ctx, db.CreateUserParams{ID: uid, State: enum.UserActive}); err != nil {
		t.Fatal(err)
	}
	mk := func(kind enum.IdentityKind, digest string, dv, cv int16, deleted bool) string {
		id, _ := ids.New(ids.Identity)
		if _, err := q.CreateIdentity(ctx, db.CreateIdentityParams{ID: id, UserID: uid, Kind: kind, SubjectDigest: ptr(digest), DigestKeyVersion: ptr16(dv), SubjectCiphertext: []byte("ct-" + digest), CipherKeyVersion: ptr16(cv), HintPrefix: ptr("+86138"), HintSuffix: ptr("0000")}); err != nil {
			t.Fatal(err)
		}
		if deleted {
			if _, err := q.SoftDeleteIdentity(ctx, db.SoftDeleteIdentityParams{ID: id, UserID: uid, Now: time.Now()}); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}
	oldPhone := mk(enum.IdentityPhone, "d-old-phone", 1, 1, false)
	newMail := mk(enum.IdentityEmail, "d-new-mail", 2, 2, false)
	gone := mk(enum.IdentityPhone, "d-gone", 1, 1, true) // 软删：不在任何列表里
	wxID, _ := ids.New(ids.Identity)
	if _, err := q.CreateIdentity(ctx, db.CreateIdentityParams{ID: wxID, UserID: uid, Kind: enum.IdentityWeChat, ProviderSubject: ptr("union-x")}); err != nil {
		t.Fatal(err)
	}
	_ = gone

	rows, err := q.ListIdentitiesByDigestKeyVersion(ctx, db.ListIdentitiesByDigestKeyVersionParams{Version: 1, BatchSize: 10})
	if err != nil || len(rows) != 1 || rows[0].ID != oldPhone {
		t.Fatalf("digest v1 rows: %v %v", rows, err)
	}
	if rows, _ := q.ListIdentitiesByCipherKeyVersion(ctx, db.ListIdentitiesByCipherKeyVersionParams{Version: 1, BatchSize: 10}); len(rows) != 1 || rows[0].ID != oldPhone {
		t.Fatalf("cipher v1 rows: %v", rows)
	}
	if vs, err := q.ListActiveDigestKeyVersions(ctx); err != nil || len(vs) != 2 || vs[0] != 1 || vs[1] != 2 {
		t.Fatalf("active digest versions: %v %v", vs, err)
	}
	if vs, err := q.ListActiveCipherKeyVersions(ctx); err != nil || len(vs) != 2 || vs[0] != 1 || vs[1] != 2 {
		t.Fatalf("active cipher versions: %v %v", vs, err)
	}
	now := time.Now()
	// CAS：old_version 不匹配 → 0 行；匹配 → 1 行且版本/值更新
	if n, err := q.UpdateIdentityDigest(ctx, db.UpdateIdentityDigestParams{ID: oldPhone, Digest: "d-new-phone", Version: 2, OldVersion: 5, Now: now}); err != nil || n != 0 {
		t.Fatalf("cas mismatch must not update: %d %v", n, err)
	}
	if n, err := q.UpdateIdentityDigest(ctx, db.UpdateIdentityDigestParams{ID: oldPhone, Digest: "d-new-phone", Version: 2, OldVersion: 1, Now: now}); err != nil || n != 1 {
		t.Fatalf("cas hit: %d %v", n, err)
	}
	if n, err := q.UpdateIdentityCiphertext(ctx, db.UpdateIdentityCiphertextParams{ID: oldPhone, Ciphertext: []byte("ct-new"), Version: 2, OldVersion: 1, Now: now}); err != nil || n != 1 {
		t.Fatalf("cipher cas hit: %d %v", n, err)
	}
	row, _ := q.GetActiveIdentityByIDAndUser(ctx, db.GetActiveIdentityByIDAndUserParams{ID: oldPhone, UserID: uid})
	if *row.SubjectDigest != "d-new-phone" || *row.DigestKeyVersion != 2 || string(row.SubjectCiphertext) != "ct-new" || *row.CipherKeyVersion != 2 {
		t.Fatalf("row after backfill: %+v", row)
	}
	if vs, _ := q.ListActiveDigestKeyVersions(ctx); len(vs) != 1 || vs[0] != 2 {
		t.Fatalf("only v2 must remain active: %v", vs)
	}
	// 软删行不可被 CAS 改写
	if n, _ := q.UpdateIdentityDigest(ctx, db.UpdateIdentityDigestParams{ID: gone, Digest: "x", Version: 2, OldVersion: 1, Now: now}); n != 0 {
		t.Fatal("soft-deleted rows must be untouched")
	}
	_ = newMail
}

func TestRepoFreezeUnfreezeAndCountSessions(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := user.NewRepo(pool).Q()
	uid, _ := ids.New(ids.User)
	if _, err := q.CreateUser(ctx, db.CreateUserParams{ID: uid, State: enum.UserActive}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Microsecond)
	u, err := q.FreezeUser(ctx, db.FreezeUserParams{ID: uid, State: enum.UserFrozen, Now: now, Reason: "abuse", ActorSubject: "admin-sub", ActorUsername: "ops"})
	if err != nil || u.State != enum.UserFrozen || u.FreezeTime == nil || !u.FreezeTime.Equal(now) || *u.FreezeReason != "abuse" || *u.FreezeActorSubject != "admin-sub" || *u.FreezeActorUsername != "ops" {
		t.Fatalf("freeze: %+v %v", u, err)
	}
	u, err = q.UnfreezeUser(ctx, db.UnfreezeUserParams{ID: uid, State: enum.UserActive, Now: now.Add(time.Second)})
	if err != nil || u.State != enum.UserActive || u.FreezeTime != nil || u.FreezeReason != nil || u.FreezeActorSubject != nil || u.FreezeActorUsername != nil {
		t.Fatalf("unfreeze: %+v %v", u, err)
	}
	for i, dev := range []string{"d1", "d2", "d3"} {
		sid, _ := ids.New(ids.Session)
		if _, err := q.CreateSession(ctx, db.CreateSessionParams{ID: sid, UserID: uid, DeviceID: dev, AuthTime: now, RefreshTokenHash: sha("rt-" + dev), RefreshExpireTime: now.Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if _, err := q.RevokeSession(ctx, db.RevokeSessionParams{ID: sid, Reason: ptrReason(enum.RevokeAdmin), Now: now}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if n, err := q.CountActiveSessionsByUser(ctx, db.CountActiveSessionsByUserParams{UserID: uid, Now: now}); err != nil || n != 2 {
		t.Fatalf("active sessions: %d %v", n, err)
	}
}

func ptrReason(r enum.RevokeReason) *enum.RevokeReason { return &r }

func TestRepoListUsersAdminFiltersAndPaginates(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	q := user.NewRepo(pool).Q()
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	mk := func(i int, state enum.UserState) string {
		uid, _ := ids.New(ids.User)
		if _, err := q.CreateUser(ctx, db.CreateUserParams{ID: uid, State: enum.UserActive}); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `UPDATE user_account SET state = $2, create_time = $3 WHERE id = $1`, uid, int16(state), base.Add(time.Duration(i)*time.Hour)); err != nil {
			t.Fatal(err)
		}
		return uid
	}
	active1 := mk(0, enum.UserActive)
	active2 := mk(1, enum.UserActive)
	frozen := mk(2, enum.UserFrozen)
	pending := mk(3, enum.UserPendingDeletion)
	deleted := mk(4, enum.UserDeleted)
	// 身份：active1 有手机 (+86138 / 1234，digest "dg-a1")，active2 有邮箱 (ba / shifang.co)
	iid1, _ := ids.New(ids.Identity)
	if _, err := q.CreateIdentity(ctx, db.CreateIdentityParams{ID: iid1, UserID: active1, Kind: enum.IdentityPhone, SubjectDigest: ptr("dg-a1"), DigestKeyVersion: ptr16(1), SubjectCiphertext: []byte("ct"), CipherKeyVersion: ptr16(1), HintPrefix: ptr("+86138"), HintSuffix: ptr("1234")}); err != nil {
		t.Fatal(err)
	}
	iid2, _ := ids.New(ids.Identity)
	if _, err := q.CreateIdentity(ctx, db.CreateIdentityParams{ID: iid2, UserID: active2, Kind: enum.IdentityEmail, SubjectDigest: ptr("dg-a2"), DigestKeyVersion: ptr16(1), SubjectCiphertext: []byte("ct"), CipherKeyVersion: ptr16(1), HintPrefix: ptr("ba"), HintSuffix: ptr("shifang.co")}); err != nil {
		t.Fatal(err)
	}
	idsOf := func(rows []db.UserAccount) []string {
		out := make([]string, 0, len(rows))
		for _, r := range rows {
			out = append(out, r.ID)
		}
		return out
	}
	eq := func(got []string, want ...string) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}
	// 默认：不含 PENDING_DELETION / DELETED，按 create_time 升序
	rows, err := q.ListUsersAdmin(ctx, db.ListUsersAdminParams{PageLimit: 10})
	if err != nil || !eq(idsOf(rows), active1, active2, frozen) {
		t.Fatalf("default list: %v %v", idsOf(rows), err)
	}
	// show_deleted
	rows, _ = q.ListUsersAdmin(ctx, db.ListUsersAdminParams{IncludeDeleted: true, PageLimit: 10})
	if !eq(idsOf(rows), active1, active2, frozen, pending, deleted) {
		t.Fatalf("include deleted: %v", idsOf(rows))
	}
	// state 过滤（PENDING_DELETION 且不 show_deleted → 空；show_deleted → 命中）
	st := int16(enum.UserPendingDeletion)
	if rows, _ = q.ListUsersAdmin(ctx, db.ListUsersAdminParams{State: &st, PageLimit: 10}); len(rows) != 0 {
		t.Fatalf("pending without show_deleted: %v", idsOf(rows))
	}
	if rows, _ = q.ListUsersAdmin(ctx, db.ListUsersAdminParams{State: &st, IncludeDeleted: true, PageLimit: 10}); !eq(idsOf(rows), pending) {
		t.Fatalf("pending with show_deleted: %v", idsOf(rows))
	}
	// create_time 范围 [1h, 3h)
	lo, hi := base.Add(time.Hour), base.Add(3*time.Hour)
	if rows, _ = q.ListUsersAdmin(ctx, db.ListUsersAdminParams{CreateTimeMin: &lo, CreateTimeMax: &hi, PageLimit: 10}); !eq(idsOf(rows), active2, frozen) {
		t.Fatalf("time range: %v", idsOf(rows))
	}
	// 身份：digest 等值；hint 前缀+后缀；kind 不匹配 → 空
	phone := int16(enum.IdentityPhone)
	if rows, _ = q.ListUsersAdmin(ctx, db.ListUsersAdminParams{ByIdentity: true, IdentityKind: &phone, Digests: []string{"nope", "dg-a1"}, PageLimit: 10}); !eq(idsOf(rows), active1) {
		t.Fatalf("by digest: %v", idsOf(rows))
	}
	if rows, _ = q.ListUsersAdmin(ctx, db.ListUsersAdminParams{ByIdentity: true, IdentityKind: &phone, Digests: []string{}, HintPrefix: ptr("+86138"), HintSuffix: ptr("1234"), PageLimit: 10}); !eq(idsOf(rows), active1) {
		t.Fatalf("by hints: %v", idsOf(rows))
	}
	mail := int16(enum.IdentityEmail)
	if rows, _ = q.ListUsersAdmin(ctx, db.ListUsersAdminParams{ByIdentity: true, IdentityKind: &mail, Digests: []string{}, HintSuffix: ptr("shifang.co"), PageLimit: 10}); !eq(idsOf(rows), active2) {
		t.Fatalf("by email domain: %v", idsOf(rows))
	}
	if rows, _ = q.ListUsersAdmin(ctx, db.ListUsersAdminParams{ByIdentity: true, IdentityKind: &mail, Digests: []string{}, HintSuffix: ptr("1234"), PageLimit: 10}); len(rows) != 0 {
		t.Fatalf("kind mismatch must be empty: %v", idsOf(rows))
	}
	// 软删身份不参与
	if _, err := q.SoftDeleteIdentity(ctx, db.SoftDeleteIdentityParams{ID: iid1, UserID: active1, Now: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if rows, _ = q.ListUsersAdmin(ctx, db.ListUsersAdminParams{ByIdentity: true, IdentityKind: &phone, Digests: []string{"dg-a1"}, PageLimit: 10}); len(rows) != 0 {
		t.Fatalf("soft-deleted identity must not match: %v", idsOf(rows))
	}
	// keyset 分页：每页 2，游标 (create_time, id)
	page1, _ := q.ListUsersAdmin(ctx, db.ListUsersAdminParams{IncludeDeleted: true, PageLimit: 2})
	last := page1[len(page1)-1]
	page2, _ := q.ListUsersAdmin(ctx, db.ListUsersAdminParams{IncludeDeleted: true, AfterTime: &last.CreateTime, AfterID: &last.ID, PageLimit: 2})
	if !eq(idsOf(page1), active1, active2) || !eq(idsOf(page2), frozen, pending) {
		t.Fatalf("pages: %v | %v", idsOf(page1), idsOf(page2))
	}
}
