package user_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/db"
)

var admin1 = user.Admin{Issuer: "https://kc.example/realms/shifang-admin", Subject: "adm-1", Username: "ops"}

// adminEvents 返回类型匹配且 actor 为 ADMIN 的事件。
func adminEvents(m *audit.Memory, typ enum.EventType) []audit.Event {
	var out []audit.Event
	for _, e := range m.Events() {
		if e.Type == typ && e.Actor == enum.ActorAdmin {
			out = append(out, e)
		}
	}
	return out
}

func assertAdminFields(t *testing.T, e audit.Event, userID string) {
	t.Helper()
	if e.AdminIssuer != admin1.Issuer || e.AdminSubject != admin1.Subject || e.AdminUsername != admin1.Username || e.UserID != userID || e.IP != meta1.IP || e.RequestID != meta1.RequestID {
		t.Fatalf("admin event fields: %+v", e)
	}
}

func sessionIDOf(t *testing.T, f *fixture, res user.TokenResult) string {
	t.Helper()
	sess, err := f.repo.Q().GetSessionByRefreshHash(context.Background(), sha256Of(res.RefreshToken))
	if err != nil {
		t.Fatal(err)
	}
	return sess.ID
}

func TestFreezeRevokesSessionsSnapshotsAndAudits(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	a := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	b := f.signIn(t, enum.IdentityPhone, phone1, dev2)
	mem := resetAudit(f)

	u, err := f.svc.Freeze(ctx, admin1, a.UserID, "  abuse report #42  ", meta1)
	if err != nil {
		t.Fatal(err)
	}
	if u.State != enum.UserFrozen || u.Freeze == nil || u.Freeze.Reason != "abuse report #42" || u.Freeze.ActorSubject != "adm-1" || u.Freeze.ActorUsername != "ops" || !u.Freeze.Time.Equal(*f.clock) {
		t.Fatalf("frozen user: %+v", u)
	}
	// 全部会话吊销：access 被吊销集拒绝，refresh → ErrUserFrozen（3a 语义），锚点登录 → ErrUserFrozen
	for _, tok := range []user.TokenResult{a, b} {
		if _, err := f.svc.Authenticate(ctx, tok.AccessToken); !errors.Is(err, user.ErrInvalidToken) {
			t.Fatalf("access after freeze: %v", err)
		}
	}
	if rows, _ := f.repo.Q().ListActiveSessionsByUser(ctx, db.ListActiveSessionsByUserParams{UserID: a.UserID, Now: f.clock.UTC().Truncate(time.Microsecond)}); len(rows) != 0 {
		t.Fatalf("active sessions after freeze: %d", len(rows))
	}
	if _, err := f.svc.Refresh(ctx, a.RefreshToken, meta1); !errors.Is(err, user.ErrInvalidGrant) {
		t.Fatalf("refresh of a revoked session: %v", err)
	}
	_ = f.svc.SendSignInCode(ctx, enum.IdentityPhone, phone1, meta1)
	if _, err := f.svc.SignInWithCode(ctx, enum.IdentityPhone, phone1, f.sent.code(phone1), dev1, meta1); !errors.Is(err, user.ErrUserFrozen) {
		t.Fatalf("login while frozen: %v", err)
	}
	// 审计：USER_FROZEN（reason 为管理员填写值）+ 每个会话一条 SESSION_REVOKED（reason USER_FROZEN），全部 ADMIN
	frozen := adminEvents(mem, enum.EventUserFrozen)
	if len(frozen) != 1 || frozen[0].Reason != "abuse report #42" || frozen[0].Result != enum.ResultSuccess {
		t.Fatalf("USER_FROZEN: %+v", frozen)
	}
	assertAdminFields(t, frozen[0], a.UserID)
	revoked := adminEvents(mem, enum.EventSessionRevoked)
	if len(revoked) != 2 || revoked[0].Reason != enum.RevokeUserFrozen.String() || revoked[0].SessionID == "" {
		t.Fatalf("SESSION_REVOKED: %+v", revoked)
	}
	assertAdminFields(t, revoked[0], a.UserID)
	// 再冻 → ErrUserFrozen；不再产生事件
	if _, err := f.svc.Freeze(ctx, admin1, a.UserID, "again", meta1); !errors.Is(err, user.ErrUserFrozen) {
		t.Fatalf("freeze twice: %v", err)
	}
	if len(adminEvents(mem, enum.EventUserFrozen)) != 1 {
		t.Fatal("rejected freeze must not be audited")
	}
	// 解冻：快照清空、登录恢复；USER_UNFROZEN 带 reason
	f.advance(61 * time.Second)
	u, err = f.svc.Unfreeze(ctx, admin1, a.UserID, "resolved", meta1)
	if err != nil || u.State != enum.UserActive || u.Freeze != nil {
		t.Fatalf("unfreeze: %+v %v", u, err)
	}
	row, _ := f.repo.Q().GetUserByID(ctx, a.UserID)
	if row.FreezeTime != nil || row.FreezeReason != nil || row.FreezeActorSubject != nil || row.FreezeActorUsername != nil {
		t.Fatalf("freeze snapshot must be cleared: %+v", row)
	}
	unfrozen := adminEvents(mem, enum.EventUserUnfrozen)
	if len(unfrozen) != 1 || unfrozen[0].Reason != "resolved" {
		t.Fatalf("USER_UNFROZEN: %+v", unfrozen)
	}
	if again := f.signIn(t, enum.IdentityPhone, phone1, dev1); again.UserID != a.UserID || again.Scope != user.ScopeUser {
		t.Fatalf("login after unfreeze: %+v", again)
	}
	// 解冻 ACTIVE → ErrInvalidState；未知用户 → ErrNotFound；unfreeze 的 reason 可为空
	if _, err := f.svc.Unfreeze(ctx, admin1, a.UserID, "", meta1); !errors.Is(err, user.ErrInvalidState) {
		t.Fatalf("unfreeze active: %v", err)
	}
	if _, err := f.svc.Freeze(ctx, admin1, "u_0000000000000", "x", meta1); !errors.Is(err, user.ErrNotFound) {
		t.Fatalf("freeze unknown user: %v", err)
	}
	// reason 校验：必填、≤ 200 rune、无控制字符
	for name, bad := range map[string]string{"empty": "   ", "too long": strings.Repeat("长", 201), "control": "a\nb", "bidi": "a‮b"} {
		if _, err := f.svc.Freeze(ctx, admin1, a.UserID, bad, meta1); !errors.Is(err, user.ErrInvalidArgument) {
			t.Fatalf("reason %s: %v", name, err)
		}
	}
	if _, err := f.svc.Freeze(ctx, admin1, a.UserID, strings.Repeat("长", 200), meta1); err != nil {
		t.Fatalf("200-rune reason must pass: %v", err)
	}
}

