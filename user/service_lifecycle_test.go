package user_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
)

func TestNewServiceRejectsZeroDeletionCoolingPeriod(t *testing.T) {
	f := newFixture(t)
	d := f.deps
	d.DeletionCoolingPeriod = 0
	if _, err := user.NewService(d); err == nil {
		t.Fatal("DeletionCoolingPeriod = 0 must be rejected")
	}
}

func TestDeleteMeSoftDeletesRevokesAllSessionsAndAudits(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	a := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	b := f.signIn(t, enum.IdentityPhone, phone1, dev2)
	p := principalOf(t, f, a)
	start := *f.clock

	me, err := f.svc.DeleteMe(ctx, p, meta1)
	if err != nil {
		t.Fatal(err)
	}
	if me.ID != p.UserID || me.State != enum.UserPendingDeletion || me.DeleteTime == nil || !me.DeleteTime.Equal(start) || me.PurgeTime == nil || !me.PurgeTime.Equal(start.Add(360*time.Hour)) {
		t.Fatalf("me after delete: %+v", me)
	}
	// 两个会话都被吊销：DB 无活跃会话；access 因吊销集被拒；refresh 被拒
	if rows, err := f.repo.Q().ListActiveSessionsByUser(ctx, p.UserID); err != nil || len(rows) != 0 {
		t.Fatalf("active sessions after delete: %d %v", len(rows), err)
	}
	for _, tok := range []user.TokenResult{a, b} {
		if _, err := f.svc.Authenticate(ctx, tok.AccessToken); !errors.Is(err, user.ErrInvalidToken) {
			t.Fatalf("revoked session's access must be rejected: %v", err)
		}
		if _, err := f.svc.Refresh(ctx, tok.RefreshToken, meta1); !errors.Is(err, user.ErrInvalidGrant) {
			t.Fatalf("refresh after delete: %v", err)
		}
	}
	if !hasEvent(f.audit, enum.EventUserDeleted, enum.ResultSuccess) {
		t.Fatal("USER_DELETED missing")
	}
	revokedEvents := 0
	for _, e := range f.audit.Events() {
		if e.Type == enum.EventUserDeleted && (e.UserID != p.UserID || e.SessionID != p.SessionID || e.IP != meta1.IP || e.RequestID != meta1.RequestID || e.Actor != enum.ActorUser) {
			t.Fatalf("USER_DELETED fields: %+v", e)
		}
		if e.Type == enum.EventSessionRevoked && e.Reason == enum.RevokeUserDeleted.String() && e.UserID == p.UserID {
			revokedEvents++
		}
	}
	if revokedEvents != 2 {
		t.Fatalf("SESSION_REVOKED(USER_DELETED) events = %d, want 2", revokedEvents)
	}
	// 状态已非 ACTIVE：再次删除 → ErrInvalidState
	if _, err := f.svc.DeleteMe(ctx, p, meta1); !errors.Is(err, user.ErrInvalidState) {
		t.Fatalf("second delete: %v", err)
	}
}

func TestDeleteMeRejectsFrozenDeletedAndUnknownUser(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	res := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	p := principalOf(t, f, res)
	mem := resetAudit(f)

	mustExec(t, f, `UPDATE user_account SET state = 2 WHERE id = $1`, p.UserID) // FROZEN
	if _, err := f.svc.DeleteMe(ctx, p, meta1); !errors.Is(err, user.ErrUserFrozen) {
		t.Fatalf("frozen: %v", err)
	}
	// 拒绝路径不得吊销会话（吊销是冻结动作自己的事，不是这里的）
	if rows, _ := f.repo.Q().ListActiveSessionsByUser(ctx, p.UserID); len(rows) != 1 {
		t.Fatalf("rejected delete must not touch sessions: %d", len(rows))
	}
	mustExec(t, f, `UPDATE user_account SET state = 4 WHERE id = $1`, p.UserID) // DELETED
	if _, err := f.svc.DeleteMe(ctx, p, meta1); !errors.Is(err, user.ErrInvalidState) {
		t.Fatalf("deleted: %v", err)
	}
	ghost := p
	ghost.UserID = "u_0000000000000"
	if _, err := f.svc.DeleteMe(ctx, ghost, meta1); !errors.Is(err, user.ErrInvalidToken) {
		t.Fatalf("unknown user: %v", err)
	}
	if hasEvent(mem, enum.EventUserDeleted, enum.ResultSuccess) || hasEvent(mem, enum.EventSessionRevoked, enum.ResultSuccess) {
		t.Fatal("rejected deletes must not be audited as success")
	}
}

func TestUndeleteRestoresActiveAndRefreshWorksAgain(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	first := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	if _, err := f.svc.DeleteMe(ctx, principalOf(t, f, first), meta1); err != nil {
		t.Fatal(err)
	}
	// 冷静期内用锚点登录 → 同一账号、user:undelete
	again := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	if again.Scope != user.ScopeUndelete || again.IsNewUser || again.UserID != first.UserID {
		t.Fatalf("pending-deletion login: %+v", again)
	}
	p := principalOf(t, f, again)
	// 冷静期内刷新仍被拒并吊销（3a 语义不变）——这里用 dev2 的另一个会话验证，避免吊销掉 p 的会话
	other := f.signIn(t, enum.IdentityPhone, phone1, dev2)
	if _, err := f.svc.Refresh(ctx, other.RefreshToken, meta1); !errors.Is(err, user.ErrInvalidGrant) {
		t.Fatalf("refresh while pending deletion: %v", err)
	}

	me, err := f.svc.Undelete(ctx, p, meta1)
	if err != nil || me.State != enum.UserActive || me.DeleteTime != nil || me.PurgeTime != nil {
		t.Fatalf("undelete: %+v %v", me, err)
	}
	if !hasEvent(f.audit, enum.EventUserUndeleted, enum.ResultSuccess) {
		t.Fatal("USER_UNDELETED missing")
	}
	// 当前会话未被吊销；刷新后 scope 回到 user
	if _, err := f.svc.Authenticate(ctx, again.AccessToken); err != nil {
		t.Fatalf("undelete must keep the current session: %v", err)
	}
	ref, err := f.svc.Refresh(ctx, again.RefreshToken, meta1)
	if err != nil || ref.Scope != user.ScopeUser {
		t.Fatalf("refresh after undelete: %+v %v", ref, err)
	}
	// 再次 undelete → ErrInvalidState；FROZEN → ErrUserFrozen；DELETED（已 purge）→ ErrInvalidState
	if _, err := f.svc.Undelete(ctx, p, meta1); !errors.Is(err, user.ErrInvalidState) {
		t.Fatalf("undelete active: %v", err)
	}
	mustExec(t, f, `UPDATE user_account SET state = 2 WHERE id = $1`, p.UserID)
	if _, err := f.svc.Undelete(ctx, p, meta1); !errors.Is(err, user.ErrUserFrozen) {
		t.Fatalf("undelete frozen: %v", err)
	}
	mustExec(t, f, `UPDATE user_account SET state = 4 WHERE id = $1`, p.UserID)
	if _, err := f.svc.Undelete(ctx, p, meta1); !errors.Is(err, user.ErrInvalidState) {
		t.Fatalf("undelete purged: %v", err)
	}
}
