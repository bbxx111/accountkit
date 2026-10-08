package user_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/email"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/phone"
	"github.com/bbxx111/accountkit/pii"
	"github.com/bbxx111/accountkit/session/grace"
	"github.com/bbxx111/accountkit/session/revocation"
	"github.com/bbxx111/accountkit/tokens"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/code"
	"github.com/bbxx111/accountkit/user/db"
	"github.com/bbxx111/accountkit/user/sender"
)

// capture 记录最近发出的验证码，供测试取码登录。
type capture struct {
	mu   sync.Mutex
	last map[string]sender.Message // target → message
	fail error
}

func (c *capture) set(target string, m sender.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fail != nil {
		return c.fail
	}
	if c.last == nil {
		c.last = map[string]sender.Message{}
	}
	c.last[target] = m
	return nil
}
func (c *capture) SendSMS(_ context.Context, to string, m sender.Message) error { return c.set(to, m) }
func (c *capture) SendEmail(_ context.Context, to string, m sender.Message) error {
	return c.set(to, m)
}
func (c *capture) code(target string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last[target].Code
}

type fixture struct {
	challenges sync.Map
	svc        *user.Service
	repo       *user.Repo
	pool       *pgxpool.Pool
	mr         *miniredis.Miniredis
	sent       *capture
	audit      *audit.Memory
	dig        *pii.Digester
	ciph       *pii.Cipher
	clock      *time.Time // 可推进的时钟
	deps       user.Deps
	wechat     *fakeWeChat
	apple      *fakeApple
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pool := testPool(t)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	dig, _ := pii.NewDigester(map[uint16][]byte{1: bytes.Repeat([]byte{1}, 32)}, 1)
	ciph, _ := pii.NewCipher(map[uint16][]byte{1: bytes.Repeat([]byte{2}, 32)}, 1)
	signer, _ := tokens.NewSigner(tokens.Options{Keys: map[uint16][]byte{1: bytes.Repeat([]byte{3}, 32)}, Active: 1, Issuer: "shifang", Audience: "app"})
	now := time.Now().Truncate(time.Second)
	clock := &now
	sent := &capture{}
	mem := &audit.Memory{}
	repo := user.NewRepo(pool)
	wechat := &fakeWeChat{}
	apple := &fakeApple{}
	deps := user.Deps{
		Repo:       repo,
		Codes:      code.NewStore(rdb, "t:", dig, code.Options{FailureLimitPerTarget: 10, FailureWindow: 15 * time.Minute, TTL: 5 * time.Minute, Cooldown: 60 * time.Second, MaxAttempts: 5, DailyLimitPerTarget: 10, DailyLimitPerIP: 100, Now: func() time.Time { return *clock }}),
		Revocation: revocation.NewSet(rdb, "t:"), Grace: grace.NewCache(rdb, "t:", ciph),
		Signer: signer, Cipher: ciph, Digester: dig, SMS: sent, Email: sent, Audit: mem,
		WeChat:    wechat,
		Apple:     apple,
		Logger:    slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)),
		AccessTTL: 15 * time.Minute, RefreshTTL: 720 * time.Hour, RefreshGrace: 30 * time.Second, CodeTTL: 5 * time.Minute,
		DefaultRegion: "CN", Now: func() time.Time { return *clock },
		MaxIdentitiesPerKind:  1,
		DeletionCoolingPeriod: 360 * time.Hour,
	}
	svc, err := user.NewService(deps)
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{svc: svc, repo: repo, pool: pool, mr: mr, sent: sent, audit: mem, dig: dig, ciph: ciph, clock: clock, deps: deps, wechat: wechat, apple: apple}
}

func (f *fixture) advance(d time.Duration) { *f.clock = f.clock.Add(d); f.mr.FastForward(d) }

const phone1 = "+8613812341234"
const phone2 = "+8613800000002"
const email1 = "bind.me@shifang.co"

var dev1 = user.Device{ID: "11111111-1111-1111-1111-111111111111", Name: "iPhone"}
var meta1 = user.Meta{IP: "203.0.113.5", RequestID: "req-1"}

// signIn 发码并登录，返回结果。
func (f *fixture) signIn(t *testing.T, channel enum.IdentityKind, target string, dev user.Device) user.TokenResult {
	t.Helper()
	ctx := context.Background()
	if err := f.sendSignInCode(ctx, channel, target, meta1); err != nil {
		t.Fatalf("send: %v", err)
	}
	// 服务按渠道归一化后的地址发送验证码（生产行为——短信/邮件永远发到归一化地址），
	// 因此取码也要按归一化地址查找 capture，而不是调用方传入的原始写法。
	res, err := f.svc.SignInWithCode(ctx, f.credential(enum.PurposeSignIn, channel, target, f.sent.code(normalizedTarget(channel, target))), dev, meta1)
	if err != nil {
		t.Fatalf("sign in: %v", err)
	}
	f.advance(61 * time.Second) // 过冷却，便于后续再次发码
	return res
}

// normalizedTarget 镜像 Service.normalizeTarget 对 channel 的归一化，仅供测试查找 capture 用。
func normalizedTarget(channel enum.IdentityKind, target string) string {
	switch channel {
	case enum.IdentityPhone:
		if n, err := phone.Normalize(target, "CN"); err == nil {
			return n
		}
	case enum.IdentityEmail:
		if n, err := email.Normalize(target); err == nil {
			return n
		}
	}
	return target
}

func hasEvent(m *audit.Memory, typ enum.EventType, result enum.Result) bool {
	for _, e := range m.Events() {
		if e.Type == typ && e.Result == result {
			return true
		}
	}
	return false
}

