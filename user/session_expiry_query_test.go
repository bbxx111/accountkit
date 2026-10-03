package user_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/ids"
	"github.com/bbxx111/accountkit/user/db"
)

// TestSessionExpiryQueries 验证严格到期过滤与写入保护，并保留旧 access 所需的未吊销查询/撤销范围。
func TestSessionExpiryQueries(t *testing.T) {
	t.Run("list_and_count_are_read_only", func(t *testing.T) {
		f := newFixture(t)
		ctx := context.Background()
		at := time.Date(2030, 1, 2, 3, 4, 5, 123456000, time.UTC)
		*f.clock = at.In(time.FixedZone("UTC+8", 8*3600)).Add(999 * time.Nanosecond)
		q := f.repo.Q()
		uid, _ := ids.New(ids.User)
		other, _ := ids.New(ids.User)
		for _, id := range []string{uid, other} {
			if _, err := q.CreateUser(ctx, db.CreateUserParams{ID: id, State: enum.UserActive}); err != nil {
				t.Fatal(err)
			}
		}
		var before []db.Session
		var wantIDs []string
		for _, tc := range []struct {
			user    string
			expiry  time.Time
			revoked bool
			visible bool
		}{
			{uid, at.Add(-time.Microsecond), false, false},
			{uid, at, false, false},
			{uid, at.Add(time.Microsecond), false, true},
			{uid, at.Add(time.Hour), false, true},
			{uid, at.Add(time.Hour), true, false},
			{other, at.Add(time.Hour), false, false},
		} {
			sid, _ := ids.New(ids.Session)
			sess, err := q.CreateSession(ctx, db.CreateSessionParams{ID: sid, UserID: tc.user, DeviceID: sid,
				AuthTime: at.Add(-time.Hour), RefreshTokenHash: sha(sid), RefreshExpireTime: tc.expiry})
			if err != nil {
				t.Fatal(err)
			}
			// 相同创建时刻验证既有 ID 次级排序。
			if _, err := f.pool.Exec(ctx, "UPDATE session SET create_time = $1 WHERE id = $2", at, sid); err != nil {
				t.Fatal(err)
			}
			if tc.revoked {
				reason := enum.RevokeUserLogout
				if _, err := q.RevokeSession(ctx, db.RevokeSessionParams{ID: sid, Reason: &reason, Now: at.Add(-time.Minute)}); err != nil {
					t.Fatal(err)
				}
			}
			sess, err = q.GetSessionByRefreshHash(ctx, sha(sid))
			if err != nil {
				t.Fatal(err)
			}
			before = append(before, sess)
			if tc.visible {
				wantIDs = append(wantIDs, sid)
			}
		}
		// 显式比较字符串次序，不依赖 ID 创建顺序恰好与数据库 C collation 一致。
		if wantIDs[0] > wantIDs[1] {
			wantIDs[0], wantIDs[1] = wantIDs[1], wantIDs[0]
		}
		consumer, err := f.svc.ListSessions(ctx, uid, wantIDs[0])
		if err != nil {
			t.Fatal(err)
		}
		var gotIDs []string
		for _, sess := range consumer {
			gotIDs = append(gotIDs, sess.ID)
		}
		if !reflect.DeepEqual(gotIDs, wantIDs) {
			t.Errorf("consumer IDs = %v, want %v", gotIDs, wantIDs)
		}
		admin, err := f.svc.AdminListSessions(ctx, uid)
		if err != nil {
			t.Fatal(err)
		}
		gotIDs = nil
		for _, sess := range admin {
			gotIDs = append(gotIDs, sess.ID)
		}
		if !reflect.DeepEqual(gotIDs, wantIDs) {
			t.Errorf("admin IDs = %v, want %v", gotIDs, wantIDs)
		}
		detail, err := f.svc.GetUserDetail(ctx, uid)
		if err != nil {
			t.Fatal(err)
		}
		if detail.ActiveSessionCount != 2 {
			t.Errorf("active_session_count = %d, want 2", detail.ActiveSessionCount)
		}
		if empty, err := f.svc.ListSessions(ctx, other, ""); err != nil || len(empty) != 1 {
			t.Errorf("other user's isolated list = %+v, %v", empty, err)
		}
		emptyID, _ := ids.New(ids.User)
		if _, err := q.CreateUser(ctx, db.CreateUserParams{ID: emptyID, State: enum.UserActive}); err != nil {
			t.Fatal(err)
		}
		if empty, err := f.svc.ListSessions(ctx, emptyID, ""); err != nil || empty == nil || len(empty) != 0 {
			t.Errorf("empty user's list = %+v, %v", empty, err)
		}
		for _, sess := range before {
			after, err := q.GetSessionByRefreshHash(ctx, sess.RefreshTokenHash)
			if err != nil || !reflect.DeepEqual(after, sess) {
				t.Errorf("read mutated session %s: err=%v", sess.ID, err)
			}
		}
		if events := f.audit.Events(); len(events) != 0 {
			t.Errorf("read added audit events: %+v", events)
		}
		if keys := f.mr.Keys(); len(keys) != 0 {
			t.Errorf("read wrote Redis keys: %v", keys)
		}
		u, err := q.GetUserByID(ctx, uid)
		if err != nil || u.State != enum.UserActive {
			t.Errorf("read changed user: %+v %v", u, err)
		}
	})

	t.Run("strict_write_guards_and_unrevoked_scope", func(t *testing.T) {
		pool := testPool(t)
		ctx := context.Background()
		q := db.New(pool)
		at := time.Date(2030, 1, 2, 3, 4, 5, 123456000, time.UTC)
		uid, _ := ids.New(ids.User)
		if _, err := q.CreateUser(ctx, db.CreateUserParams{ID: uid, State: enum.UserActive}); err != nil {
			t.Fatal(err)
		}
		for _, offset := range []time.Duration{-time.Microsecond, 0, time.Microsecond} {
			for _, operation := range []string{"rotate", "reauthenticate"} {
				t.Run(offset.String()+"/"+operation, func(t *testing.T) {
					sid, _ := ids.New(ids.Session)
					before, err := q.CreateSession(ctx, db.CreateSessionParams{ID: sid, UserID: uid, DeviceID: sid,
						AuthTime: at.Add(-time.Hour), RefreshTokenHash: sha(sid), RefreshExpireTime: at.Add(offset)})
					if err != nil {
						t.Fatal(err)
					}
					var n int64
					if operation == "rotate" {
						n, err = q.RotateSession(ctx, db.RotateSessionParams{ID: sid, OldHash: sha(sid), NewHash: sha("new:" + sid), Now: at, RefreshExpireTime: at.Add(time.Hour)})
					} else {
						n, err = q.UpdateSessionAuthTime(ctx, db.UpdateSessionAuthTimeParams{ID: sid, AuthTime: at})
					}
					want := int64(0)
					if offset > 0 {
						want = 1
					}
					if err != nil || n != want {
						t.Errorf("write rows = %d, %v; want %d", n, err, want)
					}
					after, err := q.GetActiveSessionByIDAndUser(ctx, db.GetActiveSessionByIDAndUserParams{ID: sid, UserID: uid})
					if err != nil {
						t.Fatal(err)
					}
					if offset <= 0 && !reflect.DeepEqual(after, before) {
						t.Errorf("expired write changed stored session")
					}
					device, err := q.GetActiveSessionByUserDevice(ctx, db.GetActiveSessionByUserDeviceParams{UserID: uid, DeviceID: sid})
					if err != nil || device.ID != sid {
						t.Errorf("unrevoked device lookup lost expired session: %v", err)
					}
					if _, err := q.GetActiveSessionByIDAndUser(ctx, db.GetActiveSessionByIDAndUserParams{ID: sid, UserID: "u_0000000000000"}); !errors.Is(err, pgx.ErrNoRows) {
						t.Errorf("foreign lookup = %v", err)
					}
					if offset <= 0 {
						reason := enum.RevokeUserLogout
						n, err := q.RevokeSession(ctx, db.RevokeSessionParams{ID: sid, Reason: &reason, Now: at})
						if err != nil || n != 1 {
							t.Errorf("expired single revoke = %d, %v", n, err)
						}
					}
				})
			}
		}
	})

	t.Run("expired_bulk_revocation_is_not_filtered", func(t *testing.T) {
		pool := testPool(t)
		ctx := context.Background()
		q := db.New(pool)
		at := time.Date(2030, 1, 2, 3, 4, 5, 123456000, time.UTC)
		uid, _ := ids.New(ids.User)
		if _, err := q.CreateUser(ctx, db.CreateUserParams{ID: uid, State: enum.UserActive}); err != nil {
			t.Fatal(err)
		}
		var sids []string
		for _, expiry := range []time.Time{at.Add(-time.Microsecond), at} {
			sid, _ := ids.New(ids.Session)
			if _, err := q.CreateSession(ctx, db.CreateSessionParams{ID: sid, UserID: uid, DeviceID: sid,
				AuthTime: at.Add(-time.Hour), RefreshTokenHash: sha(sid), RefreshExpireTime: expiry}); err != nil {
				t.Fatal(err)
			}
			sids = append(sids, sid)
		}
		reason := enum.RevokeUserLogout
		revoked, err := q.RevokeSessionsByUser(ctx, db.RevokeSessionsByUserParams{UserID: uid, Reason: &reason, Now: at})
		if err != nil || len(revoked) != 2 {
			t.Fatalf("expired bulk revoke = %v, %v", revoked, err)
		}
		for _, sid := range sids {
			sess, err := q.GetSessionByRefreshHash(ctx, sha(sid))
			if err != nil || sess.RevokeTime == nil || !sess.RevokeTime.Equal(at) || sess.RevokeReason == nil || *sess.RevokeReason != reason {
				t.Errorf("expired session not revoked: %+v, %v", sess, err)
			}
		}
	})
}
