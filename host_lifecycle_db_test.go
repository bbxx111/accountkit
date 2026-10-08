package accountkit_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type activeUsers interface {
	WithActiveUsers(context.Context, []string, func(pgx.Tx) error) error
}

func hostActiveUsers(t *testing.T, a *accountkit.Auth) activeUsers {
	t.Helper()
	s, ok := any(a.Users()).(activeUsers)
	if !ok {
		t.Fatal("public WithActiveUsers transaction guard is missing")
	}
	return s
}

func hostLifecycleFixture(t *testing.T, hook func(context.Context, pgx.Tx, string) error) (*accountkit.Auth, *pgxpool.Pool, sourceFixture) {
	t.Helper()
	rdb := testRedis(t)
	a, pool, sent := integrationInstance(t, minimal(), rdb)
	f, _ := seedSourceFixture(t, pool, a.Config())
	if err := a.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), "CREATE TABLE host_note(user_id text PRIMARY KEY, body text); INSERT INTO host_note VALUES('u_0000000000001','original')"); err != nil {
		t.Fatal(err)
	}
	deps := accountkit.Deps{Pool: pool, Redis: rdb, SMSSender: sent, EmailSender: sent, Audit: audit.Noop{}}
	deps.BeforeDelete = hook
	withHost, err := accountkit.New(a.Config(), deps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(withHost.Close)
	return withHost, pool, f
}

func TestBeforeDeleteSharedTransaction(t *testing.T) {
	blocked := user.ErrDeletionBlocked
	for _, admin := range []bool{false, true} {
		for _, result := range []string{"success", "blocked", "internal"} {
			t.Run(fmt.Sprintf("admin=%v/%s", admin, result), func(t *testing.T) {
				calls := 0
				var hookErr error
				if result == "blocked" {
					hookErr = blocked
				}
				if result == "internal" {
					hookErr = errors.New("fixture private infrastructure detail")
				}
				a, pool, f := hostLifecycleFixture(t, func(ctx context.Context, tx pgx.Tx, id string) error {
					calls++
					var state enum.UserState
					if err := tx.QueryRow(ctx, "SELECT state FROM user_account WHERE id=$1", id).Scan(&state); err != nil {
						return err
					}
					if state != enum.UserActive {
						return fmt.Errorf("hook saw state %v", state)
					}
					_, err := tx.Exec(ctx, "UPDATE host_note SET body='changed' WHERE user_id=$1", id)
					if err != nil {
						return err
					}
					return hookErr
				})
				before := snapshotSource(t, pool)
				var err error
				if admin {
					_, err = a.Users().AdminDeleteUser(context.Background(), user.Admin{Subject: "fixture-admin"}, f.UserID, user.Meta{})
				} else {
					_, err = a.Users().DeleteMe(context.Background(), user.Principal{UserID: f.UserID, SessionID: f.SessionID}, user.Meta{})
				}
				if calls != 1 || (hookErr == nil && err != nil) || (result == "blocked" && !errors.Is(err, blocked)) || (result == "internal" && (err == nil || !strings.Contains(err.Error(), "fixture private infrastructure detail"))) {
					t.Fatalf("delete=%v hook calls=%d want=%v", err, calls, hookErr)
				}
				var body string
				if err := pool.QueryRow(context.Background(), "SELECT body FROM host_note WHERE user_id=$1", f.UserID).Scan(&body); err != nil {
					t.Fatal(err)
				}
				if hookErr != nil {
					if snapshotSource(t, pool) != before || body != "original" {
						t.Fatal("rejected delete mutated authentication or host data")
					}
				} else {
					if body != "changed" {
						t.Fatal("host write not committed with deletion")
					}
					var state enum.UserState
					var revoked bool
					if err := pool.QueryRow(context.Background(), "SELECT state FROM user_account WHERE id=$1", f.UserID).Scan(&state); err != nil {
						t.Fatal(err)
					}
					if err := pool.QueryRow(context.Background(), "SELECT revoke_time IS NOT NULL FROM session WHERE id=$1", f.SessionID).Scan(&revoked); err != nil {
						t.Fatal(err)
					}
					if state != enum.UserPendingDeletion || !revoked {
						t.Fatal("deletion and revocation not committed")
					}
				}
			})
		}
	}
}