// hasEventReason 同 hasEvent，额外要求 Reason 匹配。
func hasEventReason(m *audit.Memory, typ enum.EventType, result enum.Result, reason string) bool {
	for _, e := range m.Events() {
		if e.Type == typ && e.Result == result && e.Reason == reason {
			return true
		}
	}
	return false
}

func TestSignInCreatesUserThenReusesIt(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	first := f.signIn(t, enum.IdentityPhone, "138 1234 1234", dev1) // 国内格式，按 DefaultRegion=CN 归一化
	if !first.IsNewUser || first.Scope != user.ScopeUser || first.RefreshToken == "" || first.AccessToken == "" || first.ExpiresIn != 900 {
		t.Fatalf("first sign-in: %+v", first)
	}
	second := f.signIn(t, enum.IdentityPhone, phone1, user.Device{ID: "22222222-2222-2222-2222-222222222222"})
	if second.IsNewUser || second.UserID != first.UserID {
		t.Fatalf("second sign-in must reuse the user: %+v vs %+v", first, second)
	}
	// 身份行：密文可解密为 E.164，hint 正确，digest 是 active 版本
	idents, err := f.repo.Q().ListActiveIdentitiesByUser(ctx, first.UserID)
	if err != nil || len(idents) != 1 {
		t.Fatalf("identities: %v %v", idents, err)
	}
	id := idents[0]
	plain, err := f.ciph.Decrypt(id.SubjectCiphertext, uint16(*id.CipherKeyVersion))
	if err != nil || plain != phone1 || id.Kind != enum.IdentityPhone || *id.HintPrefix != "+86138" || *id.HintSuffix != "1234" {
		t.Fatalf("identity row: plain=%q err=%v row=%+v", plain, err, id)
	}
	if want, _ := f.dig.Digest(phone1); *id.SubjectDigest != want {
		t.Fatal("digest must be the active-version digest of the normalized target")
	}
	if !hasEvent(f.audit, enum.EventCodeSent, enum.ResultSuccess) || !hasEvent(f.audit, enum.EventSignIn, enum.ResultSuccess) {
		t.Fatal("audit must record CODE_SENT and SIGN_IN")
	}
	for _, e := range f.audit.Events() {
		if e.SubjectHint != "" && len(e.SubjectHint) != 8 {
			t.Fatalf("subject hint must be 8 chars: %q", e.SubjectHint)
		}
	}
}

func TestSignInWithEmailNormalizes(t *testing.T) {
	f := newFixture(t)
	res := f.signIn(t, enum.IdentityEmail, " Bo.Bai@Shifang.CO ", dev1)
	idents, _ := f.repo.Q().ListActiveIdentitiesByUser(context.Background(), res.UserID)
	if len(idents) != 1 || idents[0].Kind != enum.IdentityEmail || *idents[0].HintPrefix != "bo" || *idents[0].HintSuffix != "shifang.co" {
		t.Fatalf("email identity: %+v", idents)
	}
	// 同一邮箱不同写法 → 同一账号
	again := f.signIn(t, enum.IdentityEmail, "bo.bai@shifang.co", user.Device{ID: "33333333-3333-3333-3333-333333333333"})
	if again.UserID != res.UserID {
		t.Fatal("normalized email must map to the same user")
	}
}

func TestSignInWrongCodeAndCrossChannel(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.sendSignInCode(ctx, enum.IdentityPhone, phone1, meta1); err != nil {
		t.Fatal(err)
	}
	phoneCredential := f.credential(enum.PurposeSignIn, enum.IdentityPhone, phone1, f.sent.code(phone1))
	if _, err := f.svc.SignInWithCode(ctx, f.credential(enum.PurposeSignIn, enum.IdentityPhone, phone1, "000000"), dev1, meta1); !errors.Is(err, code.ErrInvalid) {
		t.Fatalf("wrong code: %v", err)
	}
	if !hasEvent(f.audit, enum.EventSignInFailed, enum.ResultFailure) {
		t.Fatal("audit must record SIGN_IN_FAILED")
	}
	// 保留 PHONE 发码的真实 code_id 和验证码，只变更渠道与目标；跨渠道引用应统一失效。
	crossChannel := phoneCredential
	crossChannel.Channel, crossChannel.Target = enum.IdentityEmail, "a@b.cd"
	if _, err := f.svc.SignInWithCode(ctx, crossChannel, dev1, meta1); !errors.Is(err, code.ErrExpired) {
		t.Fatalf("cross-channel: %v", err)
	}
	// 跨渠道引用不能消费源 PHONE 轮次；先前错码之后原凭证仍可成功。
	if _, err := f.svc.SignInWithCode(ctx, phoneCredential, dev1, meta1); err != nil {
		t.Fatalf("correct code after failures: %v", err)
	}
}

func TestSignInSameDeviceReplacesSession(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	first := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	second := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	active, _ := f.repo.Q().ListActiveSessionsByUser(ctx, db.ListActiveSessionsByUserParams{UserID: first.UserID, Now: f.clock.UTC().Truncate(time.Microsecond)})
	if len(active) != 1 {
		t.Fatalf("same device re-login must leave exactly one active session, got %d", len(active))
	}
	if second.RefreshToken == first.RefreshToken {
		t.Fatal("new session must carry a new refresh token")
	}
	old, err := f.repo.Q().GetSessionByRefreshHash(ctx, sha256Of(first.RefreshToken))
	if err != nil || old.RevokeTime == nil || *old.RevokeReason != enum.RevokeReplacedByRelogin {
		t.Fatalf("old session must be revoked with REPLACED_BY_RELOGIN: %+v %v", old, err)
	}
	if revoked, _ := f.mr.Get("t:revoked:" + old.ID); revoked == "" {
		t.Fatal("replaced session must be in the revocation set")
	}
}

