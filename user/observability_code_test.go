package user_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/internal/obstest"
	ob "github.com/bbxx111/accountkit/observability"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/code"
	"github.com/bbxx111/accountkit/user/sender"
)

func requireCodeSend(t *testing.T, r *obstest.Recorder, result ob.Result, reason ob.Reason) {
	t.Helper()
	values := codeValues(r)
	if len(values) != 1 || values[0].Operation != ob.CodeSend || values[0].Result != result || values[0].Reason != reason {
		t.Fatal(values)
	}
}

func TestCodeObservationRejectionReasons(t *testing.T) {
	ctx := context.Background()
	t.Run("disabled channel avoids dependency calls", func(t *testing.T) {
		f, r := observedFixture(t)
		f.deps.Email = sender.Disabled{}
		rebuildMetricsService(t, f)
		if _, err := f.svc.SendSignInCode(ctx, enum.IdentityEmail, email1, meta1); !errors.Is(err, sender.ErrDisabled) {
			t.Fatal(err)
		}
		requireCodeSend(t, r, ob.ResultRejected, ob.ReasonChannelDisabled)
		if len(dependencyValues(r, ob.ComponentCodeStore, ob.DependencyIssue)) != 0 || len(dependencyValues(r, ob.ComponentSender, ob.DependencyEmail)) != 0 {
			t.Fatal(r.Snapshot())
		}
	})
	t.Run("unsupported target avoids dependency calls", func(t *testing.T) {
		f, r := observedFixture(t)
		f.deps.SMS = &regionalSMS{inner: f.sent}
		rebuildMetricsService(t, f)
		if _, err := f.svc.SendSignInCode(ctx, enum.IdentityPhone, foreignPhone, meta1); !errors.Is(err, sender.ErrTargetNotSupported) {
			t.Fatal(err)
		}
		requireCodeSend(t, r, ob.ResultRejected, ob.ReasonInvalidArgument)
		if len(dependencyValues(r, ob.ComponentCodeStore, ob.DependencyIssue)) != 0 || len(dependencyValues(r, ob.ComponentSender, ob.DependencySMS)) != 0 {
			t.Fatal(r.Snapshot())
		}
	})
	t.Run("deployment identity policy avoids dependency calls", func(t *testing.T) {
		f, r := observedFixture(t)
		f.deps.EnabledIdentityKinds = []enum.IdentityKind{enum.IdentityPhone}
		f.deps.RequiredIdentityKinds = []enum.IdentityKind{enum.IdentityPhone}
		rebuildMetricsService(t, f)
		if _, err := f.svc.SendSignInCode(ctx, enum.IdentityEmail, email1, meta1); !errors.Is(err, user.ErrIdentityKindNotEnabled) {
			t.Fatal(err)
		}
		requireCodeSend(t, r, ob.ResultRejected, ob.ReasonIdentityKindDisabled)
		if len(dependencyValues(r, ob.ComponentCodeStore, ob.DependencyIssue)) != 0 {
			t.Fatal(r.Snapshot())
		}
	})
	t.Run("cooldown target and IP quotas", func(t *testing.T) {
		f, r := observedFixture(t)
		rdb := metricsRedisClient(t, f)
		f.deps.Codes = code.NewStore(rdb, f.deps.Codes.InstancePrefix(), f.dig, code.Options{TTL: time.Minute, Cooldown: time.Minute, MaxAttempts: 5, DailyLimitPerTarget: 1, DailyLimitPerIP: 1, FailureLimitPerTarget: 10, FailureWindow: time.Minute})
		rebuildMetricsService(t, f)
		if _, err := f.svc.SendSignInCode(ctx, enum.IdentityEmail, email1, meta1); err != nil {
			t.Fatal(err)
		}
		r.Reset()
		if _, err := f.svc.SendSignInCode(ctx, enum.IdentityEmail, email1, meta1); err == nil {
			t.Fatal("cooldown accepted")
		}
		requireCodeSend(t, r, ob.ResultRejected, ob.ReasonCooldown)
		clearMetricsCooldown(t, f)
		r.Reset()
		if _, err := f.svc.SendSignInCode(ctx, enum.IdentityEmail, email1, meta1); err == nil {
			t.Fatal("target quota accepted")
		}
		requireCodeSend(t, r, ob.ResultRejected, ob.ReasonTargetLimit)
		r.Reset()
		if _, err := f.svc.SendSignInCode(ctx, enum.IdentityEmail, "other@example.org", meta1); err == nil {
			t.Fatal("IP quota accepted")
		}
		requireCodeSend(t, r, ob.ResultRejected, ob.ReasonIPLimit)
		deps := dependencyValues(r, ob.ComponentCodeStore, ob.DependencyIssue)
		if len(deps) != 1 || deps[0].Result != ob.ResultRejected {
			t.Fatal(deps)
		}
	})
	t.Run("verification budget and expired challenge", func(t *testing.T) {
		f, r := observedFixture(t)
		rdb := metricsRedisClient(t, f)
		f.deps.Codes = code.NewStore(rdb, f.deps.Codes.InstancePrefix(), f.dig, code.Options{TTL: time.Minute, Cooldown: time.Minute, MaxAttempts: 5, DailyLimitPerTarget: 10, DailyLimitPerIP: 10, FailureLimitPerTarget: 2, FailureWindow: time.Minute})
		rebuildMetricsService(t, f)
		if err := f.sendSignInCode(ctx, enum.IdentityEmail, email1, meta1); err != nil {
			t.Fatal(err)
		}
		cred := f.credential(enum.PurposeSignIn, enum.IdentityEmail, email1, "000000")
		if cred.Code == f.sent.code(email1) {
			cred.Code = "111111"
		}
		if _, err := f.svc.SignInWithCode(ctx, cred, dev1, meta1); !errors.Is(err, code.ErrInvalid) {
			t.Fatal(err)
		}
		if _, err := f.svc.SignInWithCode(ctx, cred, dev1, meta1); err == nil {
			t.Fatal("verification budget accepted")
		}
		clearMetricsCooldown(t, f)
		r.Reset()
		if _, err := f.svc.SendSignInCode(ctx, enum.IdentityEmail, email1, meta1); err == nil {
			t.Fatal("verification budget accepted")
		}
		requireCodeSend(t, r, ob.ResultRejected, ob.ReasonTargetVerifyLimit)
		r.Reset()
		cred.Target = "unknown@example.org"
		cred.CodeID = "abcdef0123456789abcdef0123456789"
		if _, err := f.svc.SignInWithCode(ctx, cred, dev1, meta1); !errors.Is(err, code.ErrExpired) {
			t.Fatal(err)
		}
		values := codeValues(r)
		if len(values) != 1 || values[0].Reason != ob.ReasonCodeExpired {
			t.Fatal(values)
		}
	})
}
