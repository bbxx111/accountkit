package user_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/audit"
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

func observedFixture(t *testing.T) (*fixture, *obstest.Recorder) {
	t.Helper()
	f := newFixture(t)
	if os.Getenv("ACCOUNTSVC_TEST_REDIS_URL") != "" {
		rdb := metricsRedisClient(t, f)
		prefix := fmt.Sprintf("metrics_test_%016x:", rand.Uint64())
		f.deps.Codes = code.NewStore(rdb, prefix, f.dig, code.Options{TTL: 5 * time.Minute, Cooldown: time.Minute, MaxAttempts: 5, DailyLimitPerTarget: 10, DailyLimitPerIP: 100, FailureLimitPerTarget: 10, FailureWindow: 15 * time.Minute, Now: f.deps.Now})
		f.deps.Grace = grace.NewCache(rdb, prefix, f.ciph)
		f.deps.Revocation = revocation.NewSet(rdb, prefix)
		t.Cleanup(func() {
			keys, err := rdb.Keys(context.Background(), prefix+"*").Result()
			if err != nil {
				t.Error(err)
				return
			}
			if len(keys) > 0 {
				if err := rdb.Del(context.Background(), keys...).Err(); err != nil {
					t.Error(err)
				}
			}
		})
	}
	r := &obstest.Recorder{}
	f.deps.Observer = r
	var err error
	f.svc, err = user.NewService(f.deps)
	if err != nil {
		t.Fatal(err)
	}
	return f, r
}

func metricsRedisClient(t *testing.T, f *fixture) *redis.Client {
	t.Helper()
	opts := &redis.Options{Addr: f.mr.Addr(), MaxRetries: -1}
	if url := os.Getenv("ACCOUNTSVC_TEST_REDIS_URL"); url != "" {
		var err error
		opts, err = redis.ParseURL(url)
		if err != nil {
			t.Fatal(err)
		}
		opts.MaxRetries = -1
	}
	rdb := redis.NewClient(opts)
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb
}

type metricsObserverFunc func(context.Context, ob.Observation)

func (f metricsObserverFunc) Observe(ctx context.Context, v ob.Observation) { f(ctx, v) }

type metricsAuditFunc func(context.Context, audit.Event)

func (f metricsAuditFunc) Record(ctx context.Context, v audit.Event) { f(ctx, v) }

type metricsDeliveryFunc func(context.Context, string, sender.CodeMessage) error

func (f metricsDeliveryFunc) SendEmail(ctx context.Context, target string, msg sender.CodeMessage) error {
	return f(ctx, target, msg)
}
func (f metricsDeliveryFunc) SendSMS(ctx context.Context, target string, msg sender.CodeMessage) error {
	return f(ctx, target, msg)
}

func rebuildMetricsService(t *testing.T, f *fixture) {
	t.Helper()
	var err error
	f.svc, err = user.NewService(f.deps)
	if err != nil {
		t.Fatal(err)
	}
}

func authValues(r *obstest.Recorder) []ob.AuthObservation {
	var out []ob.AuthObservation
	for _, v := range r.Snapshot() {
		if a, ok := v.(ob.AuthObservation); ok {
			out = append(out, a)
		}
	}
	return out
}

func codeValues(r *obstest.Recorder) []ob.CodeObservation {
	var out []ob.CodeObservation
	for _, v := range r.Snapshot() {
		if a, ok := v.(ob.CodeObservation); ok {
			out = append(out, a)
		}
	}
	return out
}

func dependencyValues(r *obstest.Recorder, component ob.Component, operation ob.DependencyOperation) []ob.DependencyObservation {
	var out []ob.DependencyObservation
	for _, v := range r.Snapshot() {
		if a, ok := v.(ob.DependencyObservation); ok && a.Component == component && a.Operation == operation {
			out = append(out, a)
		}
	}
	return out
}

func requireAuth(t *testing.T, r *obstest.Recorder, operation ob.AuthOperation, result ob.Result, reason ob.Reason, grant ob.Grant, mode ob.RefreshMode) ob.AuthObservation {
	t.Helper()
	values := authValues(r)
	if len(values) != 1 {
		t.Fatalf("auth calls=%d: %+v", len(values), values)
	}
	a := values[0]
	if a.Operation != operation || a.Result != result || a.Reason != reason || a.Grant != grant || a.Mode != mode || a.Duration < 0 {
		t.Fatalf("unexpected auth: %+v", a)
	}
	return a
}

