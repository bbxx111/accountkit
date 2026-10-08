package user_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/ids"
	"github.com/bbxx111/accountkit/pii"
	"github.com/bbxx111/accountkit/session/grace"
	"github.com/bbxx111/accountkit/session/revocation"
	"github.com/bbxx111/accountkit/tokens"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/code"
	"github.com/bbxx111/accountkit/user/db"
	"github.com/bbxx111/accountkit/user/idp"
	"github.com/bbxx111/accountkit/user/sender"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// 离线导入不得触发外部身份校验或投递；这些边界一旦调用直接使测试失败。
type importExternalGuard struct{ t *testing.T }

func (g importExternalGuard) SendSMS(context.Context, string, sender.Message) error {
	g.t.Fatal("import sent SMS")
	return nil
}
func (g importExternalGuard) SendEmail(context.Context, string, sender.Message) error {
	g.t.Fatal("import sent email")
	return nil
}
func (g importExternalGuard) VerifyWeChat(context.Context, string, string) (idp.Identity, error) {
	g.t.Fatal("import accessed WeChat")
	return idp.Identity{}, nil
}
func (g importExternalGuard) VerifyApple(context.Context, string, string) (idp.Identity, error) {
	g.t.Fatal("import accessed Apple")
	return idp.Identity{}, nil
}

type importFixture struct {
	svc  *user.Service
	pool *pgxpool.Pool
	dig  *pii.Digester
	ciph *pii.Cipher
	mr   *miniredis.Miniredis
	logs *bytes.Buffer
}

