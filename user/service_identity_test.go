package user_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/code"
	"github.com/bbxx111/accountkit/user/idp"
)

// principalOf 用 Authenticate 取当前 access 的 Principal。
func principalOf(t *testing.T, f *fixture, res user.TokenResult) user.Principal {
	t.Helper()
	p, err := f.svc.Authenticate(context.Background(), res.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestListIdentitiesMasksAnchors(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	res := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	list, err := f.svc.ListIdentities(ctx, res.UserID)
	if err != nil || len(list) != 1 || list[0].Kind != enum.IdentityPhone || list[0].MaskedSubject != "+86 138****1234" || list[0].ID == "" || list[0].CreateTime.IsZero() {
		t.Fatalf("list: %+v %v", list, err)
	}
	if list, _ := f.svc.ListIdentities(ctx, "u_0000000000000"); len(list) != 0 {
		t.Fatal("unknown user → empty list")
	}
}

func TestSendBindCodeUsesBindPurposeAndQuota(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	res := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	p := principalOf(t, f, res)
	if err := f.svc.SendBindCode(ctx, p, enum.IdentityEmail, "Bind.Me@Shifang.co ", meta1); err != nil {
		t.Fatal(err)
	}
	if f.sent.code(email1) == "" {
		t.Fatal("code must be sent to the normalized target")
	}
	if !hasEvent(f.audit, enum.EventCodeSent, enum.ResultSuccess) {
		t.Fatal("audit CODE_SENT")
	}
	// 冷却生效（与登录码共用额度）
	if err := f.svc.SendBindCode(ctx, p, enum.IdentityEmail, email1, meta1); err == nil {
		t.Fatal("cooldown must apply")
	}
	// 非锚点渠道 / 非法 target
	if err := f.svc.SendBindCode(ctx, p, enum.IdentityWeChat, "x", meta1); !errors.Is(err, user.ErrInvalidArgument) && !errors.Is(err, user.ErrInvalidTarget) {
		t.Fatalf("wechat channel: %v", err)
	}
	if err := f.svc.SendBindCode(ctx, p, enum.IdentityEmail, "not-an-email", meta1); !errors.Is(err, user.ErrInvalidTarget) {
		t.Fatalf("bad target: %v", err)
	}
}

func TestBindWithCodeHappyPathIdempotentAndScopeAfterRefresh(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	// 微信登录 → user:bind
	res, err := f.svc.SignInWithIdp(ctx, user.IdpCredential{Kind: enum.IdentityWeChat, AppID: "wx1", Code: "union:UB1@o1"}, dev1, meta1)
	if err != nil || res.Scope != user.ScopeBind {
		t.Fatalf("wechat sign-in: %+v %v", res, err)
	}
	p := principalOf(t, f, res)
	if err := f.svc.SendBindCode(ctx, p, enum.IdentityPhone, phone2, meta1); err != nil {
		t.Fatal(err)
	}
	// 登录码不能用于绑定（用途隔离）。code.Store 的 codeKey 按 purpose 区分，但此刻 BIND 用途
	// 下 phone2 仍有一个未消费、未过期的活码（上面 SendBindCode 签发的那个），因此这里递交的
	// SIGN_IN 码比对的是同一把 BIND 键、不同的摘要 → MISMATCH（ErrInvalid），而不是键缺失
	// （ErrExpired）；两者都是"这码不能用于绑定"的合法体现，与既有的
	// TestReauthenticateUpdatesAuthTimeOnlyForAnchor 用途隔离断言（只判 err != nil，不钉死具体
	// sentinel）同一取舍，避免对 code 包内部键控实现细节的偶然性做过度断言。
	f.advance(61 * time.Second)
	_ = f.svc.SendSignInCode(ctx, enum.IdentityPhone, phone2, meta1)
	if _, _, err := f.svc.BindWithCode(ctx, p, enum.IdentityPhone, phone2, f.sent.code(phone2), meta1); !errors.Is(err, code.ErrInvalid) && !errors.Is(err, code.ErrExpired) {
		t.Fatalf("SIGN_IN code must not bind: %v", err)
	}
	if !hasEvent(f.audit, enum.EventIdentityBindRejected, enum.ResultFailure) {
		t.Fatal("audit IDENTITY_BIND_REJECTED")
	}
	f.advance(61 * time.Second)
	_ = f.svc.SendBindCode(ctx, p, enum.IdentityPhone, phone2, meta1)
	// 错码
	if _, _, err := f.svc.BindWithCode(ctx, p, enum.IdentityPhone, phone2, "000000", meta1); !errors.Is(err, code.ErrInvalid) {
		t.Fatalf("wrong code: %v", err)
	}
	info, created, err := f.svc.BindWithCode(ctx, p, enum.IdentityPhone, phone2, f.sent.code(phone2), meta1)
	if err != nil || !created || info.Kind != enum.IdentityPhone || info.MaskedSubject != "+86 138****0002" {
		t.Fatalf("bind: %+v %v %v", info, created, err)
	}
	if !hasEvent(f.audit, enum.EventIdentityBound, enum.ResultSuccess) {
		t.Fatal("audit IDENTITY_BOUND")
	}
	list, _ := f.svc.ListIdentities(ctx, res.UserID)
	if len(list) != 2 {
		t.Fatalf("identities: %+v", list)
	}
	// 当前 access 仍是 user:bind；刷新后得到 user
	if p2 := principalOf(t, f, res); p2.Scope != user.ScopeBind {
		t.Fatal("bind must not change the live access token")
	}
	next, err := f.svc.Refresh(ctx, res.RefreshToken, meta1)
	if err != nil || next.Scope != user.ScopeUser {
		t.Fatalf("refresh after bind: %+v %v", next, err)
	}
	// 幂等：同 subject 再绑到本账号 → 既有身份、created=false、不计上限
	f.advance(61 * time.Second)
	_ = f.svc.SendBindCode(ctx, p, enum.IdentityPhone, phone2, meta1)
	again, created, err := f.svc.BindWithCode(ctx, p, enum.IdentityPhone, phone2, f.sent.code(phone2), meta1)
	if err != nil || created || again.ID != info.ID {
		t.Fatalf("idempotent bind: %+v %v %v", again, created, err)
	}
	if !hasEventReason(f.audit, enum.EventIdentityBound, enum.ResultSuccess, "ALREADY_BOUND") {
		t.Fatal("audit ALREADY_BOUND")
	}
}

func TestBindWithCodeConflictAndKindLimit(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	// 账号 A 拥有 phone1；账号 B（微信）尝试绑定 phone1 → 冲突
	f.signIn(t, enum.IdentityPhone, phone1, dev1)
	resB, _ := f.svc.SignInWithIdp(ctx, user.IdpCredential{Kind: enum.IdentityWeChat, AppID: "wx1", Code: "union:UB2@o1"}, dev2, meta1)
	pB := principalOf(t, f, resB)
	f.advance(61 * time.Second)
	_ = f.svc.SendBindCode(ctx, pB, enum.IdentityPhone, phone1, meta1)
	if _, _, err := f.svc.BindWithCode(ctx, pB, enum.IdentityPhone, phone1, f.sent.code(phone1), meta1); !errors.Is(err, user.ErrIdentityConflict) {
		t.Fatalf("conflict: %v", err)
	}
	if !hasEventReason(f.audit, enum.EventIdentityBindRejected, enum.ResultFailure, "IDENTITY_ALREADY_BOUND") {
		t.Fatal("audit IDENTITY_ALREADY_BOUND")
	}
	if list, _ := f.svc.ListIdentities(ctx, resB.UserID); len(list) != 1 {
		t.Fatal("rejected bind must not create a row")
	}
	// 每 kind 上限（默认 1）：B 已有 WECHAT；A 已有 PHONE，再绑第二个手机号 → 上限。
	// 先过冷却：上面 pB 对 phone1 的 SendBindCode 与登录码共用同一冷却桶（channel+digest，不含
	// purpose），f.signIn 内部要为 phone1 重新发登录码，必须等冷却过期。
	f.advance(61 * time.Second)
	resA := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	pA := principalOf(t, f, resA)
	f.advance(61 * time.Second)
	_ = f.svc.SendBindCode(ctx, pA, enum.IdentityPhone, phone2, meta1)
	if _, _, err := f.svc.BindWithCode(ctx, pA, enum.IdentityPhone, phone2, f.sent.code(phone2), meta1); !errors.Is(err, user.ErrIdentityKindLimit) {
		t.Fatalf("kind limit: %v", err)
	}
	if !hasEventReason(f.audit, enum.EventIdentityBindRejected, enum.ResultFailure, "IDENTITY_KIND_LIMIT") {
		t.Fatal("audit IDENTITY_KIND_LIMIT")
	}
	// 不同 kind 不受影响：A 绑邮箱成功
	_ = f.svc.SendBindCode(ctx, pA, enum.IdentityEmail, email1, meta1)
	if _, created, err := f.svc.BindWithCode(ctx, pA, enum.IdentityEmail, email1, f.sent.code(email1), meta1); err != nil || !created {
		t.Fatalf("email bind: %v", err)
	}
	// 冻结用户不能绑
	mustExec(t, f, "UPDATE user_account SET state = 2 WHERE id = $1", resB.UserID)
	f.advance(61 * time.Second)
	_ = f.svc.SendBindCode(ctx, pB, enum.IdentityPhone, phone2, meta1)
	if _, _, err := f.svc.BindWithCode(ctx, pB, enum.IdentityPhone, phone2, f.sent.code(phone2), meta1); !errors.Is(err, user.ErrUserFrozen) {
		t.Fatalf("frozen: %v", err)
	}
}

func TestNewServiceRejectsZeroMaxIdentitiesPerKind(t *testing.T) {
	f := newFixture(t)
	d := f.deps
	d.MaxIdentitiesPerKind = 0
	if _, err := user.NewService(d); err == nil {
		t.Fatal("MaxIdentitiesPerKind must be >= 1")
	}
}

func TestBindWithIdpHappyIdempotentConflictAndLimit(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	// 手机号账号 A 绑定微信
	resA := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	pA := principalOf(t, f, resA)
	info, created, err := f.svc.BindWithIdp(ctx, pA, user.IdpCredential{Kind: enum.IdentityWeChat, AppID: "wx1", Code: "union:UBI1@o-wx1"}, meta1)
	if err != nil || !created || info.Kind != enum.IdentityWeChat || info.MaskedSubject != "" {
		t.Fatalf("bind wechat: %+v %v %v", info, created, err)
	}
	if !hasEvent(f.audit, enum.EventIdentityBound, enum.ResultSuccess) {
		t.Fatal("audit IDENTITY_BOUND")
	}
	// 幂等 + openid 合并（另一 App）
	again, created, err := f.svc.BindWithIdp(ctx, pA, user.IdpCredential{Kind: enum.IdentityWeChat, AppID: "wx2", Code: "union:UBI1@o-wx2"}, meta1)
	if err != nil || created || again.ID != info.ID {
		t.Fatalf("idempotent: %+v %v %v", again, created, err)
	}
	rows, _ := f.repo.Q().ListActiveIdentitiesByUser(ctx, resA.UserID)
	var meta string
	for _, r := range rows {
		if r.Kind == enum.IdentityWeChat {
			meta = string(r.ProviderMeta)
		}
	}
	if !strings.Contains(meta, `"wx1": "o-wx1"`) || !strings.Contains(meta, `"wx2": "o-wx2"`) {
		t.Fatalf("openids must merge on idempotent bind: %s", meta)
	}
	// 该 UnionID 已属于 A；账号 B 绑定 → 冲突
	resB := f.signIn(t, enum.IdentityPhone, phone2, dev2)
	pB := principalOf(t, f, resB)
	if _, _, err := f.svc.BindWithIdp(ctx, pB, user.IdpCredential{Kind: enum.IdentityWeChat, AppID: "wx1", Code: "union:UBI1@o-x"}, meta1); !errors.Is(err, user.ErrIdentityConflict) {
		t.Fatalf("conflict: %v", err)
	}
	// A 已有一个 WECHAT，绑另一个 UnionID → 上限
	if _, _, err := f.svc.BindWithIdp(ctx, pA, user.IdpCredential{Kind: enum.IdentityWeChat, AppID: "wx1", Code: "union:UBI2@o-y"}, meta1); !errors.Is(err, user.ErrIdentityKindLimit) {
		t.Fatalf("kind limit: %v", err)
	}
	// Apple 绑定：hint_email 不影响；nonce 重放被拒并审计
	appleInfo, created, err := f.svc.BindWithIdp(ctx, pB, user.IdpCredential{Kind: enum.IdentityApple, IDToken: "tokB+email", Nonce: "nb1"}, meta1)
	if err != nil || !created || appleInfo.Kind != enum.IdentityApple || appleInfo.MaskedSubject != "" {
		t.Fatalf("bind apple: %+v %v", appleInfo, err)
	}
	if _, _, err := f.svc.BindWithIdp(ctx, pB, user.IdpCredential{Kind: enum.IdentityApple, IDToken: "tokB+email", Nonce: "nb1"}, meta1); !errors.Is(err, idp.ErrNonceReplayed) {
		t.Fatalf("replay: %v", err)
	}
	if !hasEventReason(f.audit, enum.EventIdentityBindRejected, enum.ResultFailure, "IDP_NONCE_REPLAYED") {
		t.Fatal("audit IDP_NONCE_REPLAYED")
	}
	// 绑定不改变现有会话；刷新后 scope 仍是 user（本来就有锚点）
	next, _ := f.svc.Refresh(ctx, resA.RefreshToken, meta1)
	if next.Scope != user.ScopeUser {
		t.Fatal("scope after idp bind")
	}
	// 邮箱账号绑定手机后 EMAIL 不受影响：列表 2 个身份
	list, _ := f.svc.ListIdentities(ctx, resB.UserID)
	if len(list) != 2 { // PHONE + APPLE
		t.Fatalf("B identities: %+v", list)
	}
}

func TestBindWithIdpRejectsNonIdpKindAndDisabledProvider(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	res := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	p := principalOf(t, f, res)
	if _, _, err := f.svc.BindWithIdp(ctx, p, user.IdpCredential{Kind: enum.IdentityEmail}, meta1); !errors.Is(err, user.ErrInvalidArgument) {
		t.Fatalf("email kind: %v", err)
	}
	f.svc = newServiceWithout(t, f, "apple")
	if _, _, err := f.svc.BindWithIdp(ctx, p, user.IdpCredential{Kind: enum.IdentityApple, IDToken: "t", Nonce: "n"}, meta1); !errors.Is(err, idp.ErrAppNotAllowed) {
		t.Fatalf("apple disabled: %v", err)
	}
	if !hasEventReason(f.audit, enum.EventIdentityBindRejected, enum.ResultFailure, "IDP_APP_NOT_ALLOWED") {
		t.Fatal("audit IDP_APP_NOT_ALLOWED")
	}
}

func TestUnbindIdentityGuardsLastAnchorAndOwnership(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	res := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	p := principalOf(t, f, res)
	// 只有一个锚点 → 不能解绑
	list, _ := f.svc.ListIdentities(ctx, res.UserID)
	phoneID := list[0].ID
	if err := f.svc.UnbindIdentity(ctx, p, phoneID, meta1); !errors.Is(err, user.ErrLastAnchor) {
		t.Fatalf("last anchor: %v", err)
	}
	// 绑一个邮箱后可以解绑手机
	_ = f.svc.SendBindCode(ctx, p, enum.IdentityEmail, email1, meta1)
	emailInfo, _, err := f.svc.BindWithCode(ctx, p, enum.IdentityEmail, email1, f.sent.code(email1), meta1)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.UnbindIdentity(ctx, p, phoneID, meta1); err != nil {
		t.Fatalf("unbind phone: %v", err)
	}
	if !hasEvent(f.audit, enum.EventIdentityUnbound, enum.ResultSuccess) {
		t.Fatal("audit IDENTITY_UNBOUND")
	}
	list, _ = f.svc.ListIdentities(ctx, res.UserID)
	if len(list) != 1 || list[0].ID != emailInfo.ID {
		t.Fatalf("after unbind: %+v", list)
	}
	// 再解绑同一 id → 404；剩下的邮箱是最后锚点 → 拒绝
	if err := f.svc.UnbindIdentity(ctx, p, phoneID, meta1); !errors.Is(err, user.ErrNotFound) {
		t.Fatalf("double unbind: %v", err)
	}
	if err := f.svc.UnbindIdentity(ctx, p, emailInfo.ID, meta1); !errors.Is(err, user.ErrLastAnchor) {
		t.Fatalf("last email anchor: %v", err)
	}
	// 第三方身份不是锚点，随时可解绑；解绑后同 UnionID 可绑到别的账号
	wx, _, err := f.svc.BindWithIdp(ctx, p, user.IdpCredential{Kind: enum.IdentityWeChat, AppID: "wx1", Code: "union:UU1@o"}, meta1)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.UnbindIdentity(ctx, p, wx.ID, meta1); err != nil {
		t.Fatalf("unbind wechat: %v", err)
	}
	resB, _ := f.svc.SignInWithIdp(ctx, user.IdpCredential{Kind: enum.IdentityWeChat, AppID: "wx1", Code: "union:UB2@o1"}, dev2, meta1)
	pB := principalOf(t, f, resB)
	// A 解绑的 phone1 可被 B 绑定（部分唯一索引）
	f.advance(61 * time.Second)
	_ = f.svc.SendBindCode(ctx, pB, enum.IdentityPhone, phone1, meta1)
	if _, created, err := f.svc.BindWithCode(ctx, pB, enum.IdentityPhone, phone1, f.sent.code(phone1), meta1); err != nil || !created {
		t.Fatalf("rebind phone1 to B: %v", err)
	}
	// A 解绑的 WeChat UU1 也可被 C 绑定
	resC := f.signIn(t, enum.IdentityPhone, phone2, dev2)
	pC := principalOf(t, f, resC)
	if _, created, err := f.svc.BindWithIdp(ctx, pC, user.IdpCredential{Kind: enum.IdentityWeChat, AppID: "wx1", Code: "union:UU1@o"}, meta1); err != nil || !created {
		t.Fatalf("rebind wx after unbind: %v", err)
	}
	// 非本人 / 非法 id
	if err := f.svc.UnbindIdentity(ctx, p, emailInfo.ID, meta1); !errors.Is(err, user.ErrLastAnchor) {
		t.Fatalf("still last anchor for A: %v", err)
	}
	if err := f.svc.UnbindIdentity(ctx, pB, emailInfo.ID, meta1); !errors.Is(err, user.ErrNotFound) {
		t.Fatalf("foreign identity: %v", err)
	}
	if err := f.svc.UnbindIdentity(ctx, pB, "not-an-id", meta1); !errors.Is(err, user.ErrInvalidArgument) {
		t.Fatalf("malformed id: %v", err)
	}
	// 解绑不吊销会话：A 的 access 仍有效
	if _, err := f.svc.Authenticate(ctx, res.AccessToken); err != nil {
		t.Fatalf("session must survive unbind: %v", err)
	}
}

// TestBindWithCodeRejectsNonActiveAccountStates：PENDING_DELETION/DELETED 下的绑定必须
// 报 ErrInvalidToken（该会话按不变式已被吊销，客户端应丢弃凭证），且不写 IDENTITY_BOUND /
// IDENTITY_BIND_REJECTED 审计（这是"会话失效"而非安全事件）。
func TestBindWithCodeRejectsNonActiveAccountStates(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	res := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	p := principalOf(t, f, res)

	mustExec(t, f, "UPDATE user_account SET state = 3 WHERE id = $1", res.UserID)
	_ = f.svc.SendBindCode(ctx, p, enum.IdentityEmail, email1, meta1)
	if _, _, err := f.svc.BindWithCode(ctx, p, enum.IdentityEmail, email1, f.sent.code(email1), meta1); !errors.Is(err, user.ErrInvalidToken) {
		t.Fatalf("pending deletion: %v", err)
	}
	if hasEvent(f.audit, enum.EventIdentityBound, enum.ResultSuccess) || hasEvent(f.audit, enum.EventIdentityBindRejected, enum.ResultFailure) {
		t.Fatal("no bind audit event on invariant rejection")
	}

	f.advance(61 * time.Second)
	mustExec(t, f, "UPDATE user_account SET state = 4 WHERE id = $1", res.UserID)
	_ = f.svc.SendBindCode(ctx, p, enum.IdentityEmail, email1, meta1)
	if _, _, err := f.svc.BindWithCode(ctx, p, enum.IdentityEmail, email1, f.sent.code(email1), meta1); !errors.Is(err, user.ErrInvalidToken) {
		t.Fatalf("deleted: %v", err)
	}
	if hasEvent(f.audit, enum.EventIdentityBound, enum.ResultSuccess) || hasEvent(f.audit, enum.EventIdentityBindRejected, enum.ResultFailure) {
		t.Fatal("no bind audit event on invariant rejection")
	}
}

// TestBindWithIdpRejectsNonActiveAccountStates：同上，走第三方绑定路径（不需要验证码，
// 状态切换后直接复用同一 principal 再次调用）。
func TestBindWithIdpRejectsNonActiveAccountStates(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	res := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	p := principalOf(t, f, res)

	mustExec(t, f, "UPDATE user_account SET state = 3 WHERE id = $1", res.UserID)
	if _, _, err := f.svc.BindWithIdp(ctx, p, user.IdpCredential{Kind: enum.IdentityWeChat, AppID: "wx1", Code: "union:USTATE1@o1"}, meta1); !errors.Is(err, user.ErrInvalidToken) {
		t.Fatalf("pending deletion: %v", err)
	}
	if hasEvent(f.audit, enum.EventIdentityBound, enum.ResultSuccess) || hasEvent(f.audit, enum.EventIdentityBindRejected, enum.ResultFailure) {
		t.Fatal("no bind audit event on invariant rejection")
	}

	mustExec(t, f, "UPDATE user_account SET state = 4 WHERE id = $1", res.UserID)
	if _, _, err := f.svc.BindWithIdp(ctx, p, user.IdpCredential{Kind: enum.IdentityWeChat, AppID: "wx1", Code: "union:USTATE2@o2"}, meta1); !errors.Is(err, user.ErrInvalidToken) {
		t.Fatalf("deleted: %v", err)
	}
	if hasEvent(f.audit, enum.EventIdentityBound, enum.ResultSuccess) || hasEvent(f.audit, enum.EventIdentityBindRejected, enum.ResultFailure) {
		t.Fatal("no bind audit event on invariant rejection")
	}
}

// TestUnbindIdentityRejectsFrozenPendingDeletionAndDeletedAccounts：解绑此前完全不检查账号
// 状态；FROZEN → ErrUserFrozen，PENDING_DELETION/DELETED → ErrInvalidToken，且都不写
// IDENTITY_UNBOUND 审计。
func TestUnbindIdentityRejectsFrozenPendingDeletionAndDeletedAccounts(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	res := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	p := principalOf(t, f, res)
	_ = f.svc.SendBindCode(ctx, p, enum.IdentityEmail, email1, meta1)
	emailInfo, _, err := f.svc.BindWithCode(ctx, p, enum.IdentityEmail, email1, f.sent.code(email1), meta1)
	if err != nil {
		t.Fatal(err)
	}

	mustExec(t, f, "UPDATE user_account SET state = 2 WHERE id = $1", res.UserID)
	if err := f.svc.UnbindIdentity(ctx, p, emailInfo.ID, meta1); !errors.Is(err, user.ErrUserFrozen) {
		t.Fatalf("frozen: %v", err)
	}

	mustExec(t, f, "UPDATE user_account SET state = 3 WHERE id = $1", res.UserID)
	if err := f.svc.UnbindIdentity(ctx, p, emailInfo.ID, meta1); !errors.Is(err, user.ErrInvalidToken) {
		t.Fatalf("pending deletion: %v", err)
	}

	mustExec(t, f, "UPDATE user_account SET state = 4 WHERE id = $1", res.UserID)
	if err := f.svc.UnbindIdentity(ctx, p, emailInfo.ID, meta1); !errors.Is(err, user.ErrInvalidToken) {
		t.Fatalf("deleted: %v", err)
	}
	if hasEvent(f.audit, enum.EventIdentityUnbound, enum.ResultSuccess) {
		t.Fatal("no unbind audit event on invariant rejection")
	}
}

// TestUnbindLastTwoAnchorsConcurrently：账号只有 PHONE+EMAIL 两个锚点，并发解绑两个，
// 数据库行锁（LockUserByID）必须让两次解绑串行化，恰好一个成功、一个撞上"最后锚点"。
func TestUnbindLastTwoAnchorsConcurrently(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	res := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	p := principalOf(t, f, res)
	list, _ := f.svc.ListIdentities(ctx, res.UserID)
	phoneID := list[0].ID
	_ = f.svc.SendBindCode(ctx, p, enum.IdentityEmail, email1, meta1)
	emailInfo, _, err := f.svc.BindWithCode(ctx, p, enum.IdentityEmail, email1, f.sent.code(email1), meta1)
	if err != nil {
		t.Fatal(err)
	}
	targets := [2]string{phoneID, emailInfo.ID}
	errs := [2]error{}
	var ready, start sync.WaitGroup
	ready.Add(2)
	start.Add(1)
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Go(func() {
			ready.Done()
			start.Wait()
			errs[i] = f.svc.UnbindIdentity(ctx, p, targets[i], meta1)
		})
	}
	ready.Wait()
	start.Done()
	wg.Wait()
	nilCount, lastAnchorCount := 0, 0
	for _, e := range errs {
		switch {
		case e == nil:
			nilCount++
		case errors.Is(e, user.ErrLastAnchor):
			lastAnchorCount++
		default:
			t.Fatalf("unexpected error: %v", e)
		}
	}
	if nilCount != 1 || lastAnchorCount != 1 {
		t.Fatalf("want exactly one success and one ErrLastAnchor, got errs=%v", errs)
	}
	if list, _ := f.svc.ListIdentities(ctx, res.UserID); len(list) != 1 {
		t.Fatalf("exactly one anchor must remain: %+v", list)
	}
}

// TestBindSameSubjectConcurrentlyFromTwoAccounts：同一微信 UnionID 被两个已存在账号并发
// BindWithIdp。选用 WeChat 而非短信/邮件 BIND 码方案：f.sent（capture）按 (channel, target)
// 只保留最后一条验证码，两个账号并发对同一 target 发送 BIND 码会互相覆盖，导致其中一次绑定
// 用的其实是另一次发的码而不可控地失败于验证码校验，而不是命中我们要验证的"唯一索引兜底"
// 路径；WeChat 绑定不经过验证码，天然给出确定性的并发场景。
func TestBindSameSubjectConcurrentlyFromTwoAccounts(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	// A、B 均以 PHONE 登录（而非微信）：MaxIdentitiesPerKind=1，若改用微信登录，两个账号各自
	// 已有的登录期微信身份会先一步占满 WECHAT 的每 kind 上限，掩盖本测试要验证的唯一索引路径。
	resA := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	pA := principalOf(t, f, resA)
	resB := f.signIn(t, enum.IdentityPhone, phone2, dev2)
	pB := principalOf(t, f, resB)
	principals := [2]user.Principal{pA, pB}
	codes := [2]string{"union:UCONFLICT@ca", "union:UCONFLICT@cb"} // 同一 UnionID，不同 openid
	created := [2]bool{}
	errs := [2]error{}
	var ready, start sync.WaitGroup
	ready.Add(2)
	start.Add(1)
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Go(func() {
			ready.Done()
			start.Wait()
			_, created[i], errs[i] = f.svc.BindWithIdp(ctx, principals[i], user.IdpCredential{Kind: enum.IdentityWeChat, AppID: "wx2", Code: codes[i]}, meta1)
		})
	}
	ready.Wait()
	start.Done()
	wg.Wait()
	createdCount, conflictCount := 0, 0
	for _, c := range created {
		if c {
			createdCount++
		}
	}
	for _, e := range errs {
		switch {
		case e == nil:
		case errors.Is(e, user.ErrIdentityConflict):
			conflictCount++
		default:
			t.Fatalf("unexpected error: %v", e)
		}
	}
	if createdCount != 1 || conflictCount != 1 {
		t.Fatalf("want exactly one created and one ErrIdentityConflict, got created=%v errs=%v", created, errs)
	}
	rowsA, _ := f.repo.Q().ListActiveIdentitiesByUser(ctx, resA.UserID)
	rowsB, _ := f.repo.Q().ListActiveIdentitiesByUser(ctx, resB.UserID)
	n := 0
	for _, r := range rowsA {
		if r.Kind == enum.IdentityWeChat && r.ProviderSubject != nil && *r.ProviderSubject == "UCONFLICT" {
			n++
		}
	}
	for _, r := range rowsB {
		if r.Kind == enum.IdentityWeChat && r.ProviderSubject != nil && *r.ProviderSubject == "UCONFLICT" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("exactly one active identity row must exist for the contested subject, got %d", n)
	}
}
