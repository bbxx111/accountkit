package user_test

import (
	"context"
	"errors"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bbxx111/accountkit/anonymize"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
)

// recordingAnonymizer 是宿主匿名化器的替身：在同一事务内把 host_note.user_id 改成 'purged'；fail 非空时报错。
type recordingAnonymizer struct {
	name  string
	fail  error
	calls atomic.Int32
}

func (a *recordingAnonymizer) Name() string     { return a.name }
func (a *recordingAnonymizer) Tables() []string { return []string{"host_note"} }
func (a *recordingAnonymizer) Anonymize(ctx context.Context, tx pgx.Tx, userID string) error {
	a.calls.Add(1)
	if a.fail != nil {
		return a.fail
	}
	_, err := tx.Exec(ctx, `UPDATE host_note SET user_id = 'purged', body = NULL WHERE user_id = $1`, userID)
	return err
}

// newServiceWith 用修改过的 Deps 重建 Service 并替换到 fixture 上（fixture 的助手都经 f.svc 调用）。
func newServiceWith(t *testing.T, f *fixture, mutate func(*user.Deps)) {
	t.Helper()
	d := f.deps
	mutate(&d)
	svc, err := user.NewService(d)
	if err != nil {
		t.Fatal(err)
	}
	f.svc = svc
	f.deps = d
}

var hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

func TestNewServiceRejectsBadAnonymizers(t *testing.T) {
	f := newFixture(t)
	for name, list := range map[string][]anonymize.Anonymizer{
		"nil entry":  {nil},
		"empty name": {&recordingAnonymizer{name: ""}},
		"duplicate":  {&recordingAnonymizer{name: "a"}, &recordingAnonymizer{name: "a"}},
	} {
		d := f.deps
		d.Anonymizers = list
		if _, err := user.NewService(d); err == nil {
			t.Fatalf("%s must be rejected", name)
		}
	}
}

