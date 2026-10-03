package user_test

import (
	"context"
	"errors"
	"net"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/session/grace"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/db"
)

// 原子时钟只替换服务既有依赖；等待阶段不再访问 fixture 的可变时钟。
func refreshExpiryClock(t *testing.T, f *fixture) *atomic.Value {
	t.Helper()
	clock := &atomic.Value{}
	clock.Store(*f.clock)
	f.deps.Now = func() time.Time { return clock.Load().(time.Time) }
	refreshExpiryService(t, f)
	return clock
}

// TraceQueryEnd 暂停真实查询完成向调用方交付的时刻，避免用 sleep 猜测读取阶段。
type refreshQueryGate struct {
	query            string
	occurrence       int32
	seen             atomic.Int32
	entered, release chan struct{}
}
type refreshQueryGateKey struct{}

func (g *refreshQueryGate) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.Contains(data.SQL, "-- name: "+g.query+" ") && g.seen.Add(1) == g.occurrence {
		return context.WithValue(ctx, refreshQueryGateKey{}, true)
	}
	return ctx
}
func (g *refreshQueryGate) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
	if ctx.Value(refreshQueryGateKey{}) != true {
		return
	}
	close(g.entered)
	select {
	case <-g.release:
	case <-ctx.Done():
	}
}
func refreshWithQueryGate(t *testing.T, f *fixture, query string, occurrence int32) (*user.Service, *refreshQueryGate) {
	t.Helper()
	gate := &refreshQueryGate{query: query, occurrence: occurrence, entered: make(chan struct{}), release: make(chan struct{})}
	cfg := f.pool.Config()
	cfg.ConnConfig.Tracer = gate
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	deps := f.deps
	deps.Repo = user.NewRepo(pool)
	svc, err := user.NewService(deps)
	if err != nil {
		t.Fatal(err)
	}
	return svc, gate
}

func TestRefreshExpiryAfterQueries(t *testing.T) {
	for _, tc := range []struct {
		name, query string
		occurrence  int32
		previous    bool
	}{
		{"current", "GetSessionByRefreshHash", 1, false},
		{"previous", "GetSessionByPreviousRefreshHash", 1, true},
		{"current-user", "GetUserByID", 1, false},
		{"previous-user", "GetUserByID", 1, true},
		{"locked-user", "GetUserByID", 2, false},
		{"locked-anchor", "ListActiveIdentitiesByUser", 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			first := f.signIn(t, enum.IdentityPhone, phone1, dev1)
			clock := refreshExpiryClock(t, f)
			current := first
			if tc.previous {
				var err error
				current, err = f.svc.Refresh(context.Background(), first.RefreshToken, meta1)
				if err != nil {
					t.Fatal(err)
				}
			}
			deadline := clock.Load().(time.Time).Add(time.Second)
			mustExec(t, f, `UPDATE session SET refresh_expire_time = $1 WHERE user_id = $2`, deadline, first.UserID)
			before := refreshExpirySession(t, f, sha256Of(current.RefreshToken))
			keys := f.mr.Keys()
			svc, gate := refreshWithQueryGate(t, f, tc.query, tc.occurrence)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			meta := user.Meta{RequestID: "expiry-query"}
			done := startReplacementRace(func() replacementRaceResult {
				res, err := svc.Refresh(ctx, first.RefreshToken, meta)
				return replacementRaceResult{token: res, err: err}
			})
			select {
			case <-gate.entered:
			case <-ctx.Done():
				t.Fatal("query not reached")
			}
			clock.Store(deadline)
			close(gate.release)
			out := finishReplacementRace(t, ctx, done)
			if !errors.Is(out.err, user.ErrInvalidGrant) || out.token.AccessToken != "" {
				t.Fatalf("expired after query: %v", out.err)
			}
			assertRefreshExpiryAudit(t, f, meta.RequestID, "SESSION_EXPIRED")
			if !reflect.DeepEqual(before, refreshExpirySession(t, f, sha256Of(current.RefreshToken))) {
				t.Fatal("expired refresh changed session")
			}
			if !reflect.DeepEqual(keys, f.mr.Keys()) {
				t.Fatal("expired refresh changed Redis keys")
			}
		})
	}
}

