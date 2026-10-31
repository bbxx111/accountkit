package user_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/internal/userdb"
	"github.com/bbxx111/accountkit/user"
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

func TestSignInWithIdpWeChatEmptyRequiredRegistersAndMergesOpenIDs(t *testing.T) {
	f := newFixture(t)
	newServiceWith(t, f, func(d *user.Deps) { d.RequiredIdentityKinds = []enum.IdentityKind{} })
	ctx := context.Background()
	outcome, err := f.svc.SignInWithIdp(ctx, wechatCred("wx1", "union:U1@o-wx1"), dev1, meta1)
	res := requireSignInTokens(t, outcome)
	if err != nil || !res.IsNewUser || res.Scope != user.ScopeUser || res.RefreshToken == "" || res.HintEmail != "" {
		t.Fatalf("first wechat login: %+v %v", res, err)
	}
	idents, _ := user.TestRepoQueries(f.repo).ListActiveIdentitiesByUser(ctx, res.UserID)
	if len(idents) != 1 || idents[0].Kind != enum.IdentityWeChat || idents[0].ProviderSubject == nil || *idents[0].ProviderSubject != "U1" || idents[0].SubjectDigest != nil || !strings.Contains(string(idents[0].ProviderMeta), `"wx1": "o-wx1"`) {
		t.Fatalf("identity row: %+v", idents)
	}
	if !hasEvent(f.audit, enum.EventSignIn, enum.ResultSuccess) {
		t.Fatal("audit SIGN_IN")
	}
	// 从另一 App 登录同一 UnionID：同一用户、不是新用户、openid 合并
	outcome2, err := f.svc.SignInWithIdp(ctx, wechatCred("wx2", "union:U1@o-wx2"), dev2, meta1)
	res2 := requireSignInTokens(t, outcome2)
	if err != nil || res2.IsNewUser || res2.UserID != res.UserID || res2.Scope != user.ScopeUser {
		t.Fatalf("second app login: %+v %v", res2, err)
	}
	idents, _ = user.TestRepoQueries(f.repo).ListActiveIdentitiesByUser(ctx, res.UserID)
	if len(idents) != 1 || !strings.Contains(string(idents[0].ProviderMeta), `"wx1": "o-wx1"`) || !strings.Contains(string(idents[0].ProviderMeta), `"wx2": "o-wx2"`) {
		t.Fatalf("openids must merge: %s", idents[0].ProviderMeta)
	}
	// 两个设备各一条活动会话
	sessions, _ := f.svc.ListSessions(ctx, res.UserID, "")
	if len(sessions) != 2 {
		t.Fatalf("sessions: %d", len(sessions))
	}
	// 同设备再登录 → 替换旧会话
	outcome3, _ := f.svc.SignInWithIdp(ctx, wechatCred("wx1", "union:U1@o-wx1"), dev1, meta1)
	res3 := requireSignInTokens(t, outcome3)
	sessions, _ = f.svc.ListSessions(ctx, res.UserID, "")
	if len(sessions) != 2 || res3.UserID != res.UserID {
		t.Fatalf("same-device relogin must replace: %d", len(sessions))
	}
	// Required=[] 时纯第三方身份的新一轮刷新仍满足完整性。
	next, err := f.svc.Refresh(ctx, res3.RefreshToken, meta1)
	if err != nil || next.Scope != user.ScopeUser {
		t.Fatalf("refresh scope: %+v %v", next, err)
	}
}

