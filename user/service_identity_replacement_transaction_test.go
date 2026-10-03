package user_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/ids"
	"github.com/bbxx111/accountkit/pii"
	"github.com/bbxx111/accountkit/session/revocation"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/code"
	"github.com/bbxx111/accountkit/user/db"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
)

func TestReplaceIdentityConflictAndKeyRotation(t *testing.T) {
	for _, sameUser := range []bool{false, true} {
		t.Run(map[bool]string{false: "other-account", true: "same-account"}[sameUser], func(t *testing.T) {
			f, p, id, _ := replacementSetup(t, enum.IdentityEmail, email1)
			ctx := context.Background()
			target := "occupied@example.test"
			if sameUser {
				deps := f.deps
				deps.MaxIdentitiesPerKind = 2
				f.svc, _ = user.NewService(deps)
				plain := replacementCode(t, f, p, enum.IdentityEmail, target)
				if _, _, err := f.svc.BindWithCode(ctx, p, enum.IdentityEmail, target, plain, meta1); err != nil {
					t.Fatal(err)
				}
				f.advance(61 * time.Second)
			} else {
				f.signIn(t, enum.IdentityEmail, target, user.Device{ID: "occupied"})
			}
			// 已有身份仍使用版本1，新代码使用版本2及完整旧版本集合。
			deps := f.deps
			deps.MaxIdentitiesPerKind = 2
			deps.Digester, _ = pii.NewDigester(map[uint16][]byte{1: bytes.Repeat([]byte{1}, 32), 2: bytes.Repeat([]byte{4}, 32)}, 2)
			deps.Cipher, _ = pii.NewCipher(map[uint16][]byte{1: bytes.Repeat([]byte{2}, 32), 2: bytes.Repeat([]byte{5}, 32)}, 2)
			var err error
			f.svc, err = user.NewService(deps)
			if err != nil {
				t.Fatal(err)
			}
			plain := replacementCode(t, f, p, enum.IdentityEmail, target)
			if _, err := f.svc.ReplaceIdentity(ctx, p, id, enum.IdentityEmail, target, plain, meta1); !errors.Is(err, user.ErrIdentityConflict) {
				t.Fatalf("legacy digest conflict: %v", err)
			}
			if err := f.deps.Codes.Verify(ctx, enum.IdentityEmail, enum.PurposeBind, target, plain); !errors.Is(err, code.ErrExpired) {
				t.Fatalf("conflict restored code: %v", err)
			}
			if !hasEventReason(f.audit, enum.EventIdentityReplaceRejected, enum.ResultFailure, "IDENTITY_ALREADY_BOUND") {
				t.Fatal("missing conflict audit")
			}
			plain = replacementCode(t, f, p, enum.IdentityEmail, "fresh@example.test")
			out, err := f.svc.ReplaceIdentity(ctx, p, id, enum.IdentityEmail, "fresh@example.test", plain, meta1)
			if err != nil {
				t.Fatal(err)
			}
			row, err := f.repo.Q().GetActiveIdentityByIDAndUser(ctx, db.GetActiveIdentityByIDAndUserParams{ID: out.ID, UserID: p.UserID})
			if err != nil || row.DigestKeyVersion == nil || *row.DigestKeyVersion != 2 || row.CipherKeyVersion == nil || *row.CipherKeyVersion != 2 {
				t.Fatalf("active key versions: %+v %v", row, err)
			}
			digest, _ := deps.Digester.Digest("fresh@example.test")
			if row.SubjectDigest == nil || *row.SubjectDigest != digest {
				t.Fatal("wrong active digest")
			}
			plainTarget, err := deps.Cipher.Decrypt(row.SubjectCiphertext, uint16(*row.CipherKeyVersion))
			if err != nil || plainTarget != "fresh@example.test" {
				t.Fatalf("new encrypted subject: %v", err)
			}
		})
	}
}