func TestAdminDeleteAndUndeleteShareConsumerRules(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	res := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	mem := resetAudit(f)

	u, err := f.svc.AdminDeleteUser(ctx, admin1, res.UserID, meta1)
	if err != nil || u.State != enum.UserPendingDeletion || u.DeleteTime == nil || u.PurgeTime == nil || !u.PurgeTime.Equal(f.clock.Add(360*time.Hour)) {
		t.Fatalf("admin delete: %+v %v", u, err)
	}
	if _, err := f.svc.Authenticate(ctx, res.AccessToken); !errors.Is(err, user.ErrInvalidToken) {
		t.Fatalf("sessions must be revoked: %v", err)
	}
	deleted := adminEvents(mem, enum.EventUserDeleted)
	if len(deleted) != 1 || deleted[0].SessionID != "" {
		t.Fatalf("USER_DELETED by admin: %+v", deleted)
	}
	assertAdminFields(t, deleted[0], res.UserID)
	if n := len(adminEvents(mem, enum.EventSessionRevoked)); n != 1 {
		t.Fatalf("SESSION_REVOKED by admin: %d", n)
	}
	// 冷静期锚点登录仍得 user:undelete；代办恢复
	if again := f.signIn(t, enum.IdentityPhone, phone1, dev1); again.Scope != user.ScopeUndelete {
		t.Fatalf("pending login: %+v", again)
	}
	u, err = f.svc.AdminUndeleteUser(ctx, admin1, res.UserID, meta1)
	if err != nil || u.State != enum.UserActive || u.DeleteTime != nil || u.PurgeTime != nil {
		t.Fatalf("admin undelete: %+v %v", u, err)
	}
	if ev := adminEvents(mem, enum.EventUserUndeleted); len(ev) != 1 {
		t.Fatalf("USER_UNDELETED by admin: %+v", ev)
	}
	if _, err := f.svc.AdminUndeleteUser(ctx, admin1, res.UserID, meta1); !errors.Is(err, user.ErrInvalidState) {
		t.Fatalf("undelete active: %v", err)
	}
	// 冻结账号不能代办删除（ErrUserFrozen → 管理面 400）；未知用户 → ErrNotFound（不是 ErrInvalidToken）
	if _, err := f.svc.Freeze(ctx, admin1, res.UserID, "hold", meta1); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.AdminDeleteUser(ctx, admin1, res.UserID, meta1); !errors.Is(err, user.ErrUserFrozen) {
		t.Fatalf("delete frozen: %v", err)
	}
	if _, err := f.svc.AdminDeleteUser(ctx, admin1, "u_0000000000000", meta1); !errors.Is(err, user.ErrNotFound) {
		t.Fatalf("delete unknown: %v", err)
	}
	// C 端语义不变：未知用户 → ErrInvalidToken
	ghost := user.Principal{UserID: "u_0000000000000", SessionID: "s_0000000000000", Scope: user.ScopeUser}
	if _, err := f.svc.DeleteMe(ctx, ghost, meta1); !errors.Is(err, user.ErrInvalidToken) {
		t.Fatalf("consumer delete unknown: %v", err)
	}
}