func TestSignInFrozenAndPendingDeletion(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	res := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	mustExec(t, f, `UPDATE user_account SET state = 2 WHERE id = $1`, res.UserID) // FROZEN
	_ = f.sendSignInCode(ctx, enum.IdentityPhone, phone1, meta1)
	if _, err := f.svc.SignInWithCode(ctx, f.credential(enum.PurposeSignIn, enum.IdentityPhone, phone1, f.sent.code(phone1)), dev1, meta1); !errors.Is(err, user.ErrUserFrozen) {
		t.Fatalf("frozen: %v", err)
	}
	f.advance(61 * time.Second)
	mustExec(t, f, `UPDATE user_account SET state = 3 WHERE id = $1`, res.UserID) // PENDING_DELETION
	_ = f.sendSignInCode(ctx, enum.IdentityPhone, phone1, meta1)
	got, err := f.svc.SignInWithCode(ctx, f.credential(enum.PurposeSignIn, enum.IdentityPhone, phone1, f.sent.code(phone1)), dev1, meta1)
	if err != nil || got.Scope != user.ScopeUndelete {
		t.Fatalf("pending deletion via anchor must yield user:undelete scope: %+v %v", got, err)
	}
}

// TestSignInDeletedUserWithLiveIdentityIsInvariantViolation 钉住"DELETED 但身份仍活跃"这个不该
// 发生的状态：正常情况下阶段 5 的 purge 必须在同一事务内把 state 置为 DELETED 并把该用户全部
// 身份的 digest/密文随机化（匿名化），二者不同步就会让一个已删除账号继续可被登录识别到。
// 这里直接改列模拟该不变式被破坏后的情形，断言服务发现即报错，而不是当作冻结处理或悄悄放行。
func TestSignInDeletedUserWithLiveIdentityIsInvariantViolation(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	res := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	mustExec(t, f, `UPDATE user_account SET state = 4 WHERE id = $1`, res.UserID) // DELETED
	if err := f.sendSignInCode(ctx, enum.IdentityPhone, phone1, meta1); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SignInWithCode(ctx, f.credential(enum.PurposeSignIn, enum.IdentityPhone, phone1, f.sent.code(phone1)), dev1, meta1); err == nil || errors.Is(err, user.ErrUserFrozen) || errors.Is(err, user.ErrInvalidGrant) {
		t.Fatalf("deleted user with a live identity must surface as an error, not ErrUserFrozen/ErrInvalidGrant: %v", err)
	}
	var count int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM user_account`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("the invariant-violation path must not create a second user_account row: count=%d", count)
	}
}

func TestSignInValidationAndRateLimitAndRedisDown(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.sendSignInCode(ctx, enum.IdentityPhone, "abc", meta1); !errors.Is(err, user.ErrInvalidTarget) {
		t.Fatalf("invalid target: %v", err)
	}
	if err := f.sendSignInCode(ctx, enum.IdentityWeChat, phone1, meta1); !errors.Is(err, user.ErrInvalidArgument) {
		t.Fatalf("non-channel kind: %v", err)
	}
	if err := f.sendSignInCode(ctx, enum.IdentityPhone, phone1, meta1); err != nil {
		t.Fatal(err)
	}
	var rl *code.RateLimitedError
	if err := f.sendSignInCode(ctx, enum.IdentityPhone, phone1, meta1); !errors.As(err, &rl) {
		t.Fatalf("cooldown must surface as *code.RateLimitedError: %v", err)
	}
	if !hasEvent(f.audit, enum.EventCodeSendRejected, enum.ResultFailure) {
		t.Fatal("audit must record CODE_SEND_REJECTED")
	}
	if _, err := f.svc.SignInWithCode(ctx, f.credential(enum.PurposeSignIn, enum.IdentityPhone, phone1, f.sent.code(phone1)), user.Device{}, meta1); !errors.Is(err, user.ErrInvalidArgument) {
		t.Fatalf("missing device id: %v", err)
	}
	// 发送失败：额度不退（冷却仍在）
	f.advance(61 * time.Second)
	f.sent.fail = errors.New("provider down")
	if err := f.sendSignInCode(ctx, enum.IdentityPhone, phone1, meta1); err == nil || errors.Is(err, user.ErrUnavailable) {
		t.Fatalf("provider failure must be returned as a plain error: %v", err)
	}
	f.sent.fail = nil
	if err := f.sendSignInCode(ctx, enum.IdentityPhone, phone1, meta1); !errors.As(err, &rl) || rl.Dimension != "COOLDOWN" {
		t.Fatalf("quota must not be refunded after a failed send: %v", err)
	}
	if !hasEvent(f.audit, enum.EventCodeSendFailed, enum.ResultFailure) {
		t.Fatal("audit must record CODE_SEND_FAILED")
	}
	f.mr.Close()
	if err := f.sendSignInCode(ctx, enum.IdentityPhone, "+8613900000001", meta1); !errors.Is(err, user.ErrUnavailable) {
		t.Fatalf("redis down must be ErrUnavailable: %v", err)
	}
}

// TestSendSignInCodeRequiresIP 钉住"空 IP 会把所有调用方并入同一个 IP 配额桶"这个问题：
// meta.IP 为空必须在触碰 code.Store 之前被拒绝，且不能有码被发出。
func TestSendSignInCodeRequiresIP(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.sendSignInCode(ctx, enum.IdentityPhone, phone1, user.Meta{RequestID: "req-noip"}); !errors.Is(err, user.ErrInvalidArgument) {
		t.Fatalf("empty ip must be ErrInvalidArgument: %v", err)
	}
	if got := f.sent.code(normalizedTarget(enum.IdentityPhone, phone1)); got != "" {
		t.Fatalf("no code must have been sent when ip is missing: %q", got)
	}
}

// resetAudit 用新的 Memory 记录器重建 Service（Deps 不可变），返回新记录器。
func resetAudit(f *fixture) *audit.Memory {
	mem := &audit.Memory{}
	d := f.deps
	d.Audit = mem
	svc, err := user.NewService(d)
	if err != nil {
		panic(err)
	}
	f.svc = svc
	return mem
}

// newServiceWithout 构造去掉某个 IdP 校验器的 Service。
func newServiceWithout(t *testing.T, f *fixture, which string) *user.Service {
	t.Helper()
	d := f.deps
	switch which {
	case "wechat":
		d.WeChat = nil
	case "apple":
		d.Apple = nil
	}
	svc, err := user.NewService(d)
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func mustExec(t *testing.T, f *fixture, sql string, args ...any) {
	t.Helper()
	if _, err := f.pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec: %v", err)
	}
}

func sha256Of(s string) []byte { h := sha256.Sum256([]byte(s)); return h[:] }

func TestRefreshRotatesAndOldTokenEntersGrace(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	first := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	// 登录时刻取自首个 access token 自身的 auth_time，而不是从当前 clock 反推：signIn 助手在
	// 登录成功后还会推进 61s 时钟以越过发码冷却（Task 8 既有行为），若按 f.clock 反推固定的
	// 1 分钟会与这额外的 61s 混在一起，得到错误的期望值。
	p0, err := f.svc.Authenticate(ctx, first.AccessToken)
	if err != nil {
		t.Fatalf("authenticate first access: %v", err)
	}
	f.advance(time.Minute)
	next, err := f.svc.Refresh(ctx, first.RefreshToken, meta1)
	if err != nil || next.RefreshToken == first.RefreshToken || next.RefreshToken == "" || next.Scope != user.ScopeUser {
		t.Fatalf("refresh: %+v %v", next, err)
	}
	// 刷新保留 auth_time：新 access 的 auth_time 等于登录时刻
	p, err := f.svc.Authenticate(ctx, next.AccessToken)
	if err != nil || !p.AuthTime.Equal(p0.AuthTime) {
		t.Fatalf("auth_time must be preserved across refresh: %+v %v", p, err)
	}
	// 宽限内重放旧 token：返回同一 pair
	replay, err := f.svc.Refresh(ctx, first.RefreshToken, meta1)
	if err != nil || replay.RefreshToken != next.RefreshToken || replay.AccessToken != next.AccessToken {
		t.Fatalf("grace replay must return the identical pair: %+v %v", replay, err)
	}
	// 新 token 正常可用
	if _, err := f.svc.Refresh(ctx, next.RefreshToken, meta1); err != nil {
		t.Fatalf("new token must refresh: %v", err)
	}
	if !hasEvent(f.audit, enum.EventTokenRefreshed, enum.ResultSuccess) {
		t.Fatal("audit TOKEN_REFRESHED")
	}
}

func TestRefreshReplayAfterGraceRevokesSession(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	first := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	next, _ := f.svc.Refresh(ctx, first.RefreshToken, meta1)
	f.advance(31 * time.Second) // 超过 RefreshGrace
	if _, err := f.svc.Refresh(ctx, first.RefreshToken, meta1); !errors.Is(err, user.ErrInvalidGrant) {
		t.Fatalf("replay after grace: %v", err)
	}
	// 会话被吊销：当前有效的 next 也失效
	if _, err := f.svc.Refresh(ctx, next.RefreshToken, meta1); !errors.Is(err, user.ErrInvalidGrant) {
		t.Fatalf("current token must be dead after reuse detection: %v", err)
	}
	if _, err := f.svc.Authenticate(ctx, next.AccessToken); !errors.Is(err, user.ErrInvalidToken) {
		t.Fatalf("access must be rejected via revocation set: %v", err)
	}
	if !hasEvent(f.audit, enum.EventRefreshReuseDetected, enum.ResultFailure) {
		t.Fatal("audit REFRESH_REUSE_DETECTED")
	}
	sess, _ := f.repo.Q().GetSessionByRefreshHash(ctx, sha256Of(next.RefreshToken))
	if sess.RevokeReason == nil || *sess.RevokeReason != enum.RevokeReuseDetected {
		t.Fatalf("revoke_reason must be REUSE_DETECTED: %+v", sess)
	}
}

func TestRefreshRejectsUnknownRevokedAndExpired(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.svc.Refresh(ctx, "never-issued", meta1); !errors.Is(err, user.ErrInvalidGrant) {
		t.Fatalf("unknown: %v", err)
	}
	if !hasEvent(f.audit, enum.EventRefreshRejected, enum.ResultFailure) {
		t.Fatal("audit REFRESH_REJECTED")
	}
	res := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	if err := f.svc.Revoke(ctx, res.RefreshToken, meta1); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Refresh(ctx, res.RefreshToken, meta1); !errors.Is(err, user.ErrInvalidGrant) {
		t.Fatalf("revoked: %v", err)
	}
	if _, err := f.svc.Authenticate(ctx, res.AccessToken); !errors.Is(err, user.ErrInvalidToken) {
		t.Fatalf("access after logout must be rejected immediately: %v", err)
	}
	// Revoke 幂等且对未知 token 静默
	if err := f.svc.Revoke(ctx, res.RefreshToken, meta1); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Revoke(ctx, "nope", meta1); err != nil {
		t.Fatal(err)
	}
	// 过期
	res2 := f.signIn(t, enum.IdentityPhone, "+8613900000002", dev1)
	f.advance(721 * time.Hour)
	if _, err := f.svc.Refresh(ctx, res2.RefreshToken, meta1); !errors.Is(err, user.ErrInvalidGrant) {
		t.Fatalf("expired: %v", err)
	}
}

func TestRefreshFrozenAndPendingDeletionRevoke(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	res := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	mustExec(t, f, `UPDATE user_account SET state = 2 WHERE id = $1`, res.UserID)
	if _, err := f.svc.Refresh(ctx, res.RefreshToken, meta1); !errors.Is(err, user.ErrUserFrozen) {
		t.Fatalf("frozen refresh: %v", err)
	}
	if _, err := f.svc.Authenticate(ctx, res.AccessToken); !errors.Is(err, user.ErrInvalidToken) {
		t.Fatal("frozen refresh must revoke the session immediately")
	}
	res2 := f.signIn(t, enum.IdentityPhone, "+8613900000003", dev1)
	mustExec(t, f, `UPDATE user_account SET state = 3 WHERE id = $1`, res2.UserID)
	if _, err := f.svc.Refresh(ctx, res2.RefreshToken, meta1); !errors.Is(err, user.ErrInvalidGrant) {
		t.Fatalf("pending deletion refresh: %v", err)
	}
	sess, _ := f.repo.Q().GetSessionByRefreshHash(ctx, sha256Of(res2.RefreshToken))
	if sess.RevokeReason == nil || *sess.RevokeReason != enum.RevokeUserDeleted {
		t.Fatalf("revoke_reason must be USER_DELETED: %+v", sess)
	}
}

func TestRefreshConcurrentOnlyOneRotatesOthersGetSamePair(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	res := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	var wg sync.WaitGroup
	results := make([]user.TokenResult, 8)
	errs := make([]error, 8)
	for i := range 8 {
		wg.Go(func() { results[i], errs[i] = f.svc.Refresh(ctx, res.RefreshToken, meta1) })
	}
	wg.Wait()
	first := ""
	for i := range 8 {
		if errs[i] != nil {
			t.Fatalf("concurrent refresh %d: %v", i, errs[i])
		}
		if first == "" {
			first = results[i].RefreshToken
		} else if results[i].RefreshToken != first {
			t.Fatal("all concurrent refreshes must yield the same rotated pair")
		}
	}
	active, _ := f.repo.Q().ListActiveSessionsByUser(ctx, db.ListActiveSessionsByUserParams{UserID: res.UserID, Now: f.clock.UTC().Truncate(time.Microsecond)})
	if len(active) != 1 || active[0].RevokeTime != nil {
		t.Fatalf("session must remain active and single: %+v", active)
	}
}

func TestRefreshGraceDegradesWhenRedisDown(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	res := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	next, _ := f.svc.Refresh(ctx, res.RefreshToken, meta1)
	f.mr.Close()
	// 宽限内重放，但 Redis 不可用：拒绝但不吊销
	if _, err := f.svc.Refresh(ctx, res.RefreshToken, meta1); !errors.Is(err, user.ErrInvalidGrant) {
		t.Fatalf("grace with redis down: %v", err)
	}
	sess, _ := f.repo.Q().GetSessionByRefreshHash(ctx, sha256Of(next.RefreshToken))
	if sess.RevokeTime != nil {
		t.Fatal("redis outage during grace must not revoke the session")
	}
	// 吊销检查 fail-open：access 仍按签名有效
	if _, err := f.svc.Authenticate(ctx, next.AccessToken); err != nil {
		t.Fatalf("authenticate must fail-open when redis is down: %v", err)
	}
}

func TestAuthenticateRejectsGarbageAndBadIDs(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for _, tok := range []string{"", "a.b.c"} {
		if _, err := f.svc.Authenticate(ctx, tok); !errors.Is(err, user.ErrInvalidToken) {
			t.Fatalf("%q: %v", tok, err)
		}
	}
	res := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	p, err := f.svc.Authenticate(ctx, res.AccessToken)
	if err != nil || p.UserID != res.UserID || p.Scope != user.ScopeUser || p.SessionID == "" {
		t.Fatalf("principal: %+v %v", p, err)
	}
}

func TestSessionsListRevokeOneAndOthers(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	a := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	b := f.signIn(t, enum.IdentityPhone, phone1, user.Device{ID: "22222222-2222-2222-2222-222222222222", Name: "iPad"})
	c := f.signIn(t, enum.IdentityPhone, phone1, user.Device{ID: "33333333-3333-3333-3333-333333333333"})
	pa, _ := f.svc.Authenticate(ctx, a.AccessToken)
	pb, _ := f.svc.Authenticate(ctx, b.AccessToken)
	list, err := f.svc.ListSessions(ctx, a.UserID, pa.SessionID)
	if err != nil || len(list) != 3 {
		t.Fatalf("list: %v %v", list, err)
	}
	var current int
	for _, s := range list {
		if s.IsCurrent {
			current++
			if s.ID != pa.SessionID {
				t.Fatal("IsCurrent must mark the caller's session")
			}
		}
		if s.ID == pb.SessionID && s.DeviceName != "iPad" {
			t.Fatalf("device name must round-trip: %+v", s)
		}
	}
	if current != 1 {
		t.Fatalf("exactly one current session, got %d", current)
	}
	// 踢出 b
	if err := f.svc.RevokeSession(ctx, a.UserID, pb.SessionID, meta1); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.RevokeSession(ctx, a.UserID, pb.SessionID, meta1); !errors.Is(err, user.ErrNotFound) {
		t.Fatalf("revoking an already-revoked session: %v", err)
	}
	if _, err := f.svc.Authenticate(ctx, b.AccessToken); !errors.Is(err, user.ErrInvalidToken) {
		t.Fatal("kicked device's access must be rejected immediately")
	}
	// 他人的会话 → 404
	other := f.signIn(t, enum.IdentityPhone, "+8613900000009", user.Device{ID: "44444444-4444-4444-4444-444444444444"})
	po, _ := f.svc.Authenticate(ctx, other.AccessToken)
	if err := f.svc.RevokeSession(ctx, a.UserID, po.SessionID, meta1); !errors.Is(err, user.ErrNotFound) {
		t.Fatalf("foreign session must be not found: %v", err)
	}
	// revokeOthers 保留当前
	if err := f.svc.RevokeOtherSessions(ctx, a.UserID, pa.SessionID, meta1); err != nil {
		t.Fatal(err)
	}
	list, _ = f.svc.ListSessions(ctx, a.UserID, pa.SessionID)
	if len(list) != 1 || list[0].ID != pa.SessionID {
		t.Fatalf("only current must remain: %+v", list)
	}
	if _, err := f.svc.Authenticate(ctx, c.AccessToken); !errors.Is(err, user.ErrInvalidToken) {
		t.Fatal("other device's access must be rejected")
	}
	if _, err := f.svc.Authenticate(ctx, a.AccessToken); err != nil {
		t.Fatalf("current session must survive: %v", err)
	}
}

// TestRefreshReuseDetectedContainmentFailurePropagatesError 钉住"遏制失败"这个必须与"遏制
// 成功、正常拒绝"区分开的情形：宽限外重放本应吊销会话，但如果吊销这条 UPDATE 本身失败
// （这里用一个只拦截 revoke_reason = REUSE_DETECTED 的触发器确定性地制造这个失败，不影响
// 其余查询/写入），Refresh 必须把这个 DB 错误原样向上传播，而不是悄悄返回 ErrInvalidGrant
// 并把 REFRESH_REUSE_DETECTED 记成"已处理"——那会让调用方以为泄露的会话已被拦下，而它可能
// 仍然活着。吊销集这一侧仍应尽力写入（代价低、安全），因此单独断言它确实生效。
func TestRefreshReuseDetectedContainmentFailurePropagatesError(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	first := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	next, err := f.svc.Refresh(ctx, first.RefreshToken, meta1)
	if err != nil {
		t.Fatalf("initial refresh: %v", err)
	}
	sess, err := f.repo.Q().GetSessionByRefreshHash(ctx, sha256Of(next.RefreshToken))
	if err != nil {
		t.Fatalf("lookup session: %v", err)
	}
	sid := sess.ID
	f.advance(31 * time.Second) // 超过 RefreshGrace：再用旧 token 重放会走吊销分支

	mustExec(t, f, `CREATE OR REPLACE FUNCTION test_block_reuse_revoke() RETURNS trigger AS $$
BEGIN
  IF NEW.revoke_reason = 3 THEN
    RAISE EXCEPTION 'test: block reuse-detected revoke';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql`)
	mustExec(t, f, `CREATE TRIGGER test_block_reuse_revoke_trg BEFORE UPDATE ON session
FOR EACH ROW EXECUTE FUNCTION test_block_reuse_revoke()`)

	if _, err := f.svc.Refresh(ctx, first.RefreshToken, meta1); err == nil || errors.Is(err, user.ErrInvalidGrant) {
		t.Fatalf("a failed containment must surface as a real error, not ErrInvalidGrant: %v", err)
	}
	for _, e := range f.audit.Events() {
		if e.Type == enum.EventRefreshReuseDetected {
			t.Fatal("must not record REFRESH_REUSE_DETECTED as contained when the revoke itself failed")
		}
	}
	// 吊销集仍应尽力写入，即使会话行的 DB 更新失败
	if revoked, _ := f.mr.Get("t:revoked:" + sid); revoked == "" {
		t.Fatal("revocation set must still be written even when the DB revoke fails")
	}
	// 会话行确实没有被吊销（触发器拦下了这次 UPDATE）
	sess2, err := f.repo.Q().GetSessionByRefreshHash(ctx, sha256Of(next.RefreshToken))
	if err != nil || sess2.RevokeTime != nil {
		t.Fatalf("session must remain unrevoked in the DB when the revoke write failed: %+v %v", sess2, err)
	}
}

func TestGetMeAndUpdateDisplayName(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	res := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	me, err := f.svc.GetMe(ctx, res.UserID)
	if err != nil || me.ID != res.UserID || me.State != enum.UserActive || me.DisplayName != "" || me.DeleteTime != nil || me.PurgeTime != nil || me.CreateTime.IsZero() {
		t.Fatalf("me: %+v %v", me, err)
	}
	me, err = f.svc.UpdateDisplayName(ctx, res.UserID, "  白博  ")
	if err != nil || me.DisplayName != "白博" {
		t.Fatalf("update: %+v %v", me, err)
	}
	if _, err := f.svc.UpdateDisplayName(ctx, res.UserID, strings.Repeat("字", 33)); !errors.Is(err, user.ErrInvalidArgument) {
		t.Fatalf("33 runes must be rejected: %v", err)
	}
	if _, err := f.svc.UpdateDisplayName(ctx, res.UserID, "line1\nline2"); !errors.Is(err, user.ErrInvalidArgument) {
		t.Fatalf("a control character (newline) must be rejected: %v", err)
	}
	if _, err := f.svc.UpdateDisplayName(ctx, res.UserID, "abc\u202Edef"); !errors.Is(err, user.ErrInvalidArgument) {
		t.Fatalf("a bidi override character (U+202E) must be rejected: %v", err)
	}
	if me, err = f.svc.UpdateDisplayName(ctx, res.UserID, "👨‍👩‍👧"); err != nil || me.DisplayName != "👨‍👩‍👧" {
		t.Fatalf("a compound emoji using ZWJ (U+200D) must be accepted as given: %+v %v", me, err)
	}
	if me, err = f.svc.UpdateDisplayName(ctx, res.UserID, "می‌خواهم"); err != nil || me.DisplayName != "می‌خواهم" {
		t.Fatalf("Persian text using ZWNJ (U+200C) must be accepted as given: %+v %v", me, err)
	}
	if _, err := f.svc.UpdateDisplayName(ctx, res.UserID, "⁦x⁩"); !errors.Is(err, user.ErrInvalidArgument) {
		t.Fatalf("bidi isolate characters (U+2066/U+2069) must be rejected: %v", err)
	}
	if _, err := f.svc.UpdateDisplayName(ctx, res.UserID, "\uFEFFname"); !errors.Is(err, user.ErrInvalidArgument) {
		t.Fatalf("a leading BOM (U+FEFF) must be rejected: %v", err)
	}
	if me, err = f.svc.UpdateDisplayName(ctx, res.UserID, strings.Repeat("字", 32)); err != nil || len([]rune(me.DisplayName)) != 32 {
		t.Fatalf("32 runes must be accepted: %v", err)
	}
	if me, err = f.svc.UpdateDisplayName(ctx, res.UserID, ""); err != nil || me.DisplayName != "" {
		t.Fatalf("empty clears the name: %+v %v", me, err)
	}
	if _, err := f.svc.GetMe(ctx, "u_0000000000000"); !errors.Is(err, user.ErrNotFound) {
		t.Fatalf("unknown user: %v", err)
	}
}

func TestReauthenticateUpdatesAuthTimeOnlyForAnchor(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	res := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	p, _ := f.svc.Authenticate(ctx, res.AccessToken)
	loginTime := p.AuthTime
	f.advance(10 * time.Minute)

	// 非本人锚点 → ErrNotAnchor，且不发码
	if err := f.sendReauthenticationCode(ctx, p, enum.IdentityPhone, "+8613900000008", meta1); !errors.Is(err, user.ErrNotAnchor) {
		t.Fatalf("foreign target: %v", err)
	}
	if err := f.sendReauthenticationCode(ctx, p, enum.IdentityPhone, phone1, meta1); err != nil {
		t.Fatal(err)
	}
	// 错码
	if _, err := f.svc.Reauthenticate(ctx, p, f.credential(enum.PurposeReauth, enum.IdentityPhone, phone1, "000000"), meta1); !errors.Is(err, code.ErrInvalid) {
		t.Fatalf("wrong code: %v", err)
	}
	if !hasEvent(f.audit, enum.EventReauthenticationFailed, enum.ResultFailure) {
		t.Fatal("audit REAUTHENTICATION_FAILED")
	}
	// 登录码不能用于重新认证（用途隔离）
	f.advance(61 * time.Second)
	_ = f.sendSignInCode(ctx, enum.IdentityPhone, phone1, meta1)
	if _, err := f.svc.Reauthenticate(ctx, p, f.credential(enum.PurposeReauth, enum.IdentityPhone, phone1, f.sent.code(phone1)), meta1); err == nil {
		t.Fatal("a SIGN_IN code must not satisfy REAUTH")
	}
	// 正确的 REAUTH 码
	f.advance(61 * time.Second)
	_ = f.sendReauthenticationCode(ctx, p, enum.IdentityPhone, phone1, meta1)
	out, err := f.svc.Reauthenticate(ctx, p, f.credential(enum.PurposeReauth, enum.IdentityPhone, phone1, f.sent.code(phone1)), meta1)
	if err != nil || out.RefreshToken != "" || out.RefreshExpiresIn != 0 || out.ExpiresIn != 900 || out.Scope != user.ScopeUser {
		t.Fatalf("reauth result: %+v %v", out, err)
	}
	p2, err := f.svc.Authenticate(ctx, out.AccessToken)
	if err != nil || !p2.AuthTime.After(loginTime) || !p2.AuthTime.Equal(*f.clock) || p2.SessionID != p.SessionID {
		t.Fatalf("auth_time must advance to now on the same session: %+v %v", p2, err)
	}
	// 刷新后仍保留新的 auth_time
	next, _ := f.svc.Refresh(ctx, res.RefreshToken, meta1)
	p3, _ := f.svc.Authenticate(ctx, next.AccessToken)
	if !p3.AuthTime.Equal(p2.AuthTime) {
		t.Fatal("refresh must carry the updated auth_time")
	}
	if !hasEvent(f.audit, enum.EventReauthenticated, enum.ResultSuccess) {
		t.Fatal("audit REAUTHENTICATED")
	}
	// 会话吊销后重新认证 → ErrInvalidToken，且审计原因是 SESSION_REVOKED（不是笼统的
	// SESSION_INVALID）：只有能明确归因的业务拒绝才记这个原因。
	_ = f.svc.Revoke(ctx, next.RefreshToken, meta1)
	f.advance(61 * time.Second)
	_ = f.sendReauthenticationCode(ctx, p, enum.IdentityPhone, phone1, meta1)
	if _, err := f.svc.Reauthenticate(ctx, p, f.credential(enum.PurposeReauth, enum.IdentityPhone, phone1, f.sent.code(phone1)), meta1); !errors.Is(err, user.ErrInvalidToken) {
		t.Fatalf("revoked session: %v", err)
	}
	if !hasEventReason(f.audit, enum.EventReauthenticationFailed, enum.ResultFailure, "SESSION_REVOKED") {
		t.Fatal("audit must record REAUTHENTICATION_FAILED with reason SESSION_REVOKED for a revoked session")
	}
}

// TestReauthenticateFrozenUserAudited 钉住 Reauthenticate 事务后失败分支的归因：账号冻结必须
// 记 REAUTHENTICATION_FAILED/USER_FROZEN（而不是笼统的 SESSION_INVALID，也不是完全不审计）。
func TestReauthenticateFrozenUserAudited(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	res := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	p, err := f.svc.Authenticate(ctx, res.AccessToken)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if err := f.sendReauthenticationCode(ctx, p, enum.IdentityPhone, phone1, meta1); err != nil {
		t.Fatalf("send reauth code: %v", err)
	}
	plain := f.sent.code(phone1)
	mustExec(t, f, `UPDATE user_account SET state = 2 WHERE id = $1`, res.UserID) // FROZEN
	if _, err := f.svc.Reauthenticate(ctx, p, f.credential(enum.PurposeReauth, enum.IdentityPhone, phone1, plain), meta1); !errors.Is(err, user.ErrUserFrozen) {
		t.Fatalf("frozen user must be ErrUserFrozen: %v", err)
	}
	if !hasEventReason(f.audit, enum.EventReauthenticationFailed, enum.ResultFailure, "USER_FROZEN") {
		t.Fatal("audit must record REAUTHENTICATION_FAILED with reason USER_FROZEN")
	}
}

// TestCleanupSessionsDeletesOnlyStaleRows 钉住 CleanupSessions 只删除超过保留期的吊销/过期会话。
func TestCleanupSessionsDeletesOnlyStaleRows(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	devs := []user.Device{{ID: "cl-1"}, {ID: "cl-2"}, {ID: "cl-3"}, {ID: "cl-4"}}
	var sids []string
	for _, d := range devs {
		res := f.signIn(t, enum.IdentityPhone, phone1, d)
		sess, err := f.repo.Q().GetSessionByRefreshHash(ctx, sha256Of(res.RefreshToken))
		if err != nil {
			t.Fatal(err)
		}
		sids = append(sids, sess.ID)
	}
	now := *f.clock
	day := 24 * time.Hour
	mustExec(t, f, `UPDATE session SET revoke_time = $2, revoke_reason = 1 WHERE id = $1`, sids[0], now.Add(-31*day)) // 吊销 31 天：删
	mustExec(t, f, `UPDATE session SET revoke_time = $2, revoke_reason = 1 WHERE id = $1`, sids[1], now.Add(-29*day)) // 吊销 29 天：留
	mustExec(t, f, `UPDATE session SET refresh_expire_time = $2 WHERE id = $1`, sids[2], now.Add(-31*day))            // 过期 31 天未吊销：删
	// sids[3] 活跃：留
	n, err := f.svc.CleanupSessions(ctx)
	if err != nil || n != 2 {
		t.Fatalf("cleanup: n=%d err=%v", n, err)
	}
	for i, want := range []bool{false, true, false, true} {
		var exists bool
		if err := f.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM session WHERE id = $1)`, sids[i]).Scan(&exists); err != nil || exists != want {
			t.Fatalf("session %d exists=%v want %v (%v)", i, exists, want, err)
		}
	}
	if n, err := f.svc.CleanupSessions(ctx); err != nil || n != 0 {
		t.Fatalf("second run: n=%d err=%v", n, err)
	}
}