func TestAuthenticationObservations(t *testing.T) {
	ctx := context.Background()
	t.Run("invalid input is one normal return", func(t *testing.T) {
		f, r := observedFixture(t)
		_, err := f.svc.SignInWithCode(ctx, user.CodeCredential{Channel: enum.IdentityEmail}, dev1, meta1)
		if !errors.Is(err, user.ErrInvalidArgument) {
			t.Fatal(err)
		}
		a := requireAuth(t, r, ob.AuthSignIn, ob.ResultRejected, ob.ReasonInvalidArgument, ob.GrantNone, ob.RefreshNone)
		if a.Method != ob.MethodEmail || len(dependencyValues(r, ob.ComponentCodeStore, ob.DependencyVerify)) != 0 {
			t.Fatal(r.Snapshot())
		}
	})
	t.Run("user token and principal", func(t *testing.T) {
		f, r := observedFixture(t)
		pair := f.signIn(t, enum.IdentityEmail, email1, dev1)
		requireAuth(t, r, ob.AuthSignIn, ob.ResultSuccess, ob.ReasonNone, ob.GrantUser, ob.RefreshNone)
		r.Reset()
		if _, err := f.svc.Authenticate(ctx, pair.AccessToken); err != nil {
			t.Fatal(err)
		}
		requireAuth(t, r, ob.AuthAuthenticate, ob.ResultSuccess, ob.ReasonNone, ob.GrantUser, ob.RefreshNone)
	})
	t.Run("pending and completion normalization", func(t *testing.T) {
		f, r := observedFixture(t)
		pendingFlow(t, f, "metrics-pending")
		requireAuth(t, r, ob.AuthSignIn, ob.ResultPending, ob.ReasonNone, ob.GrantNone, ob.RefreshNone)
		r.Reset()
		_, err := f.svc.CompleteSignIn(ctx, "invalid-ticket", user.CodeCredential{Channel: enum.IdentityEmail, Target: email1, CodeID: "0123456789abcdef0123456789abcdef", Code: "123456"}, dev1, meta1)
		if !errors.Is(err, user.ErrSignInCompletionInvalid) {
			t.Fatal(err)
		}
		requireAuth(t, r, ob.AuthCompleteSignIn, ob.ResultRejected, ob.ReasonFlowInvalid, ob.GrantNone, ob.RefreshNone)
	})
	t.Run("restricted bind and undelete grants", func(t *testing.T) {
		f, r := observedFixture(t)
		seedExistingBindScopeProvider(t, f, "metrics-bind", dev1)
		requireAuth(t, r, ob.AuthSignIn, ob.ResultSuccess, ob.ReasonNone, ob.GrantBind, ob.RefreshNone)
		r.Reset()
		pair := f.signIn(t, enum.IdentityPhone, phone1, dev2)
		p := principalOf(t, f, pair)
		if _, err := f.svc.DeleteMe(ctx, p, meta1); err != nil {
			t.Fatal(err)
		}
		// The business clock advances independently of Redis TTL in real fixtures.
		rdb := metricsRedisClient(t, f)
		keys, err := rdb.Keys(ctx, f.deps.Codes.InstancePrefix()+"cooldown:*").Result()
		if err != nil {
			t.Fatal(err)
		}
		if len(keys) > 0 {
			if err := rdb.Del(ctx, keys...).Err(); err != nil {
				t.Fatal(err)
			}
		}
		r.Reset()
		f.signIn(t, enum.IdentityPhone, phone1, dev2)
		requireAuth(t, r, ob.AuthSignIn, ob.ResultSuccess, ob.ReasonNone, ob.GrantUndelete, ob.RefreshNone)
	})
	t.Run("business panic propagates without terminal result", func(t *testing.T) {
		f, r := observedFixture(t)
		f.deps.Email = metricsDeliveryFunc(func(context.Context, string, sender.CodeMessage) error { panic("synthetic-business-panic") })
		rebuildMetricsService(t, f)
		func() {
			defer func() {
				if p := recover(); p != "synthetic-business-panic" {
					t.Fatalf("panic=%v", p)
				}
			}()
			_, _ = f.svc.SendSignInCode(ctx, enum.IdentityEmail, email1, meta1)
			t.Fatal("panic swallowed")
		}()
		if len(codeValues(r)) != 0 || len(authValues(r)) != 0 {
			t.Fatal(r.Snapshot())
		}
	})
	t.Run("committed success remains success after cancellation", func(t *testing.T) {
		f, r := observedFixture(t)
		callCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		f.deps.Audit = metricsAuditFunc(func(_ context.Context, e audit.Event) {
			if e.Action == enum.EventSignIn.String() {
				cancel()
			}
		})
		rebuildMetricsService(t, f)
		if err := f.sendSignInCode(ctx, enum.IdentityEmail, email1, meta1); err != nil {
			t.Fatal(err)
		}
		res, err := f.svc.SignInWithCode(callCtx, f.credential(enum.PurposeSignIn, enum.IdentityEmail, email1, f.sent.code(email1)), dev1, meta1)
		if err != nil || res.Tokens == nil || callCtx.Err() == nil {
			t.Fatalf("result=%+v err=%v canceled=%v", res, err, callCtx.Err())
		}
		requireAuth(t, r, ob.AuthSignIn, ob.ResultSuccess, ob.ReasonNone, ob.GrantUser, ob.RefreshNone)
	})
	t.Run("confirmed context causes", func(t *testing.T) {
		for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
			f, r := observedFixture(t)
			f.deps.Email = metricsDeliveryFunc(func(context.Context, string, sender.CodeMessage) error { return cause })
			rebuildMetricsService(t, f)
			_, err := f.svc.SendSignInCode(ctx, enum.IdentityEmail, email1, meta1)
			if !errors.Is(err, cause) {
				t.Fatal(err)
			}
			values := codeValues(r)
			deps := dependencyValues(r, ob.ComponentSender, ob.DependencyEmail)
			want := ob.ResultCanceled
			if cause == context.DeadlineExceeded {
				want = ob.ResultDeadlineExceeded
			}
			if len(values) != 1 || values[0].Result != want || len(deps) != 1 || deps[0].Result != want {
				t.Fatalf("codes=%+v deps=%+v", values, deps)
			}
		}
	})
}