func newImportFixture(t *testing.T) *importFixture {
	t.Helper()
	pool := testPool(t)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	dig, err := pii.NewDigester(map[uint16][]byte{1: bytes.Repeat([]byte{1}, 32), 2: bytes.Repeat([]byte{4}, 32)}, 2)
	if err != nil {
		t.Fatal(err)
	}
	ciph, err := pii.NewCipher(map[uint16][]byte{1: bytes.Repeat([]byte{2}, 32), 2: bytes.Repeat([]byte{5}, 32)}, 2)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := tokens.NewSigner(tokens.Options{Keys: map[uint16][]byte{1: bytes.Repeat([]byte{3}, 32)}, Active: 1, Issuer: "import-test", Audience: "import-test"})
	if err != nil {
		t.Fatal(err)
	}
	logs := &bytes.Buffer{}
	guard := importExternalGuard{t}
	svc, err := user.NewService(user.Deps{
		Repo: user.NewRepo(pool), Codes: code.NewStore(rdb, "import-test:", dig, code.Options{FailureLimitPerTarget: 10, FailureWindow: 15 * time.Minute, TTL: 5 * time.Minute, Cooldown: time.Minute, MaxAttempts: 5, DailyLimitPerTarget: 10, DailyLimitPerIP: 100}),
		Revocation: revocation.NewSet(rdb, "import-test:"), Grace: grace.NewCache(rdb, "import-test:", ciph),
		Signer: signer, Cipher: ciph, Digester: dig, SMS: guard, Email: guard, WeChat: guard, Apple: guard,
		Logger: slog.New(slog.NewTextHandler(logs, nil)), DefaultRegion: "CN",
		AccessTTL: 15 * time.Minute, RefreshTTL: 720 * time.Hour, RefreshGrace: 30 * time.Second, CodeTTL: 5 * time.Minute,
		MaxIdentitiesPerKind: 2, DeletionCoolingPeriod: 360 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &importFixture{svc: svc, pool: pool, dig: dig, ciph: ciph, mr: mr, logs: logs}
}
func importAccount(t *testing.T, target string) user.ImportAccount {
	t.Helper()
	id, err := ids.New(ids.User)
	if err != nil {
		t.Fatal(err)
	}
	// 原时钟可能回退，更新时间不应被自动改写或强制排序。
	return user.ImportAccount{ID: id, State: enum.UserActive, DisplayName: "历史账号",
		CreateTime: time.Date(2020, 1, 2, 3, 4, 5, 123456000, time.UTC), UpdateTime: time.Date(2019, 1, 2, 3, 4, 5, 234567000, time.UTC),
		Anchors: []user.ImportAnchor{{Kind: enum.IdentityEmail, Target: target, CreateTime: time.Date(2020, 2, 2, 3, 4, 5, 345678000, time.UTC), UpdateTime: time.Date(2019, 2, 2, 3, 4, 5, 456789000, time.UTC)}},
	}
}
func importTx(t *testing.T, pool *pgxpool.Pool) pgx.Tx {
	t.Helper()
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	return tx
}
func TestImportAccounts(t *testing.T) {
	f := newImportFixture(t)
	ctx := context.Background()
	tx := importTx(t, f.pool)
	active := importAccount(t, " Legacy.User@Example.COM ")
	phoneAnchor := active.Anchors[0]
	phoneAnchor.Kind = enum.IdentityPhone
	phoneAnchor.Target = "138 1234 1234"
	active.Anchors = append(active.Anchors, phoneAnchor)
	deleted := importAccount(t, "unused@example.com")
	deleted.State = enum.UserDeleted
	deleted.DisplayName = ""
	deleted.Anchors = nil
	dt := time.Date(2021, 2, 3, 4, 5, 6, 0, time.UTC)
	pt := dt.Add(24 * time.Hour)
	deleted.DeleteTime = &dt
	deleted.PurgeTime = &pt
	if err := f.svc.ImportAccounts(ctx, tx, []user.ImportAccount{active, deleted}); err != nil {
		t.Fatal(err)
	}
	q := db.New(tx)
	got, err := q.GetUserByID(ctx, active.ID)
	if err != nil || got.State != enum.UserActive || got.DisplayName == nil || *got.DisplayName != active.DisplayName || !got.CreateTime.Equal(active.CreateTime) || !got.UpdateTime.Equal(active.UpdateTime) {
		t.Fatal("active account ID/name/original times were not preserved", err)
	}
	tomb, err := q.GetUserByID(ctx, deleted.ID)
	if err != nil || tomb.State != enum.UserDeleted || tomb.DisplayName != nil || !tomb.CreateTime.Equal(deleted.CreateTime) || !tomb.UpdateTime.Equal(deleted.UpdateTime) || tomb.DeleteTime == nil || !tomb.DeleteTime.Equal(dt) || tomb.PurgeTime == nil || !tomb.PurgeTime.Equal(pt) {
		t.Fatal("invalid legacy tombstone", err)
	}
	anchors, err := q.ListActiveIdentitiesByUser(ctx, active.ID)
	if err != nil || len(anchors) != 2 {
		t.Fatal("missing imported anchors", err)
	}
	for _, a := range anchors {
		want := "legacy.user@example.com"
		hp, hs := "le", "example.com"
		if a.Kind == enum.IdentityPhone {
			want = "+8613812341234"
			hp, hs = "+86138", "1234"
		}
		if a.CipherKeyVersion == nil || *a.CipherKeyVersion != 2 || a.DigestKeyVersion == nil || *a.DigestKeyVersion != 2 {
			t.Fatal("active key versions not used")
		}
		plain, err := f.ciph.Decrypt(a.SubjectCiphertext, uint16(*a.CipherKeyVersion))
		if err != nil || plain != want || bytes.Contains(a.SubjectCiphertext, []byte(want)) {
			t.Fatal("invalid encrypted normalized anchor")
		}
		digest, _ := f.dig.Digest(want)
		if a.SubjectDigest == nil || *a.SubjectDigest != digest || a.HintPrefix == nil || *a.HintPrefix != hp || a.HintSuffix == nil || *a.HintSuffix != hs {
			t.Fatal("invalid digest/hints")
		}
		if !a.CreateTime.Equal(active.Anchors[0].CreateTime) || !a.UpdateTime.Equal(active.Anchors[0].UpdateTime) {
			t.Fatal("original anchor times were not preserved")
		}
	}
	tombAnchors, err := q.ListIdentitiesByUserIncludingDeleted(ctx, deleted.ID)
	if err != nil || len(tombAnchors) != 0 {
		t.Fatal("tombstone claimed login anchors", err)
	}
	// 调用者提交前新连接不可见，证明导入没有自行提交或另开连接。
	var visible int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM user_account`).Scan(&visible); err != nil || visible != 0 {
		t.Fatal("import committed outside caller transaction", err)
	}
	var sessions int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM session`).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatal("import created sessions", err)
	}
	if len(f.mr.Keys()) != 0 || f.logs.Len() != 0 {
		t.Fatal("import touched authentication state or logged input")
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.New(f.pool).GetUserByID(ctx, active.ID); err != nil {
		t.Fatal("caller could not commit imported account", err)
	}
}
func TestImportAccountsValidation(t *testing.T) {
	f := newImportFixture(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		mutate func(*user.ImportAccount)
		want   error
	}{
		{"invalid ID", func(a *user.ImportAccount) { a.ID = "u_invalid" }, user.ErrInvalidArgument},
		{"frozen", func(a *user.ImportAccount) { a.State = enum.UserFrozen }, user.ErrInvalidState},
		{"pending", func(a *user.ImportAccount) { a.State = enum.UserPendingDeletion }, user.ErrInvalidState},
		{"unspecified", func(a *user.ImportAccount) { a.State = enum.UserStateUnspecified }, user.ErrInvalidState},
		{"no anchor", func(a *user.ImportAccount) { a.Anchors = nil }, user.ErrInvalidArgument},
		{"third party", func(a *user.ImportAccount) { a.Anchors[0].Kind = enum.IdentityWeChat }, user.ErrInvalidArgument},
		{"Apple", func(a *user.ImportAccount) { a.Anchors[0].Kind = enum.IdentityApple }, user.ErrInvalidArgument},
		{"invalid target", func(a *user.ImportAccount) { a.Anchors[0].Target = "secret-invalid-address" }, user.ErrInvalidTarget},
		{"deleted name", func(a *user.ImportAccount) { a.State = enum.UserDeleted; a.Anchors = nil }, user.ErrInvalidArgument},
		{"deleted anchors", func(a *user.ImportAccount) { a.State = enum.UserDeleted; a.DisplayName = "" }, user.ErrInvalidArgument},
		{"active delete time", func(a *user.ImportAccount) { a.DeleteTime = &a.CreateTime }, user.ErrInvalidArgument},
		{"active purge time", func(a *user.ImportAccount) { a.PurgeTime = &a.CreateTime }, user.ErrInvalidArgument},
		{"missing account time", func(a *user.ImportAccount) { a.CreateTime = time.Time{} }, user.ErrInvalidArgument},
		{"missing anchor time", func(a *user.ImportAccount) { a.Anchors[0].UpdateTime = time.Time{} }, user.ErrInvalidArgument},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := importAccount(t, "private@example.com")
			tc.mutate(&a)
			tx := importTx(t, f.pool)
			err := f.svc.ImportAccounts(ctx, tx, []user.ImportAccount{a})
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v; got %v", tc.want, err)
			}
			if strings.Contains(err.Error(), "private@example.com") || strings.Contains(err.Error(), "secret-invalid-address") {
				t.Fatal("import error leaked target")
			}
			var count int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM user_account`).Scan(&count); err != nil || count != 0 {
				t.Fatal("invalid account was inserted", err)
			}
		})
	}
	if err := f.svc.ImportAccounts(ctx, nil, nil); !errors.Is(err, user.ErrInvalidArgument) {
		t.Fatalf("nil transaction: %v", err)
	}
	tx := importTx(t, f.pool)
	if err := f.svc.ImportAccounts(ctx, tx, nil); err != nil {
		t.Fatalf("empty input: %v", err)
	}
}
func TestImportAccountsConflicts(t *testing.T) {
	for _, scenario := range []string{"normalized batch duplicate", "normalized same account duplicate", "existing old digest", "duplicate ID"} {
		t.Run(scenario, func(t *testing.T) {
			f := newImportFixture(t)
			ctx := context.Background()
			first := importAccount(t, "conflict@example.com")
			second := importAccount(t, " CONFLICT@EXAMPLE.COM ")
			tx := importTx(t, f.pool)
			accounts := []user.ImportAccount{first, second}
			switch scenario {
			case "normalized same account duplicate":
				first.Anchors = append(first.Anchors, second.Anchors[0])
				accounts = []user.ImportAccount{first}
			case "duplicate ID":
				second.ID = first.ID
				second.Anchors[0].Target = "other@example.com"
				accounts = []user.ImportAccount{first, second}
			case "existing old digest":
				q := db.New(tx)
				if _, err := q.CreateUser(ctx, db.CreateUserParams{ID: first.ID, State: enum.UserActive}); err != nil {
					t.Fatal(err)
				}
				iid, _ := ids.New(ids.Identity)
				old, _ := f.dig.DigestFor("conflict@example.com", 1)
				ct, cv, err := f.ciph.Encrypt("conflict@example.com")
				if err != nil {
					t.Fatal(err)
				}
				dv := int16(1)
				cipherV := int16(cv)
				if _, err := q.CreateIdentity(ctx, db.CreateIdentityParams{ID: iid, UserID: first.ID, Kind: enum.IdentityEmail, SubjectDigest: &old, DigestKeyVersion: &dv, SubjectCiphertext: ct, CipherKeyVersion: &cipherV}); err != nil {
					t.Fatal(err)
				}
				accounts = []user.ImportAccount{second}
			}
			err := f.svc.ImportAccounts(ctx, tx, accounts)
			if err == nil {
				t.Fatal("import silently merged or accepted conflicting identity/ID")
			}
			if strings.Contains(err.Error(), "conflict@example.com") || strings.Contains(err.Error(), "CONFLICT@EXAMPLE.COM") {
				t.Fatal("conflict error leaked target")
			}
			if scenario != "duplicate ID" && !errors.Is(err, user.ErrIdentityConflict) {
				t.Fatalf("unclassifiable identity conflict: %v", err)
			}
		})
	}
	t.Run("already stored ID", func(t *testing.T) {
		f := newImportFixture(t)
		ctx := context.Background()
		a := importAccount(t, "stored@example.com")
		tx := importTx(t, f.pool)
		if err := f.svc.ImportAccounts(ctx, tx, []user.ImportAccount{a}); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		a.Anchors[0].Target = "new@example.com"
		tx = importTx(t, f.pool)
		if err := f.svc.ImportAccounts(ctx, tx, []user.ImportAccount{a}); err == nil {
			t.Fatal("duplicate stored ID accepted")
		}
	})
}
func TestImportAccountsRollback(t *testing.T) {
	f := newImportFixture(t)
	ctx := context.Background()
	tx := importTx(t, f.pool)
	if _, err := tx.Exec(ctx, `CREATE TABLE import_business_ref (user_id text PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	first := importAccount(t, "first@example.com")
	second := importAccount(t, "conflict@example.com")
	existing := importAccount(t, " CONFLICT@EXAMPLE.COM ")
	if err := f.svc.ImportAccounts(ctx, tx, []user.ImportAccount{existing}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	tx = importTx(t, f.pool)
	if _, err := tx.Exec(ctx, `INSERT INTO import_business_ref VALUES ($1)`, first.ID); err != nil {
		t.Fatal(err)
	}
	err := f.svc.ImportAccounts(ctx, tx, []user.ImportAccount{first, second})
	if !errors.Is(err, user.ErrIdentityConflict) {
		t.Fatalf("second account conflict: %v", err)
	}
	// 失败没有自行回滚，先前导入和业务修改仍在调用者事务中。
	if _, err := db.New(tx).GetUserByID(ctx, first.ID); err != nil {
		t.Fatal("import rolled back caller transaction", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM user_account WHERE id = ANY($1::text[])`, []string{first.ID, second.ID}).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed import left accounts behind", err)
	}
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM identity WHERE user_id = ANY($1::text[])`, []string{first.ID, second.ID}).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed import left identities behind", err)
	}
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM import_business_ref`).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed import left business references behind", err)
	}
	tx = importTx(t, f.pool)
	if err := f.svc.ImportAccounts(ctx, tx, []user.ImportAccount{first}); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO import_business_ref VALUES ($1)`, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO import_business_ref VALUES ($1)`, "another"); err != nil {
		t.Fatal(err)
	}
	// 宿主后续业务 SQL 实际失败，调用者回滚也必须撤销成功导入。
	if _, err := tx.Exec(ctx, `INSERT INTO import_business_ref VALUES ($1)`, first.ID); !user.IsUniqueViolation(err) {
		t.Fatal("expected actual business uniqueness failure", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.New(f.pool).GetUserByID(ctx, first.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("business failure left account behind", err)
	}
}

// 两个停写工具误并行时，相同 active 版本的索引冲突仍必须显式失败，不能合并账号。
func TestImportAccountsConcurrentConflict(t *testing.T) {
	f := newImportFixture(t)
	ctx := context.Background()
	first := importAccount(t, "race@example.com")
	second := importAccount(t, " RACE@EXAMPLE.COM ")
	firstTx := importTx(t, f.pool)
	secondTx := importTx(t, f.pool)
	if err := f.svc.ImportAccounts(ctx, firstTx, []user.ImportAccount{first}); err != nil {
		t.Fatal(err)
	}
	secondCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- f.svc.ImportAccounts(secondCtx, secondTx, []user.ImportAccount{second}) }()
	// 观察真实 PostgreSQL 等待唯一索引锁，再提交第一批，不靠睡眠推断并发时序。
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
waitForLock:
	for {
		var waiting bool
		if err := f.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE pid = $1 AND wait_event_type = 'Lock')`, secondTx.Conn().PgConn().PID()).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break waitForLock
		}
		select {
		case err := <-done:
			t.Fatalf("concurrent import did not await the unique index: %v", err)
		case <-deadline.C:
			t.Fatal("concurrent import did not reach unique index lock")
		case <-tick.C:
		}
	}
	if err := firstTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	err := <-done
	if !errors.Is(err, user.ErrIdentityConflict) || !user.IsUniqueViolation(err) {
		t.Fatalf("concurrent conflict lost its class/cause: %v", err)
	}
	if err := secondTx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM user_account`).Scan(&count); err != nil || count != 1 {
		t.Fatal("concurrent conflict left account behind", err)
	}
	if strings.Contains(err.Error(), "race@example.com") {
		t.Fatal("concurrent error leaked target")
	}
}

func TestImportAccountsDatabaseErrorPrivacy(t *testing.T) {
	f := newImportFixture(t)
	ctx := context.Background()
	tx := importTx(t, f.pool)
	if _, err := tx.Exec(ctx, `CREATE FUNCTION import_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'private@example.com'; END $$; CREATE TRIGGER import_failure BEFORE INSERT ON user_account FOR EACH ROW EXECUTE FUNCTION import_failure()`); err != nil {
		t.Fatal(err)
	}
	a := importAccount(t, "private@example.com")
	err := f.svc.ImportAccounts(ctx, tx, []user.ImportAccount{a})
	if err == nil || strings.Contains(err.Error(), "private@example.com") {
		t.Fatal("database detail was exposed")
	}
	var cause *pgconn.PgError
	if !errors.As(err, &cause) || cause.Code != "P0001" {
		t.Fatal("database diagnostic cause was lost")
	}
	if f.logs.Len() != 0 {
		t.Fatal("import logged database PII")
	}
}

func TestImportAccountsSignInWithNewProtocol(t *testing.T) {
	f := newImportFixture(t)
	ctx := context.Background()
	a := importAccount(t, " Login.User@Example.COM ")
	tx := importTx(t, f.pool)
	if err := f.svc.ImportAccounts(ctx, tx, []user.ImportAccount{a}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	// 直接创建真实 Redis 轮次，导入阶段的投递/IdP guard 保持生效。
	rdb := redis.NewClient(&redis.Options{Addr: f.mr.Addr()})
	defer func() { _ = rdb.Close() }()
	store := code.NewStore(rdb, "import-test:", f.dig, code.Options{FailureLimitPerTarget: 10, FailureWindow: 15 * time.Minute, TTL: 5 * time.Minute, Cooldown: time.Minute, MaxAttempts: 5, DailyLimitPerTarget: 10, DailyLimitPerIP: 100})
	issued, err := store.IssueChallenge(ctx, enum.IdentityEmail, enum.PurposeSignIn, "login.user@example.com", "203.0.113.10", code.Binding{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.svc.SignInWithCode(ctx, user.CodeCredential{Channel: enum.IdentityEmail, Target: "LOGIN.USER@EXAMPLE.COM", CodeID: issued.CodeID, Code: issued.Code}, user.Device{ID: "legacy-import-device"}, user.Meta{IP: "203.0.113.10"})
	if err != nil || result.UserID != a.ID || result.IsNewUser || result.Scope != user.ScopeUser || result.AccessToken == "" || result.RefreshToken == "" {
		t.Fatal("imported anchor did not sign in with the new protocol", err)
	}
}
