package user_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/internal/obstest"
	ob "github.com/bbxx111/accountkit/observability"
	"github.com/bbxx111/accountkit/session/grace"
	"github.com/bbxx111/accountkit/session/revocation"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/code"
	"github.com/bbxx111/accountkit/user/sender"
	"github.com/redis/go-redis/v9"
)

// A client-local hook faults one actual dependency boundary without stopping a
// shared Redis server or changing any other test's namespace.
type metricsRedisHook struct{ match func(redis.Cmder) bool }

func (h metricsRedisHook) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) { return next(ctx, network, addr) }
}
func (h metricsRedisHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if h.match(cmd) {
			return errors.New("synthetic dependency outage")
		}
		return next(ctx, cmd)
	}
}
func (h metricsRedisHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func degradationCount(r *obstest.Recorder, scenario ob.DegradationScenario) int64 {
	var n int64
	for _, v := range r.Snapshot() {
		if d, ok := v.(ob.DegradationObservation); ok && d.Scenario == scenario {
			n += d.Count
		}
	}
	return n
}

func TestSessionObservationFailureBoundaries(t *testing.T) {
	ctx := context.Background()
	for _, failure := range []string{"miss", "read_error", "pair_expired"} {
		t.Run("grace_"+failure, func(t *testing.T) {
			f, r := observedFixture(t)
			original := f.signIn(t, enum.IdentityEmail, email1, dev1)
			if _, err := f.svc.Refresh(ctx, original.RefreshToken, meta1); err != nil {
				t.Fatal(err)
			}
			rdb := metricsRedisClient(t, f)
			prefix := f.deps.Codes.InstancePrefix()
			cache := grace.NewCache(rdb, prefix, f.ciph)
			wantReason := ob.ReasonGraceMiss
			wantDependency := ob.ResultMiss
			hash := sha256.Sum256([]byte(original.RefreshToken))
			switch failure {
			case "miss":
				keys, err := rdb.Keys(ctx, prefix+"grace:*").Result()
				if err != nil {
					t.Fatal(err)
				}
				if len(keys) > 0 {
					if err := rdb.Del(ctx, keys...).Err(); err != nil {
						t.Fatal(err)
					}
				}
			case "read_error":
				rdb.AddHook(metricsRedisHook{match: func(cmd redis.Cmder) bool { return cmd.Name() == "get" }})
				wantReason = ob.ReasonGraceReadError
				wantDependency = ob.ResultError
			case "pair_expired":
				if err := cache.Put(ctx, hash[:], grace.Pair{AccessExpireTime: *f.clock, RefreshExpireTime: f.clock.Add(time.Hour), Scope: user.ScopeUser}, time.Minute); err != nil {
					t.Fatal(err)
				}
				wantReason = ob.ReasonGracePairExpired
				wantDependency = ob.ResultSuccess
			}
			f.deps.Grace = cache
			rebuildMetricsService(t, f)
			r.Reset()
			if _, err := f.svc.Refresh(ctx, original.RefreshToken, meta1); !errors.Is(err, user.ErrInvalidGrant) {
				t.Fatal(err)
			}
			wantResult := ob.ResultRejected
			if failure == "read_error" {
				wantResult = ob.ResultDependencyError
			}
			requireAuth(t, r, ob.AuthRefresh, wantResult, wantReason, ob.GrantNone, ob.RefreshNone)
			deps := dependencyValues(r, ob.ComponentGraceCache, ob.DependencyGet)
			if len(deps) != 1 || deps[0].Result != wantDependency {
				t.Fatal(deps)
			}
			sess, err := user.TestRepoQueries(f.repo).GetSessionByPreviousRefreshHash(ctx, hash[:])
			if err != nil || sess.RevokeTime != nil {
				t.Fatalf("spurious revocation: %+v %v", sess, err)
			}
		})
	}
	for _, commitFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "grace_write_failure_after_commit", true: "grace_write_failure_commit_rolls_back"}[commitFailure], func(t *testing.T) {
			f, r := observedFixture(t)
			original := f.signIn(t, enum.IdentityEmail, email1, dev1)
			rdb := metricsRedisClient(t, f)
			rdb.AddHook(metricsRedisHook{match: func(cmd redis.Cmder) bool { return cmd.Name() == "set" }})
			f.deps.Grace = grace.NewCache(rdb, f.deps.Codes.InstancePrefix(), f.ciph)
			rebuildMetricsService(t, f)
			if commitFailure {
				mustExec(t, f, "CREATE FUNCTION metrics_fail_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic deferred commit rejection'; END $$")
				mustExec(t, f, "CREATE CONSTRAINT TRIGGER metrics_fail_commit AFTER UPDATE ON session DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION metrics_fail_commit()")
			}
			r.Reset()
			_, err := f.svc.Refresh(ctx, original.RefreshToken, meta1)
			want := int64(1)
			if commitFailure {
				want = 0
				if err == nil {
					t.Fatal("commit unexpectedly succeeded")
				}
				requireAuth(t, r, ob.AuthRefresh, ob.ResultDependencyError, ob.ReasonDependencyUnavailable, ob.GrantNone, ob.RefreshNone)
			} else {
				if err != nil {
					t.Fatal(err)
				}
				requireAuth(t, r, ob.AuthRefresh, ob.ResultSuccess, ob.ReasonNone, ob.GrantUser, ob.RefreshRotation)
			}
			deps := dependencyValues(r, ob.ComponentGraceCache, ob.DependencyPut)
			if len(deps) != 1 || deps[0].Result != ob.ResultError {
				t.Fatal(deps)
			}
			if got := degradationCount(r, ob.DegradationRefreshGraceWriteFailed); got != want {
				t.Fatalf("successful degradation=%d want=%d", got, want)
			}
			if commitFailure {
				hash := sha256.Sum256([]byte(original.RefreshToken))
				if _, err := user.TestRepoQueries(f.repo).GetSessionByRefreshHash(ctx, hash[:]); err != nil {
					t.Fatal("rollback lost original refresh", err)
				}
			}
		})
	}
	t.Run("revocation fail open and revoked normal check", func(t *testing.T) {
		f, r := observedFixture(t)
		pair := f.signIn(t, enum.IdentityEmail, email1, dev1)
		rdb := metricsRedisClient(t, f)
		rdb.AddHook(metricsRedisHook{match: func(cmd redis.Cmder) bool { return cmd.Name() == "exists" }})
		f.deps.Revocation = revocation.NewSet(rdb, f.deps.Codes.InstancePrefix())
		rebuildMetricsService(t, f)
		r.Reset()
		if _, err := f.svc.Authenticate(ctx, pair.AccessToken); err != nil {
			t.Fatal(err)
		}
		requireAuth(t, r, ob.AuthAuthenticate, ob.ResultSuccess, ob.ReasonNone, ob.GrantUser, ob.RefreshNone)
		deps := dependencyValues(r, ob.ComponentRevocation, ob.DependencyCheck)
		if len(deps) != 1 || deps[0].Result != ob.ResultError || degradationCount(r, ob.DegradationRevocationCheckFailOpen) != 1 {
			t.Fatal(r.Snapshot())
		}
		good := metricsRedisClient(t, f)
		f.deps.Revocation = revocation.NewSet(good, f.deps.Codes.InstancePrefix())
		rebuildMetricsService(t, f)
		if err := f.svc.Revoke(ctx, pair.RefreshToken, meta1); err != nil {
			t.Fatal(err)
		}
		r.Reset()
		if _, err := f.svc.Authenticate(ctx, pair.AccessToken); !errors.Is(err, user.ErrInvalidToken) {
			t.Fatal(err)
		}
		requireAuth(t, r, ob.AuthAuthenticate, ob.ResultRejected, ob.ReasonSessionRevoked, ob.GrantNone, ob.RefreshNone)
		deps = dependencyValues(r, ob.ComponentRevocation, ob.DependencyCheck)
		if len(deps) != 1 || deps[0].Result != ob.ResultSuccess {
			t.Fatal(deps)
		}
	})
	t.Run("expired and containment failure", func(t *testing.T) {
		f, r := observedFixture(t)
		pair := f.signIn(t, enum.IdentityEmail, email1, dev1)
		if _, err := f.svc.Refresh(ctx, pair.RefreshToken, meta1); err != nil {
			t.Fatal(err)
		}
		f.advance(f.deps.RefreshGrace + time.Second)
		mustExec(t, f, "CREATE FUNCTION metrics_fail_revoke() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic revocation rejection'; END $$")
		mustExec(t, f, "CREATE TRIGGER metrics_fail_revoke BEFORE UPDATE ON session FOR EACH ROW EXECUTE FUNCTION metrics_fail_revoke()")
		r.Reset()
		if _, err := f.svc.Refresh(ctx, pair.RefreshToken, meta1); err == nil {
			t.Fatal("containment succeeded")
		}
		requireAuth(t, r, ob.AuthRefresh, ob.ResultDependencyError, ob.ReasonContainmentFailed, ob.GrantNone, ob.RefreshNone)
		deps := dependencyValues(r, ob.ComponentRevocation, ob.DependencyRevoke)
		if len(deps) != 1 {
			t.Fatal(deps)
		}
	})
	t.Run("flow expiry and fail closed replay", func(t *testing.T) {
		f, r := observedFixture(t)
		pending := pendingFlow(t, f, "metrics-expiry")
		cred := completionCredential(t, f, pending, enum.IdentityEmail, email1)
		if _, err := f.svc.CompleteSignIn(ctx, pending.SignInCompletionToken, cred, dev1, meta1); err != nil {
			t.Fatal(err)
		}
		rdb := metricsRedisClient(t, f)
		rdb.AddHook(metricsRedisHook{match: func(cmd redis.Cmder) bool { return cmd.Name() == "exists" }})
		f.deps.Revocation = revocation.NewSet(rdb, f.deps.Codes.InstancePrefix())
		rebuildMetricsService(t, f)
		r.Reset()
		if _, err := f.svc.CompleteSignIn(ctx, pending.SignInCompletionToken, cred, dev1, meta1); !errors.Is(err, user.ErrUnavailable) {
			t.Fatal(err)
		}
		requireAuth(t, r, ob.AuthCompleteSignIn, ob.ResultDependencyError, ob.ReasonDependencyUnavailable, ob.GrantNone, ob.RefreshNone)
		if degradationCount(r, ob.DegradationRevocationCheckFailOpen) != 0 {
			t.Fatal("completion replay incorrectly reported fail-open")
		}
		f.advance(6 * time.Minute)
		r.Reset()
		if _, err := f.svc.CompleteSignIn(ctx, pending.SignInCompletionToken, cred, dev1, meta1); !errors.Is(err, user.ErrSignInCompletionInvalid) {
			t.Fatal(err)
		}
		requireAuth(t, r, ob.AuthCompleteSignIn, ob.ResultRejected, ob.ReasonFlowExpired, ob.GrantNone, ob.RefreshNone)
		if len(dependencyValues(r, ob.ComponentCodeStore, ob.DependencyVerify)) != 0 {
			t.Fatal("expired flow verified code")
		}
	})
	for _, expired := range []bool{false, true} {
		t.Run(map[bool]string{false: "revoked_refresh", true: "expired_refresh"}[expired], func(t *testing.T) {
			f, r := observedFixture(t)
			pair := f.signIn(t, enum.IdentityEmail, email1, dev1)
			want := ob.ReasonSessionRevoked
			if expired {
				f.advance(f.deps.RefreshTTL + time.Second)
				want = ob.ReasonSessionExpired
			} else {
				if err := f.svc.Revoke(ctx, pair.RefreshToken, meta1); err != nil {
					t.Fatal(err)
				}
			}
			r.Reset()
			if _, err := f.svc.Refresh(ctx, pair.RefreshToken, meta1); !errors.Is(err, user.ErrInvalidGrant) {
				t.Fatal(err)
			}
			requireAuth(t, r, ob.AuthRefresh, ob.ResultRejected, want, ob.GrantNone, ob.RefreshNone)
			if len(dependencyValues(r, ob.ComponentGraceCache, ob.DependencyGet)) != 0 {
				t.Fatal("ineligible session read grace cache")
			}
		})
	}
	t.Run("redis revocation write failure retains database revocation", func(t *testing.T) {
		f, r := observedFixture(t)
		pair := f.signIn(t, enum.IdentityEmail, email1, dev1)
		rdb := metricsRedisClient(t, f)
		rdb.AddHook(metricsRedisHook{match: func(cmd redis.Cmder) bool { return cmd.Name() == "set" }})
		f.deps.Revocation = revocation.NewSet(rdb, f.deps.Codes.InstancePrefix())
		rebuildMetricsService(t, f)
		r.Reset()
		if err := f.svc.Revoke(ctx, pair.RefreshToken, meta1); err != nil {
			t.Fatal(err)
		}
		deps := dependencyValues(r, ob.ComponentRevocation, ob.DependencyRevoke)
		if len(deps) != 1 || deps[0].Result != ob.ResultError {
			t.Fatal(deps)
		}
		hash := sha256.Sum256([]byte(pair.RefreshToken))
		sess, err := user.TestRepoQueries(f.repo).GetSessionByRefreshHash(ctx, hash[:])
		if err != nil || sess.RevokeTime == nil {
			t.Fatalf("database revocation=%v %v", sess.RevokeTime, err)
		}
		if degradationCount(r, ob.DegradationRevocationCheckFailOpen) != 0 {
			t.Fatal("write failure fabricated check fail-open")
		}
	})
}