func TestCodeObservations(t *testing.T) {
	ctx := context.Background()
	t.Run("all public send preconditions", func(t *testing.T) {
		f, r := observedFixture(t)
		calls := []struct {
			purpose ob.Purpose
			call    func() error
		}{
			{ob.PurposeSignIn, func() error { _, e := f.svc.SendSignInCode(ctx, enum.IdentityEmail, "bad", meta1); return e }},
			{ob.PurposeBind, func() error {
				_, e := f.svc.SendBindCode(ctx, user.Principal{}, enum.IdentityWeChat, "bad", meta1)
				return e
			}},
			{ob.PurposeReauth, func() error {
				_, e := f.svc.SendReauthenticationCode(ctx, user.Principal{}, enum.IdentityWeChat, "bad", meta1)
				return e
			}},
			{ob.PurposeSignInComplete, func() error {
				_, e := f.svc.SendSignInCompletionCode(ctx, "bad", enum.IdentityEmail, email1, dev1, meta1)
				return e
			}},
		}
		for _, tt := range calls {
			r.Reset()
			if err := tt.call(); err == nil {
				t.Fatal("expected precondition error")
			}
			values := codeValues(r)
			if len(values) != 1 || values[0].Operation != ob.CodeSend || values[0].Purpose != tt.purpose || values[0].Result != ob.ResultRejected {
				t.Fatalf("send: %+v", values)
			}
			if len(dependencyValues(r, ob.ComponentCodeStore, ob.DependencyIssue)) != 0 {
				t.Fatal("precondition performed store call")
			}
		}
	})
	t.Run("logical verification and rejection", func(t *testing.T) {
		f, r := observedFixture(t)
		if err := f.sendSignInCode(ctx, enum.IdentityEmail, email1, meta1); err != nil {
			t.Fatal(err)
		}
		r.Reset()
		wrongCode := "000000"
		if f.sent.code(email1) == wrongCode {
			wrongCode = "111111"
		}
		_, err := f.svc.SignInWithCode(ctx, f.credential(enum.PurposeSignIn, enum.IdentityEmail, email1, wrongCode), dev1, meta1)
		if !errors.Is(err, code.ErrInvalid) {
			t.Fatal(err)
		}
		values := codeValues(r)
		deps := dependencyValues(r, ob.ComponentCodeStore, ob.DependencyVerify)
		if len(values) != 1 || values[0].Purpose != ob.PurposeSignIn || values[0].Operation != ob.CodeVerify || values[0].Reason != ob.ReasonCodeInvalid || len(deps) != 1 || deps[0].Result != ob.ResultRejected {
			t.Fatalf("codes=%+v deps=%+v", values, deps)
		}
	})
	t.Run("binding replacement and reauthentication actual verification", func(t *testing.T) {
		f, r := observedFixture(t)
		pair := f.signIn(t, enum.IdentityPhone, phone1, dev1)
		p := principalOf(t, f, pair)
		challenge, err := f.svc.SendBindCode(ctx, p, enum.IdentityEmail, email1, meta1)
		if err != nil {
			t.Fatal(err)
		}
		r.Reset()
		_, _, err = f.svc.BindWithCode(ctx, p, user.CodeCredential{Channel: enum.IdentityEmail, Target: email1, CodeID: challenge.CodeID, Code: f.sent.code(email1)}, meta1)
		if err != nil {
			t.Fatal(err)
		}
		requireVerification(t, r, ob.PurposeBind)
		identities, err := f.svc.ListIdentities(ctx, p.UserID)
		if err != nil {
			t.Fatal(err)
		}
		var oldID string
		for _, id := range identities {
			if id.Kind == enum.IdentityPhone {
				oldID = id.ID
			}
		}
		challenge, err = f.svc.SendBindCode(ctx, p, enum.IdentityPhone, phone2, meta1)
		if err != nil {
			t.Fatal(err)
		}
		r.Reset()
		_, err = f.svc.ReplaceIdentity(ctx, p, oldID, user.CodeCredential{Channel: enum.IdentityPhone, Target: phone2, CodeID: challenge.CodeID, Code: f.sent.code(phone2)}, meta1)
		if err != nil {
			t.Fatal(err)
		}
		requireVerification(t, r, ob.PurposeBind)
		// Replacement revoked the old session; sign in with a different bound target.
		clearMetricsCooldown(t, f)
		pair = f.signIn(t, enum.IdentityEmail, email1, dev2)
		p = principalOf(t, f, pair)
		clearMetricsCooldown(t, f)
		challenge, err = f.svc.SendReauthenticationCode(ctx, p, enum.IdentityEmail, email1, meta1)
		if err != nil {
			t.Fatal(err)
		}
		r.Reset()
		_, err = f.svc.Reauthenticate(ctx, p, user.CodeCredential{Channel: enum.IdentityEmail, Target: email1, CodeID: challenge.CodeID, Code: f.sent.code(email1)}, meta1)
		if err != nil {
			t.Fatal(err)
		}
		requireVerification(t, r, ob.PurposeReauth)
		requireAuth(t, r, ob.AuthReauthenticate, ob.ResultSuccess, ob.ReasonNone, ob.GrantUser, ob.RefreshNone)
	})
	t.Run("completion actual verification and idempotent replay", func(t *testing.T) {
		f, r := observedFixture(t)
		pending := pendingFlow(t, f, "metrics-completion")
		cred := completionCredential(t, f, pending, enum.IdentityEmail, email1)
		r.Reset()
		result, err := f.svc.CompleteSignIn(ctx, pending.SignInCompletionToken, cred, dev1, meta1)
		if err != nil {
			t.Fatal(err)
		}
		requireVerification(t, r, ob.PurposeSignInComplete)
		requireAuth(t, r, ob.AuthCompleteSignIn, ob.ResultSuccess, ob.ReasonNone, ob.GrantUser, ob.RefreshNone)
		r.Reset()
		replayed, err := f.svc.CompleteSignIn(ctx, pending.SignInCompletionToken, cred, dev1, meta1)
		if err != nil || *replayed.Tokens != *result.Tokens {
			t.Fatalf("replay: %v", err)
		}
		if len(codeValues(r)) != 0 || len(dependencyValues(r, ob.ComponentCodeStore, ob.DependencyVerify)) != 0 {
			t.Fatal("idempotent replay verified code again")
		}
		checks := dependencyValues(r, ob.ComponentRevocation, ob.DependencyCheck)
		if len(checks) != 1 || checks[0].Result != ob.ResultSuccess {
			t.Fatal(checks)
		}
		requireAuth(t, r, ob.AuthCompleteSignIn, ob.ResultSuccess, ob.ReasonNone, ob.GrantUser, ob.RefreshNone)
	})
}

