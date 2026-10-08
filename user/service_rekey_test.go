package user_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/pii"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/db"
)

// rotate 用两版本密钥重建 Service（active = 2）；1 号密钥与 newFixture 相同，旧行仍可查找/解密。
// 返回新的 Digester/Cipher 与捕获日志的 buffer。
func rotate(t *testing.T, f *fixture) (*pii.Digester, *pii.Cipher, *bytes.Buffer) {
	t.Helper()
	dig, err := pii.NewDigester(map[uint16][]byte{1: bytes.Repeat([]byte{1}, 32), 2: bytes.Repeat([]byte{7}, 32)}, 2)
	if err != nil {
		t.Fatal(err)
	}
	ciph, err := pii.NewCipher(map[uint16][]byte{1: bytes.Repeat([]byte{2}, 32), 2: bytes.Repeat([]byte{8}, 32)}, 2)
	if err != nil {
		t.Fatal(err)
	}
	logs := &bytes.Buffer{}
	newServiceWith(t, f, func(d *user.Deps) {
		d.Digester, d.Cipher = dig, ciph
		d.Logger = slog.New(slog.NewTextHandler(logs, nil))
	})
	f.dig, f.ciph = dig, ciph
	return dig, ciph, logs
}

// findKind 返回该用户指定 kind 的身份行：includeDeleted=false 取活跃行，true 取已软删的行。
func findKind(t *testing.T, f *fixture, userID string, kind enum.IdentityKind, includeDeleted bool) db.Identity {
	t.Helper()
	rows, err := f.repo.Q().ListIdentitiesByUserIncludingDeleted(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Kind == kind && (r.DeleteTime != nil) == includeDeleted {
			return r
		}
	}
	t.Fatalf("identity kind %s (deleted=%v) not found among %d rows", kind, includeDeleted, len(rows))
	return db.Identity{}
}

func TestRekeyDigestsRecomputesOnlyActiveOldVersionRows(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	res := f.signIn(t, enum.IdentityPhone, phone1, dev1) // digest v1 / cipher v1
	p := principalOf(t, f, res)
	// 绑邮箱再解绑：留一条软删行（v1，不得被改）；绑微信：无版本列，不得被碰
	if err := f.sendBindCode(ctx, p, enum.IdentityEmail, email1, meta1); err != nil {
		t.Fatal(err)
	}
	mail, _, err := f.svc.BindWithCode(ctx, p, f.credential(enum.PurposeBind, enum.IdentityEmail, email1, f.sent.code(email1)), meta1)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.UnbindIdentity(ctx, p, mail.ID, meta1); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.svc.BindWithIdp(ctx, p, user.IdpCredential{Kind: enum.IdentityWeChat, AppID: "wx1", Code: "union:U-REKEY@o1"}, meta1); err != nil {
		t.Fatal(err)
	}
	dig, _, _ := rotate(t, f)
	// 回填前：旧摘要仍可登录（AllDigests 覆盖 v1）
	if again := f.signIn(t, enum.IdentityPhone, phone1, dev2); again.UserID != res.UserID || again.IsNewUser {
		t.Fatalf("login before rekey: %+v", again)
	}
	n, err := f.svc.RekeyDigests(ctx)
	if err != nil || n != 1 {
		t.Fatalf("rekey: n=%d err=%v", n, err)
	}
	phoneRow := findKind(t, f, res.UserID, enum.IdentityPhone, false)
	want, _ := dig.DigestFor(phone1, 2)
	if phoneRow.DigestKeyVersion == nil || *phoneRow.DigestKeyVersion != 2 || phoneRow.SubjectDigest == nil || *phoneRow.SubjectDigest != want {
		t.Fatalf("phone row after rekey: %+v", phoneRow)
	}
	if mailRow := findKind(t, f, res.UserID, enum.IdentityEmail, true); mailRow.DigestKeyVersion == nil || *mailRow.DigestKeyVersion != 1 {
		t.Fatalf("soft-deleted row must keep v1: %+v", mailRow)
	}
	if wx := findKind(t, f, res.UserID, enum.IdentityWeChat, false); wx.DigestKeyVersion != nil || wx.CipherKeyVersion != nil {
		t.Fatalf("provider identity must have no key versions: %+v", wx)
	}
	// 回填后仍登录到同一账号；幂等
	if third := f.signIn(t, enum.IdentityPhone, phone1, dev1); third.UserID != res.UserID || third.IsNewUser {
		t.Fatalf("login after rekey: %+v", third)
	}
	if n, err := f.svc.RekeyDigests(ctx); err != nil || n != 0 {
		t.Fatalf("second run: n=%d err=%v", n, err)
	}
}

