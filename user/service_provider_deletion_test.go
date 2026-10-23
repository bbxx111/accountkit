package user_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/db"
	"github.com/bbxx111/accountkit/user/idp"
)

// useFacebook 重建 Service，登记 fakeFacebook 并按需修改其余依赖。
func useFacebook(t *testing.T, f *fixture, fb *fakeFacebook, mutate func(*user.Deps)) {
	t.Helper()
	newServiceWith(t, f, func(d *user.Deps) {
		*d = withProvider(*d, fb.provider())
		if mutate != nil {
			mutate(d)
		}
	})
}

func noRequired(d *user.Deps) { d.RequiredIdentityKinds = []enum.IdentityKind{} }

// fbSignIn 以 Classic access_token 登录 Facebook；不同设备避免同设备会话替换。
func fbSignIn(t *testing.T, f *fixture, token string, dev user.Device) user.TokenResult {
	t.Helper()
	res, err := f.svc.SignInWithIdp(context.Background(), facebookCred(`{"access_token":"`+token+`"}`), dev, meta1)
	if err != nil {
		t.Fatal(err)
	}
	return requireSignInTokens(t, res)
}

func eventsOf(f *fixture, typ enum.EventType) []audit.AuthenticationEvent {
	var out []audit.AuthenticationEvent
	for _, e := range f.audit.Events() {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

func deletionEvents(f *fixture) []audit.AuthenticationEvent {
	return eventsOf(f, enum.EventProviderDataDeletionProcessed)
}

// subjectHint 是数据删除路径使用的脱敏提示：subject 的 SHA-256 十六进制前 8 字符。
func subjectHint(subject string) string {
	sum := sha256.Sum256([]byte(subject))
	return audit.Hint(hex.EncodeToString(sum[:]))
}

// assertNoRawSubject 确认审计原始事件（含 data）不出现完整第三方用户 id。
// from 为调用前的事件数，只检查本次处理产生的事件。
func assertNoRawSubject(t *testing.T, f *fixture, from int, subject string) {
	t.Helper()
	for _, e := range f.audit.RawEvents()[from:] {
		if strings.Contains(fmt.Sprintf("%+v", e), subject) || strings.Contains(string(e.Data), subject) {
			t.Fatalf("audit leaks subject: %+v %s", e, e.Data)
		}
	}
}

func identitiesOf(t *testing.T, f *fixture, userID string) (active, all []db.Identity) {
	t.Helper()
	ctx := context.Background()
	active, err := f.repo.Q().ListActiveIdentitiesByUser(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	all, err = f.repo.Q().ListIdentitiesByUserIncludingDeleted(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	return active, all
}

func hasKind(idents []db.Identity, kind enum.IdentityKind) bool {
	for _, id := range idents {
		if id.Kind == kind {
			return true
		}
	}
	return false
}

// subjectRows 统计库中仍以明文保存该 subject 的身份行（含已删除行与元数据）。
func subjectRows(t *testing.T, f *fixture, subject string) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM identity WHERE provider_subject = $1 OR provider_meta::text LIKE '%' || $1 || '%'`, subject).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func stateOf(t *testing.T, f *fixture, userID string) user.Me {
	t.Helper()
	me, err := f.svc.GetMe(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	return me
}

func requireOneDeletionEvent(t *testing.T, f *fixture, res user.ProviderDeletionResult, userID string, status user.ProviderDeletionStatus) {
	t.Helper()
	evs := deletionEvents(f)
	if len(evs) != 1 {
		t.Fatalf("deletion events: %+v", evs)
	}
	e := evs[0]
	if e.ID.String() != res.ConfirmationCode || e.Actor != enum.ActorSystem || e.Result != enum.ResultSuccess || e.Reason != string(status) ||
		e.IdentityKind != enum.IdentityFacebook || e.SubjectHint != subjectHint("asid-a") || e.UserID != userID || e.SessionID != "" || e.IdentityID != "" {
		t.Fatalf("deletion event: %+v want code=%s status=%s user=%s", e, res.ConfirmationCode, status, userID)
	}
}

func TestProviderDataDeletion(t *testing.T) {
	ctx := context.Background()
	fb := &fakeFacebook{identities: map[string]idp.Identity{"a": {Subject: "asid-a", Name: "Ada", HintEmail: "ada@example.test"}}}

	t.Run("unknown subject", func(t *testing.T) {
		f := newFixture(t)
		useFacebook(t, f, fb, noRequired)
		res, err := f.svc.DeleteProviderIdentityData(ctx, enum.IdentityFacebook, "nobody", *f.clock, meta1)
		if err != nil || res.Status != user.ProviderDeletionDeleted || res.ConfirmationCode == "" || !res.PurgeTime.IsZero() {
			t.Fatalf("%+v %v", res, err)
		}
		evs := deletionEvents(f)
		if len(evs) != 1 || evs[0].ID.String() != res.ConfirmationCode || evs[0].UserID != "" || evs[0].Reason != "DELETED" || evs[0].Actor != enum.ActorSystem {
			t.Fatalf("audit: %+v", evs)
		}
		assertNoRawSubject(t, f, 0, "nobody")
	})

	t.Run("other identity remains: anonymize and revoke source sessions", func(t *testing.T) {
		f := newFixture(t)
		useFacebook(t, f, fb, noRequired)
		phone := f.signIn(t, enum.IdentityPhone, phone1, dev1)
		if _, _, err := f.svc.BindWithIdp(ctx, principalOf(t, f, phone), facebookCred(`{"access_token":"a"}`), meta1); err != nil {
			t.Fatal(err)
		}
		fbTokens := fbSignIn(t, f, "a", dev2)
		if fbTokens.UserID != phone.UserID {
			t.Fatalf("facebook must hit bound account: %+v", fbTokens)
		}
		f.advance(time.Minute)
		before := len(f.audit.RawEvents())
		res, err := f.svc.DeleteProviderIdentityData(ctx, enum.IdentityFacebook, "asid-a", *f.clock, meta1)
		if err != nil || res.Status != user.ProviderDeletionDeleted || !res.PurgeTime.IsZero() {
			t.Fatalf("%+v %v", res, err)
		}
		active, all := identitiesOf(t, f, phone.UserID)
		if hasKind(active, enum.IdentityFacebook) || !hasKind(active, enum.IdentityPhone) {
			t.Fatalf("active identities: %+v", active)
		}
		for _, id := range all {
			if id.Kind == enum.IdentityFacebook && (id.DeleteTime == nil || id.ProviderSubject == nil || *id.ProviderSubject == "asid-a" || id.ProviderMeta != nil || id.HintPrefix != nil || id.HintSuffix != nil) {
				t.Fatalf("facebook identity not anonymized: %+v", id)
			}
		}
		if n := subjectRows(t, f, "asid-a"); n != 0 {
			t.Fatalf("subject still stored in %d rows", n)
		}
		if me := stateOf(t, f, phone.UserID); me.State != enum.UserActive {
			t.Fatalf("account: %+v", me)
		}
		if _, err := f.svc.Refresh(ctx, fbTokens.RefreshToken, meta1); err == nil {
			t.Fatal("facebook-sourced session must be revoked")
		}
		if _, err := f.svc.Refresh(ctx, phone.RefreshToken, meta1); err != nil {
			t.Fatalf("phone session must stay: %v", err)
		}
		requireOneDeletionEvent(t, f, res, phone.UserID, user.ProviderDeletionDeleted)
		unbound := eventsOf(f, enum.EventIdentityUnbound)
		if len(unbound) != 1 || unbound[0].Actor != enum.ActorSystem || unbound[0].Result != enum.ResultSuccess || unbound[0].IdentityKind != enum.IdentityFacebook ||
			unbound[0].SubjectHint != subjectHint("asid-a") || unbound[0].UserID != phone.UserID || unbound[0].IdentityID == "" {
			t.Fatalf("unbound audit: %+v", unbound)
		}
		revoked := 0
		for _, e := range eventsOf(f, enum.EventSessionRevoked) {
			if e.Reason == enum.RevokeIdentityUnbound.String() {
				if e.Actor != enum.ActorSystem {
					t.Fatalf("revoke actor: %+v", e)
				}
				revoked++
			}
		}
		if revoked != 1 || len(eventsOf(f, enum.EventUserDeleted)) != 0 {
			t.Fatalf("revoked=%d events=%+v", revoked, f.audit.Events())
		}
		assertNoRawSubject(t, f, before, "asid-a")

		again, err := f.svc.DeleteProviderIdentityData(ctx, enum.IdentityFacebook, "asid-a", *f.clock, meta1)
		if err != nil || again.Status != user.ProviderDeletionDeleted || again.ConfirmationCode == res.ConfirmationCode || again.ConfirmationCode == "" {
			t.Fatalf("repeat: %+v %v", again, err)
		}
		if evs := deletionEvents(f); len(evs) != 2 || evs[1].ID.String() != again.ConfirmationCode {
			t.Fatalf("repeat audit: %+v", evs)
		}
	})

	t.Run("remaining does not satisfy Required", func(t *testing.T) {
		f := newFixture(t)
		useFacebook(t, f, fb, noRequired)
		fbTokens := fbSignIn(t, f, "a", dev1)
		wx := wechatCred("wx1", "union:wx-a@o1")
		if _, _, err := f.svc.BindWithIdp(ctx, principalOf(t, f, fbTokens), wx, meta1); err != nil {
			t.Fatal(err)
		}
		wxRes, err := f.svc.SignInWithIdp(ctx, wx, dev2, meta1)
		if err != nil {
			t.Fatal(err)
		}
		wxTokens := requireSignInTokens(t, wxRes)
		// 恢复默认 Required（PHONE、EMAIL）。
		useFacebook(t, f, fb, func(d *user.Deps) { d.RequiredIdentityKinds = nil })
		f.advance(time.Minute)
		res, err := f.svc.DeleteProviderIdentityData(ctx, enum.IdentityFacebook, "asid-a", *f.clock, meta1)
		if err != nil || res.Status != user.ProviderDeletionDeleted {
			t.Fatalf("%+v %v", res, err)
		}
		active, _ := identitiesOf(t, f, fbTokens.UserID)
		if len(active) != 1 || active[0].Kind != enum.IdentityWeChat {
			t.Fatalf("active: %+v", active)
		}
		next, err := f.svc.Refresh(ctx, wxTokens.RefreshToken, meta1)
		if err != nil || next.Scope != user.ScopeBind {
			t.Fatalf("refresh: %+v %v", next, err)
		}
	})

	t.Run("frozen with other identity", func(t *testing.T) {
		f := newFixture(t)
		useFacebook(t, f, fb, noRequired)
		phone := f.signIn(t, enum.IdentityPhone, phone1, dev1)
		if _, _, err := f.svc.BindWithIdp(ctx, principalOf(t, f, phone), facebookCred(`{"access_token":"a"}`), meta1); err != nil {
			t.Fatal(err)
		}
		if _, err := f.svc.Freeze(ctx, admin1, phone.UserID, "abuse", meta1); err != nil {
			t.Fatal(err)
		}
		f.advance(time.Minute)
		res, err := f.svc.DeleteProviderIdentityData(ctx, enum.IdentityFacebook, "asid-a", *f.clock, meta1)
		if err != nil || res.Status != user.ProviderDeletionDeleted {
			t.Fatalf("%+v %v", res, err)
		}
		if me := stateOf(t, f, phone.UserID); me.State != enum.UserFrozen {
			t.Fatalf("must stay frozen: %+v", me)
		}
		active, _ := identitiesOf(t, f, phone.UserID)
		if hasKind(active, enum.IdentityFacebook) || !hasKind(active, enum.IdentityPhone) || subjectRows(t, f, "asid-a") != 0 {
			t.Fatalf("active: %+v", active)
		}
	})

	t.Run("only facebook", func(t *testing.T) {
		f := newFixture(t)
		useFacebook(t, f, fb, noRequired)
		fbTokens := fbSignIn(t, f, "a", dev1)
		f.advance(time.Minute)
		before := len(f.audit.RawEvents())
		res, err := f.svc.DeleteProviderIdentityData(ctx, enum.IdentityFacebook, "asid-a", *f.clock, meta1)
		if err != nil || res.Status != user.ProviderDeletionScheduled || !res.PurgeTime.Equal(f.clock.Add(f.deps.DeletionCoolingPeriod)) {
			t.Fatalf("%+v %v want purge %v", res, err, f.clock.Add(f.deps.DeletionCoolingPeriod))
		}
		me := stateOf(t, f, fbTokens.UserID)
		if me.State != enum.UserPendingDeletion || me.PurgeTime == nil || !me.PurgeTime.Equal(res.PurgeTime) {
			t.Fatalf("me: %+v", me)
		}
		if _, err := f.svc.Refresh(ctx, fbTokens.RefreshToken, meta1); err == nil {
			t.Fatal("all sessions must be revoked")
		}
		requireOneDeletionEvent(t, f, res, fbTokens.UserID, user.ProviderDeletionScheduled)
		deleted := eventsOf(f, enum.EventUserDeleted)
		if len(deleted) != 1 || deleted[0].Actor != enum.ActorSystem || deleted[0].UserID != fbTokens.UserID {
			t.Fatalf("user deleted audit: %+v", deleted)
		}
		for _, e := range eventsOf(f, enum.EventSessionRevoked) {
			if e.Reason == enum.RevokeUserDeleted.String() && e.Actor != enum.ActorSystem {
				t.Fatalf("revoke actor: %+v", e)
			}
		}
		if len(eventsOf(f, enum.EventIdentityUnbound)) != 0 {
			t.Fatal("no unbind expected")
		}
		assertNoRawSubject(t, f, before, "asid-a")
	})

	t.Run("host blocks", func(t *testing.T) {
		f := newFixture(t)
		useFacebook(t, f, fb, func(d *user.Deps) {
			noRequired(d)
			d.BeforeDelete = func(context.Context, pgx.Tx, string) error { return user.ErrDeletionBlocked }
		})
		fbTokens := fbSignIn(t, f, "a", dev1)
		f.advance(time.Minute)
		resetAuditKeep(t, f)
		res, err := f.svc.DeleteProviderIdentityData(ctx, enum.IdentityFacebook, "asid-a", *f.clock, meta1)
		if err != nil || res.Status != user.ProviderDeletionBlocked || res.ConfirmationCode == "" || !res.PurgeTime.IsZero() {
			t.Fatalf("%+v %v", res, err)
		}
		if me := stateOf(t, f, fbTokens.UserID); me.State != enum.UserActive {
			t.Fatalf("me: %+v", me)
		}
		active, _ := identitiesOf(t, f, fbTokens.UserID)
		if !hasKind(active, enum.IdentityFacebook) {
			t.Fatalf("identity must stay: %+v", active)
		}
		requireOneDeletionEvent(t, f, res, fbTokens.UserID, user.ProviderDeletionBlocked)
		for _, typ := range []enum.EventType{enum.EventIdentityUnbound, enum.EventUserDeleted, enum.EventSessionRevoked} {
			if evs := eventsOf(f, typ); len(evs) != 0 {
				t.Fatalf("unexpected %s: %+v", typ, evs)
			}
		}
		if _, err := f.svc.Refresh(ctx, fbTokens.RefreshToken, meta1); err != nil {
			t.Fatalf("session must stay: %v", err)
		}
	})

	t.Run("host error", func(t *testing.T) {
		f := newFixture(t)
		useFacebook(t, f, fb, func(d *user.Deps) {
			noRequired(d)
			d.BeforeDelete = func(context.Context, pgx.Tx, string) error { return errors.New("db down") }
		})
		fbTokens := fbSignIn(t, f, "a", dev1)
		f.advance(time.Minute)
		res, err := f.svc.DeleteProviderIdentityData(ctx, enum.IdentityFacebook, "asid-a", *f.clock, meta1)
		if err == nil || errors.Is(err, user.ErrDeletionBlocked) || res != (user.ProviderDeletionResult{}) {
			t.Fatalf("%+v %v", res, err)
		}
		if me := stateOf(t, f, fbTokens.UserID); me.State != enum.UserActive {
			t.Fatalf("me: %+v", me)
		}
		active, _ := identitiesOf(t, f, fbTokens.UserID)
		if !hasKind(active, enum.IdentityFacebook) {
			t.Fatalf("identity must stay: %+v", active)
		}
		evs := deletionEvents(f)
		if len(evs) != 1 || evs[0].Result != enum.ResultFailure || evs[0].Actor != enum.ActorSystem {
			t.Fatalf("failure audit: %+v", evs)
		}
		if _, err := f.svc.Refresh(ctx, fbTokens.RefreshToken, meta1); err != nil {
			t.Fatalf("session must stay: %v", err)
		}
	})

	t.Run("frozen only facebook", func(t *testing.T) {
		f := newFixture(t)
		useFacebook(t, f, fb, noRequired)
		fbTokens := fbSignIn(t, f, "a", dev1)
		if _, err := f.svc.Freeze(ctx, admin1, fbTokens.UserID, "abuse", meta1); err != nil {
			t.Fatal(err)
		}
		f.advance(time.Minute)
		resetAuditKeep(t, f)
		res, err := f.svc.DeleteProviderIdentityData(ctx, enum.IdentityFacebook, "asid-a", *f.clock, meta1)
		if err != nil || res.Status != user.ProviderDeletionBlocked {
			t.Fatalf("%+v %v", res, err)
		}
		if me := stateOf(t, f, fbTokens.UserID); me.State != enum.UserFrozen {
			t.Fatalf("me: %+v", me)
		}
		active, _ := identitiesOf(t, f, fbTokens.UserID)
		if !hasKind(active, enum.IdentityFacebook) {
			t.Fatalf("identity must stay: %+v", active)
		}
		requireOneDeletionEvent(t, f, res, fbTokens.UserID, user.ProviderDeletionBlocked)
		if len(eventsOf(f, enum.EventIdentityUnbound))+len(eventsOf(f, enum.EventUserDeleted)) != 0 {
			t.Fatalf("unexpected events: %+v", f.audit.Events())
		}
	})

	t.Run("already pending deletion", func(t *testing.T) {
		f := newFixture(t)
		useFacebook(t, f, fb, noRequired)
		fbTokens := fbSignIn(t, f, "a", dev1)
		me, err := f.svc.DeleteMe(ctx, principalOf(t, f, fbTokens), meta1)
		if err != nil || me.PurgeTime == nil {
			t.Fatalf("delete me: %+v %v", me, err)
		}
		f.advance(time.Minute)
		res, err := f.svc.DeleteProviderIdentityData(ctx, enum.IdentityFacebook, "asid-a", *f.clock, meta1)
		if err != nil || res.Status != user.ProviderDeletionScheduled || !res.PurgeTime.Equal(*me.PurgeTime) {
			t.Fatalf("%+v %v want %v", res, err, me.PurgeTime)
		}
		after := stateOf(t, f, fbTokens.UserID)
		if after.State != enum.UserPendingDeletion || !after.PurgeTime.Equal(*me.PurgeTime) {
			t.Fatalf("after: %+v", after)
		}
		active, _ := identitiesOf(t, f, fbTokens.UserID)
		if !hasKind(active, enum.IdentityFacebook) {
			t.Fatalf("identity must stay: %+v", active)
		}
		if n := len(eventsOf(f, enum.EventUserDeleted)); n != 1 {
			t.Fatalf("USER_DELETED events: %d", n)
		}
	})

	t.Run("identity created after request", func(t *testing.T) {
		f := newFixture(t)
		useFacebook(t, f, fb, noRequired)
		issued := f.clock.Add(-time.Minute)
		fbTokens := fbSignIn(t, f, "a", dev1)
		res, err := f.svc.DeleteProviderIdentityData(ctx, enum.IdentityFacebook, "asid-a", issued, meta1)
		if err != nil || res.Status != user.ProviderDeletionDeleted {
			t.Fatalf("%+v %v", res, err)
		}
		if me := stateOf(t, f, fbTokens.UserID); me.State != enum.UserActive {
			t.Fatalf("me: %+v", me)
		}
		active, _ := identitiesOf(t, f, fbTokens.UserID)
		if !hasKind(active, enum.IdentityFacebook) {
			t.Fatalf("identity must stay: %+v", active)
		}
		if _, err := f.svc.Refresh(ctx, fbTokens.RefreshToken, meta1); err != nil {
			t.Fatalf("session must stay: %v", err)
		}
	})

	// bindUnbindFb 为手机账号绑定再解绑 Facebook，留下一条仍存明文 subject 的历史行。
	bindUnbindFb := func(t *testing.T, f *fixture, phone user.TokenResult) {
		t.Helper()
		p := principalOf(t, f, phone)
		if _, _, err := f.svc.BindWithIdp(ctx, p, facebookCred(`{"access_token":"a"}`), meta1); err != nil {
			t.Fatal(err)
		}
		active, _ := identitiesOf(t, f, phone.UserID)
		for _, id := range active {
			if id.Kind == enum.IdentityFacebook {
				if err := f.svc.UnbindIdentity(ctx, p, id.ID, meta1); err != nil {
					t.Fatal(err)
				}
				return
			}
		}
		t.Fatal("facebook identity not bound")
	}

	t.Run("previously unbound identity is scrubbed", func(t *testing.T) {
		f := newFixture(t)
		useFacebook(t, f, fb, noRequired)
		phone := f.signIn(t, enum.IdentityPhone, phone1, dev1)
		bindUnbindFb(t, f, phone)
		if n := subjectRows(t, f, "asid-a"); n != 1 {
			t.Fatalf("precondition: historical rows %d", n)
		}
		f.advance(time.Minute)
		before := len(f.audit.RawEvents())
		res, err := f.svc.DeleteProviderIdentityData(ctx, enum.IdentityFacebook, "asid-a", *f.clock, meta1)
		if err != nil || res.Status != user.ProviderDeletionDeleted {
			t.Fatalf("%+v %v", res, err)
		}
		if n := subjectRows(t, f, "asid-a"); n != 0 {
			t.Fatalf("subject still stored in %d rows", n)
		}
		active, all := identitiesOf(t, f, phone.UserID)
		if !hasKind(active, enum.IdentityPhone) || len(active) != 1 {
			t.Fatalf("active: %+v", active)
		}
		for _, id := range all {
			if id.Kind == enum.IdentityFacebook && (id.DeleteTime == nil || id.ProviderMeta != nil || id.HintPrefix != nil || id.HintSuffix != nil) {
				t.Fatalf("facebook history row: %+v", id)
			}
		}
		if _, err := f.svc.Refresh(ctx, phone.RefreshToken, meta1); err != nil {
			t.Fatalf("phone session must stay: %v", err)
		}
		if len(eventsOf(f, enum.EventIdentityUnbound)) != 1 { // 仅来自此前的普通解绑
			t.Fatalf("unbound events: %+v", eventsOf(f, enum.EventIdentityUnbound))
		}
		assertNoRawSubject(t, f, before, "asid-a")
		again, err := f.svc.DeleteProviderIdentityData(ctx, enum.IdentityFacebook, "asid-a", *f.clock, meta1)
		if err != nil || again.Status != user.ProviderDeletionDeleted {
			t.Fatalf("repeat: %+v %v", again, err)
		}
	})

	t.Run("blocked still scrubs historical rows", func(t *testing.T) {
		f := newFixture(t)
		useFacebook(t, f, fb, func(d *user.Deps) {
			noRequired(d)
			d.BeforeDelete = func(context.Context, pgx.Tx, string) error { return user.ErrDeletionBlocked }
		})
		fbTokens := fbSignIn(t, f, "a", dev1)
		wx := wechatCred("wx1", "union:wx-a@o1")
		if _, _, err := f.svc.BindWithIdp(ctx, principalOf(t, f, fbTokens), wx, meta1); err != nil {
			t.Fatal(err)
		}
		wxRes, err := f.svc.SignInWithIdp(ctx, wx, dev2, meta1)
		if err != nil {
			t.Fatal(err)
		}
		wxP := principalOf(t, f, requireSignInTokens(t, wxRes))
		active, _ := identitiesOf(t, f, fbTokens.UserID)
		for _, id := range active {
			if id.Kind == enum.IdentityFacebook {
				if err := f.svc.UnbindIdentity(ctx, wxP, id.ID, meta1); err != nil {
					t.Fatal(err)
				}
			}
		}
		if _, _, err := f.svc.BindWithIdp(ctx, wxP, facebookCred(`{"access_token":"a"}`), meta1); err != nil {
			t.Fatal(err)
		}
		fbAgain := fbSignIn(t, f, "a", user.Device{ID: "dev-3", Name: "Mac"})
		if fbAgain.UserID != fbTokens.UserID {
			t.Fatalf("rebinding must hit same account: %+v", fbAgain)
		}
		active, _ = identitiesOf(t, f, fbTokens.UserID)
		for _, id := range active {
			if id.Kind == enum.IdentityWeChat {
				if err := f.svc.UnbindIdentity(ctx, principalOf(t, f, fbAgain), id.ID, meta1); err != nil {
					t.Fatal(err)
				}
			}
		}
		if n := subjectRows(t, f, "asid-a"); n != 2 {
			t.Fatalf("precondition: want active + historical, got %d", n)
		}
		f.advance(time.Minute)
		res, err := f.svc.DeleteProviderIdentityData(ctx, enum.IdentityFacebook, "asid-a", *f.clock, meta1)
		if err != nil || res.Status != user.ProviderDeletionBlocked {
			t.Fatalf("%+v %v", res, err)
		}
		active, all := identitiesOf(t, f, fbTokens.UserID)
		if len(active) != 1 || active[0].Kind != enum.IdentityFacebook || active[0].ProviderSubject == nil || *active[0].ProviderSubject != "asid-a" {
			t.Fatalf("active identity must stay: %+v", active)
		}
		stored := 0
		for _, id := range all {
			if id.Kind == enum.IdentityFacebook && id.ProviderSubject != nil && *id.ProviderSubject == "asid-a" {
				stored++
			}
		}
		if stored != 1 {
			t.Fatalf("only the active row may keep the subject, got %d", stored)
		}
		if me := stateOf(t, f, fbTokens.UserID); me.State != enum.UserActive {
			t.Fatalf("me: %+v", me)
		}
		if _, err := f.svc.Refresh(ctx, fbAgain.RefreshToken, meta1); err != nil {
			t.Fatalf("active identity session must stay: %v", err)
		}
	})

	t.Run("scrub failure keeps committed side effects", func(t *testing.T) {
		f := newFixture(t)
		useFacebook(t, f, fb, noRequired)
		phone := f.signIn(t, enum.IdentityPhone, phone1, dev1)
		bindUnbindFb(t, f, phone)
		if _, _, err := f.svc.BindWithIdp(ctx, principalOf(t, f, phone), facebookCred(`{"access_token":"a"}`), meta1); err != nil {
			t.Fatal(err)
		}
		fbTokens := fbSignIn(t, f, "a", dev2)
		// 测试缝：库内触发器只让“已软删的该 subject 行”的更新失败，主事务匿名化活动行不受影响。
		for _, stmt := range []string{
			`CREATE FUNCTION fail_scrub() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected scrub failure'; END $$`,
			`CREATE TRIGGER fail_scrub BEFORE UPDATE ON identity FOR EACH ROW WHEN (OLD.delete_time IS NOT NULL AND OLD.provider_subject = 'asid-a') EXECUTE FUNCTION fail_scrub()`,
		} {
			if _, err := f.pool.Exec(ctx, stmt); err != nil {
				t.Fatal(err)
			}
		}
		t.Cleanup(func() {
			_, _ = f.pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS fail_scrub ON identity`)
			_, _ = f.pool.Exec(context.Background(), `DROP FUNCTION IF EXISTS fail_scrub()`)
		})
		f.advance(time.Minute)
		res, err := f.svc.DeleteProviderIdentityData(ctx, enum.IdentityFacebook, "asid-a", *f.clock, meta1)
		if err == nil || res != (user.ProviderDeletionResult{}) {
			t.Fatalf("scrub failure must return error: %+v %v", res, err)
		}
		if _, err := f.svc.Authenticate(ctx, fbTokens.AccessToken); err == nil {
			t.Fatal("revoked session access token must be rejected")
		}
		if _, err := f.svc.Refresh(ctx, fbTokens.RefreshToken, meta1); err == nil {
			t.Fatal("facebook-sourced session must be revoked")
		}
		if _, err := f.svc.Refresh(ctx, phone.RefreshToken, meta1); err != nil {
			t.Fatalf("phone session must stay: %v", err)
		}
		if n := len(eventsOf(f, enum.EventIdentityUnbound)); n != 2 { // 先前普通解绑 + 本次
			t.Fatalf("unbound events: %d", n)
		}
		revoked := 0
		for _, e := range eventsOf(f, enum.EventSessionRevoked) {
			if e.Reason == enum.RevokeIdentityUnbound.String() {
				revoked++
			}
		}
		if revoked != 1 {
			t.Fatalf("session revoked events: %d", revoked)
		}
		evs := deletionEvents(f)
		if len(evs) != 1 || evs[0].Result != enum.ResultFailure {
			t.Fatalf("deletion events: %+v", evs)
		}
	})

	t.Run("historical row created after request is kept", func(t *testing.T) {
		f := newFixture(t)
		useFacebook(t, f, fb, noRequired)
		phone := f.signIn(t, enum.IdentityPhone, phone1, dev1)
		issued := f.clock.Add(-time.Hour)
		bindUnbindFb(t, f, phone)
		res, err := f.svc.DeleteProviderIdentityData(ctx, enum.IdentityFacebook, "asid-a", issued, meta1)
		if err != nil || res.Status != user.ProviderDeletionDeleted {
			t.Fatalf("%+v %v", res, err)
		}
		if n := subjectRows(t, f, "asid-a"); n != 1 {
			t.Fatalf("newer historical row must keep subject, rows=%d", n)
		}
	})

	t.Run("historical rows of two accounts are scrubbed", func(t *testing.T) {
		f := newFixture(t)
		useFacebook(t, f, fb, noRequired)
		first := f.signIn(t, enum.IdentityPhone, phone1, dev1)
		second := f.signIn(t, enum.IdentityPhone, phone2, dev1)
		bindUnbindFb(t, f, first)
		bindUnbindFb(t, f, second)
		if n := subjectRows(t, f, "asid-a"); n != 2 {
			t.Fatalf("precondition: historical rows %d", n)
		}
		f.advance(time.Minute)
		res, err := f.svc.DeleteProviderIdentityData(ctx, enum.IdentityFacebook, "asid-a", *f.clock, meta1)
		if err != nil || res.Status != user.ProviderDeletionDeleted {
			t.Fatalf("%+v %v", res, err)
		}
		for _, uid := range []string{first.UserID, second.UserID} {
			_, all := identitiesOf(t, f, uid)
			for _, id := range all {
				if id.ProviderSubject != nil && *id.ProviderSubject == "asid-a" {
					t.Fatalf("subject kept for %s: %+v", uid, id)
				}
			}
		}
		if n := subjectRows(t, f, "asid-a"); n != 0 {
			t.Fatalf("rows %d", n)
		}
	})

	t.Run("invalid arguments", func(t *testing.T) {
		f := newFixture(t)
		useFacebook(t, f, fb, noRequired)
		for _, c := range []struct {
			kind    enum.IdentityKind
			subject string
		}{
			{enum.IdentityPhone, "x"}, {enum.IdentityEmail, "x"}, {enum.IdentityKindUnspecified, "x"},
			{enum.IdentityKind("bad kind"), "x"}, {enum.IdentityFacebook, ""},
		} {
			res, err := f.svc.DeleteProviderIdentityData(ctx, c.kind, c.subject, *f.clock, meta1)
			if !errors.Is(err, user.ErrInvalidArgument) || res != (user.ProviderDeletionResult{}) {
				t.Fatalf("%+v: %+v %v", c, res, err)
			}
		}
		for _, subject := range []string{"asid-a", "nobody"} {
			res, err := f.svc.DeleteProviderIdentityData(ctx, enum.IdentityFacebook, subject, time.Time{}, meta1)
			if !errors.Is(err, user.ErrInvalidArgument) || res != (user.ProviderDeletionResult{}) {
				t.Fatalf("zero request time %s: %+v %v", subject, res, err)
			}
		}
		if evs := deletionEvents(f); len(evs) != 0 {
			t.Fatalf("invalid arguments must not be audited as processed: %+v", evs)
		}
	})

	t.Run("audit failure does not change result", func(t *testing.T) {
		f := newFixture(t)
		// Recorder.Record 没有错误返回；失败发生在默认 Async 的底层写入，这里让 Inserter 每次失败。
		async := audit.NewAsync(failingInserter{}, audit.AsyncOptions{QueueSize: 64, BatchSize: 1, FlushInterval: time.Millisecond, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
		async.Start(ctx)
		defer async.Close()
		useFacebook(t, f, fb, func(d *user.Deps) {
			noRequired(d)
			d.Audit = async
		})
		phone := f.signIn(t, enum.IdentityPhone, phone1, dev1)
		if _, _, err := f.svc.BindWithIdp(ctx, principalOf(t, f, phone), facebookCred(`{"access_token":"a"}`), meta1); err != nil {
			t.Fatal(err)
		}
		f.advance(time.Minute)
		res, err := f.svc.DeleteProviderIdentityData(ctx, enum.IdentityFacebook, "asid-a", *f.clock, meta1)
		if err != nil || res.Status != user.ProviderDeletionDeleted || res.ConfirmationCode == "" {
			t.Fatalf("%+v %v", res, err)
		}
		if subjectRows(t, f, "asid-a") != 0 {
			t.Fatal("identity must be anonymized regardless of audit")
		}
		async.Close()
		if st := async.Stats(); st.Failed == 0 || st.Written != 0 {
			t.Fatalf("audit write failure not exercised: %+v", st)
		}
	})

	t.Run("unrevoked session without authentication identities", func(t *testing.T) {
		f := newFixture(t)
		useFacebook(t, f, fb, noRequired)
		phone := f.signIn(t, enum.IdentityPhone, phone1, dev1)
		if _, _, err := f.svc.BindWithIdp(ctx, principalOf(t, f, phone), facebookCred(`{"access_token":"a"}`), meta1); err != nil {
			t.Fatal(err)
		}
		fbTokens := fbSignIn(t, f, "a", dev2)
		mustExec(t, f, `DELETE FROM session_authentication_identity WHERE session_id = $1`, principalOf(t, f, phone).SessionID)
		f.advance(time.Minute)
		resetAuditKeep(t, f)
		res, err := f.svc.DeleteProviderIdentityData(ctx, enum.IdentityFacebook, "asid-a", *f.clock, meta1)
		if err == nil || !strings.Contains(err.Error(), "invariant violated") || res != (user.ProviderDeletionResult{}) {
			t.Fatalf("%+v %v", res, err)
		}
		active, _ := identitiesOf(t, f, phone.UserID)
		if !hasKind(active, enum.IdentityFacebook) || subjectRows(t, f, "asid-a") == 0 {
			t.Fatalf("identity must stay: %+v", active)
		}
		if _, err := f.svc.Refresh(ctx, fbTokens.RefreshToken, meta1); err != nil {
			t.Fatalf("facebook session must stay: %v", err)
		}
		evs := deletionEvents(f)
		if len(evs) != 1 || evs[0].Result != enum.ResultFailure || len(eventsOf(f, enum.EventIdentityUnbound))+len(eventsOf(f, enum.EventSessionRevoked)) != 0 {
			t.Fatalf("audit: %+v", f.audit.Events())
		}
	})
}

type failingInserter struct{}

func (failingInserter) InsertBatch(context.Context, []audit.Event) (audit.BatchResult, error) {
	return audit.BatchResult{}, errors.New("synthetic audit outage")
}

// resetAuditKeep 换上新的审计内存并保留 f.deps 中的其他修改。
func resetAuditKeep(t *testing.T, f *fixture) {
	t.Helper()
	mem := &testAuditMemory{}
	newServiceWith(t, f, func(d *user.Deps) { d.Audit = mem })
	f.audit = mem
}

// 并发场景每轮使用新 fixture，重复固定轮数；每轮结束时结果必须等同于某一串行顺序。
const providerDeletionRounds = 20

// stagger 在交替轮次中让一方稍后启动，使两种串行顺序都有机会出现；0 延迟轮次直接竞争。
func stagger(round int, deletion bool) time.Duration {
	d := time.Duration(round/2%5) * 2 * time.Millisecond
	if (round%2 == 0) == deletion {
		return d
	}
	return 0
}

func TestProviderDataDeletionConcurrentWithBind(t *testing.T) {
	ctx := context.Background()
	counts := map[string]int{}
	for round := 0; round < providerDeletionRounds; round++ {
		f := newFixture(t)
		fb := &fakeFacebook{identities: map[string]idp.Identity{"c": {Subject: "asid-c"}}}
		useFacebook(t, f, fb, noRequired)
		fbTokens := fbSignIn(t, f, "c", dev1)
		p := principalOf(t, f, fbTokens)
		challenge, err := f.svc.SendBindCode(ctx, p, enum.IdentityPhone, phone1, meta1)
		if err != nil {
			t.Fatal(err)
		}
		cred := user.CodeCredential{Channel: enum.IdentityPhone, Target: phone1, CodeID: challenge.CodeID, Code: f.sent.code(normalizedTarget(enum.IdentityPhone, phone1))}
		f.advance(time.Minute)
		issued := *f.clock

		var (
			wg              sync.WaitGroup
			res             user.ProviderDeletionResult
			delErr, bindErr error
		)
		start := make(chan struct{})
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			time.Sleep(stagger(round, true))
			res, delErr = f.svc.DeleteProviderIdentityData(ctx, enum.IdentityFacebook, "asid-c", issued, meta1)
		}()
		go func() {
			defer wg.Done()
			<-start
			time.Sleep(stagger(round, false))
			_, _, bindErr = f.svc.BindWithCode(ctx, p, cred, meta1)
		}()
		close(start)
		wg.Wait()

		if delErr != nil {
			t.Fatalf("round %d: delete: %v", round, delErr)
		}
		me := stateOf(t, f, fbTokens.UserID)
		active, _ := identitiesOf(t, f, fbTokens.UserID)
		phoneActive, fbActive := hasKind(active, enum.IdentityPhone), hasKind(active, enum.IdentityFacebook)
		if me.State == enum.UserPendingDeletion && phoneActive {
			t.Fatalf("round %d: account pending deletion with active phone", round)
		}
		switch res.Status {
		case user.ProviderDeletionScheduled: // 删除在前：账号注销，绑定被拒。
			if me.State != enum.UserPendingDeletion || phoneActive || !errors.Is(bindErr, user.ErrInvalidToken) || !fbActive {
				t.Fatalf("round %d: scheduled but state=%s phone=%v fb=%v bind=%v", round, me.State, phoneActive, fbActive, bindErr)
			}
		case user.ProviderDeletionDeleted: // 绑定在前：只抹除 Facebook 身份。
			if me.State != enum.UserActive || !phoneActive || fbActive || bindErr != nil || subjectRows(t, f, "asid-c") != 0 {
				t.Fatalf("round %d: deleted but state=%s phone=%v fb=%v bind=%v", round, me.State, phoneActive, fbActive, bindErr)
			}
		default:
			t.Fatalf("round %d: unexpected %+v", round, res)
		}
		counts[string(res.Status)]++
	}
	t.Logf("outcomes: %v", counts)
}

func TestProviderDataDeletionConcurrentWithUnbind(t *testing.T) {
	ctx := context.Background()
	counts := map[string]int{}
	for round := 0; round < providerDeletionRounds; round++ {
		f := newFixture(t)
		fb := &fakeFacebook{identities: map[string]idp.Identity{"c": {Subject: "asid-c"}}}
		useFacebook(t, f, fb, noRequired)
		phone := f.signIn(t, enum.IdentityPhone, phone1, dev1)
		p := principalOf(t, f, phone)
		info, _, err := f.svc.BindWithIdp(ctx, p, facebookCred(`{"access_token":"c"}`), meta1)
		if err != nil {
			t.Fatal(err)
		}
		f.advance(time.Minute)
		issued := *f.clock

		var (
			wg                sync.WaitGroup
			res               user.ProviderDeletionResult
			delErr, unbindErr error
		)
		start := make(chan struct{})
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			time.Sleep(stagger(round, true))
			res, delErr = f.svc.DeleteProviderIdentityData(ctx, enum.IdentityFacebook, "asid-c", issued, meta1)
		}()
		go func() {
			defer wg.Done()
			<-start
			time.Sleep(stagger(round, false))
			unbindErr = f.svc.UnbindIdentity(ctx, p, info.ID, meta1)
		}()
		close(start)
		wg.Wait()

		if delErr != nil || res.Status != user.ProviderDeletionDeleted {
			t.Fatalf("round %d: delete: %+v %v", round, res, delErr)
		}
		if unbindErr != nil && !errors.Is(unbindErr, user.ErrNotFound) {
			t.Fatalf("round %d: unbind: %v", round, unbindErr)
		}
		me := stateOf(t, f, phone.UserID)
		active, _ := identitiesOf(t, f, phone.UserID)
		if me.State != enum.UserActive || !hasKind(active, enum.IdentityPhone) || hasKind(active, enum.IdentityFacebook) {
			t.Fatalf("round %d: state=%s active=%+v", round, me.State, active)
		}
		// 删除在前：身份已抹除，解绑未命中；解绑在前：删除找不到活动身份，不修改。
		if unbindErr != nil {
			if subjectRows(t, f, "asid-c") != 0 {
				t.Fatalf("round %d: delete first must anonymize", round)
			}
			counts["delete-first"]++
		} else {
			counts["unbind-first-or-anonymized"]++
		}
	}
	t.Logf("outcomes: %v", counts)
}

func TestProviderDataDeletionConcurrentWithDeleteMe(t *testing.T) {
	ctx := context.Background()
	counts := map[string]int{}
	for round := 0; round < providerDeletionRounds; round++ {
		f := newFixture(t)
		fb := &fakeFacebook{identities: map[string]idp.Identity{"c": {Subject: "asid-c"}}}
		useFacebook(t, f, fb, noRequired)
		fbTokens := fbSignIn(t, f, "c", dev1)
		p := principalOf(t, f, fbTokens)
		f.advance(time.Minute)
		issued := *f.clock

		var (
			wg               sync.WaitGroup
			res              user.ProviderDeletionResult
			delErr, deleteMe error
		)
		start := make(chan struct{})
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			time.Sleep(stagger(round, true))
			res, delErr = f.svc.DeleteProviderIdentityData(ctx, enum.IdentityFacebook, "asid-c", issued, meta1)
		}()
		go func() {
			defer wg.Done()
			<-start
			time.Sleep(stagger(round, false))
			_, deleteMe = f.svc.DeleteMe(ctx, p, meta1)
		}()
		close(start)
		wg.Wait()

		if delErr != nil || res.Status != user.ProviderDeletionScheduled {
			t.Fatalf("round %d: delete: %+v %v", round, res, delErr)
		}
		me := stateOf(t, f, fbTokens.UserID)
		if me.State != enum.UserPendingDeletion || me.PurgeTime == nil || !me.PurgeTime.Equal(res.PurgeTime) {
			t.Fatalf("round %d: me=%+v res=%+v", round, me, res)
		}
		if n := len(eventsOf(f, enum.EventUserDeleted)); n != 1 {
			t.Fatalf("round %d: USER_DELETED events %d", round, n)
		}
		if deleteMe != nil {
			counts["provider-first"]++
		} else {
			counts["deleteMe-first"]++
		}
	}
	t.Logf("outcomes: %v", counts)
}