func TestPurgeAnonymizesAccountIdentitiesSessionsAuditAndHostTables(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	mustExec(t, f, `CREATE TABLE host_note (id SERIAL PRIMARY KEY, user_id TEXT NOT NULL, body TEXT)`)
	host := &recordingAnonymizer{name: "host_note"}
	newServiceWith(t, f, func(d *user.Deps) { d.Anonymizers = []anonymize.Anonymizer{host} })

	// 账号：手机锚点 + 一条已解绑的邮箱（软删行）+ 微信；一行宿主数据；一行带 ip/device/hint 的审计
	res := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	p := principalOf(t, f, res)
	if err := f.svc.SendBindCode(ctx, p, enum.IdentityEmail, email1, meta1); err != nil {
		t.Fatal(err)
	}
	mail, _, err := f.svc.BindWithCode(ctx, p, enum.IdentityEmail, email1, f.sent.code(email1), meta1)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.UnbindIdentity(ctx, p, mail.ID, meta1); err != nil {
		t.Fatal(err)
	}
	wx := user.IdpCredential{Kind: enum.IdentityWeChat, AppID: "wx1", Code: "union:U-PURGE@o1"}
	if _, _, err := f.svc.BindWithIdp(ctx, p, wx, meta1); err != nil {
		t.Fatal(err)
	}
	mustExec(t, f, `INSERT INTO host_note (user_id, body) VALUES ($1, 'secret')`, p.UserID)
	mustExec(t, f, `INSERT INTO audit_event (id, event_type, actor_kind, user_id, result, ip, device_id, subject_hint) VALUES ('e_0000000000001', 4, 1, $1, 1, '203.0.113.5', 'dev-x', 'abcdefgh')`, p.UserID)
	before, err := f.repo.Q().ListIdentitiesByUserIncludingDeleted(ctx, p.UserID)
	if err != nil || len(before) != 3 {
		t.Fatalf("identities before: %d %v", len(before), err)
	}
	original := map[string]string{}
	for _, row := range before {
		switch {
		case row.SubjectDigest != nil:
			original[row.ID] = *row.SubjectDigest
		case row.ProviderSubject != nil:
			original[row.ID] = *row.ProviderSubject
		}
	}

	if _, err := f.svc.DeleteMe(ctx, p, meta1); err != nil {
		t.Fatal(err)
	}
	// 冷静期内锚点登录再建一个会话（purge 需兜底吊销它）
	pending := f.signIn(t, enum.IdentityPhone, phone1, dev2)
	if pending.Scope != user.ScopeUndelete {
		t.Fatalf("pending login: %+v", pending)
	}
	// 未到期：不处理、不调宿主
	if n, err := f.svc.PurgeDueUsers(ctx); err != nil || n != 0 || host.calls.Load() != 0 {
		t.Fatalf("not due yet: n=%d err=%v calls=%d", n, err, host.calls.Load())
	}

	f.advance(360*time.Hour + time.Second)
	mem := resetAudit(f)
	n, err := f.svc.PurgeDueUsers(ctx)
	if err != nil || n != 1 {
		t.Fatalf("purge: n=%d err=%v", n, err)
	}
	now := *f.clock
	// 账号行
	u, err := f.repo.Q().GetUserByID(ctx, p.UserID)
	if err != nil || u.State != enum.UserDeleted || u.DisplayName != nil || u.PurgeTime == nil || !u.PurgeTime.Equal(now) || u.DeleteTime == nil {
		t.Fatalf("user row after purge: %+v %v", u, err)
	}
	// 身份：3 行全部软删、PII 清空、subject 列换成 64 位十六进制且不等于原值
	after, _ := f.repo.Q().ListIdentitiesByUserIncludingDeleted(ctx, p.UserID)
	if len(after) != 3 {
		t.Fatalf("identities after: %d", len(after))
	}
	seen := map[string]bool{}
	for _, row := range after {
		if row.DeleteTime == nil || row.SubjectCiphertext != nil || row.ProviderMeta != nil || row.HintPrefix != nil || row.HintSuffix != nil || row.DigestKeyVersion != nil || row.CipherKeyVersion != nil {
			t.Fatalf("pii left on %s: %+v", row.ID, row)
		}
		var subject string
		switch {
		case row.SubjectDigest != nil && row.ProviderSubject == nil:
			subject = *row.SubjectDigest
		case row.ProviderSubject != nil && row.SubjectDigest == nil:
			subject = *row.ProviderSubject
		default:
			t.Fatalf("exactly-one violated: %+v", row)
		}
		if !hex64.MatchString(subject) || subject == original[row.ID] || seen[subject] {
			t.Fatalf("replacement must be fresh 64-hex per row: %q (orig %q)", subject, original[row.ID])
		}
		seen[subject] = true
	}
	// 会话：全部吊销；冷静期会话的 access 被吊销集拒绝
	if rows, _ := f.repo.Q().ListActiveSessionsByUser(ctx, p.UserID); len(rows) != 0 {
		t.Fatalf("active sessions after purge: %d", len(rows))
	}
	if _, err := f.svc.Authenticate(ctx, pending.AccessToken); !errors.Is(err, user.ErrInvalidToken) {
		t.Fatalf("pending session must be revoked by purge: %v", err)
	}
	// 审计行被清洗但保留
	var ipNull, devNull, hintNull bool
	if err := f.pool.QueryRow(ctx, `SELECT ip IS NULL, device_id IS NULL, subject_hint IS NULL FROM audit_event WHERE id = 'e_0000000000001' AND user_id = $1`, p.UserID).Scan(&ipNull, &devNull, &hintNull); err != nil || !ipNull || !devNull || !hintNull {
		t.Fatalf("audit scrub: ip=%v dev=%v hint=%v err=%v", ipNull, devNull, hintNull, err)
	}
	// 宿主表在同一事务内被匿名化
	var hostUser string
	var hostBody *string
	if err := f.pool.QueryRow(ctx, `SELECT user_id, body FROM host_note`).Scan(&hostUser, &hostBody); err != nil || hostUser != "purged" || hostBody != nil || host.calls.Load() != 1 {
		t.Fatalf("host anonymizer: user=%q body=%v calls=%d err=%v", hostUser, hostBody, host.calls.Load(), err)
	}
	// 审计：USER_PURGED（SYSTEM）+ 兜底吊销的 SESSION_REVOKED
	purgedOK, revokedOK := false, false
	for _, e := range mem.Events() {
		if e.Type == enum.EventUserPurged && e.Actor == enum.ActorSystem && e.Result == enum.ResultSuccess && e.UserID == p.UserID && e.IP == "" && e.DeviceID == "" {
			purgedOK = true
		}
		if e.Type == enum.EventSessionRevoked && e.Actor == enum.ActorSystem && e.Reason == enum.RevokeUserDeleted.String() && e.UserID == p.UserID {
			revokedOK = true
		}
	}
	if !purgedOK || !revokedOK {
		t.Fatalf("audit after purge: purged=%v revoked=%v events=%+v", purgedOK, revokedOK, mem.Events())
	}
	// 原手机号与微信都能重新注册为新账号；已 purge 的账号不会再被处理
	fresh := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	if !fresh.IsNewUser || fresh.UserID == p.UserID || fresh.Scope != user.ScopeUser {
		t.Fatalf("re-register phone: %+v", fresh)
	}
	wxAgain, err := f.svc.SignInWithIdp(ctx, wx, dev2, meta1)
	if err != nil || !wxAgain.IsNewUser || wxAgain.UserID == p.UserID {
		t.Fatalf("re-register wechat: %+v %v", wxAgain, err)
	}
	if n, err := f.svc.PurgeDueUsers(ctx); err != nil || n != 0 {
		t.Fatalf("second purge round: n=%d err=%v", n, err)
	}
}

