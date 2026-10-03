package user_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/db"
	"github.com/bbxx111/accountkit/user/idp"
)

// fakeWeChat：code → 结果；"union:<id>@<openid>" 形式的 code 直接成功。
type fakeWeChat struct{ err map[string]error }

func (f *fakeWeChat) VerifyWeChat(_ context.Context, appID, code string) (idp.Identity, error) {
	if appID != "wx1" && appID != "wx2" {
		return idp.Identity{}, idp.ErrAppNotAllowed
	}
	if e, ok := f.err[code]; ok {
		return idp.Identity{}, e
	}
	if rest, ok := strings.CutPrefix(code, "union:"); ok {
		sub, openid, _ := strings.Cut(rest, "@")
		return idp.Identity{Kind: enum.IdentityWeChat, Subject: sub, OpenIDs: map[string]string{appID: openid}}, nil
	}
	return idp.Identity{}, idp.ErrInvalidCredential
}

type fakeApple struct{ used map[string]bool }

func (f *fakeApple) VerifyApple(_ context.Context, idToken, nonce string) (idp.Identity, error) {
	if idToken == "bad" {
		return idp.Identity{}, idp.ErrInvalidCredential
	}
	if f.used == nil {
		f.used = map[string]bool{}
	}
	if f.used[nonce] {
		return idp.Identity{}, idp.ErrNonceReplayed
	}
	f.used[nonce] = true
	id := idp.Identity{Kind: enum.IdentityApple, Subject: "apple-" + idToken}
	if strings.HasSuffix(idToken, "+email") {
		id.HintEmail = "x@privaterelay.appleid.com"
	}
	return id, nil
}

var dev2 = user.Device{ID: "dev-2", Name: "iPad"}

func TestSignInWithIdpWeChatRegistersWithBindScopeAndMergesOpenIDs(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	res, err := f.svc.SignInWithIdp(ctx, user.IdpCredential{Kind: enum.IdentityWeChat, AppID: "wx1", Code: "union:U1@o-wx1"}, dev1, meta1)
	if err != nil || !res.IsNewUser || res.Scope != user.ScopeBind || res.RefreshToken == "" || res.HintEmail != "" {
		t.Fatalf("first wechat login: %+v %v", res, err)
	}
	idents, _ := f.repo.Q().ListActiveIdentitiesByUser(ctx, res.UserID)
	if len(idents) != 1 || idents[0].Kind != enum.IdentityWeChat || idents[0].ProviderSubject == nil || *idents[0].ProviderSubject != "U1" || idents[0].SubjectDigest != nil || !strings.Contains(string(idents[0].ProviderMeta), `"wx1": "o-wx1"`) {
		t.Fatalf("identity row: %+v", idents)
	}
	if !hasEvent(f.audit, enum.EventSignIn, enum.ResultSuccess) {
		t.Fatal("audit SIGN_IN")
	}
	// 从另一 App 登录同一 UnionID：同一用户、不是新用户、openid 合并
	res2, err := f.svc.SignInWithIdp(ctx, user.IdpCredential{Kind: enum.IdentityWeChat, AppID: "wx2", Code: "union:U1@o-wx2"}, dev2, meta1)
	if err != nil || res2.IsNewUser || res2.UserID != res.UserID || res2.Scope != user.ScopeBind {
		t.Fatalf("second app login: %+v %v", res2, err)
	}
	idents, _ = f.repo.Q().ListActiveIdentitiesByUser(ctx, res.UserID)
	if len(idents) != 1 || !strings.Contains(string(idents[0].ProviderMeta), `"wx1": "o-wx1"`) || !strings.Contains(string(idents[0].ProviderMeta), `"wx2": "o-wx2"`) {
		t.Fatalf("openids must merge: %s", idents[0].ProviderMeta)
	}
	// 两个设备各一条活动会话
	sessions, _ := f.svc.ListSessions(ctx, res.UserID, "")
	if len(sessions) != 2 {
		t.Fatalf("sessions: %d", len(sessions))
	}
	// 同设备再登录 → 替换旧会话
	res3, _ := f.svc.SignInWithIdp(ctx, user.IdpCredential{Kind: enum.IdentityWeChat, AppID: "wx1", Code: "union:U1@o-wx1"}, dev1, meta1)
	sessions, _ = f.svc.ListSessions(ctx, res.UserID, "")
	if len(sessions) != 2 || res3.UserID != res.UserID {
		t.Fatalf("same-device relogin must replace: %d", len(sessions))
	}
	// 刷新保持 user:bind（无锚点）
	next, err := f.svc.Refresh(ctx, res3.RefreshToken, meta1)
	if err != nil || next.Scope != user.ScopeBind {
		t.Fatalf("refresh scope: %+v %v", next, err)
	}
}