func TestSignInWithIdpAppleHintEmailAndReplay(t *testing.T) {
	f := newFixture(t)
	newServiceWith(t, f, func(d *user.Deps) { d.RequiredIdentityKinds = []enum.IdentityKind{} })
	ctx := context.Background()
	outcome, err := f.svc.SignInWithIdp(ctx, appleCred("tok1+email", "n1"), dev1, meta1)
	res := requireSignInTokens(t, outcome)
	if err != nil || !res.IsNewUser || res.Scope != user.ScopeUser || res.HintEmail != "x@privaterelay.appleid.com" {
		t.Fatalf("apple login: %+v %v", res, err)
	}
	idents, _ := user.TestRepoQueries(f.repo).ListActiveIdentitiesByUser(ctx, res.UserID)
	if len(idents) != 1 || idents[0].Kind != enum.IdentityApple || *idents[0].ProviderSubject != "apple-tok1+email" || idents[0].ProviderMeta != nil {
		t.Fatalf("identity: %+v", idents)
	}
	// hint_email 绝不自动绑定为 EMAIL 身份
	for _, id := range idents {
		if id.Kind == enum.IdentityEmail {
			t.Fatal("apple email must not be bound automatically")
		}
	}
	_, err = f.svc.SignInWithIdp(ctx, appleCred("tok1+email", "n1"), dev1, meta1)
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
		{"wechat invalid code", wechatCred("wx1", "nope"), idp.ErrInvalidCredential, "IDP_CREDENTIAL_INVALID"},
		{"wechat unknown app", wechatCred("wx9", "union:U@o"), idp.ErrAppNotAllowed, "IDP_APP_NOT_ALLOWED"},
		{"wechat unavailable", wechatCred("wx1", "down"), idp.ErrUnavailable, ""},
		{"wechat misconfigured", wechatCred("wx1", "miscfg"), idp.ErrMisconfigured, ""},
		{"apple invalid", appleCred("bad", "n"), idp.ErrInvalidCredential, "IDP_CREDENTIAL_INVALID"},
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
	if _, err := f2.svc.SignInWithIdp(ctx, wechatCred("wx1", "union:U@o"), dev1, meta1); !errors.Is(err, idp.ErrAppNotAllowed) {
		t.Fatalf("wechat disabled: %v", err)
	}
	// 冻结用户
	f3 := newFixture(t)
	newServiceWith(t, f3, func(d *user.Deps) { d.RequiredIdentityKinds = []enum.IdentityKind{} })
	outcome, _ := f3.svc.SignInWithIdp(ctx, wechatCred("wx1", "union:UF@o"), dev1, meta1)
	res := requireSignInTokens(t, outcome)
	mustExec(t, f3, "UPDATE user_account SET state = 2 WHERE id = $1", res.UserID)
	if _, err := f3.svc.SignInWithIdp(ctx, wechatCred("wx1", "union:UF@o"), dev1, meta1); !errors.Is(err, user.ErrUserFrozen) {
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
	newServiceWith(t, f, func(d *user.Deps) { d.RequiredIdentityKinds = []enum.IdentityKind{} })
	ctx := context.Background()
	outcome, err := f.svc.SignInWithIdp(ctx, wechatCred("wx1", "union:U-empty"), dev1, meta1)
	if err != nil {
		t.Fatalf("signin: %v", err)
	}
	res := requireSignInTokens(t, outcome)
	idents, _ := user.TestRepoQueries(f.repo).ListActiveIdentitiesByUser(ctx, res.UserID)
	if len(idents) != 1 || idents[0].ProviderMeta != nil {
		t.Fatalf("empty openid must not be persisted: %+v", idents)
	}
	// 再从另一个 app 用同一 UnionID 登录、同样是空 openid：合并路径也不能写入。
	outcome2, err := f.svc.SignInWithIdp(ctx, wechatCred("wx2", "union:U-empty"), dev2, meta1)
	res2 := requireSignInTokens(t, outcome2)
	if err != nil || res2.UserID != res.UserID {
		t.Fatalf("second empty-openid login: %+v %v", res2, err)
	}
	idents, _ = user.TestRepoQueries(f.repo).ListActiveIdentitiesByUser(ctx, res.UserID)
	if len(idents) != 1 || idents[0].ProviderMeta != nil {
		t.Fatalf("merge path must not persist empty openid either: %+v", idents)
	}
}

// M8(b)：并发的“同一第三方账号、不同设备”首次登录必须只创建一个用户与一条身份
// （withRetryOnUnique 在唯一冲突上重试一次），每个设备各自建立会话，且恰好一个
// 调用方看到 IsNewUser。这需要真实数据库上的唯一约束，不能用 fake repo 模拟。
func TestSignInWithIdpWeChatConcurrentFirstLoginCreatesOneUser(t *testing.T) {
	f := newFixture(t)
	newServiceWith(t, f, func(d *user.Deps) { d.RequiredIdentityKinds = []enum.IdentityKind{} })
	ctx := context.Background()
	const n = 8

	var wg sync.WaitGroup
	results := make([]user.SignInResult, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			dev := user.Device{ID: fmt.Sprintf("concurrent-dev-%02d", i), Name: "dev"}
			cred := wechatCred("wx1", "union:CONC@o-conc")
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
		pair := requireSignInTokens(t, results[i])
		if userID == "" {
			userID = pair.UserID
		} else if pair.UserID != userID {
			t.Fatalf("goroutine %d: user id mismatch: got %s want %s", i, pair.UserID, userID)
		}
		if pair.IsNewUser {
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
	idents, err := user.TestRepoQueries(f.repo).ListActiveIdentitiesByUser(ctx, userID)
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
	cred := wechatCred("wx1", "union:U-PD@o1")
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
	if rows, _ := user.TestRepoQueries(f.repo).ListActiveSessionsByUser(ctx, db.ListActiveSessionsByUserParams{UserID: p.UserID, Now: f.clock.UTC().Truncate(time.Microsecond)}); len(rows) != 0 {
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
	if err != nil || back.Tokens == nil || back.Pending != nil || back.Tokens.UserID != p.UserID || back.Tokens.Scope != user.ScopeUser {
		t.Fatalf("idp login after undelete: %+v %v", back, err)
	}
}

func TestSignInWithCodeFrozenAuditCarriesUserID(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	res := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	mustExec(t, f, `UPDATE user_account SET state = 2 WHERE id = $1`, res.UserID)
	mem := resetAudit(f)
	if err := f.sendSignInCode(ctx, enum.IdentityPhone, phone1, meta1); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SignInWithCode(ctx, f.credential(enum.PurposeSignIn, enum.IdentityPhone, phone1, f.sent.code(phone1)), dev1, meta1); !errors.Is(err, user.ErrUserFrozen) {
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

type fakeGoogle struct {
	identities map[string]idp.Identity
	used       map[string]bool
}

func (f *fakeGoogle) VerifyGoogle(_ context.Context, idToken, nonce string) (idp.Identity, error) {
	if f.used == nil {
		f.used = map[string]bool{}
	}
	if f.used[nonce] {
		return idp.Identity{}, idp.ErrNonceReplayed
	}
	f.used[nonce] = true
	id := f.identities[idToken]
	if id.Subject == "" {
		id.Subject = "google-" + idToken
	}
	id.Kind = enum.IdentityGoogle
	return id, nil
}

func useGoogle(t *testing.T, f *fixture, g *fakeGoogle, required []enum.IdentityKind, logs *bytes.Buffer) {
	t.Helper()
	newServiceWith(t, f, func(d *user.Deps) {
		*d = withProvider(*d, googleProvider(g))
		if required != nil {
			d.RequiredIdentityKinds = required
		}
		if logs != nil {
			d.Logger = slog.New(slog.NewTextHandler(logs, nil))
		}
	})
}

func TestGoogleProfileFillHintAndOpenIDCoexistence(t *testing.T) {
	ctx := context.Background()
	longName := strings.Repeat("名", 300) + "\n\u202e"
	g := &fakeGoogle{identities: map[string]idp.Identity{
		"first":  {Subject: "gsub", HintEmail: "person@example.test", Name: longName, Picture: "https://example.test/a.png"},
		"second": {Subject: "gsub", Name: "Updated Name"},
		"badpic": {Subject: "gpicture", Name: "Pic", Picture: "http://insecure.example/a.png"},
	}}
	f := newFixture(t)
	var logs bytes.Buffer
	useGoogle(t, f, g, []enum.IdentityKind{}, &logs)
	out, err := f.svc.SignInWithIdp(ctx, googleCred("first", "n1"), dev1, meta1)
	res := requireSignInTokens(t, out)
	if err != nil || !res.IsNewUser || res.HintEmail != "person@example.test" || strings.Contains(logs.String(), "person@example.test") {
		t.Fatalf("direct register: %+v %v logs=%s", res, err, logs.String())
	}
	me, err := f.svc.GetMe(ctx, res.UserID)
	if err != nil || me.DisplayName != strings.Repeat("名", 128) || me.AvatarURL != "https://example.test/a.png" {
		t.Fatalf("filled profile: %+v %v", me, err)
	}
	idents, _ := user.TestRepoQueries(f.repo).ListActiveIdentitiesByUser(ctx, res.UserID)
	if len(idents) != 1 || !strings.Contains(string(idents[0].ProviderMeta), strings.Repeat("名", 256)) || strings.Contains(string(idents[0].ProviderMeta), "\u202e") {
		t.Fatalf("provider meta: %s", idents[0].ProviderMeta)
	}
	for _, id := range idents {
		if id.Kind == enum.IdentityEmail {
			t.Fatal("hint email must not become an identity")
		}
	}
	if _, err := f.svc.SignInWithIdp(ctx, googleCred("second", "n2"), dev1, meta1); err != nil {
		t.Fatal(err)
	}
	me, _ = f.svc.GetMe(ctx, res.UserID)
	idents, _ = user.TestRepoQueries(f.repo).ListActiveIdentitiesByUser(ctx, res.UserID)
	meta := string(idents[0].ProviderMeta)
	if me.DisplayName != strings.Repeat("名", 128) || me.AvatarURL != "https://example.test/a.png" || !strings.Contains(meta, "Updated Name") || !strings.Contains(meta, "https://example.test/a.png") {
		t.Fatalf("later login changed account profile or dropped picture: %+v %s", me, meta)
	}
	bad, err := f.svc.SignInWithIdp(ctx, googleCred("badpic", "n3"), dev2, meta1)
	badRes := requireSignInTokens(t, bad)
	if err != nil {
		t.Fatal(err)
	}
	me, _ = f.svc.GetMe(ctx, badRes.UserID)
	idents, _ = user.TestRepoQueries(f.repo).ListActiveIdentitiesByUser(ctx, badRes.UserID)
	if me.DisplayName != "Pic" || me.AvatarURL != "" || strings.Contains(string(idents[0].ProviderMeta), "insecure") {
		t.Fatalf("invalid picture must be skipped: %+v %s", me, idents[0].ProviderMeta)
	}

	kept := newFixture(t)
	useGoogle(t, kept, &fakeGoogle{identities: map[string]idp.Identity{"bind": {Subject: "gbind", Name: "Google", Picture: "https://example.test/new.png"}}}, nil, nil)
	phone := kept.signIn(t, enum.IdentityPhone, phone1, dev1)
	name, avatar := "Kept", "https://example.test/kept.png"
	if _, err := kept.svc.UpdateProfile(ctx, phone.UserID, user.ProfilePatch{DisplayName: &name, AvatarURL: &avatar}); err != nil {
		t.Fatal(err)
	}
	if _, created, err := kept.svc.BindWithIdp(ctx, principalOf(t, kept, phone), googleCred("bind", "nb"), meta1); err != nil || !created {
		t.Fatalf("bind: created=%v %v", created, err)
	}
	me, _ = kept.svc.GetMe(ctx, phone.UserID)
	if me.DisplayName != "Kept" || me.AvatarURL != "https://example.test/kept.png" {
		t.Fatalf("bind must not overwrite profile: %+v", me)
	}

	emailUser := newFixture(t)
	useGoogle(t, emailUser, &fakeGoogle{identities: map[string]idp.Identity{"mail": {Subject: "gmail", HintEmail: email1, Name: "From Google", Picture: "https://example.test/g.png"}}}, []enum.IdentityKind{}, nil)
	existing := emailUser.signIn(t, enum.IdentityEmail, email1, dev2)
	linked, err := emailUser.svc.SignInWithIdp(ctx, googleCred("mail", "nm"), dev1, meta1)
	linkedRes := requireSignInTokens(t, linked)
	if err != nil || linkedRes.UserID == existing.UserID {
		t.Fatalf("verified email must not merge accounts: %+v %v", linkedRes, err)
	}
	me, _ = emailUser.svc.GetMe(ctx, linkedRes.UserID)
	if me.DisplayName != "From Google" || me.AvatarURL != "https://example.test/g.png" {
		t.Fatalf("new account fill: %+v", me)
	}

	pendingSame := func(register bool) string {
		t.Helper()
		pf := newFixture(t)
		var buf bytes.Buffer
		useGoogle(t, pf, &fakeGoogle{identities: map[string]idp.Identity{"p": {Subject: "gpending", HintEmail: email1, Name: "Pending Name", Picture: "https://example.test/p.png"}}}, nil, &buf)
		if register {
			pf.signIn(t, enum.IdentityEmail, email1, dev2)
		}
		got, err := pf.svc.SignInWithIdp(ctx, googleCred("p", "np"), dev1, meta1)
		if err != nil || got.Pending == nil || got.Pending.HintEmail != email1 || strings.Contains(buf.String(), email1) {
			t.Fatalf("pending hint register=%v: %+v %v logs=%s", register, got.Pending, err, buf.String())
		}
		if _, err := pf.svc.CompleteSignIn(ctx, got.Pending.SignInCompletionToken, user.CodeCredential{Channel: enum.IdentityEmail}, dev1, meta1); !errors.Is(err, user.ErrInvalidArgument) {
			t.Fatalf("omitted target: %v", err)
		}
		return got.Pending.HintEmail
	}
	if pendingSame(false) != pendingSame(true) {
		t.Fatal("hint must not depend on whether the email is registered")
	}

	created := newFixture(t)
	useGoogle(t, created, &fakeGoogle{identities: map[string]idp.Identity{"c": {Subject: "gcreate", Name: "Created", Picture: "https://example.test/c.png"}}}, nil, nil)
	pending, err := created.svc.SignInWithIdp(ctx, googleCred("c", "nc"), dev1, meta1)
	if err != nil || pending.Pending == nil {
		t.Fatal(err)
	}
	cred := completionCredential(t, created, pending.Pending, enum.IdentityEmail, "new.google@example.test")
	done, err := created.svc.CompleteSignIn(ctx, pending.Pending.SignInCompletionToken, cred, dev1, meta1)
	doneRes := requireSignInTokens(t, done)
	if err != nil {
		t.Fatal(err)
	}
	me, _ = created.svc.GetMe(ctx, doneRes.UserID)
	if me.DisplayName != "Created" || me.AvatarURL != "https://example.test/c.png" {
		t.Fatalf("completion create fill: %+v", me)
	}

	linkedFlow := newFixture(t)
	useGoogle(t, linkedFlow, &fakeGoogle{identities: map[string]idp.Identity{"l": {Subject: "glink", Name: "Should Not Replace", Picture: "https://example.test/link.png"}}}, nil, nil)
	owner := linkedFlow.signIn(t, enum.IdentityEmail, email1, dev2)
	if _, err := linkedFlow.svc.UpdateDisplayName(ctx, owner.UserID, "Already Set"); err != nil {
		t.Fatal(err)
	}
	flow, err := linkedFlow.svc.SignInWithIdp(ctx, googleCred("l", "nl"), dev1, meta1)
	if err != nil || flow.Pending == nil {
		t.Fatal(err)
	}
	linkCred := completionCredential(t, linkedFlow, flow.Pending, enum.IdentityEmail, email1)
	if _, err := linkedFlow.svc.CompleteSignIn(ctx, flow.Pending.SignInCompletionToken, linkCred, dev1, meta1); err != nil {
		t.Fatal(err)
	}
	me, _ = linkedFlow.svc.GetMe(ctx, owner.UserID)
	if me.DisplayName != "Already Set" {
		t.Fatalf("completion link must keep display name: %+v", me)
	}

	wx := newFixture(t)
	newServiceWith(t, wx, func(d *user.Deps) { d.RequiredIdentityKinds = []enum.IdentityKind{} })
	first, err := wx.svc.SignInWithIdp(ctx, wechatCred("wx1", "union:profile@o1"), dev1, meta1)
	firstRes := requireSignInTokens(t, first)
	if err != nil {
		t.Fatal(err)
	}
	rows, _ := user.TestRepoQueries(wx.repo).ListActiveIdentitiesByUser(ctx, firstRes.UserID)
	if _, err := user.TestRepoQueries(wx.repo).UpdateIdentityProviderMeta(ctx, db.UpdateIdentityProviderMetaParams{ID: rows[0].ID, ProviderMeta: []byte(`{"openids":{"wx1":"o1"},"profile":{"name":"keep-me"}}`), UpdateTime: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := wx.svc.SignInWithIdp(ctx, wechatCred("wx2", "union:profile@o2"), dev2, meta1); err != nil {
		t.Fatal(err)
	}
	rows, _ = user.TestRepoQueries(wx.repo).ListActiveIdentitiesByUser(ctx, firstRes.UserID)
	meta = string(rows[0].ProviderMeta)
	if !strings.Contains(meta, `"wx2": "o2"`) || !strings.Contains(meta, "keep-me") {
		t.Fatalf("openid merge dropped profile: %s", meta)
	}
}