func requireVerification(t *testing.T, r *obstest.Recorder, purpose ob.Purpose) {
	t.Helper()
	values := codeValues(r)
	deps := dependencyValues(r, ob.ComponentCodeStore, ob.DependencyVerify)
	if len(values) != 1 || values[0].Operation != ob.CodeVerify || values[0].Purpose != purpose || values[0].Result != ob.ResultSuccess || len(deps) != 1 || deps[0].Result != ob.ResultSuccess {
		t.Fatalf("codes=%+v deps=%+v", values, deps)
	}
}

func clearMetricsCooldown(t *testing.T, f *fixture) {
	t.Helper()
	rdb := metricsRedisClient(t, f)
	keys, err := rdb.Keys(context.Background(), f.deps.Codes.InstancePrefix()+"cooldown:*").Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) > 0 {
		if err := rdb.Del(context.Background(), keys...).Err(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSessionObservations(t *testing.T) {
	ctx := context.Background()
	t.Run("rotation grace and replay", func(t *testing.T) {
		f, r := observedFixture(t)
		original := f.signIn(t, enum.IdentityEmail, email1, dev1)
		r.Reset()
		pair, err := f.svc.Refresh(ctx, original.RefreshToken, meta1)
		if err != nil {
			t.Fatal(err)
		}
		requireAuth(t, r, ob.AuthRefresh, ob.ResultSuccess, ob.ReasonNone, ob.GrantUser, ob.RefreshRotation)
		r.Reset()
		replay, err := f.svc.Refresh(ctx, original.RefreshToken, meta1)
		if err != nil || replay.AccessToken != pair.AccessToken || replay.RefreshToken != pair.RefreshToken {
			t.Fatalf("same pair: %v", err)
		}
		requireAuth(t, r, ob.AuthRefresh, ob.ResultSuccess, ob.ReasonNone, ob.GrantUser, ob.RefreshGrace)
		f.advance(f.deps.RefreshGrace + time.Second)
		r.Reset()
		if _, err := f.svc.Refresh(ctx, original.RefreshToken, meta1); !errors.Is(err, user.ErrInvalidGrant) {
			t.Fatal(err)
		}
		requireAuth(t, r, ob.AuthRefresh, ob.ResultRejected, ob.ReasonTokenReuse, ob.GrantNone, ob.RefreshNone)
	})
	t.Run("concurrent refresh counts public calls", func(t *testing.T) {
		f, r := observedFixture(t)
		original := f.signIn(t, enum.IdentityEmail, email1, dev1)
		r.Reset()
		const n = 6
		var wg sync.WaitGroup
		pairs := make(chan user.TokenResult, n)
		errs := make(chan error, n)
		for range n {
			wg.Go(func() { pair, err := f.svc.Refresh(ctx, original.RefreshToken, meta1); pairs <- pair; errs <- err })
		}
		wg.Wait()
		close(pairs)
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		var first string
		for pair := range pairs {
			if first == "" {
				first = pair.RefreshToken
			}
			if pair.RefreshToken != first {
				t.Fatal("CAS lost pair identity")
			}
		}
		values := authValues(r)
		rotations := 0
		for _, a := range values {
			if a.Result != ob.ResultSuccess {
				t.Fatal(a)
			}
			if a.Mode == ob.RefreshRotation {
				rotations++
			}
		}
		if len(values) != n || rotations != 1 {
			t.Fatalf("public=%d rotations=%d", len(values), rotations)
		}
	})
}

func TestDependencyObservations(t *testing.T) {
	ctx := context.Background()
	for _, sendErr := range []error{errors.New("synthetic provider private response"), sender.ErrUnavailable} {
		t.Run(sendErr.Error(), func(t *testing.T) {
			f, r := observedFixture(t)
			f.sent.fail = sendErr
			_, err := f.svc.SendSignInCode(ctx, enum.IdentityEmail, email1, meta1)
			if err == nil {
				t.Fatal("expected send failure")
			}
			deps := dependencyValues(r, ob.ComponentSender, ob.DependencyEmail)
			discard := dependencyValues(r, ob.ComponentCodeStore, ob.DependencyDiscard)
			if len(deps) != 1 || deps[0].Result != ob.ResultError || len(discard) != 1 {
				t.Fatalf("sender=%+v discard=%+v", deps, discard)
			}
			values := codeValues(r)
			if len(values) != 1 || values[0].Result != ob.ResultDependencyError || values[0].Reason != ob.ReasonSendFailed {
				t.Fatal(values)
			}
		})
	}
}