func TestAdminSessionsListRevokeOneAndAll(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	a := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	b := f.signIn(t, enum.IdentityPhone, phone1, dev2)
	c := f.signIn(t, enum.IdentityPhone, phone1, user.Device{ID: "dev-3"})
	mem := resetAudit(f)

	list, err := f.svc.AdminListSessions(ctx, a.UserID)
	if err != nil || len(list) != 3 {
		t.Fatalf("list: %d %v", len(list), err)
	}
	for _, s := range list {
		if s.IsCurrent {
			t.Fatal("admin listing has no current session")
		}
	}
	if _, err := f.svc.AdminListSessions(ctx, "u_0000000000000"); !errors.Is(err, user.ErrNotFound) {
		t.Fatalf("list unknown user: %v", err)
	}
	sidA := sessionIDOf(t, f, a)
	if err := f.svc.AdminRevokeSession(ctx, admin1, a.UserID, sidA, meta1); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Authenticate(ctx, a.AccessToken); !errors.Is(err, user.ErrInvalidToken) {
		t.Fatalf("revoked session's access: %v", err)
	}
	if err := f.svc.AdminRevokeSession(ctx, admin1, a.UserID, sidA, meta1); !errors.Is(err, user.ErrNotFound) {
		t.Fatalf("revoke twice: %v", err)
	}
	if err := f.svc.AdminRevokeSession(ctx, admin1, "u_0000000000000", sessionIDOf(t, f, b), meta1); !errors.Is(err, user.ErrNotFound) {
		t.Fatalf("revoke with wrong user: %v", err)
	}
	n, err := f.svc.AdminRevokeAllSessions(ctx, admin1, a.UserID, meta1)
	if err != nil || n != 2 {
		t.Fatalf("revoke all: %d %v", n, err)
	}
	for _, tok := range []user.TokenResult{b, c} {
		if _, err := f.svc.Authenticate(ctx, tok.AccessToken); !errors.Is(err, user.ErrInvalidToken) {
			t.Fatalf("access after revoke all: %v", err)
		}
	}
	if n, err := f.svc.AdminRevokeAllSessions(ctx, admin1, a.UserID, meta1); err != nil || n != 0 {
		t.Fatalf("revoke all again: %d %v", n, err)
	}
	if _, err := f.svc.AdminRevokeAllSessions(ctx, admin1, "u_0000000000000", meta1); !errors.Is(err, user.ErrNotFound) {
		t.Fatalf("revoke all unknown: %v", err)
	}
	revoked := adminEvents(mem, enum.EventSessionRevoked)
	if len(revoked) != 3 {
		t.Fatalf("SESSION_REVOKED events: %d", len(revoked))
	}
	for _, e := range revoked {
		if e.Reason != enum.RevokeAdmin.String() || e.SessionID == "" {
			t.Fatalf("admin revoke event: %+v", e)
		}
		assertAdminFields(t, e, a.UserID)
	}
}

func userIDs(p user.UserPage) []string {
	out := make([]string, 0, len(p.Users))
	for _, u := range p.Users {
		out = append(out, u.ID)
	}
	return out
}