func TestWithActiveUsers(t *testing.T) {
	a, pool, f := hostLifecycleFixture(t, nil)
	s := hostActiveUsers(t, a)
	ctx := context.Background()
	callback := func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "UPDATE host_note SET body='changed' WHERE user_id=$1", f.UserID)
		return err
	}
	if err := s.WithActiveUsers(ctx, []string{f.UserID, f.UserID}, callback); err != nil {
		t.Fatal(err)
	}
	var body string
	if err := pool.QueryRow(ctx, "SELECT body FROM host_note").Scan(&body); err != nil || body != "changed" {
		t.Fatalf("committed host write: %s %v", body, err)
	}
	for _, tc := range []struct {
		name  string
		ids   []string
		state int
		want  error
	}{
		{"empty", nil, 1, user.ErrInvalidArgument},
		{"invalid", []string{f.UserID, "bad-id"}, 1, user.ErrInvalidArgument},
		{"unknown", []string{f.UserID, "u_0000000000000"}, 1, user.ErrNotFound},
		{"frozen", []string{f.UserID}, 2, user.ErrUserFrozen},
		{"pending", []string{f.UserID}, 3, user.ErrInvalidState},
		{"deleted", []string{f.UserID}, 4, user.ErrInvalidState},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, "UPDATE user_account SET state=$1 WHERE id=$2", tc.state, f.UserID); err != nil {
				t.Fatal(err)
			}
			called := false
			err := s.WithActiveUsers(ctx, tc.ids, func(pgx.Tx) error { called = true; return nil })
			if !errors.Is(err, tc.want) || called {
				t.Fatalf("guard=%v callback=%v", err, called)
			}
		})
	}
	if err := s.WithActiveUsers(ctx, []string{f.UserID}, nil); !errors.Is(err, user.ErrInvalidArgument) {
		t.Fatalf("nil callback: %v", err)
	}
}

func TestWithActiveUsersRollback(t *testing.T) {
	for _, panics := range []bool{false, true} {
		t.Run(fmt.Sprintf("panic=%v", panics), func(t *testing.T) {
			a, pool, f := hostLifecycleFixture(t, nil)
			s := hostActiveUsers(t, a)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			before := snapshotSource(t, pool)
			marker := errors.New("callback failure")
			func() {
				if panics {
					defer func() {
						if recover() != marker {
							t.Error("callback panic did not propagate")
						}
					}()
				}
				err := s.WithActiveUsers(ctx, []string{f.UserID}, func(tx pgx.Tx) error {
					if _, err := tx.Exec(ctx, "UPDATE host_note SET body='changed'; UPDATE user_account SET display_name='changed'"); err != nil {
						return err
					}
					if panics {
						panic(marker)
					}
					return marker
				})
				if panics {
					t.Error("panic swallowed")
				} else if !errors.Is(err, marker) {
					t.Fatalf("callback error: %v", err)
				}
			}()
			if snapshotSource(t, pool) != before {
				t.Fatal("account write escaped rollback")
			}
			var body string
			if err := pool.QueryRow(ctx, "SELECT body FROM host_note").Scan(&body); err != nil || body != "original" {
				t.Fatalf("host rollback: %s %v", body, err)
			}
			if err := s.WithActiveUsers(ctx, []string{f.UserID}, func(pgx.Tx) error { return nil }); err != nil {
				t.Fatalf("account lock not released: %v", err)
			}
			if pool.Stat().AcquiredConns() != 0 {
				t.Fatal("transaction leaked pool connection")
			}
		})
	}
}