func TestPurgeRollsBackWhenAnonymizerFailsAndSkipsUndueOrUndeleted(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	mustExec(t, f, `CREATE TABLE host_note (id SERIAL PRIMARY KEY, user_id TEXT NOT NULL, body TEXT)`)
	host := &recordingAnonymizer{name: "host_note", fail: errors.New("host down")}
	newServiceWith(t, f, func(d *user.Deps) { d.Anonymizers = []anonymize.Anonymizer{host} })

	res := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	p := principalOf(t, f, res)
	if _, err := f.svc.DeleteMe(ctx, p, meta1); err != nil {
		t.Fatal(err)
	}
	// 第二个账号：到期但已被 undelete（state 回到 ACTIVE）→ 不在批内
	res2 := f.signIn(t, enum.IdentityPhone, phone2, dev2)
	p2 := principalOf(t, f, res2)
	if _, err := f.svc.DeleteMe(ctx, p2, meta1); err != nil {
		t.Fatal(err)
	}
	f.advance(360*time.Hour + time.Second)
	mustExec(t, f, `UPDATE user_account SET state = 1 WHERE id = $1`, p2.UserID) // 模拟 undelete 后残留的 purge_time（Undelete 会清空，这里故意留着）
	mem := resetAudit(f)

	n, err := f.svc.PurgeDueUsers(ctx)
	if err == nil || !strings.Contains(err.Error(), "host_note") || !strings.Contains(err.Error(), "host down") || n != 0 {
		t.Fatalf("failed anonymizer must fail the round: n=%d err=%v", n, err)
	}
	// 整事务回滚：状态、身份、会话吊销状态都未变
	u, _ := f.repo.Q().GetUserByID(ctx, p.UserID)
	if u.State != enum.UserPendingDeletion {
		t.Fatalf("state after rollback: %s", u.State)
	}
	idents, _ := f.repo.Q().ListActiveIdentitiesByUser(ctx, p.UserID)
	if len(idents) != 1 || idents[0].SubjectCiphertext == nil || idents[0].HintSuffix == nil {
		t.Fatalf("identity must be intact after rollback: %+v", idents)
	}
	if hasEvent(mem, enum.EventUserPurged, enum.ResultSuccess) {
		t.Fatal("no USER_PURGED on rollback")
	}
	// 第二个账号未被碰（ACTIVE 不在批内）
	if u2, _ := f.repo.Q().GetUserByID(ctx, p2.UserID); u2.State != enum.UserActive || u2.PurgeTime == nil {
		t.Fatalf("undeleted user must be untouched: %+v", u2)
	}
	// 宿主恢复后下一轮成功，且只处理第一个账号
	host.fail = nil
	n, err = f.svc.PurgeDueUsers(ctx)
	if err != nil || n != 1 {
		t.Fatalf("retry: n=%d err=%v", n, err)
	}
	if u, _ = f.repo.Q().GetUserByID(ctx, p.UserID); u.State != enum.UserDeleted {
		t.Fatalf("state after retry: %s", u.State)
	}
}