func TestSignInWithIdpAppleHintEmailAndReplay(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	res, err := f.svc.SignInWithIdp(ctx, user.IdpCredential{Kind: enum.IdentityApple, IDToken: "tok1+email", Nonce: "n1"}, dev1, meta1)
	if err != nil || !res.IsNewUser || res.Scope != user.ScopeBind || res.HintEmail != "x@privaterelay.appleid.com" {
		t.Fatalf("apple login: %+v %v", res, err)
	}
	idents, _ := f.repo.Q().ListActiveIdentitiesByUser(ctx, res.UserID)
	if len(idents) != 1 || idents[0].Kind != enum.IdentityApple || *idents[0].ProviderSubject != "apple-tok1+email" || idents[0].ProviderMeta != nil {
		t.Fatalf("identity: %+v", idents)
	}
	// hint_email 绝不自动绑定为 EMAIL 身份
	for _, id := range idents {
		if id.Kind == enum.IdentityEmail {
			t.Fatal("apple email must not be bound automatically")
		}
	}
	_, err = f.svc.SignInWithIdp(ctx, user.IdpCredential{Kind: enum.IdentityApple, IDToken: "tok1+email", Nonce: "n1"}, dev1, meta1)
	if !errors.Is(err, idp.ErrNonceReplayed) {
		t.Fatalf("replay: %v", err)
	}
	if !hasEventReason(f.audit, enum.EventSignInFailed, enum.ResultFailure, "IDP_NONCE_REPLAYED") {
		t.Fatal("audit SIGN_IN_FAILED IDP_NONCE_REPLAYED")
	}
}

func TestSignInWithIdpRejections(t *testing.T) {
	f := newFixture(t)
	f.wechat.err = map[string]error{"down": idp.ErrUnavailable, "miscfg": idp.ErrMisconfigured}
	ctx := context.Background()
	cases := []struct {
		name string
		cred user.IdpCredential
		want error
		aud  string // 期望的 SIGN_IN_FAILED reason；空 = 不写审计
	}{
		{"wechat invalid code", user.IdpCredential{Kind: enum.IdentityWeChat, AppID: "wx1", Code: "nope"}, idp.ErrInvalidCredential, "IDP_CREDENTIAL_INVALID"},
		{"wechat unknown app", user.IdpCredential{Kind: enum.IdentityWeChat, AppID: "wx9", Code: "union:U@o"}, idp.ErrAppNotAllowed, "IDP_APP_NOT_ALLOWED"},
		{"wechat unavailable", user.IdpCredential{Kind: enum.IdentityWeChat, AppID: "wx1", Code: "down"}, idp.ErrUnavailable, ""},
		{"wechat misconfigured", user.IdpCredential{Kind: enum.IdentityWeChat, AppID: "wx1", Code: "miscfg"}, idp.ErrMisconfigured, ""},
		{"apple invalid", user.IdpCredential{Kind: enum.IdentityApple, IDToken: "bad", Nonce: "n"}, idp.ErrInvalidCredential, "IDP_CREDENTIAL_INVALID"},
		{"phone kind not idp", user.IdpCredential{Kind: enum.IdentityPhone}, user.ErrInvalidArgument, ""},
	}
	for _, c := range cases {
		f.audit = resetAudit(f)
		_, err := f.svc.SignInWithIdp(ctx, c.cred, dev1, meta1)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: got %v want %v", c.name, err, c.want)
		}
		if c.aud != "" && !hasEventReason(f.audit, enum.EventSignInFailed, enum.ResultFailure, c.aud) {
			t.Errorf("%s: audit reason %s missing", c.name, c.aud)
		}
		if c.aud == "" && len(f.audit.Events()) != 0 {
			t.Errorf("%s: must not audit infrastructure errors: %+v", c.name, f.audit.Events())
		}
	}
	// 未启用（nil 校验器）
	f2 := newFixture(t)
	f2.svc = newServiceWithout(t, f2, "wechat")
	if _, err := f2.svc.SignInWithIdp(ctx, user.IdpCredential{Kind: enum.IdentityWeChat, AppID: "wx1", Code: "union:U@o"}, dev1, meta1); !errors.Is(err, idp.ErrAppNotAllowed) {
		t.Fatalf("wechat disabled: %v", err)
	}
	// 冻结用户
	f3 := newFixture(t)
	res, _ := f3.svc.SignInWithIdp(ctx, user.IdpCredential{Kind: enum.IdentityWeChat, AppID: "wx1", Code: "union:UF@o"}, dev1, meta1)
	mustExec(t, f3, "UPDATE user_account SET state = 2 WHERE id = $1", res.UserID)
	if _, err := f3.svc.SignInWithIdp(ctx, user.IdpCredential{Kind: enum.IdentityWeChat, AppID: "wx1", Code: "union:UF@o"}, dev1, meta1); !errors.Is(err, user.ErrUserFrozen) {
		t.Fatalf("frozen: %v", err)
	}
	if !hasEventReason(f3.audit, enum.EventSignInFailed, enum.ResultFailure, "USER_FROZEN") {
		t.Fatal("audit USER_FROZEN")
	}
}