func TestReencryptSubjectsRewrapsCiphertextAndKeepsPlaintext(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	res := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	before := findKind(t, f, res.UserID, enum.IdentityPhone, false)
	_, ciph, _ := rotate(t, f)
	n, err := f.svc.ReencryptSubjects(ctx)
	if err != nil || n != 1 {
		t.Fatalf("reencrypt: n=%d err=%v", n, err)
	}
	after := findKind(t, f, res.UserID, enum.IdentityPhone, false)
	if after.CipherKeyVersion == nil || *after.CipherKeyVersion != 2 || bytes.Equal(after.SubjectCiphertext, before.SubjectCiphertext) {
		t.Fatalf("ciphertext must be rewrapped under v2: %+v", after)
	}
	if plain, err := ciph.Decrypt(after.SubjectCiphertext, 2); err != nil || plain != phone1 {
		t.Fatalf("plaintext must survive: %q %v", plain, err)
	}
	// 摘要仍是 v1（两个任务互相独立）；掩码展示不受影响
	if after.DigestKeyVersion == nil || *after.DigestKeyVersion != 1 {
		t.Fatalf("digest version must be untouched by reencrypt: %+v", after)
	}
	list, err := f.svc.ListIdentities(ctx, res.UserID)
	if err != nil || len(list) != 1 || list[0].MaskedSubject != "+86 138****1234" {
		t.Fatalf("masked subject after reencrypt: %+v %v", list, err)
	}
	if n, err := f.svc.ReencryptSubjects(ctx); err != nil || n != 0 {
		t.Fatalf("second run: n=%d err=%v", n, err)
	}
	// 重加密之后重算摘要照常（用 v2 密文解密取明文）
	if n, err := f.svc.RekeyDigests(ctx); err != nil || n != 1 {
		t.Fatalf("rekey after reencrypt: n=%d err=%v", n, err)
	}
}

func TestBackfillSkipsUndecryptableRowLogsWithoutPIIAndContinues(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	a := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	b := f.signIn(t, enum.IdentityPhone, phone2, dev2)
	mustExec(t, f, `UPDATE identity SET subject_ciphertext = decode('00', 'hex') WHERE user_id = $1`, a.UserID) // 破坏 a 的密文
	_, _, logs := rotate(t, f)
	n, err := f.svc.RekeyDigests(ctx)
	if err == nil || n != 1 {
		t.Fatalf("one bad row: n=%d err=%v", n, err)
	}
	if row := findKind(t, f, b.UserID, enum.IdentityPhone, false); row.DigestKeyVersion == nil || *row.DigestKeyVersion != 2 {
		t.Fatalf("good row must be rekeyed despite the bad one: %+v", row)
	}
	if row := findKind(t, f, a.UserID, enum.IdentityPhone, false); row.DigestKeyVersion == nil || *row.DigestKeyVersion != 1 {
		t.Fatalf("bad row must stay at v1: %+v", row)
	}
	out := logs.String()
	if !strings.Contains(out, "identity_id") || strings.Contains(out, phone1) || strings.Contains(out, phone2) {
		t.Fatalf("failure log must name identity_id and never a phone number: %s", out)
	}
	// ReencryptSubjects 对同一坏行同样跳过并报错，好行成功
	n, err = f.svc.ReencryptSubjects(ctx)
	if err == nil || n != 1 {
		t.Fatalf("reencrypt with one bad row: n=%d err=%v", n, err)
	}
}

func TestCheckKeyVersionsRejectsUnconfiguredVersions(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	res := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	if err := f.svc.CheckKeyVersions(ctx); err != nil {
		t.Fatalf("all versions configured: %v", err)
	}
	mustExec(t, f, `UPDATE identity SET digest_key_version = 9 WHERE user_id = $1`, res.UserID)
	err := f.svc.CheckKeyVersions(ctx)
	if !errors.Is(err, user.ErrUnknownKeyVersion) || !strings.Contains(err.Error(), "digest_key_version") || !strings.Contains(err.Error(), "9") || strings.Contains(err.Error(), phone1) {
		t.Fatalf("unknown digest version: %v", err)
	}
	mustExec(t, f, `UPDATE identity SET digest_key_version = 1, cipher_key_version = 9 WHERE user_id = $1`, res.UserID)
	if err := f.svc.CheckKeyVersions(ctx); !errors.Is(err, user.ErrUnknownKeyVersion) || !strings.Contains(err.Error(), "cipher_key_version") {
		t.Fatalf("unknown cipher version: %v", err)
	}
	// 软删行上的未知版本不算（已解绑的行不再参与查找与展示）
	mustExec(t, f, `UPDATE identity SET cipher_key_version = 1 WHERE user_id = $1`, res.UserID)
	p := principalOf(t, f, res)
	if err := f.sendBindCode(ctx, p, enum.IdentityEmail, email1, meta1); err != nil {
		t.Fatal(err)
	}
	mail, _, err := f.svc.BindWithCode(ctx, p, f.credential(enum.PurposeBind, enum.IdentityEmail, email1, f.sent.code(email1)), meta1)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.UnbindIdentity(ctx, p, mail.ID, meta1); err != nil {
		t.Fatal(err)
	}
	mustExec(t, f, `UPDATE identity SET digest_key_version = 9, cipher_key_version = 9 WHERE id = $1`, mail.ID)
	if err := f.svc.CheckKeyVersions(ctx); err != nil {
		t.Fatalf("soft-deleted rows must not count: %v", err)
	}
	// 配置里多出的版本（表里没人用）不是错误
	rotate(t, f)
	if err := f.svc.CheckKeyVersions(ctx); err != nil {
		t.Fatalf("extra configured versions are fine: %v", err)
	}
}