func TestWithActiveUsersReverseOrder(t *testing.T) {
	a, pool, f := hostLifecycleFixture(t, nil)
	s := hostActiveUsers(t, a)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	second := "u_0000000000002"
	if _, err := pool.Exec(ctx, "INSERT INTO user_account(id,state) VALUES($1,1)", second); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "CREATE TABLE host_team(id int PRIMARY KEY, changes int); INSERT INTO host_team VALUES(1,0)"); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	done := make(chan error, 2)
	for _, ids := range [][]string{{f.UserID, second, f.UserID}, {second, f.UserID, second}} {
		go func(ids []string) {
			<-start
			for range 12 {
				if err := s.WithActiveUsers(ctx, ids, func(tx pgx.Tx) error {
					// 宿主业务资源锁只能在全部账号锁之后取得。
					_, err := tx.Exec(ctx, "UPDATE host_team SET changes=changes+1 WHERE id=1")
					return err
				}); err != nil {
					done <- err
					return
				}
			}
			done <- nil
		}(ids)
	}
	close(start)
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	var changes int
	if err := pool.QueryRow(ctx, "SELECT changes FROM host_team").Scan(&changes); err != nil || changes != 24 {
		t.Fatalf("serialized business updates=%d %v", changes, err)
	}
}

func TestHostDeleteCreateRace(t *testing.T) {
	blocked := user.ErrDeletionBlocked
	for _, createFirst := range []bool{true, false} {
		t.Run(fmt.Sprintf("create_first=%v", createFirst), func(t *testing.T) {
			entered := make(chan struct{})
			release := make(chan struct{})
			a, pool, f := hostLifecycleFixture(t, func(ctx context.Context, tx pgx.Tx, id string) error {
				var hasOwner bool
				if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM host_note WHERE user_id=$1)", id).Scan(&hasOwner); err != nil {
					return err
				}
				if hasOwner {
					return blocked
				}
				if !createFirst {
					close(entered)
					<-release
				}
				return nil
			})
			s := hostActiveUsers(t, a)
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			if _, err := pool.Exec(ctx, "DELETE FROM host_note"); err != nil {
				t.Fatal(err)
			}
			create := func() error {
				return s.WithActiveUsers(ctx, []string{f.UserID}, func(tx pgx.Tx) error {
					if _, err := tx.Exec(ctx, "INSERT INTO host_note VALUES($1,'owner')", f.UserID); err != nil {
						return err
					}
					if createFirst {
						close(entered)
						<-release
					}
					return nil
				})
			}
			deleteUser := func() error {
				_, err := a.Users().DeleteMe(ctx, user.Principal{UserID: f.UserID, SessionID: f.SessionID}, user.Meta{})
				return err
			}
			first, second := create, deleteUser
			if !createFirst {
				first, second = deleteUser, create
			}
			firstDone, secondDone := make(chan error, 1), make(chan error, 1)
			go func() { firstDone <- first() }()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			started := make(chan struct{})
			go func() { close(started); secondDone <- second() }()
			<-started
			// 第一笔事务仍持账号锁；第二笔不能提前完成。
			select {
			case err := <-secondDone:
				close(release)
				t.Fatalf("contender escaped account lock: %v", err)
			case <-time.After(50 * time.Millisecond):
			}
			close(release)
			if err := <-firstDone; err != nil {
				t.Fatal(err)
			}
			err := <-secondDone
			if createFirst && !errors.Is(err, blocked) {
				t.Fatalf("delete must see committed owner: %v", err)
			}
			if !createFirst && !errors.Is(err, user.ErrInvalidState) {
				t.Fatalf("creation after delete: %v", err)
			}
			var state enum.UserState
			var owner bool
			if err := pool.QueryRow(ctx, "SELECT state,EXISTS(SELECT 1 FROM host_note) FROM user_account WHERE id=$1", f.UserID).Scan(&state, &owner); err != nil {
				t.Fatal(err)
			}
			if createFirst && (state != enum.UserActive || !owner) {
				t.Fatal("owner-first outcome violates invariant")
			}
			if !createFirst && (state != enum.UserPendingDeletion || owner) {
				t.Fatal("delete-first outcome violates invariant")
			}
		})
	}
}

