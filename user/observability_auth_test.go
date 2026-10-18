package user_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
	ob "github.com/bbxx111/accountkit/observability"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/code"
	"github.com/bbxx111/accountkit/user/idp"
	"github.com/bbxx111/accountkit/user/sender"
	"github.com/redis/go-redis/v9"
)

type metricsWeChatFunc func(context.Context, string, string) (idp.Identity, error)

func (f metricsWeChatFunc) VerifyWeChat(ctx context.Context, appID, code string) (idp.Identity, error) {
	return f(ctx, appID, code)
}

func TestAuthenticationObservationBoundaries(t *testing.T) {
	ctx := context.Background()
	t.Run("business verifier panic", func(t *testing.T) {
		f, r := observedFixture(t)
		f.deps.WeChat = metricsWeChatFunc(func(context.Context, string, string) (idp.Identity, error) {
			panic("synthetic business verifier panic")
		})
		rebuildMetricsService(t, f)
		func() {
			defer func() {
				if p := recover(); p != "synthetic business verifier panic" {
					t.Fatalf("panic=%v", p)
				}
			}()
			_, _ = f.svc.SignInWithIdp(ctx, user.IdpCredential{Kind: enum.IdentityWeChat}, dev1, meta1)
			t.Fatal("panic swallowed")
		}()
		if len(authValues(r)) != 0 || len(dependencyValues(r, ob.ComponentIDP, ob.DependencyVerifyWechat)) != 0 {
			t.Fatal(r.Snapshot())
		}
	})
	t.Run("observer panic preserves committed success", func(t *testing.T) {
		f, _ := observedFixture(t)
		f.deps.Observer = metricsObserverFunc(func(context.Context, ob.Observation) { panic("synthetic observer secret") })
		rebuildMetricsService(t, f)
		pair := f.signIn(t, enum.IdentityEmail, email1, dev1)
		if _, err := f.svc.Authenticate(ctx, pair.AccessToken); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("retry does not duplicate authentication or verification", func(t *testing.T) {
		f, r := observedFixture(t)
		mustExec(t, f, "CREATE SEQUENCE metrics_first_collision")
		mustExec(t, f, "CREATE FUNCTION metrics_collision() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF nextval('metrics_first_collision') = 1 THEN RAISE EXCEPTION USING ERRCODE = '23505', MESSAGE = 'synthetic one-time collision'; END IF; RETURN NEW; END $$")
		mustExec(t, f, "CREATE TRIGGER metrics_collision BEFORE INSERT ON user_account FOR EACH ROW EXECUTE FUNCTION metrics_collision()")
		f.signIn(t, enum.IdentityEmail, email1, dev1)
		requireAuth(t, r, ob.AuthSignIn, ob.ResultSuccess, ob.ReasonNone, ob.GrantUser, ob.RefreshNone)
		deps := dependencyValues(r, ob.ComponentCodeStore, ob.DependencyVerify)
		if len(deps) != 1 {
			t.Fatal(deps)
		}
		var attempts int
		if err := f.pool.QueryRow(ctx, "SELECT last_value FROM metrics_first_collision").Scan(&attempts); err != nil || attempts != 2 {
			t.Fatalf("retry attempts=%d err=%v", attempts, err)
		}
	})
	t.Run("confirmed canceled and deadline authentication", func(t *testing.T) {
		for _, timeout := range []bool{false, true} {
			f, r := observedFixture(t)
			var callCtx context.Context
			var cancel context.CancelFunc
			wantResult, wantReason := ob.ResultCanceled, ob.ReasonCanceled
			if timeout {
				callCtx, cancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
				wantResult, wantReason = ob.ResultDeadlineExceeded, ob.ReasonDeadlineExceeded
			} else {
				callCtx, cancel = context.WithCancel(ctx)
				cancel()
			}
			defer cancel()
			if _, err := f.svc.Refresh(callCtx, "unknown-refresh", meta1); err == nil {
				t.Fatal("unexpected refresh success")
			}
			requireAuth(t, r, ob.AuthRefresh, wantResult, wantReason, ob.GrantNone, ob.RefreshNone)
		}
	})
	t.Run("final flow storage normalization", func(t *testing.T) {
		f, r := observedFixture(t)
		pending := pendingFlow(t, f, "metrics-storage")
		cred := completionCredential(t, f, pending, enum.IdentityEmail, email1)
		mustExec(t, f, "CREATE FUNCTION metrics_storage_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION USING ERRCODE = '08006', MESSAGE = 'synthetic flow storage unavailable'; END $$")
		mustExec(t, f, "CREATE TRIGGER metrics_storage_failure BEFORE UPDATE ON sign_in_completion_flow FOR EACH ROW EXECUTE FUNCTION metrics_storage_failure()")
		r.Reset()
		if _, err := f.svc.CompleteSignIn(ctx, pending.SignInCompletionToken, cred, dev1, meta1); !errors.Is(err, user.ErrUnavailable) {
			t.Fatal(err)
		}
		requireAuth(t, r, ob.AuthCompleteSignIn, ob.ResultDependencyError, ob.ReasonDependencyUnavailable, ob.GrantNone, ob.RefreshNone)
		requireVerification(t, r, ob.PurposeSignInComplete)
	})
	t.Run("idp dependency rejection unavailable and internal", func(t *testing.T) {
		cases := []struct {
			err    error
			result ob.Result
			reason ob.Reason
			dep    ob.Result
		}{
			{idp.ErrInvalidCredential, ob.ResultRejected, ob.ReasonInvalidCredential, ob.ResultRejected},
			{idp.ErrUnavailable, ob.ResultDependencyError, ob.ReasonDependencyUnavailable, ob.ResultError},
			{idp.ErrMisconfigured, ob.ResultInternalError, ob.ReasonInternal, ob.ResultError},
			{context.Canceled, ob.ResultCanceled, ob.ReasonCanceled, ob.ResultCanceled},
			{context.DeadlineExceeded, ob.ResultDeadlineExceeded, ob.ReasonDeadlineExceeded, ob.ResultDeadlineExceeded},
		}
		for _, tt := range cases {
			f, r := observedFixture(t)
			f.wechat.err = map[string]error{"bad-proof": tt.err}
			_, err := f.svc.SignInWithIdp(ctx, user.IdpCredential{Kind: enum.IdentityWeChat, AppID: "wx1", Code: "bad-proof"}, dev1, meta1)
			if !errors.Is(err, tt.err) {
				t.Fatal(err)
			}
			requireAuth(t, r, ob.AuthSignIn, tt.result, tt.reason, ob.GrantNone, ob.RefreshNone)
			deps := dependencyValues(r, ob.ComponentIDP, ob.DependencyVerifyWechat)
			if len(deps) != 1 || deps[0].Result != tt.dep {
				t.Fatal(deps)
			}
		}
	})
	t.Run("apple verifier is one logical call", func(t *testing.T) {
		f, r := observedFixture(t)
		_, err := f.svc.SignInWithIdp(ctx, user.IdpCredential{Kind: enum.IdentityApple, IDToken: "metrics-apple", Nonce: "synthetic-nonce"}, dev1, meta1)
		if err != nil {
			t.Fatal(err)
		}
		deps := dependencyValues(r, ob.ComponentIDP, ob.DependencyVerifyApple)
		if len(deps) != 1 || deps[0].Result != ob.ResultSuccess {
			t.Fatal(deps)
		}
		requireAuth(t, r, ob.AuthSignIn, ob.ResultPending, ob.ReasonNone, ob.GrantNone, ob.RefreshNone)
	})
	t.Run("direct service authentication mapper receives observer", func(t *testing.T) {
		f, r := observedFixture(t)
		meta := meta1
		meta.RequestID = string(make([]byte, 1025))
		if _, err := f.svc.SendSignInCode(ctx, enum.IdentityEmail, email1, meta); err != nil {
			t.Fatal(err)
		}
		var omitted int64
		for _, v := range r.Snapshot() {
			if a, ok := v.(ob.AuditObservation); ok && a.Kind == ob.AuditOmittedFields && a.Field == ob.FieldRequestID {
				omitted += a.Count
			}
		}
		if omitted != 1 {
			t.Fatalf("omitted request IDs=%d", omitted)
		}
	})
	t.Run("code sign in and completion can remain pending", func(t *testing.T) {
		f, r := observedFixture(t)
		f.deps.RequiredIdentityKinds = []enum.IdentityKind{enum.IdentityPhone}
		rebuildMetricsService(t, f)
		if err := f.sendSignInCode(ctx, enum.IdentityEmail, email1, meta1); err != nil {
			t.Fatal(err)
		}
		result, err := f.svc.SignInWithCode(ctx, f.credential(enum.PurposeSignIn, enum.IdentityEmail, email1, f.sent.code(email1)), dev1, meta1)
		if err != nil || result.Pending == nil {
			t.Fatalf("pending=%+v err=%v", result, err)
		}
		requireAuth(t, r, ob.AuthSignIn, ob.ResultPending, ob.ReasonNone, ob.GrantNone, ob.RefreshNone)
		clearMetricsCooldown(t, f)
		cred := completionCredential(t, f, result.Pending, enum.IdentityEmail, email1)
		r.Reset()
		result, err = f.svc.CompleteSignIn(ctx, result.Pending.SignInCompletionToken, cred, dev1, meta1)
		if err != nil || result.Pending == nil {
			t.Fatalf("completion pending=%+v err=%v", result, err)
		}
		requireAuth(t, r, ob.AuthCompleteSignIn, ob.ResultPending, ob.ReasonNone, ob.GrantNone, ob.RefreshNone)
		requireVerification(t, r, ob.PurposeSignInComplete)
	})
	t.Run("reauthentication revoked session reason", func(t *testing.T) {
		f, r := observedFixture(t)
		pair := f.signIn(t, enum.IdentityEmail, email1, dev1)
		p := principalOf(t, f, pair)
		clearMetricsCooldown(t, f)
		c, err := f.svc.SendReauthenticationCode(ctx, p, enum.IdentityEmail, email1, meta1)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.svc.Revoke(ctx, pair.RefreshToken, meta1); err != nil {
			t.Fatal(err)
		}
		r.Reset()
		_, err = f.svc.Reauthenticate(ctx, p, user.CodeCredential{Channel: enum.IdentityEmail, Target: email1, CodeID: c.CodeID, Code: f.sent.code(email1)}, meta1)
		if !errors.Is(err, user.ErrInvalidToken) {
			t.Fatal(err)
		}
		requireAuth(t, r, ob.AuthReauthenticate, ob.ResultRejected, ob.ReasonSessionRevoked, ob.GrantNone, ob.RefreshNone)
	})
	t.Run("code store unavailable remains dependency error", func(t *testing.T) {
		f, r := observedFixture(t)
		rdb := metricsRedisClient(t, f)
		rdb.AddHook(metricsRedisHook{match: func(cmd redis.Cmder) bool { return cmd.Name() == "eval" || cmd.Name() == "evalsha" }})
		f.deps.Codes = code.NewStore(rdb, f.deps.Codes.InstancePrefix(), f.dig, code.Options{TTL: time.Minute, Cooldown: time.Minute, MaxAttempts: 5, DailyLimitPerIP: 10, DailyLimitPerTarget: 10, FailureLimitPerTarget: 10, FailureWindow: time.Minute})
		rebuildMetricsService(t, f)
		_, err := f.svc.SendSignInCode(ctx, enum.IdentityEmail, email1, meta1)
		if !errors.Is(err, user.ErrUnavailable) {
			t.Fatal(err)
		}
		values := codeValues(r)
		deps := dependencyValues(r, ob.ComponentCodeStore, ob.DependencyIssue)
		if len(values) != 1 || values[0].Result != ob.ResultDependencyError || values[0].Reason != ob.ReasonDependencyUnavailable || len(deps) != 1 || deps[0].Result != ob.ResultError {
			t.Fatalf("codes=%+v deps=%+v", values, deps)
		}
		if len(dependencyValues(r, ob.ComponentSender, ob.DependencyEmail)) != 0 {
			t.Fatal("failed issue called sender")
		}
	})
	t.Run("nested disabled service cannot overwrite caller branch facts", func(t *testing.T) {
		f, r := observedFixture(t)
		disabledDeps := f.deps
		disabledDeps.Observer = nil
		disabled, err := user.NewService(disabledDeps)
		if err != nil {
			t.Fatal(err)
		}
		f.deps.Observer = metricsObserverFunc(func(callCtx context.Context, v ob.Observation) {
			r.Observe(callCtx, v)
			if d, ok := v.(ob.DependencyObservation); ok && d.Component == ob.ComponentCodeStore && d.Operation == ob.DependencyDiscard {
				_, _ = disabled.Refresh(callCtx, "unknown", meta1)
			}
		})
		f.sent.fail = sender.ErrUnavailable
		rebuildMetricsService(t, f)
		if _, err := f.svc.SendSignInCode(ctx, enum.IdentityEmail, email1, meta1); !errors.Is(err, user.ErrUnavailable) {
			t.Fatal(err)
		}
		requireCodeSend(t, r, ob.ResultDependencyError, ob.ReasonSendFailed)
	})
}