func TestRefreshGraceEligibilityAfterQueryAndCAS(t *testing.T) {
	for _, cas := range []bool{false, true} {
		for _, offset := range []time.Duration{-time.Microsecond, 0, time.Microsecond} {
			name := "previous-query/"
			if cas {
				name = "cas-miss/"
			}
			t.Run(name+offset.String(), func(t *testing.T) {
				f := newFixture(t)
				first := f.signIn(t, enum.IdentityPhone, phone1, dev1)
				clock := refreshExpiryClock(t, f)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				var winner user.TokenResult
				query := "GetSessionByRefreshHash"
				if !cas {
					var err error
					winner, err = f.svc.Refresh(ctx, first.RefreshToken, meta1)
					if err != nil {
						t.Fatal(err)
					}
					query = "GetSessionByPreviousRefreshHash"
				}
				svc, gate := refreshWithQueryGate(t, f, query, 1)
				meta := user.Meta{RequestID: "eligibility"}
				done := startReplacementRace(func() replacementRaceResult {
					res, err := svc.Refresh(ctx, first.RefreshToken, meta)
					return replacementRaceResult{token: res, err: err}
				})
				select {
				case <-gate.entered:
				case <-ctx.Done():
					t.Fatal("query not reached")
				}
				if cas {
					var err error
					winner, err = f.svc.Refresh(ctx, first.RefreshToken, meta1)
					if err != nil {
						t.Fatal(err)
					}
				}
				before := refreshExpirySession(t, f, sha256Of(winner.RefreshToken))
				decision := clock.Load().(time.Time).Add(30*time.Second + offset)
				clock.Store(decision)
				close(gate.release)
				out := finishReplacementRace(t, ctx, done)
				after := refreshExpirySession(t, f, sha256Of(winner.RefreshToken))
				if offset > 0 {
					if !errors.Is(out.err, user.ErrInvalidGrant) || after.RevokeReason == nil || *after.RevokeReason != enum.RevokeReuseDetected {
						t.Fatalf("late eligibility must revoke: %v reason=%v", out.err, after.RevokeReason)
					}
					if !hasEventReason(f.audit, enum.EventRefreshReuseDetected, enum.ResultFailure, "TOKEN_REUSE") {
						t.Fatal("missing reuse audit")
					}
				} else {
					if out.err != nil || out.token.AccessToken != winner.AccessToken || out.token.RefreshToken != winner.RefreshToken {
						t.Fatalf("eligible retry must return winner pair: %v", out.err)
					}
					if out.token.ExpiresIn != 870 || out.token.RefreshExpiresIn != 2591970 {
						t.Fatalf("eligibility TTLs: %d/%d", out.token.ExpiresIn, out.token.RefreshExpiresIn)
					}
					if !reflect.DeepEqual(before, after) {
						t.Fatal("grace extended or changed session")
					}
				}
			})
		}
	}
}

func refreshExpiryService(t *testing.T, f *fixture) {
	t.Helper()
	var err error
	f.svc, err = user.NewService(f.deps)
	if err != nil {
		t.Fatal(err)
	}
}

func refreshExpirySession(t *testing.T, f *fixture, hash []byte) db.Session {
	t.Helper()
	sess, err := f.repo.Q().GetSessionByRefreshHash(context.Background(), hash)
	if err != nil {
		t.Fatal(err)
	}
	return sess
}

func assertRefreshExpiryAudit(t *testing.T, f *fixture, requestID, reason string) {
	t.Helper()
	count := 0
	for _, e := range f.audit.Events() {
		if e.RequestID != requestID {
			continue
		}
		count++
		if e.Type != enum.EventRefreshRejected || e.Result != enum.ResultFailure || e.Reason != reason {
			t.Errorf("rejection audit: type=%v result=%v reason=%q", e.Type, e.Result, e.Reason)
		}
	}
	if count != 1 {
		t.Errorf("request audit count = %d, want 1", count)
	}
}

func TestRefreshExpiryAfterSessionLock(t *testing.T) {
	for _, offset := range []time.Duration{0, time.Microsecond} {
		t.Run(offset.String(), func(t *testing.T) { refreshAfterSessionLock(t, offset, true) })
	}
}

func TestRefreshUsesLockedDecisionTime(t *testing.T) {
	refreshAfterSessionLock(t, -time.Microsecond, false)
}