func TestBeforeDeleteHTTPErrorClassification(t *testing.T) {
	for _, admin := range []bool{false, true} {
		for _, tc := range []struct {
			name    string
			cause   error
			code    int
			status  string
			reason  string
			message string
		}{
			{"blocked", user.ErrDeletionBlocked, 400, "FAILED_PRECONDITION", "DELETION_BLOCKED", "account deletion is blocked"},
			{"infrastructure", errors.New("host unavailable"), 500, "INTERNAL", "", "internal error"},
			{"invalid_argument", user.ErrInvalidArgument, 500, "INTERNAL", "", "internal error"},
			{"not_found", user.ErrNotFound, 500, "INTERNAL", "", "internal error"},
			{"unavailable", user.ErrUnavailable, 500, "INTERNAL", "", "internal error"},
		} {
			t.Run(fmt.Sprintf("admin=%v/%s", admin, tc.name), func(t *testing.T) {
				ctx := context.Background()
				rdb := testRedis(t)
				a, pool, sent := integrationInstance(t, minimal(), rdb)
				f, access := seedSourceFixture(t, pool, a.Config())
				if err := a.Migrate(ctx); err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(ctx, "CREATE TABLE host_note(user_id text PRIMARY KEY, body text); INSERT INTO host_note VALUES('u_0000000000001','original')"); err != nil {
					t.Fatal(err)
				}
				calls := 0
				withHost, err := accountkit.New(a.Config(), accountkit.Deps{Pool: pool, Redis: rdb, SMSSender: sent, EmailSender: sent, Audit: audit.Noop{}, AdminVerifier: stubVerifier{}, AdminPrincipal: stubPrincipalFrom,
					BeforeDelete: func(ctx context.Context, tx pgx.Tx, id string) error {
						calls++
						if _, err := tx.Exec(ctx, "UPDATE host_note SET body='changed' WHERE user_id=$1", id); err != nil {
							return err
						}
						return fmt.Errorf("private host record detail: %w", tc.cause)
					},
				})
				if err != nil {
					t.Fatal(err)
				}
				defer withHost.Close()
				before := snapshotSource(t, pool)
				req := httptest.NewRequest("DELETE", "/users/me", nil)
				handler := withHost.EndUserHandler()
				req.Header.Set("Authorization", "Bearer "+access)
				if admin {
					req = httptest.NewRequest("DELETE", "/users/"+f.UserID, nil)
					req.Header.Set("X-Test-Admin", "fixture|admin|ops|super-admin")
					handler = stubVerifier{}.Middleware()(withHost.AdminHandler())
				}
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				var out struct {
					Error struct {
						Status  string `json:"status"`
						Reason  string `json:"reason"`
						Message string `json:"message"`
					} `json:"error"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
					t.Fatal(err)
				}
				if rec.Code != tc.code || out.Error.Status != tc.status || out.Error.Reason != tc.reason || out.Error.Message != tc.message || calls != 1 {
					t.Fatalf("hook calls=%d response=%d %s", calls, rec.Code, rec.Body.String())
				}
				if strings.Contains(rec.Body.String(), "private") {
					t.Fatal("private hook detail leaked")
				}
				if snapshotSource(t, pool) != before {
					t.Fatal("failed hook mutated authentication")
				}
				var body string
				if err := pool.QueryRow(ctx, "SELECT body FROM host_note").Scan(&body); err != nil || body != "original" {
					t.Fatalf("host rollback=%s %v", body, err)
				}
			})
		}
	}
}

func TestBeforeDeleteLocksAndChecksState(t *testing.T) {
	for _, admin := range []bool{false, true} {
		t.Run(fmt.Sprintf("admin=%v", admin), func(t *testing.T) {
			var pool *pgxpool.Pool
			calls := 0
			a, p, f := hostLifecycleFixture(t, func(ctx context.Context, tx pgx.Tx, id string) error {
				calls++
				contender, err := pool.Begin(ctx)
				if err != nil {
					return err
				}
				defer contender.Rollback(ctx)
				_, err = contender.Exec(ctx, "SELECT id FROM user_account WHERE id=$1 FOR UPDATE NOWAIT", id)
				var pgerr *pgconn.PgError
				if !errors.As(err, &pgerr) || pgerr.Code != "55P03" {
					return fmt.Errorf("hook ran without account lock: %v", err)
				}
				return user.ErrDeletionBlocked
			})
			pool = p
			ctx := context.Background()
			deleteUser := func(id string) error {
				if admin {
					_, err := a.Users().AdminDeleteUser(ctx, user.Admin{Subject: "admin"}, id, user.Meta{})
					return err
				}
				_, err := a.Users().DeleteMe(ctx, user.Principal{UserID: id}, user.Meta{})
				return err
			}
			for _, state := range []int{2, 3, 4} {
				if _, err := pool.Exec(ctx, "UPDATE user_account SET state=$1", state); err != nil {
					t.Fatal(err)
				}
				if err := deleteUser(f.UserID); err == nil || calls != 0 {
					t.Fatalf("state=%d error=%v hook calls=%d", state, err, calls)
				}
			}
			if err := deleteUser("u_0000000000000"); err == nil || calls != 0 {
				t.Fatalf("unknown error=%v hook calls=%d", err, calls)
			}
			if _, err := pool.Exec(ctx, "UPDATE user_account SET state=1"); err != nil {
				t.Fatal(err)
			}
			if err := deleteUser(f.UserID); !errors.Is(err, user.ErrDeletionBlocked) || calls != 1 {
				t.Fatalf("locked ACTIVE error=%v hook calls=%d", err, calls)
			}
		})
	}
}

type hostAccountLockTracer struct{ locked []string }

func (tr *hostAccountLockTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.Contains(data.SQL, "FOR UPDATE") && strings.Contains(data.SQL, "user_account") {
		tr.locked = append(tr.locked, data.Args[0].(string))
	}
	return ctx
}
func (*hostAccountLockTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestWithActiveUsersDeduplicatesAndLocksBeforeCallback(t *testing.T) {
	a, pool, f := hostLifecycleFixture(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	second := "u_0000000000002"
	if _, err := pool.Exec(ctx, "INSERT INTO user_account(id,state) VALUES($1,1)", second); err != nil {
		t.Fatal(err)
	}
	tracer := &hostAccountLockTracer{}
	pc := pool.Config()
	pc.ConnConfig.Tracer = tracer
	traced, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		t.Fatal(err)
	}
	defer traced.Close()
	rdb := testRedis(t)
	sent := &captureSender{}
	withTrace, err := accountkit.New(a.Config(), accountkit.Deps{Pool: traced, Redis: rdb, SMSSender: sent, EmailSender: sent, Audit: audit.Noop{}})
	if err != nil {
		t.Fatal(err)
	}
	defer withTrace.Close()
	if err := withTrace.Users().WithActiveUsers(ctx, []string{second, f.UserID, second, f.UserID}, func(tx pgx.Tx) error {
		for _, id := range []string{f.UserID, second} {
			contender, err := pool.Begin(ctx)
			if err != nil {
				return err
			}
			_, err = contender.Exec(ctx, "SELECT id FROM user_account WHERE id=$1 FOR UPDATE NOWAIT", id)
			_ = contender.Rollback(ctx)
			var pgerr *pgconn.PgError
			if !errors.As(err, &pgerr) || pgerr.Code != "55P03" {
				return fmt.Errorf("account %s not locked before callback: %v", id, err)
			}
		}
		_, err := tx.Exec(ctx, "UPDATE host_note SET body='locked' WHERE user_id=$1", f.UserID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(tracer.locked) != 2 || tracer.locked[0] != "u_0000000000001" || tracer.locked[1] != "u_0000000000002" {
		t.Fatalf("account locks=%v want two distinct sorted IDs", tracer.locked)
	}
}