func challengeKey(p enum.CodePurpose, ch enum.IdentityKind, target string) string {
	return p.String() + ":" + ch.String() + ":" + normalizedTarget(ch, target)
}
func (f *fixture) remember(p enum.CodePurpose, ch enum.IdentityKind, target string, c user.CodeChallenge, err error) error {
	if err == nil {
		f.challenges.Store(challengeKey(p, ch, target), c.CodeID)
		f.challenges.Store("latest:"+ch.String()+":"+normalizedTarget(ch, target), c.CodeID)
	}
	return err
}
func (f *fixture) sendSignInCode(ctx context.Context, ch enum.IdentityKind, target string, meta user.Meta) error {
	c, err := f.svc.SendSignInCode(ctx, ch, target, meta)
	return f.remember(enum.PurposeSignIn, ch, target, c, err)
}
func (f *fixture) sendBindCode(ctx context.Context, p user.Principal, ch enum.IdentityKind, target string, meta user.Meta) error {
	c, err := f.svc.SendBindCode(ctx, p, ch, target, meta)
	return f.remember(enum.PurposeBind, ch, target, c, err)
}
func (f *fixture) sendReauthenticationCode(ctx context.Context, p user.Principal, ch enum.IdentityKind, target string, meta user.Meta) error {
	c, err := f.svc.SendReauthenticationCode(ctx, p, ch, target, meta)
	return f.remember(enum.PurposeReauth, ch, target, c, err)
}
func (f *fixture) credential(p enum.CodePurpose, ch enum.IdentityKind, target, plain string) user.CodeCredential {
	id := "00000000000000000000000000000000"
	if value, ok := f.challenges.Load("latest:" + ch.String() + ":" + normalizedTarget(ch, target)); ok {
		id = value.(string)
	} else if value, ok := f.challenges.Load(challengeKey(p, ch, target)); ok {
		id = value.(string)
	}
	return user.CodeCredential{Channel: ch, Target: target, CodeID: id, Code: plain}
}

func (f *fixture) storeCredential(purpose enum.CodePurpose, ch enum.IdentityKind, target, plain string, p user.Principal) code.Credential {
	c := f.credential(purpose, ch, target, plain)
	binding := code.Binding{}
	if purpose == enum.PurposeBind || purpose == enum.PurposeReauth {
		binding.UserID = p.UserID
	}
	if purpose == enum.PurposeReauth {
		binding.SessionID = p.SessionID
	}
	return code.Credential{Channel: ch, Purpose: purpose, Target: normalizedTarget(ch, target), CodeID: c.CodeID, Code: plain, Binding: binding}
}