func refreshAfterSessionLock(t *testing.T, offset time.Duration, expired bool) {
	t.Helper()
	f := newFixture(t)
	first := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	clock := refreshExpiryClock(t, f)
	deadline := f.clock.Add(10 * time.Second)
	mustExec(t, f, `UPDATE session SET refresh_expire_time = $1 WHERE user_id = $2`, deadline, first.UserID)
	before := refreshExpirySession(t, f, sha256Of(first.RefreshToken))
	keys := f.mr.Keys()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(ctx, `SELECT id FROM session WHERE id = $1 FOR UPDATE`, before.ID); err != nil {
		t.Fatal(err)
	}
	meta := user.Meta{RequestID: "expiry-lock"}
	done := startReplacementRace(func() replacementRaceResult {
		res, err := f.svc.Refresh(ctx, first.RefreshToken, meta)
		return replacementRaceResult{token: res, err: err}
	})
	replacementWaitForPID(t, ctx, f, tx.Conn().PgConn().PID(), done)
	decision := deadline.Add(offset)
	clock.Store(decision)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	out := finishReplacementRace(t, ctx, done)
	if expired {
		if !errors.Is(out.err, user.ErrInvalidGrant) {
			t.Fatalf("expired after lock: %v", out.err)
		}
		if out.token.AccessToken != "" || out.token.RefreshToken != "" {
			t.Fatal("expired refresh issued tokens")
		}
		after := refreshExpirySession(t, f, sha256Of(first.RefreshToken))
		if !reflect.DeepEqual(before, after) {
			t.Fatal("expired refresh changed session")
		}
		if !reflect.DeepEqual(keys, f.mr.Keys()) {
			t.Fatal("expired refresh changed Redis keys")
		}
		assertRefreshExpiryAudit(t, f, meta.RequestID, "SESSION_EXPIRED")
		return
	}
	if out.err != nil {
		t.Fatal(out.err)
	}
	after := refreshExpirySession(t, f, sha256Of(out.token.RefreshToken))
	if after.RotateTime == nil || !after.RotateTime.Equal(decision) || !after.LastUsedTime.Equal(decision) || !after.RefreshExpireTime.Equal(decision.Add(720*time.Hour)) {
		t.Fatalf("rotation must use locked decision time: rotate=%v last=%v expiry=%v decision=%v", after.RotateTime, after.LastUsedTime, after.RefreshExpireTime, decision)
	}
	claims, err := f.deps.Signer.Parse(out.token.AccessToken)
	if err != nil || !claims.IssuedAt.Equal(decision.Truncate(time.Second)) || !claims.ExpiresAt.Equal(decision.Add(15*time.Minute).Truncate(time.Second)) {
		t.Fatalf("token times: %+v %v", claims, err)
	}
	pair, ok, err := f.deps.Grace.Get(ctx, sha256Of(first.RefreshToken))
	if err != nil || !ok || !pair.AccessExpiresAt.Equal(decision.Add(15*time.Minute)) || !pair.RefreshExpiresAt.Equal(after.RefreshExpireTime) {
		t.Fatal("cached pair must use locked decision time", err)
	}
}

// 在真实 Redis GET 返回后暂停，重现缓存等待期间时钟推进；不改变缓存内容或生产路径。
type refreshGraceGate struct {
	entered, release chan struct{}
	fail             bool
}

func (g *refreshGraceGate) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) { return next(ctx, network, addr) }
}
func (g *refreshGraceGate) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (g *refreshGraceGate) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		err := next(ctx, cmd)
		if cmd.Name() == "get" && strings.Contains(cmd.Args()[1].(string), ":grace:") {
			close(g.entered)
			select {
			case <-g.release:
			case <-ctx.Done():
				return ctx.Err()
			}
			if g.fail {
				return errors.New("test grace transport failure")
			}
		}
		return err
	}
}

func TestRefreshGraceExpiryBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name                                    string
		wait, sessionTTL, accessTTL, refreshTTL time.Duration
		missing, failure                        bool
		reason                                  string
	}{
		{name: "wait-crosses-grace", wait: 31 * time.Second, sessionTTL: time.Hour, accessTTL: 15 * time.Minute, refreshTTL: time.Hour},
		{name: "wait-reaches-session", wait: 10 * time.Second, sessionTTL: 10 * time.Second, accessTTL: time.Minute, refreshTTL: time.Hour, reason: "SESSION_EXPIRED"},
		{name: "wait-passes-session", wait: 11 * time.Second, sessionTTL: 10 * time.Second, accessTTL: time.Minute, refreshTTL: time.Hour, reason: "SESSION_EXPIRED"},
		{name: "access-pair-equal", wait: 10 * time.Second, sessionTTL: time.Hour, accessTTL: 10 * time.Second, refreshTTL: time.Hour, reason: "GRACE_UNAVAILABLE"},
		{name: "refresh-pair-expired", wait: 11 * time.Second, sessionTTL: time.Hour, accessTTL: time.Minute, refreshTTL: 10 * time.Second, reason: "GRACE_UNAVAILABLE"},
		{name: "missing", sessionTTL: time.Hour, accessTTL: time.Minute, refreshTTL: time.Hour, missing: true, reason: "GRACE_UNAVAILABLE"},
		{name: "failure", sessionTTL: time.Hour, accessTTL: time.Minute, refreshTTL: time.Hour, failure: true, reason: "GRACE_UNAVAILABLE"},
		{name: "failure-after-session-expiry", wait: 10 * time.Second, sessionTTL: 10 * time.Second, accessTTL: time.Minute, refreshTTL: time.Hour, failure: true, reason: "SESSION_EXPIRED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			first := f.signIn(t, enum.IdentityPhone, phone1, dev1)
			clock := refreshExpiryClock(t, f)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			next, err := f.svc.Refresh(ctx, first.RefreshToken, meta1)
			if err != nil {
				t.Fatal(err)
			}
			start := clock.Load().(time.Time)
			mustExec(t, f, `UPDATE session SET refresh_expire_time = $1 WHERE user_id = $2`, start.Add(tc.sessionTTL), first.UserID)
			before := refreshExpirySession(t, f, sha256Of(next.RefreshToken))
			rdb := redis.NewClient(&redis.Options{Addr: f.mr.Addr(), MaxRetries: -1})
			t.Cleanup(func() { _ = rdb.Close() })
			f.deps.Grace = grace.NewCache(rdb, "t:", f.ciph)
			pair := grace.Pair{AccessToken: next.AccessToken, RefreshToken: next.RefreshToken, Scope: next.Scope, AccessExpiresAt: start.Add(tc.accessTTL), RefreshExpiresAt: start.Add(tc.refreshTTL)}
			if err := f.deps.Grace.Put(ctx, sha256Of(first.RefreshToken), pair, time.Minute); err != nil {
				t.Fatal(err)
			}
			if tc.missing {
				f.mr.FlushAll()
			}
			keys := f.mr.Keys()
			gate := &refreshGraceGate{entered: make(chan struct{}), release: make(chan struct{}), fail: tc.failure}
			rdb.AddHook(gate)
			refreshExpiryService(t, f)
			meta := user.Meta{RequestID: "expiry-grace"}
			done := startReplacementRace(func() replacementRaceResult {
				res, err := f.svc.Refresh(ctx, first.RefreshToken, meta)
				return replacementRaceResult{token: res, err: err}
			})
			select {
			case <-gate.entered:
			case <-ctx.Done():
				t.Fatal("grace GET not reached")
			}
			clock.Store(start.Add(tc.wait))
			close(gate.release)
			out := finishReplacementRace(t, ctx, done)
			if tc.reason != "" {
				if !errors.Is(out.err, user.ErrInvalidGrant) || out.token.AccessToken != "" {
					t.Fatalf("expected invalid_grant with no token, got %v", out.err)
				}
				assertRefreshExpiryAudit(t, f, meta.RequestID, tc.reason)
			} else {
				if out.err != nil || out.token.AccessToken != next.AccessToken || out.token.RefreshToken != next.RefreshToken {
					t.Fatalf("eligible grace must return same pair: %v", out.err)
				}
				if out.token.ExpiresIn != 869 || out.token.RefreshExpiresIn != 3569 {
					t.Errorf("remaining TTLs = %d/%d, want 869/3569", out.token.ExpiresIn, out.token.RefreshExpiresIn)
				}
			}
			if after := refreshExpirySession(t, f, sha256Of(next.RefreshToken)); !reflect.DeepEqual(before, after) {
				t.Fatal("grace changed session")
			}
			if !reflect.DeepEqual(keys, f.mr.Keys()) {
				t.Fatal("grace changed Redis keys")
			}
		})
	}
}