// M5：微信偶尔返回 unionid 但没有 openid；空字符串不能被当成一个真实的 app→openid
// 映射写入 provider_meta（既不该在创建账号时写入，也不该在合并时写入）。
func TestSignInWithIdpWeChatIgnoresEmptyOpenID(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	res, err := f.svc.SignInWithIdp(ctx, user.IdpCredential{Kind: enum.IdentityWeChat, AppID: "wx1", Code: "union:U-empty"}, dev1, meta1)
	if err != nil {
		t.Fatalf("signin: %v", err)
	}
	idents, _ := f.repo.Q().ListActiveIdentitiesByUser(ctx, res.UserID)
	if len(idents) != 1 || idents[0].ProviderMeta != nil {
		t.Fatalf("empty openid must not be persisted: %+v", idents)
	}
	// 再从另一个 app 用同一 UnionID 登录、同样是空 openid：合并路径也不能写入。
	res2, err := f.svc.SignInWithIdp(ctx, user.IdpCredential{Kind: enum.IdentityWeChat, AppID: "wx2", Code: "union:U-empty"}, dev2, meta1)
	if err != nil || res2.UserID != res.UserID {
		t.Fatalf("second empty-openid login: %+v %v", res2, err)
	}
	idents, _ = f.repo.Q().ListActiveIdentitiesByUser(ctx, res.UserID)
	if len(idents) != 1 || idents[0].ProviderMeta != nil {
		t.Fatalf("merge path must not persist empty openid either: %+v", idents)
	}
}