func TestReplaceIdentityRollback(t *testing.T) {
	for _, stage := range []string{"delete", "create", "revoke"} {
		t.Run(stage, func(t *testing.T) {
			f, p, id, _ := replacementSetup(t, enum.IdentityPhone, phone1)
			ctx := context.Background()
			f.signIn(t, enum.IdentityPhone, phone1, user.Device{ID: "other"})
			plain := replacementCode(t, f, p, enum.IdentityPhone, phone2)
			mustExec(t, f, `CREATE FUNCTION test_replacement_fail() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION 'test replacement write failure'; END; $$ LANGUAGE plpgsql`)
			switch stage {
			case "delete":
				mustExec(t, f, `CREATE TRIGGER test_replacement_trg BEFORE UPDATE ON identity FOR EACH ROW WHEN (NEW.delete_time IS NOT NULL) EXECUTE FUNCTION test_replacement_fail()`)
			case "create":
				mustExec(t, f, `CREATE TRIGGER test_replacement_trg BEFORE INSERT ON identity FOR EACH ROW EXECUTE FUNCTION test_replacement_fail()`)
			case "revoke":
				mustExec(t, f, `CREATE TRIGGER test_replacement_trg BEFORE UPDATE ON session FOR EACH ROW WHEN (NEW.revoke_time IS NOT NULL) EXECUTE FUNCTION test_replacement_fail()`)
			}
			if _, err := f.svc.ReplaceIdentity(ctx, p, id, enum.IdentityPhone, phone2, plain, meta1); err == nil || errors.Is(err, user.ErrIdentityConflict) {
				t.Fatalf("write failure hidden: %v", err)
			}
			list, err := f.svc.ListIdentities(ctx, p.UserID)
			if err != nil || len(list) != 1 || list[0].ID != id {
				t.Fatalf("rollback identity: %+v %v", list, err)
			}
			sessions, err := f.repo.Q().ListActiveSessionsByUser(ctx, p.UserID)
			if err != nil || len(sessions) != 2 {
				t.Fatalf("partial revocation: %+v %v", sessions, err)
			}
			if err := f.deps.Codes.Verify(ctx, enum.IdentityPhone, enum.PurposeBind, phone2, plain); !errors.Is(err, code.ErrExpired) {
				t.Fatalf("rollback restored code: %v", err)
			}
			for _, e := range f.audit.Events() {
				if e.Reason == "IDENTITY_REPLACED" || e.Type == enum.EventIdentityReplaceRejected {
					t.Fatalf("infrastructure failure produced business/success audit: %+v", e)
				}
			}
		})
	}
}

func TestReplaceIdentityFinalKindLimit(t *testing.T) {
	f, p, id, _ := replacementSetup(t, enum.IdentityEmail, email1)
	ctx := context.Background()
	deps := f.deps
	deps.MaxIdentitiesPerKind = 2
	var err error
	f.svc, err = user.NewService(deps)
	if err != nil {
		t.Fatal(err)
	}
	plain := replacementCode(t, f, p, enum.IdentityEmail, "second@example.test")
	if _, _, err := f.svc.BindWithCode(ctx, p, enum.IdentityEmail, "second@example.test", plain, meta1); err != nil {
		t.Fatal(err)
	}
	f.svc, err = user.NewService(f.deps)
	if err != nil {
		t.Fatal(err)
	}
	plain = replacementCode(t, f, p, enum.IdentityEmail, "new@example.test")
	if _, err := f.svc.ReplaceIdentity(ctx, p, id, enum.IdentityEmail, "new@example.test", plain, meta1); !errors.Is(err, user.ErrIdentityKindLimit) {
		t.Fatalf("final count over configured limit: %v", err)
	}
	list, err := f.svc.ListIdentities(ctx, p.UserID)
	if err != nil || len(list) != 2 {
		t.Fatalf("kind-limit mutated identities: %+v %v", list, err)
	}
}