func TestDependencyObservationCleanupRace(t *testing.T) {
	f, r := observedFixture(t)
	ctx := context.Background()
	rdb := metricsRedisClient(t, f)
	var newerID, newerCode string
	f.deps.Email = metricsDeliveryFunc(func(_ context.Context, target string, _ sender.CodeMessage) error {
		keys, err := rdb.Keys(ctx, f.deps.Codes.InstancePrefix()+"cooldown:*").Result()
		if err != nil {
			t.Fatal(err)
		}
		if len(keys) > 0 {
			if err := rdb.Del(ctx, keys...).Err(); err != nil {
				t.Fatal(err)
			}
		}
		newer, err := f.deps.Codes.IssueChallenge(ctx, enum.IdentityEmail, enum.PurposeSignIn, target, meta1.IP, code.Binding{})
		if err != nil {
			t.Fatal(err)
		}
		newerID, newerCode = newer.CodeID, newer.Code
		return errors.New("synthetic sender failure")
	})
	rebuildMetricsService(t, f)
	if _, err := f.svc.SendSignInCode(ctx, enum.IdentityEmail, email1, meta1); err == nil {
		t.Fatal("send succeeded")
	}
	if err := f.deps.Codes.VerifyChallenge(ctx, code.Credential{Channel: enum.IdentityEmail, Purpose: enum.PurposeSignIn, Target: email1, CodeID: newerID, Code: newerCode}); err != nil {
		t.Fatal("discard removed newer challenge", err)
	}
	deps := dependencyValues(r, ob.ComponentCodeStore, ob.DependencyDiscard)
	if len(deps) != 1 || deps[0].Result != ob.ResultSuccess {
		t.Fatal(deps)
	}
	if len(dependencyValues(r, ob.ComponentCodeStore, ob.DependencyIssue)) != 1 {
		t.Fatal("direct low-level issue was counted as a domain call")
	}
	for _, v := range r.Snapshot() {
		if strings.Contains(stringifyMetrics(v), email1) {
			t.Fatal("target entered observation")
		}
	}
}

func stringifyMetrics(v ob.Observation) string { return fmt.Sprintf("%+v", v) }