func TestPurgeIsANoopWhenContextCanceled(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	res := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	if _, err := f.svc.DeleteMe(ctx, principalOf(t, f, res), meta1); err != nil {
		t.Fatal(err)
	}
	f.advance(360*time.Hour + time.Second)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if n, err := f.svc.PurgeDueUsers(canceled); n != 0 || err == nil {
		t.Fatalf("canceled ctx: n=%d err=%v", n, err)
	}
	if u, _ := f.repo.Q().GetUserByID(ctx, res.UserID); u.State != enum.UserPendingDeletion {
		t.Fatalf("canceled round must not change state: %s", u.State)
	}
}

// uncoveredUserIDTables 返回当前 schema 中含 user_id 列、但不在 PurgeCoveredTables 里的表。
func uncoveredUserIDTables(t *testing.T, f *fixture) (all, uncovered []string) {
	t.Helper()
	rows, err := f.pool.Query(context.Background(), `SELECT table_name FROM information_schema.columns WHERE table_schema = current_schema() AND column_name = 'user_id' ORDER BY table_name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var tbl string
		if err := rows.Scan(&tbl); err != nil {
			t.Fatal(err)
		}
		all = append(all, tbl)
		if strings.TrimSpace(user.PurgeCoveredTables[tbl]) == "" {
			uncovered = append(uncovered, tbl)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return all, uncovered
}

// TestPurgeCoversEveryTableWithUserID 是 §3.5 的覆盖性断言：库内每张含 user_id 的表都必须在
// PurgeCoveredTables 里写明处理方式或豁免理由；反过来登记表里不能有不存在的表（防止改名后空挂）。
// 末尾用一张临时表做变异检查，确认断言真的会红。
func TestPurgeCoversEveryTableWithUserID(t *testing.T) {
	f := newFixture(t)
	all, uncovered := uncoveredUserIDTables(t, f)
	if len(all) < 3 {
		t.Fatalf("expected at least identity/session/audit_event to carry user_id, got %v (query broken?)", all)
	}
	if len(uncovered) != 0 {
		t.Fatalf("tables with user_id but no purge handling/exemption: %v", uncovered)
	}
	existing := map[string]bool{}
	for _, tbl := range all {
		existing[tbl] = true
	}
	var stale []string
	for tbl := range user.PurgeCoveredTables {
		if !existing[tbl] {
			stale = append(stale, tbl)
		}
	}
	sort.Strings(stale)
	if len(stale) != 0 {
		t.Fatalf("PurgeCoveredTables lists tables that do not exist: %v", stale)
	}
	// 变异检查：新表出现即红
	mustExec(t, f, `CREATE TABLE probe_with_user_id (user_id TEXT)`)
	if _, uncovered = uncoveredUserIDTables(t, f); len(uncovered) != 1 || uncovered[0] != "probe_with_user_id" {
		t.Fatalf("assertion must flag the new table, got %v", uncovered)
	}
	mustExec(t, f, `DROP TABLE probe_with_user_id`)
}