func sameIDs(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestListUsersFiltersAndPaginates(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	a := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	b := f.signIn(t, enum.IdentityPhone, phone2, dev1)
	c := f.signIn(t, enum.IdentityEmail, email1, dev1)
	// create_time 由 DB 默认 now() 决定，三者几乎同时；显式拉开以便断言顺序
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for i, id := range []string{a.UserID, b.UserID, c.UserID} {
		mustExec(t, f, `UPDATE user_account SET create_time = $2 WHERE id = $1`, id, base.Add(time.Duration(i)*time.Hour))
	}
	strp := func(s string) *string { return &s }

	// 分页：每页 2
	p1, err := f.svc.ListUsers(ctx, user.UserFilter{}, nil, 2)
	if err != nil || !sameIDs(userIDs(p1), a.UserID, b.UserID) || p1.NextCursor == nil || p1.NextCursor.ID != b.UserID {
		t.Fatalf("page 1: %v cursor=%+v err=%v", userIDs(p1), p1.NextCursor, err)
	}
	p2, err := f.svc.ListUsers(ctx, user.UserFilter{}, p1.NextCursor, 2)
	if err != nil || !sameIDs(userIDs(p2), c.UserID) || p2.NextCursor != nil {
		t.Fatalf("page 2: %v cursor=%+v err=%v", userIDs(p2), p2.NextCursor, err)
	}
	// limit 越界
	for _, bad := range []int{0, -1, user.AdminPageSizeMax + 1} {
		if _, err := f.svc.ListUsers(ctx, user.UserFilter{}, nil, bad); !errors.Is(err, user.ErrInvalidArgument) {
			t.Fatalf("limit %d: %v", bad, err)
		}
	}
	// identity.phone（原始写法带空格 → 归一化 → AllDigests）
	pg, err := f.svc.ListUsers(ctx, user.UserFilter{IdentityKind: enum.IdentityPhone, Subject: strp("138 1234 1234")}, nil, 10)
	if err != nil || !sameIDs(userIDs(pg), a.UserID) {
		t.Fatalf("by phone: %v %v", userIDs(pg), err)
	}
	if _, err := f.svc.ListUsers(ctx, user.UserFilter{IdentityKind: enum.IdentityPhone, Subject: strp("not-a-phone")}, nil, 10); !errors.Is(err, user.ErrInvalidTarget) {
		t.Fatalf("invalid phone: %v", err)
	}
	// identity.email / email_domain / phone_suffix
	if pg, _ = f.svc.ListUsers(ctx, user.UserFilter{IdentityKind: enum.IdentityEmail, Subject: strp("Bind.Me@Shifang.co")}, nil, 10); !sameIDs(userIDs(pg), c.UserID) {
		t.Fatalf("by email: %v", userIDs(pg))
	}
	if pg, _ = f.svc.ListUsers(ctx, user.UserFilter{IdentityKind: enum.IdentityEmail, HintSuffix: strp("shifang.co")}, nil, 10); !sameIDs(userIDs(pg), c.UserID) {
		t.Fatalf("by email domain: %v", userIDs(pg))
	}
	if pg, _ = f.svc.ListUsers(ctx, user.UserFilter{IdentityKind: enum.IdentityPhone, HintSuffix: strp("1234")}, nil, 10); !sameIDs(userIDs(pg), a.UserID) {
		t.Fatalf("by phone suffix: %v", userIDs(pg))
	}
	// state 与 show_deleted
	if _, err := f.svc.Freeze(ctx, admin1, b.UserID, "hold", meta1); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.AdminDeleteUser(ctx, admin1, c.UserID, meta1); err != nil {
		t.Fatal(err)
	}
	frozen := enum.UserFrozen
	if pg, _ = f.svc.ListUsers(ctx, user.UserFilter{State: &frozen}, nil, 10); !sameIDs(userIDs(pg), b.UserID) || pg.Users[0].Freeze == nil || pg.Users[0].Freeze.Reason != "hold" {
		t.Fatalf("by state: %+v", pg)
	}
	if pg, _ = f.svc.ListUsers(ctx, user.UserFilter{}, nil, 10); !sameIDs(userIDs(pg), a.UserID, b.UserID) {
		t.Fatalf("default hides pending deletion: %v", userIDs(pg))
	}
	if pg, _ = f.svc.ListUsers(ctx, user.UserFilter{IncludeDeleted: true}, nil, 10); !sameIDs(userIDs(pg), a.UserID, b.UserID, c.UserID) {
		t.Fatalf("show_deleted: %v", userIDs(pg))
	}
	// create_time 范围 [1h, 2h) → b
	lo, hi := base.Add(time.Hour), base.Add(2*time.Hour)
	if pg, _ = f.svc.ListUsers(ctx, user.UserFilter{CreateTimeMin: &lo, CreateTimeMax: &hi}, nil, 10); !sameIDs(userIDs(pg), b.UserID) {
		t.Fatalf("time range: %v", userIDs(pg))
	}
}

func TestGetUserDetailAndRevealIdentity(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	res := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	_ = f.signIn(t, enum.IdentityPhone, phone1, dev2)
	p := principalOf(t, f, res)
	if _, _, err := f.svc.BindWithIdp(ctx, p, user.IdpCredential{Kind: enum.IdentityWeChat, AppID: "wx1", Code: "union:U-REVEALS@o1"}, meta1); err != nil {
		t.Fatal(err)
	}
	other := f.signIn(t, enum.IdentityPhone, phone2, dev1)
	mem := resetAudit(f)

	d, err := f.svc.GetUserDetail(ctx, res.UserID)
	if err != nil || d.ID != res.UserID || d.State != enum.UserActive || d.ActiveSessionCount != 2 || len(d.Identities) != 2 || d.Freeze != nil {
		t.Fatalf("detail: %+v %v", d, err)
	}
	var phoneID, wxID string
	for _, id := range d.Identities {
		switch id.Kind {
		case enum.IdentityPhone:
			phoneID = id.ID
			if id.MaskedSubject != "+86 138****1234" {
				t.Fatalf("masked phone: %q", id.MaskedSubject)
			}
		case enum.IdentityWeChat:
			wxID = id.ID
			if id.MaskedSubject != "" {
				t.Fatalf("provider identity must be masked to empty: %q", id.MaskedSubject)
			}
		}
	}
	if _, err := f.svc.GetUserDetail(ctx, "u_0000000000000"); !errors.Is(err, user.ErrNotFound) {
		t.Fatalf("detail unknown: %v", err)
	}
	// reveal：锚点解密、第三方明文；审计 IDENTITY_REVEALED（ADMIN，hint 8 位，不含明文）
	rv, err := f.svc.RevealIdentity(ctx, admin1, res.UserID, phoneID, meta1)
	if err != nil || rv.Kind != enum.IdentityPhone || rv.Subject != phone1 || rv.ID != phoneID {
		t.Fatalf("reveal phone: %+v %v", rv, err)
	}
	rv, err = f.svc.RevealIdentity(ctx, admin1, res.UserID, wxID, meta1)
	if err != nil || rv.Kind != enum.IdentityWeChat || rv.Subject != "U-REVEALS" {
		t.Fatalf("reveal wechat: %+v %v", rv, err)
	}
	revealed := adminEvents(mem, enum.EventIdentityRevealed)
	if len(revealed) != 2 {
		t.Fatalf("IDENTITY_REVEALED events: %+v", revealed)
	}
	for _, e := range revealed {
		assertAdminFields(t, e, res.UserID)
		if len(e.SubjectHint) != 8 || e.SubjectHint == phone1[:8] {
			t.Fatalf("hint must be 8 chars of digest/provider_subject: %+v", e)
		}
		if e.Reason == phone1 || strings.Contains(e.SubjectHint, "+86") {
			t.Fatalf("plaintext must never enter audit: %+v", e)
		}
	}
	// 他人的身份 / 已解绑的身份 / 非法 id → 404 / 400
	if _, err := f.svc.RevealIdentity(ctx, admin1, other.UserID, phoneID, meta1); !errors.Is(err, user.ErrNotFound) {
		t.Fatalf("foreign identity: %v", err)
	}
	if err := f.svc.UnbindIdentity(ctx, p, wxID, meta1); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.RevealIdentity(ctx, admin1, res.UserID, wxID, meta1); !errors.Is(err, user.ErrNotFound) {
		t.Fatalf("unbound identity: %v", err)
	}
	if _, err := f.svc.RevealIdentity(ctx, admin1, res.UserID, "not-an-id", meta1); !errors.Is(err, user.ErrInvalidArgument) {
		t.Fatalf("malformed id: %v", err)
	}
	// 详情反映解绑与冻结
	if _, err := f.svc.Freeze(ctx, admin1, res.UserID, "hold", meta1); err != nil {
		t.Fatal(err)
	}
	d, _ = f.svc.GetUserDetail(ctx, res.UserID)
	if d.State != enum.UserFrozen || d.Freeze == nil || d.ActiveSessionCount != 0 || len(d.Identities) != 1 {
		t.Fatalf("detail after unbind+freeze: %+v", d)
	}
}