// M8(b)：并发的“同一第三方账号、不同设备”首次登录必须只创建一个用户与一条身份
// （withRetryOnUnique 在唯一冲突上重试一次），每个设备各自建立会话，且恰好一个
// 调用方看到 IsNewUser。这需要真实数据库上的唯一约束，不能用 fake repo 模拟。
func TestSignInWithIdpWeChatConcurrentFirstLoginCreatesOneUser(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	const n = 8

	var wg sync.WaitGroup
	results := make([]user.TokenResult, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			dev := user.Device{ID: fmt.Sprintf("concurrent-dev-%02d", i), Name: "dev"}
			cred := user.IdpCredential{Kind: enum.IdentityWeChat, AppID: "wx1", Code: "union:CONC@o-conc"}
			results[i], errs[i] = f.svc.SignInWithIdp(ctx, cred, dev, meta1)
		}(i)
	}
	wg.Wait()

	var userID string
	newUserCount := 0
	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: %v", i, err)
		}
		if userID == "" {
			userID = results[i].UserID
		} else if results[i].UserID != userID {
			t.Fatalf("goroutine %d: user id mismatch: got %s want %s", i, results[i].UserID, userID)
		}
		if results[i].IsNewUser {
			newUserCount++
		}
	}
	if newUserCount != 1 {
		t.Fatalf("exactly one goroutine must observe IsNewUser, got %d", newUserCount)
	}

	var userRows int
	if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM user_account WHERE id = $1", userID).Scan(&userRows); err != nil {
		t.Fatal(err)
	}
	if userRows != 1 {
		t.Fatalf("user_account rows: %d want 1", userRows)
	}
	idents, err := f.repo.Q().ListActiveIdentitiesByUser(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(idents) != 1 {
		t.Fatalf("identity rows: %d want 1", len(idents))
	}
	sessions, err := f.svc.ListSessions(ctx, userID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != n {
		t.Fatalf("active sessions: %d want %d", len(sessions), n)
	}
}

func TestSignInWithCodeStillWorksAfterRefactor(t *testing.T) {
	// 回归守卫：抽取 establishSession 后，阶段 3a 的验证码登录行为不变（其余断言在 service_test.go 中）。
	f := newFixture(t)
	res := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	if res.Scope != user.ScopeUser || res.HintEmail != "" {
		t.Fatalf("%+v", res)
	}
}

func TestSignInWithIdpRejectsPendingDeletionAccount(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	// 手机登录建号 → 绑定微信 → 软删除
	res := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	p := principalOf(t, f, res)
	cred := user.IdpCredential{Kind: enum.IdentityWeChat, AppID: "wx1", Code: "union:U-PD@o1"}
	if _, _, err := f.svc.BindWithIdp(ctx, p, cred, meta1); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.DeleteMe(ctx, p, meta1); err != nil {
		t.Fatal(err)
	}
	mem := resetAudit(f)

	_, err := f.svc.SignInWithIdp(ctx, cred, dev2, meta1)
	if !errors.Is(err, user.ErrUserPendingDeletion) {
		t.Fatalf("idp login into a pending-deletion account: %v", err)
	}
	if !hasEventReason(mem, enum.EventSignInFailed, enum.ResultFailure, "USER_PENDING_DELETION") {
		t.Fatal("SIGN_IN_FAILED/USER_PENDING_DELETION missing")
	}
	for _, e := range mem.Events() {
		if e.Type == enum.EventSignInFailed && e.UserID != p.UserID {
			t.Fatalf("SIGN_IN_FAILED must carry the user id (rejected after the account was found): %+v", e)
		}
	}
	if rows, _ := f.repo.Q().ListActiveSessionsByUser(ctx, db.ListActiveSessionsByUserParams{UserID: p.UserID, Now: f.clock.UTC().Truncate(time.Microsecond)}); len(rows) != 0 {
		t.Fatalf("rejected login must not create a session: %d", len(rows))
	}
	// 锚点登录仍可进入并拿到 user:undelete
	again := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	if again.Scope != user.ScopeUndelete || again.UserID != p.UserID {
		t.Fatalf("anchor login while pending deletion: %+v", again)
	}
	// 取消注销后微信又能登录
	if _, err := f.svc.Undelete(ctx, principalOf(t, f, again), meta1); err != nil {
		t.Fatal(err)
	}
	back, err := f.svc.SignInWithIdp(ctx, cred, dev2, meta1)
	if err != nil || back.UserID != p.UserID || back.Scope != user.ScopeUser {
		t.Fatalf("idp login after undelete: %+v %v", back, err)
	}
}

func TestSignInWithCodeFrozenAuditCarriesUserID(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	res := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	mustExec(t, f, `UPDATE user_account SET state = 2 WHERE id = $1`, res.UserID)
	mem := resetAudit(f)
	if err := f.svc.SendSignInCode(ctx, enum.IdentityPhone, phone1, meta1); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SignInWithCode(ctx, enum.IdentityPhone, phone1, f.sent.code(phone1), dev1, meta1); !errors.Is(err, user.ErrUserFrozen) {
		t.Fatalf("frozen: %v", err)
	}
	found := false
	for _, e := range mem.Events() {
		if e.Type == enum.EventSignInFailed && e.Reason == "USER_FROZEN" {
			found = true
			if e.UserID != res.UserID {
				t.Fatalf("SIGN_IN_FAILED/USER_FROZEN must carry the user id: %+v", e)
			}
		}
	}
	if !found {
		t.Fatal("SIGN_IN_FAILED/USER_FROZEN missing")
	}
}