func TestReplaceIdentityVerificationUnavailable(t *testing.T) {
	f, p, id, _ := replacementSetup(t, enum.IdentityPhone, phone1)
	plain := replacementCode(t, f, p, enum.IdentityPhone, phone2)
	f.mr.Close()
	if _, err := f.svc.ReplaceIdentity(context.Background(), p, id, enum.IdentityPhone, phone2, plain, meta1); !errors.Is(err, user.ErrUnavailable) {
		t.Fatalf("code Redis must fail closed: %v", err)
	}
	list, err := f.svc.ListIdentities(context.Background(), p.UserID)
	if err != nil || len(list) != 1 || list[0].ID != id {
		t.Fatalf("Redis failure mutated identities: %+v %v", list, err)
	}
	if hasEvent(f.audit, enum.EventIdentityReplaceRejected, enum.ResultFailure) {
		t.Fatal("infrastructure outage audited as business rejection")
	}
}

// 等待 pg_blocking_pids 观测到真实行锁等待，避免用固定 sleep 猜测事务进度。
func replacementWait(t *testing.T, ctx context.Context, f *fixture, tx pgx.Tx, done <-chan error) {
	t.Helper()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		var blocked bool
		if err := f.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE $1::int = ANY(pg_blocking_pids(pid)))`, tx.Conn().PgConn().PID()).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		select {
		case err := <-done:
			t.Fatalf("replacement completed before lock wait: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-tick.C:
		}
	}
}

func TestReplaceIdentityLockedRecheck(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*testing.T, *fixture, pgx.Tx, user.Principal, string)
		want   error
	}{
		{"identity-removed", func(t *testing.T, f *fixture, tx pgx.Tx, p user.Principal, id string) {
			_, err := db.New(tx).SoftDeleteIdentity(context.Background(), db.SoftDeleteIdentityParams{ID: id, UserID: p.UserID, Now: *f.clock})
			if err != nil {
				t.Fatal(err)
			}
		}, user.ErrNotFound},
		{"frozen", func(t *testing.T, _ *fixture, tx pgx.Tx, p user.Principal, _ string) {
			if _, err := tx.Exec(context.Background(), "UPDATE user_account SET state=2 WHERE id=$1", p.UserID); err != nil {
				t.Fatal(err)
			}
		}, user.ErrUserFrozen},
		{"session-revoked", func(t *testing.T, f *fixture, tx pgx.Tx, p user.Principal, _ string) {
			r := enum.RevokeUserLogout
			if _, err := db.New(tx).RevokeSession(context.Background(), db.RevokeSessionParams{ID: p.SessionID, Reason: &r, Now: *f.clock}); err != nil {
				t.Fatal(err)
			}
		}, user.ErrInvalidToken},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, p, id, _ := replacementSetup(t, enum.IdentityPhone, phone1)
			plain := replacementCode(t, f, p, enum.IdentityPhone, phone2)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			tx, err := f.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background())
			if _, err := db.New(tx).LockUserByID(ctx, p.UserID); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				_, err := f.svc.ReplaceIdentity(ctx, p, id, enum.IdentityPhone, phone2, plain, meta1)
				done <- err
			}()
			replacementWait(t, ctx, f, tx, done)
			// 请求已消费 BIND 码，随后才尝试获取 user 锁。
			if err := f.deps.Codes.Verify(ctx, enum.IdentityPhone, enum.PurposeBind, phone2, plain); !errors.Is(err, code.ErrExpired) {
				t.Fatalf("code verification held database lock or did not consume: %v", err)
			}
			tc.mutate(t, f, tx, p, id)
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-done; !errors.Is(err, tc.want) {
				t.Fatalf("locked recheck: %v want %v", err, tc.want)
			}
			var n int
			if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM identity WHERE user_id=$1", p.UserID).Scan(&n); err != nil || n != 1 {
				t.Fatalf("unexpected replacement insert: %d %v", n, err)
			}
		})
	}
}

func TestReplaceIdentityConcurrent(t *testing.T) {
	for _, sameUser := range []bool{true, false} {
		t.Run(map[bool]string{true: "same-old-identity", false: "same-new-target"}[sameUser], func(t *testing.T) {
			f, p, id, _ := replacementSetup(t, enum.IdentityPhone, phone1)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			p2 := p
			id2 := id
			target1 := phone2
			target2 := "+8613900000003"
			if !sameUser {
				r := f.signIn(t, enum.IdentityPhone, "+8613900000009", user.Device{ID: "contender"})
				p2, _ = f.svc.Authenticate(ctx, r.AccessToken)
				list, _ := f.svc.ListIdentities(ctx, p2.UserID)
				id2 = list[0].ID
				target2 = target1
			}
			plain1 := replacementCode(t, f, p, enum.IdentityPhone, target1)
			plain2 := plain1
			secondSvc := f.svc
			if sameUser {
				plain2 = replacementCode(t, f, p2, enum.IdentityPhone, target2)
			} else {
				// 分别有效的 BIND 证明使数据库唯一性竞争真实发生，而不是在 Redis 先淘汰一个请求。
				rdb := redis.NewClient(&redis.Options{Addr: f.mr.Addr()})
				defer rdb.Close()
				deps := f.deps
				deps.Codes = code.NewStore(rdb, "contender:", f.dig, code.Options{TTL: 5 * time.Minute, Cooldown: time.Minute, MaxAttempts: 5, DailyLimitPerTarget: 10, DailyLimitPerIP: 100})
				var err error
				secondSvc, err = user.NewService(deps)
				if err != nil {
					t.Fatal(err)
				}
				plain2, err = deps.Codes.Issue(ctx, enum.IdentityPhone, enum.PurposeBind, target2, meta1.IP)
				if err != nil {
					t.Fatal(err)
				}
			}
			var errs [2]error
			var wg sync.WaitGroup
			start := make(chan struct{})
			wg.Go(func() {
				<-start
				_, errs[0] = f.svc.ReplaceIdentity(ctx, p, id, enum.IdentityPhone, target1, plain1, meta1)
			})
			wg.Go(func() {
				<-start
				_, errs[1] = secondSvc.ReplaceIdentity(ctx, p2, id2, enum.IdentityPhone, target2, plain2, meta1)
			})
			close(start)
			wg.Wait()
			successes := 0
			for _, err := range errs {
				if err == nil {
					successes++
				} else if sameUser && !errors.Is(err, user.ErrNotFound) {
					t.Fatalf("same identity loser: %v", err)
				} else if !sameUser && !errors.Is(err, user.ErrIdentityConflict) {
					t.Fatalf("same target loser: %v", err)
				}
			}
			if successes != 1 {
				t.Fatalf("successes=%d errors=%v", successes, errs)
			}
			for _, uid := range []string{p.UserID, p2.UserID} {
				list, err := f.svc.ListIdentities(ctx, uid)
				if err != nil || len(list) != 1 {
					t.Fatalf("identity invariant: %+v %v", list, err)
				}
			}
		})
	}
}

func TestReplaceIdentityFreshnessExpiresWhileWaiting(t *testing.T) {
	f, p, id, _ := replacementSetup(t, enum.IdentityPhone, phone1)
	var nanos atomic.Int64
	nanos.Store(f.clock.UnixNano())
	deps := f.deps
	deps.Now = func() time.Time { return time.Unix(0, nanos.Load()) }
	var err error
	f.svc, err = user.NewService(deps)
	if err != nil {
		t.Fatal(err)
	}
	plain := replacementCode(t, f, p, enum.IdentityPhone, phone2)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := db.New(tx).LockUserByID(ctx, p.UserID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := f.svc.ReplaceIdentity(ctx, p, id, enum.IdentityPhone, phone2, plain, meta1)
		done <- err
	}()
	replacementWait(t, ctx, f, tx, done)
	nanos.Store(p.AuthTime.Add(5*time.Minute + time.Nanosecond).UnixNano())
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, user.ErrReauthenticationRequired) {
		t.Fatalf("expired while blocked: %v", err)
	}
	list, err := f.svc.ListIdentities(ctx, p.UserID)
	if err != nil || len(list) != 1 || list[0].ID != id {
		t.Fatalf("stale request wrote identity: %+v %v", list, err)
	}
}

func TestReplaceIdentityUniqueInsertConflict(t *testing.T) {
	f, p, id, _ := replacementSetup(t, enum.IdentityPhone, phone1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	other := f.signIn(t, enum.IdentityEmail, "contender@example.test", user.Device{ID: "contender"})
	plain := replacementCode(t, f, p, enum.IdentityPhone, phone2)
	// 独立事务持有 advisory 锁，在 INSERT 触发器中暂停换绑；此时预查询已完成且旧身份软删尚未提交。
	barrier, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer barrier.Rollback(context.Background())
	if _, err := barrier.Exec(ctx, "SELECT pg_advisory_xact_lock(2147483021)"); err != nil {
		t.Fatal(err)
	}
	mustExec(t, f, fmt.Sprintf(`CREATE FUNCTION test_replacement_barrier() RETURNS trigger AS $$ BEGIN IF NEW.user_id='%s' THEN PERFORM pg_advisory_xact_lock(2147483021); END IF; RETURN NEW; END; $$ LANGUAGE plpgsql`, p.UserID))
	mustExec(t, f, `CREATE TRIGGER test_replacement_barrier_trg BEFORE INSERT ON identity FOR EACH ROW EXECUTE FUNCTION test_replacement_barrier()`)
	done := make(chan error, 1)
	go func() {
		_, err := f.svc.ReplaceIdentity(ctx, p, id, enum.IdentityPhone, phone2, plain, meta1)
		done <- err
	}()
	replacementWait(t, ctx, f, barrier, done)
	newID, err := ids.New(ids.Identity)
	if err != nil {
		t.Fatal(err)
	}
	digest, version := f.dig.Digest(phone2)
	ct, cv, err := f.ciph.Encrypt(phone2)
	if err != nil {
		t.Fatal(err)
	}
	dv, ev := int16(version), int16(cv)
	if _, err := f.repo.Q().CreateIdentity(ctx, db.CreateIdentityParams{ID: newID, UserID: other.UserID, Kind: enum.IdentityPhone, SubjectDigest: &digest, DigestKeyVersion: &dv, SubjectCiphertext: ct, CipherKeyVersion: &ev}); err != nil {
		t.Fatal(err)
	}
	if err := barrier.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, user.ErrIdentityConflict) {
		t.Fatalf("unique index conflict: %v", err)
	}
	list, err := f.svc.ListIdentities(ctx, p.UserID)
	if err != nil || len(list) != 1 || list[0].ID != id {
		t.Fatalf("unique conflict failed rollback: %+v %v", list, err)
	}
}

func TestReplaceIdentityRedisRevocationFailure(t *testing.T) {
	f, p, id, current := replacementSetup(t, enum.IdentityPhone, phone1)
	ctx := context.Background()
	other := f.signIn(t, enum.IdentityPhone, phone1, user.Device{ID: "redis-failure-other"})
	var logs bytes.Buffer
	deps := f.deps
	deps.Logger = slog.New(slog.NewTextHandler(&logs, nil))
	// 仅吊销写入不可用；验证码存储仍使用健康的独立客户端。
	dead := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1, DialTimeout: 50 * time.Millisecond})
	defer dead.Close()
	deps.Revocation = revocation.NewSet(dead, "t:")
	var err error
	f.svc, err = user.NewService(deps)
	if err != nil {
		t.Fatal(err)
	}
	plain := replacementCode(t, f, p, enum.IdentityPhone, phone2)
	if _, err := f.svc.ReplaceIdentity(ctx, p, id, enum.IdentityPhone, phone2, plain, meta1); err != nil {
		t.Fatalf("committed replacement must stay successful: %v", err)
	}
	if !strings.Contains(logs.String(), "revocation set write failed (fail-open)") {
		t.Fatal("missing Redis warning")
	}
	if _, err := f.svc.Refresh(ctx, other.RefreshToken, meta1); !errors.Is(err, user.ErrInvalidGrant) {
		t.Fatalf("database revocation bypassed: %v", err)
	}
	if _, err := f.svc.Refresh(ctx, current.RefreshToken, meta1); err != nil {
		t.Fatalf("current refresh: %v", err)
	}
	if _, err := f.svc.Authenticate(ctx, other.AccessToken); err != nil {
		t.Fatalf("existing access fail-open changed: %v", err)
	}
}
