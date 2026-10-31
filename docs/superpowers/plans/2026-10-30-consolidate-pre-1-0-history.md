# consolidate-pre-1-0-history 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把仓库从“守护 v0.1.0 到 v0.7.2 七代历史”收缩为“只守护 v1.0.0 一代”：迁移门禁与运行时预检只保留面向未来的规则，删除开发期兼容壳，所有面向读者的文档重写为当前状态，并新增平台中立的 HTTP 调用流程文档。

**Architecture:** 先改代码（迁移检查工具、运行时预检、测试三分、源快照退役、Deprecated 删除、API 盘点、HTTP 流程示例测试），每一步独立可编译可测；再按最终代码状态重写文档与 AGENTS.md；最后跑完整验证并写 verification.md。远端 v0 tag/Release 删除与 v1.0.0 发布准备不在本计划内。

**Tech Stack:** Go 1.26（`GOWORK=off`）、PostgreSQL 17（`SERVER_TEST_DB_DSN`）、miniredis、chi、golang-migrate、sqlc（本计划不改查询）、Node（`scripts/check-openapi.cjs`）、openspec CLI 1.13。

**Spec:** [归档变更](../../../openspec/changes/archive/2026-10-31-consolidate-pre-1-0-history/)（proposal.md、design.md、specs/*/spec.md、tasks.md）。本计划的任务编号与 tasks.md 的任务 ID 对应关系写在每个任务标题后的括号内。

## Global Constraints

- 所有命令从仓库根目录运行，Go 命令前置 `export GOWORK=off`（PowerShell 用 `$env:GOWORK = 'off'`）。
- 不修改 `migrations/0001_init.up.sql` / `0001_init.down.sql` 的任何字节；`internal/migrationcheck/main.go` 中 `initialFrozen` 的两个哈希保持不变。
- 不手改 `user/db/`、`audit/db/`；本计划不改 SQL 查询，不运行 `sqlc generate`。
- Go 文件修改后运行 `gofmt -l .` 必须无输出；Go/SQL/shell 文件保持 LF。
- 不修改 `openspec/changes/archive/` 与 `docs/superpowers/plans/` 的正文，只修链接。
- 每个任务结束提交一次，Conventional Commits，scope 用能力域（migrations、tests、user、docs、examples、agents）。提交信息末尾加 `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`。
- 测试数据库用一次性 schema（`aktest_`/`authtest_` 前缀），测试结束 `DROP SCHEMA ... CASCADE`。
- 文档正文中文；OpenSpec 结构标题与 SHALL/MUST 保留英文。
- 以下文件被 Go 代码读取，重写时必须保留对应内容：`README.md` 中至少 3 行以 `{"kind": "` 开头的凭证示例（`httpapi/enduser/response_test.go:319` 解析）；`docs/openapi/*.json` 被 `scripts/check-openapi.cjs` 与 `httpapi/enduser/session_openapi_test.go` 读取，只改 `info.version` 与 `info.description`。

## Review Focus

1. 一个由更新二进制迁过的库（`schema_migrations.version` 大于当前源最大版本）被旧二进制接管：期望无写拒绝并返回 `ErrIncompatibleBaseline`，重复尝试不泄漏锁。测试：Task 2 的 `TestVersionBeyondSourceRejected`。
2. `schema_migrations` 同时 dirty 且版本超前：期望 dirty 错误优先。测试：Task 2 的 `TestVersionBeyondSourceRejected/dirty_precedence`。
3. 仓库里仍有或被重新创建 `v0.x.y` tag，其 0001 与当前基线不同：期望历史检查完全忽略它们并通过。测试：Task 1 的 `TestPreReleaseTagsAreIgnored`。
4. HTTP 客户端在宽限期外重放旧 refresh：期望 400 `invalid_grant` 且该会话被吊销，新 access 随即 401。测试：Task 9 的 `TestHTTPFlows/refresh_replay_outside_grace`。
5. 客户端解绑了当前会话的登录来源身份：期望 204 后当前 access 立即 401 并带 `WWW-Authenticate`。测试：Task 9 的 `TestHTTPFlows/unbind_source_logs_out`。

---

### Task 1: 迁移历史检查工具只认 v1.0.0 及以上（tasks 1.1、1.2）

**Files:**
- Modify: `internal/migrationcheck/main.go`
- Modify: `internal/migrationcheck/main_test.go`
- Delete: `internal/migrationcheck/published_initial_test.go`

**Interfaces:**
- Consumes: 无。
- Produces: `func checkWithInitial(root string, initialHashes map[string]string) error`（测试入口）；`func check(root string) error` 不变；`releaseTag` 捕获主版本号。

- [ ] **Step 1: 先改测试夹具，使其不再依赖源快照与 v0 tag**

把 `main_test.go` 中 `repositoryFixture`、`check()`、`fixture()` 整体替换为：

```go
type repositoryFixture struct {
	root string
}

func (r repositoryFixture) check() error {
	hashes := map[string]string{}
	for name, data := range fixtureInitial {
		sum := sha256.Sum256(data)
		hashes[name] = hex.EncodeToString(sum[:])
	}
	return checkWithInitial(r.root, hashes)
}

// fixture 创建只含当前初始定义的仓库；tag=true 时在 v1.0.0 固定该定义并再提交一次。
func fixture(t *testing.T, tag bool) repositoryFixture {
	t.Helper()
	root := t.TempDir()
	gitTest(t, root, "init")
	for name, data := range fixtureInitial {
		write(t, root, "migrations/"+name, data)
	}
	if tag {
		gitTest(t, root, "add", ".")
		gitTest(t, root, "commit", "-m", "v1.0.0 baseline")
		gitTest(t, root, "tag", "-a", "v1.0.0", "-m", "first formal release")
		write(t, root, "README.md", []byte("next"))
		gitTest(t, root, "add", ".")
		gitTest(t, root, "commit", "-m", "next")
	}
	return repositoryFixture{root}
}
```

删除整个 `TestFrozenBaselineCannotBeRewritten`、`TestPinnedLegacyReleaseCannotMove`、`TestLegacyReleaseSQLIsVerified`、`TestNewReleaseCannotUseLegacyInitialSQL` 四个函数。把其余测试里的 `"v0.3.0"` 全部改为 `"v1.1.0"`，`"v0.4.0"` 改为 `"v1.2.0"`（涉及 `TestHeadTagCannotBlessHistoricalRewrite`、`TestWorkingTreeCannotRewriteMigrationAtHeadRelease`、`TestMergedReleaseHistoryIsImmutable`、`TestPublishedIncrementCannotDisappearInLaterRelease`、`TestReleaseVersionsMustIncrease`）。删除不再使用的 `strings` import（若 `TestShallowHistoryIsRejected` 仍用 `strings.Contains` 则保留）。

在文件末尾新增：

```go
// 开发期 v0.x.y 标签已退役：即使仍存在且其迁移与当前基线不同，也不参与检查。
func TestPreReleaseTagsAreIgnored(t *testing.T) {
	for _, tc := range []struct{ name, tag string }{
		{"old minor", "v0.7.2"},
		{"old zero", "v0.0.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := fixture(t, true)
			write(t, r.root, "migrations/0001_init.up.sql", []byte("SELECT 'retired';"))
			write(t, r.root, "migrations/0002_retired.up.sql", []byte("SELECT 1;"))
			gitTest(t, r.root, "add", ".")
			gitTest(t, r.root, "commit", "-m", "retired development release")
			gitTest(t, r.root, "tag", tc.tag)
			// 恢复工作树到当前基线；退役标签内的篡改与多余增量都不能影响结果。
			for name, data := range fixtureInitial {
				write(t, r.root, "migrations/"+name, data)
			}
			if err := os.Remove(filepath.Join(r.root, "migrations/0002_retired.up.sql")); err != nil {
				t.Fatal(err)
			}
			gitTest(t, r.root, "add", ".")
			gitTest(t, r.root, "commit", "-m", "back to baseline")
			if err := r.check(); err != nil {
				t.Fatalf("pre-1.0 tag must be ignored: %v", err)
			}
		})
	}
}
```

- [ ] **Step 2: 运行测试确认编译失败**

Run: `GOWORK=off go test ./internal/migrationcheck/ 2>&1 | head -5`
Expected: 编译错误 `undefined: checkWithInitial`（`published_initial_test.go` 仍引用旧符号，先删除它：`git rm internal/migrationcheck/published_initial_test.go`）。

- [ ] **Step 3: 重写 main.go**

删除 `frozen`、`legacyReleases`、`pinnedInitialRelease`、`publishedInitialReleases`、`sourceBaseline`、`checkPinnedRelease`、`checkWithBaselines`、`checkWithReleaseBaselines`。`releaseTag` 改为 `regexp.MustCompile(`^v([0-9]+)\.[0-9]+\.[0-9]+$`)`。`releasedHistory` 的循环改为：

```go
	for _, tag := range strings.Fields(string(tags)) {
		m := releaseTag.FindStringSubmatch(tag)
		if m == nil || m[1] == "0" {
			// 主版本为 0 的开发期标签已退役，不参与比对，也不获得豁免。
			continue
		}
		refs = append(refs, "refs/tags/"+tag)
	}
```

`check` 与新入口：

```go
func check(root string) error { return checkWithInitial(root, initialFrozen) }

// checkWithInitial 固定当前初始定义，并要求 v1.0.0 及以上每个可达正式标签内的迁移不可改写、不可消失、版本递增。
func checkWithInitial(root string, initialHashes map[string]string) error {
	releases, err := releasedHistory(root)
	if err != nil {
		return err
	}
	baseline := map[string][]byte{}
	for name, want := range initialHashes {
		got, err := os.ReadFile(filepath.Join(root, "migrations", name))
		if err != nil {
			return err
		}
		sum := sha256.Sum256(got)
		if hex.EncodeToString(sum[:]) != want {
			return fmt.Errorf("consolidated initial migration changed: %s", name)
		}
		baseline[name] = got
	}
	initialVersion, err := validateNames(baseline)
	if err != nil {
		return err
	}
	snapshots := map[string]map[string][]byte{}
	for _, ref := range releases {
		files, err := releaseMigrations(root, ref)
		if err != nil {
			return err
		}
		for name := range initialHashes {
			if !bytes.Equal(files[name], baseline[name]) {
				return fmt.Errorf("release initial migration changed or missing: %s: %s", ref, name)
			}
		}
		// 以下与原实现相同：validateNames(files)、snapshots[ref]=files、增量版本必须大于 initialVersion、
		// 同名增量不可改写（baseline[name] 累积）。
		...
	}
	// 以下与原实现相同：祖先关系下的历史增量不可删改、版本递增；工作树必须包含全部历史增量且字节一致；
	// 工作树新增版本必须大于 maxVersion。
	...
	return nil
}
```

“以下与原实现相同”的部分从原 `checkWithReleaseBaselines` 中把 `hasPinned` 分支删掉后原样保留。顶部注释改为：`// migrationcheck enforces that the current initial definition and every v1.0.0+ release's migrations are immutable.`，并把 `initialFrozen` 上方注释改为 `// 当前初始定义的固定哈希；自 v1.0.0 tag 起不可改写，后续结构从 0002 递增。`

- [ ] **Step 4: 运行测试**

Run: `GOWORK=off go test -count=1 ./internal/migrationcheck/ -run 'TestMigrationHistoryCheck|TestHeadTag|TestWorkingTree|TestMergedRelease|TestPublishedIncrement|TestShallow|TestReleaseVersions|TestPreReleaseTags' -v 2>&1 | tail -20`
Expected: 全部 PASS。注意 `sourceBaseline` 删除后 `encoding/json` import 可能未使用，按编译器提示删除。

- [ ] **Step 5: 在真实仓库上运行检查**

Run: `bash scripts/check-migrations.sh && echo OK`
Expected: `OK`（仓库当前仍带 v0 tag，必须通过）。

- [ ] **Step 6: 提交**

```bash
git add internal/migrationcheck
git commit -m "refactor(migrations): history check guards only v1.0.0+ releases

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: 运行时预检只保留 dirty 优先与版本超前拒绝（task 2.1 及 2.2 的新增用例）

**Files:**
- Modify: `migrations/baseline.go`
- Delete: `migrations/identity_kind_baseline.go`、`migrations/profile_attributes_baseline.go`、`migrations/purged_user_baseline.go`
- Create: `migrations/baseline_test.go`

**Interfaces:**
- Produces: `ErrIncompatibleBaseline`（公开哨兵保留，语义改为“版本超出当前源”）；`checkBaseline(ctx, db, schema, src)` 签名不变。

- [ ] **Step 1: 写新测试**

创建 `migrations/baseline_test.go`：

```go
package migrations

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
)

// 版本高于当前迁移源（例如被更新二进制迁过的库被旧二进制接管）必须无写拒绝；dirty 优先。
func TestVersionBeyondSourceRejected(t *testing.T) {
	ctx := context.Background()
	db, schema := safetyDB(t)
	if err := initialUp(ctx, db, schema); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "INSERT INTO "+schema+".user_account(id,state,display_name) VALUES('u_0000000000001',1,'kept')"); err != nil {
		t.Fatal(err)
	}
	before := migrationCatalog(t, ctx, db, schema)
	t.Run("version_ahead", func(t *testing.T) {
		if _, err := db.Exec(ctx, "UPDATE "+schema+".schema_migrations SET version=99, dirty=false"); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			start := time.Now()
			if err := initialUp(ctx, db, schema); !errors.Is(err, ErrIncompatibleBaseline) {
				t.Fatalf("version ahead accepted: %v", err)
			}
			if time.Since(start) > time.Second {
				t.Fatal("rejection waited on a lock: connection or lock leaked")
			}
		}
		assertVersion(t, ctx, db, schema, 99, false)
		if migrationCatalog(t, ctx, db, schema) != before {
			t.Fatal("rejected database changed")
		}
		var rows int
		if err := db.QueryRow(ctx, "SELECT count(*) FROM "+schema+".user_account").Scan(&rows); err != nil || rows != 1 {
			t.Fatalf("data changed: rows=%d err=%v", rows, err)
		}
	})
	t.Run("dirty_precedence", func(t *testing.T) {
		if _, err := db.Exec(ctx, "UPDATE "+schema+".schema_migrations SET version=99, dirty=true"); err != nil {
			t.Fatal(err)
		}
		err := initialUp(ctx, db, schema)
		var dirty migrate.ErrDirty
		if !errors.As(err, &dirty) || dirty.Version != 99 {
			t.Fatalf("dirty must win over version check: %v", err)
		}
		if errors.Is(err, ErrIncompatibleBaseline) {
			t.Fatal("dirty error must not be wrapped as incompatible baseline")
		}
		assertVersion(t, ctx, db, schema, 99, true)
	})
}
```

`safetyDB`、`initialUp`、`migrationCatalog`、`assertVersion` 是 `migrations` 包已有测试助手（分别在 `migrations_test.go`、`initial_schema_test.go` 等中，签名见 `grep -n 'func safetyDB\|func initialUp\|func migrationCatalog\|func assertVersion' migrations/*_test.go`），按其实际签名调整参数顺序。

- [ ] **Step 2: 运行新测试确认当前通过（它测的是 (b) 规则，已存在）**

Run: `SERVER_TEST_DB_DSN=... GOWORK=off go test -count=1 ./migrations/ -run TestVersionBeyondSourceRejected -v`
Expected: PASS（若 `migrationCatalog` 签名不同导致编译错误，先修签名）。

- [ ] **Step 3: 收缩 baseline.go**

把 `checkBaseline` 替换为：

```go
// ErrIncompatibleBaseline 表示数据库记录的版本不存在于当前迁移源，例如被更新的二进制迁移过的库被旧二进制接管。
// 拒绝发生在任何写入之前，保留 catalog、数据与 version/dirty。
var ErrIncompatibleBaseline = errors.New("migrations: incompatible migration baseline")

// 调用方已持有两把 advisory lock；预检只读，不执行 DDL。
func checkBaseline(ctx context.Context, db *sql.DB, schema string, src source.Driver) error {
	var exists bool
	if err := db.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, schema+".schema_migrations").Scan(&exists); err != nil {
		return fmt.Errorf("migrations: inspect baseline: %w", err)
	}
	if !exists {
		return nil
	}
	var version int
	var dirty bool
	err := db.QueryRowContext(ctx, "SELECT version, dirty FROM "+schema+".schema_migrations").Scan(&version, &dirty)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("migrations: inspect baseline version: %w", err)
	}
	// dirty 优先，沿用引擎哨兵。
	if dirty {
		return fmt.Errorf("migrations: schema %s: %w", schema, migrate.ErrDirty{Version: version})
	}
	if version < 1 {
		return nil
	}
	migration, _, err := src.ReadUp(uint(version))
	if err != nil {
		return fmt.Errorf("%w: schema %s version %d is beyond the current migration source", ErrIncompatibleBaseline, schema, version)
	}
	if err := migration.Close(); err != nil {
		return fmt.Errorf("migrations: close baseline source: %w", err)
	}
	return nil
}
```

删除 `checkSessionContextBaseline` 及文件中其余内容；`git rm migrations/identity_kind_baseline.go migrations/profile_attributes_baseline.go migrations/purged_user_baseline.go`。

- [ ] **Step 4: 编译与 vet**

Run: `GOWORK=off go build ./... && GOWORK=off go vet ./migrations/ 2>&1 | head`
Expected: build 通过；vet 可能报测试文件引用已删符号（`checkIdentityKindBaseline` 等）：那些测试在 Task 3 删除，此处只要求 `go build ./...` 通过。

- [ ] **Step 5: 提交**

```bash
git add migrations/baseline.go migrations/baseline_test.go
git rm -q migrations/identity_kind_baseline.go migrations/profile_attributes_baseline.go migrations/purged_user_baseline.go
git commit -m "refactor(migrations): preflight keeps only dirty and version-ahead rejection

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: 迁移测试按用例三分（task 2.2）

**Files:**
- Modify: `migrations/initial_schema_test.go`、`generic_audit_test.go`、`session_context_test.go`、`session_context_consolidation_test.go`、`simplified_session_context_test.go`、`profile_attributes_test.go`、`avatar_test.go`、`identity_kind_test.go`、`purged_user_test.go`、`sign_in_completion_flow_rename_test.go`
- Delete: `migrations/testdata/legacy_integrated_v1.sql`

**Interfaces:** 无新接口；保留助手 `initialUp`、`migrationTestConn`、`migrationCatalog`、`safetyDB`、`assertVersion`、`latestVersion`、`safetySource`、`sourceUp`、`consolidatedProfileAttributesInitial`、`profileAttributesOldData`、`seedRenameFlow`、`renameDataSnapshot`。

- [ ] **Step 1: 按下表删除测试函数（整函数删除）**

| 文件 | 删除的函数 |
|---|---|
| initial_schema_test.go | `TestInitialSchemaPreservesNonAuditCatalog`；`TestOldMigrationBaselineRejected` 整个删除（版本超前已由 Task 2 覆盖）；常量 `historicalFlowSQL`、`databaseReviewIndexSQL` |
| generic_audit_test.go | `TestGenericAuditLegacySameVersionRejected`、`TestGenericAuditBaselineRejectsPartialLatest`、`TestAuditMigrationRejectsIncompleteInitialBaseline` |
| session_context_test.go | `TestSessionContextBaselineRejectsPartialVersion`、`TestSessionContextPrecheckIgnoresConcurrentOtherSchemaDDL` |
| session_context_consolidation_test.go | `TestSessionContextConsolidatedCatalog`、`TestSessionContextRetiredBaselinesRejected`、`sessionContextBeforeConsolidation`、`sessionContextHistoricalSQL`、`sessionContextUnchangedCatalog`（若文件为空则 `git rm`） |
| simplified_session_context_test.go | `TestSimplifiedSessionRejectsPreviousSnapshotBaseline` |
| profile_attributes_test.go | `TestProfileAttributesConsolidatedV1Repeat`、`TestProfileAttributesInitialCatalogEquivalent`、`TestProfileAttributesWithdrawnV2Rejected`（已由 `TestVersionBeyondSourceRejected` 覆盖）、`publishedProfileAttributesInitial`、`profileAttributesInitialDDL`；`TestProfileAttributesBaseline` 只保留子测试 `cancel_v1_repeat_releases_locks`，其余子测试与其专用助手删除 |
| avatar_test.go | `TestAvatarBaselineRejected`、`TestAvatarPreviousProfileV1Rejected`、`avatarInitialFixtureSQL` |
| identity_kind_test.go | `TestIdentityKindSmallintV1Rejected`、`TestIdentityKindSmallintV1WithTextColumnAccepted`、`TestIdentityKindDriftRejected`、`identityKindTextDDL`、`smallintIdentityKindCommits`、`smallintIdentityKindV1`、`assertIdentityKindRejected` |
| purged_user_test.go | `TestPurgedUserMissingV060Rejected`、`TestPurgedUserV060WithTableAccepted`、`TestPurgedUserDriftRejected`、`purgedUserDDL`、`publishedV060Commit`、`publishedV060Initial`、`assertPurgedUserRejected`；`TestPurgedUserInitialDefinition` 删除与 `publishedV060Initial`+`purgedUserDDL` 的 catalog 比对段（原约 70-78 行），保留 seq/identity 检查、约束违例循环与重复 Up catalog 检查 |
| sign_in_completion_flow_rename_test.go | `TestSignInCompletionFlowBaselineMismatch`、`sortedRenameCatalog`、`historicalFlowInitial`；`TestSignInCompletionFlowInitialDefinition` 只保留“`initialUp` 后 `to_regclass('sign_in_completion_flow')` 存在且 `identity_flow` 不存在”的断言，删除历史 catalog 比对；`wechatKindLiteral` 的 `int2` 分支删除；`seedRenameFlow`/`renameDataSnapshot` 的 `flowTable` 参数删除并固定为 `sign_in_completion_flow` |

然后 `git rm migrations/testdata/legacy_integrated_v1.sql`。`migrationCatalogForTables` 的 `nonAudit` 参数若只剩 `false` 调用，把函数折叠进 `migrationCatalog`。

- [ ] **Step 2: 清理 import 并编译**

Run: `GOWORK=off go vet ./migrations/ 2>&1 | head -30`
Expected: 逐个按提示删除未使用的 import（`os/exec`、`os`、`strings`、`fmt`、`migrate` 等）与变量，直到 vet 无输出。

- [ ] **Step 3: 确认无残留引用**

Run: `rg 'git show|rev-parse|legacy_integrated_v1|source-baseline|publishedV060|publishedProfileAttributesInitial|avatarInitialFixtureSQL|historicalFlow|sessionContextHistorical|smallintIdentityKind' migrations/ internal/migrationcheck/`
Expected: 无输出。

- [ ] **Step 4: 运行整个 migrations 包**

Run: `SERVER_TEST_DB_DSN=... GOWORK=off go test -count=1 ./migrations/ 2>&1 | tail -5`
Expected: `ok`。

- [ ] **Step 5: 提交**

```bash
git add -A migrations
git commit -m "test(migrations): retire development-era baseline rejection cases

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: 源快照退役，合成夹具改为普通测试种子（tasks 1.3、3.1）

**Files:**
- Create: `tests/testdata/synthetic/fixture.json`（内容与现 `tests/testdata/source-baseline/fixture.json` 相同）
- Create: `synthetic_fixture_test.go`（根包 `accountkit_test`）
- Delete: `compatibility_test.go`、`tests/testdata/source-baseline/`（整目录）
- Modify: `audit_activation_test.go`

**Interfaces:**
- Produces: `type syntheticFixture struct{ UserID, IdentityID, SessionID, EventID, Phone, RefreshToken string }`；`func seedSyntheticAccount(t *testing.T, pool *pgxpool.Pool, cfg accountkit.Config) (syntheticFixture, string)`；`func snapshotSource(t *testing.T, pool *pgxpool.Pool) string`。

设计偏离记录：design D5 原写“读取 fixture 的用例删除”。实施发现 `recovery_test.go`、`host_contract_test.go`、`isolation_test.go`、`audit_lifecycle_test.go`、`host_lifecycle_db_test.go`、`recovery_profile_attributes_test.go` 都用该种子函数建立合成账号，恢复演练也依赖它。因此保留 fixture.json 作为纯合成种子并改名，只删除“历史格式”这一证明。本偏离写入 design.md「Decisions」末尾（Task 18 执行）。

- [ ] **Step 1: 新建种子文件与助手**

```bash
mkdir -p tests/testdata/synthetic
git mv tests/testdata/source-baseline/fixture.json tests/testdata/synthetic/fixture.json
```

创建 `synthetic_fixture_test.go`，内容为 `compatibility_test.go` 中 `sourceFixture` 类型、`seedFormatFixture`、`snapshotSource` 的搬迁版本，改动如下：类型改名 `syntheticFixture`；函数改名 `seedSyntheticAccount`，读取路径改为 `tests/testdata/synthetic/fixture.json`，注释改为 `// seedSyntheticAccount 在当前结构上写入一个合成账号、身份、会话与审计事件，并签发一枚合法 access token；仅用于测试。`；`snapshotSource` 直接内联查询，固定表名 `sign_in_completion_flow`，删除 `snapshotSourceWithFlowTable`。保留原有 import（`crypto/*`、`encoding/*`、`jwt`、`migrations` 等）。

- [ ] **Step 2: 全仓替换引用并删除旧文件**

```bash
git rm -q compatibility_test.go
git rm -rq tests/testdata/source-baseline
sed -i 's/seedFormatFixture/seedSyntheticAccount/g; s/\bsourceFixture\b/syntheticFixture/g' audit_activation_test.go audit_lifecycle_test.go host_contract_test.go host_lifecycle_db_test.go isolation_test.go recovery_test.go recovery_profile_attributes_test.go
```

- [ ] **Step 3: 收缩 `TestMigrateAuditConfigurationMinimumPoolCapacity`**

在 `audit_activation_test.go` 中，该测试的 `mode` 循环只保留 `"fresh_repeat"`：删除 `old_v1_*` 分支（读取 `legacy_integrated_v1.sql`、`snapshotSourceWithFlowTable(..., "identity_flow")` 的整个 `if mode != "fresh_repeat"` 块），保留 `else` 分支内容为唯一路径；删除因此未用的 `os`、`migrations`、`errors` import。

- [ ] **Step 4: 编译根包测试并确认无残留**

Run: `GOWORK=off go vet . && rg 'source-baseline|legacy_integrated_v1|seedFormatFixture|snapshotSourceWithFlowTable|HistoricalFormats' --glob '!openspec/changes/archive/**' --glob '!docs/superpowers/**' --glob '!.test-output/**'`
Expected: vet 无输出；rg 只剩 `docs/`、`README.md`、`AGENTS.md`、`scripts/required-tests.txt` 中的文档/清单命中（Task 5 与文档任务处理）。

- [ ] **Step 5: 运行受影响的根包用例**

Run: `SERVER_TEST_DB_DSN=... GOWORK=off go test -count=1 . -run 'TestHostContracts|TestIndependentInstances|TestMigrateAuditConfigurationMinimumPoolCapacity|TestRecoverySessionContextSnapshot|TestAuditExternalConfigurationMatrix' 2>&1 | tail -5`
Expected: `ok`。

- [ ] **Step 6: 提交**

```bash
git add -A tests synthetic_fixture_test.go audit_activation_test.go audit_lifecycle_test.go host_contract_test.go host_lifecycle_db_test.go isolation_test.go recovery_test.go recovery_profile_attributes_test.go
git commit -m "test: retire the original-project source baseline, keep a synthetic seed

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: 删除 Deprecated 兼容入口（tasks 4.1、4.2）

**Files:**
- Modify: `ids/ids.go`、`ids/flow_test.go`、`user/service.go`、`user/service_signin_completion_store.go`、`user/service_signin_completion_context_test.go`、`user/service_client_info_test.go`、`user/service_alipay_test.go`、`user/maintenance_observability_test.go`、`user/service_delivery_test.go`、`user/sender/sender.go`
- Delete: `sender_compatibility_test.go`
- Create: `sender_contract_test.go`

**Interfaces:**
- Produces: 公开面减少六个符号；`user.Deps.SignInCompletionFlowContext` 为唯一上下文字段。

- [ ] **Step 1: 删除生产代码符号**

- `ids/ids.go`：删除 `// Deprecated: use SignInCompletionFlow.` 与 `IdentityFlow Kind = SignInCompletionFlow` 两行。
- `user/sender/sender.go`：删除第 29-42 行的三个别名及其注释。
- `user/service.go`：删除 `IdentityFlowContext string` 字段及其 Deprecated 注释。
- `user/service_signin_completion_store.go`：`signInCompletionFlowConfiguration` 中

```go
	extra := d.SignInCompletionFlowContext
	if extra == "" {
		extra = d.IdentityFlowContext
	}
```
改为 `extra := d.SignInCompletionFlowContext`；删除 `CleanupIdentityFlows` 方法及注释。

- [ ] **Step 2: 修测试**

- `ids/flow_test.go`：`IdentityFlow` 改为 `SignInCompletionFlow`，函数改名 `TestSignInCompletionFlowID`。
- `user/service_signin_completion_context_test.go`：第 27 行改用 `SignInCompletionFlowContext: "stable-instance"`；删除 `legacy_fallback`、`new_precedence` 两个子测试，保留 `new_only` 与 wire JSON/AAD/重放断言；函数改名 `TestSignInCompletionContextStable`。
- `user/service_client_info_test.go` 第 321-324 行与 `user/service_alipay_test.go` 第 205-208 行：删除 `if extra == "" { extra = d.IdentityFlowContext }` 三行。
- `user/maintenance_observability_test.go:29`：`CleanupIdentityFlows` 改为 `CleanupSignInCompletionFlows`。
- `user/service_delivery_test.go`：`sender.Message` 改 `sender.CodeMessage`，`sender.SMSSender`/`sender.EmailSender` 改 `SMSCodeSender`/`EmailCodeSender`；`legacyDelivery` 改名 `codeDelivery`，`TestLegacyDeliveryPurposesAndFailureQuota` 改名 `TestDeliveryPurposesAndFailureQuota`（同步 `service_challenge_test.go`、`service_renderer_test.go` 引用）。
- 删除 `sender_compatibility_test.go`，新建 `sender_contract_test.go`：

```go
package accountkit_test

import (
	"context"
	"testing"

	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/user/sender"
)

type smsOnlySender struct{ got []sender.CodeMessage }

func (s *smsOnlySender) SendSMS(_ context.Context, _ string, m sender.CodeMessage) error {
	s.got = append(s.got, m)
	return nil
}

var _ sender.SMSCodeSender = (*smsOnlySender)(nil)

// 只实现 SendSMS 的类型满足 SMSCodeSender，但不满足 EmailCodeSender；Deps 字段按渠道注入。
func TestCodeSenderContract(t *testing.T) {
	var _ any = (*smsOnlySender)(nil)
	if _, ok := any(&smsOnlySender{}).(sender.EmailCodeSender); ok {
		t.Fatal("SMS-only sender must not satisfy EmailCodeSender")
	}
	s := &smsOnlySender{}
	d := accountkit.Deps{SMSSender: s}
	if d.SMSSender == nil || d.EmailSender != nil {
		t.Fatal("Deps must carry SMS and email senders independently")
	}
}
```

- [ ] **Step 3: 编译、vet、残留检查**

Run: `GOWORK=off go build ./... && GOWORK=off go vet ./... && rg 'IdentityFlow\b|IdentityFlowContext|CleanupIdentityFlows|sender\.SMSSender\b|sender\.EmailSender\b|sender\.Message\b|legacyDelivery' --type go --glob '!.test-output/**'`
Expected: build/vet 通过；rg 无输出。

- [ ] **Step 4: 运行相关测试**

Run: `SERVER_TEST_DB_DSN=... GOWORK=off go test -count=1 ./ids/ ./user/ . -run 'TestSignInCompletionFlowID|TestSignInCompletionContextStable|TestMaintenanceObservations|TestDeliveryPurposesAndFailureQuota|TestDisabledChannel|TestUnavailableDeliveryIsSafeAndRetainsQuota|TestCodeSenderContract|TestPendingClientInfoLegacyAndFallback' 2>&1 | tail -5`
Expected: `ok`。

- [ ] **Step 5: 提交**

```bash
git add -A ids user sender_contract_test.go
git rm -q sender_compatibility_test.go
git commit -m "refactor!: remove development-era deprecated aliases before v1.0.0

BREAKING CHANGE: ids.IdentityFlow, user.Deps.IdentityFlowContext, CleanupIdentityFlows, sender.SMSSender, sender.EmailSender and sender.Message are removed; use the formal names.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: 同步必需测试清单（task 2.3 与 4.2 的清单部分）

**Files:**
- Modify: `scripts/required-tests.txt`

- [ ] **Step 1: 删除退役条目**

用下面脚本删除（路径前缀 `github.com/bbxx111/accountkit/`）：

```bash
for t in migrations/TestOldMigrationBaselineRejected migrations/TestInitialSchemaPreservesNonAuditCatalog migrations/TestGenericAuditLegacySameVersionRejected migrations/TestGenericAuditBaselineRejectsPartialLatest migrations/TestAuditMigrationRejectsIncompleteInitialBaseline migrations/TestSessionContextBaselineRejectsPartialVersion migrations/TestSessionContextPrecheckIgnoresConcurrentOtherSchemaDDL migrations/TestSessionContextConsolidatedCatalog migrations/TestSessionContextRetiredBaselinesRejected migrations/TestSimplifiedSessionRejectsPreviousSnapshotBaseline migrations/TestProfileAttributesConsolidatedV1Repeat migrations/TestProfileAttributesInitialCatalogEquivalent migrations/TestProfileAttributesWithdrawnV2Rejected migrations/TestAvatarBaselineRejected migrations/TestAvatarPreviousProfileV1Rejected migrations/TestIdentityKindSmallintV1Rejected migrations/TestIdentityKindSmallintV1WithTextColumnAccepted migrations/TestIdentityKindDriftRejected migrations/TestPurgedUserMissingV060Rejected migrations/TestPurgedUserV060WithTableAccepted migrations/TestPurgedUserDriftRejected migrations/TestSignInCompletionFlowBaselineMismatch internal/migrationcheck/TestFrozenBaselineCannotBeRewritten internal/migrationcheck/TestPinnedLegacyReleaseCannotMove internal/migrationcheck/TestLegacyReleaseSQLIsVerified internal/migrationcheck/TestNewReleaseCannotUseLegacyInitialSQL internal/migrationcheck/TestPinnedPublishedInitialBaseline internal/migrationcheck/TestPinnedLegacyReleaseCannotDisappearFromHistory internal/migrationcheck/TestPublishedV040Pinned internal/migrationcheck/TestCurrentInitialDiffersFromPublishedV040 internal/migrationcheck/TestPublishedV060Pinned internal/migrationcheck/TestCurrentInitialDiffersFromPublishedV060 TestHistoricalFormatsOnInitialSchema TestCodeSenderCompatibility user/TestSignInCompletionContextCompatibility ids/TestIdentityFlowID; do
  sed -i "\#^github.com/bbxx111/accountkit/$t\$#d" scripts/required-tests.txt
done
```

- [ ] **Step 2: 加入新条目并排序**

追加后按原文件顺序（文件按包分组、组内字母序）插入：
```
github.com/bbxx111/accountkit/migrations/TestVersionBeyondSourceRejected
github.com/bbxx111/accountkit/internal/migrationcheck/TestPreReleaseTagsAreIgnored
github.com/bbxx111/accountkit/TestCodeSenderContract
github.com/bbxx111/accountkit/user/TestSignInCompletionContextStable
github.com/bbxx111/accountkit/ids/TestSignInCompletionFlowID
github.com/bbxx111/accountkit/user/TestDeliveryPurposesAndFailureQuota
```

- [ ] **Step 3: 验证清单与测试集合一致**

Run: `SERVER_TEST_DB_DSN=... GOWORK=off go test -count=1 -json ./... > .test-output/tests.jsonl 2>&1; GOWORK=off go run ./internal/testgate scripts/required-tests.txt < .test-output/tests.jsonl && echo GATE_OK`
Expected: `GATE_OK`，无 missing/skip 报告。

- [ ] **Step 4: 提交**

```bash
git add scripts/required-tests.txt
git commit -m "test: sync required tests with the single-baseline gates

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: 凭证格式覆盖核对（task 3.2）

**Files:**
- Create（仅在发现缺口时）: `pii/format_test.go`、`tokens/format_test.go`
- Output: `.test-output/format-coverage.md`（被忽略目录，仅作证据）

- [ ] **Step 1: 列出现有格式测试**

Run: `rg -n 'func Test' pii/*_test.go tokens/*_test.go user/code/*_test.go | rg -i 'cipher|gcm|hmac|digest|rotation|version|hash|sign|jwt|refresh'`
Expected: 得到清单，记录到 `.test-output/format-coverage.md` 的“已有覆盖”表。

- [ ] **Step 2: 对照四项要求核对**

必须各有至少一个用例覆盖：(1) 版本化 AES-GCM 密文 `nonce||ciphertext` 按 `cipher_key_version` 解密，旧版本密钥仍可解；(2) HMAC-SHA256 摘要按 `digest_key_version` 匹配，轮换后新旧摘要都能查到同一身份；(3) refresh token 以 SHA-256 哈希存储并比对；(4) JWT `kid` 对应密钥版本，旧 `kid` 在密钥仍配置时可验证。缺哪一项就在对应包补一个表驱动单元测试（不依赖数据库）。

- [ ] **Step 3: 运行并记录**

Run: `GOWORK=off go test -count=1 ./pii/ ./tokens/ ./user/code/ 2>&1 | tail -3`
Expected: `ok`。把每项要求对应的测试名写进 `.test-output/format-coverage.md`，供 Task 18 引用。

- [ ] **Step 4: 提交（仅当新增测试）**

```bash
git add pii tokens
git commit -m "test(pii,tokens): pin credential format coverage after baseline retirement

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: 公开 Go API 冻结前盘点（tasks 5.1、5.2）

**Files:**
- Output: `.test-output/api-inventory/<pkg>.txt`
- Modify: `openspec/changes/consolidate-pre-1-0-history/design.md`（附录「接受的 API 现状」）
- Modify（仅处理项）: 对应源文件

- [ ] **Step 1: 导出公开面**

```bash
mkdir -p .test-output/api-inventory
for p in . ./user ./user/sender ./user/code ./user/idp ./httpapi/enduser ./httpapi/admin ./httpapi/authn ./httpapi/apierror ./tokens ./audit ./maintenance ./anonymize ./migrations ./ids ./enum ./pii ./session/grace ./session/revocation; do
  n=$(echo "$p" | tr './' '__'); GOWORK=off go doc -all "$p" > ".test-output/api-inventory/$n.txt" 2>&1
done
```

- [ ] **Step 2: 逐包通读并分类**

对每个文件按三类记录：命名与既有约定不一致（如 `_at` 时间、非 `<verb>_time`、缩写大小写不一致）；不应导出（只被包内或测试使用的导出符号，用 `rg '\bName\b' --type go` 确认无外部引用）；文档缺失（导出符号无注释，`go vet` 不查，用 `rg -B1 '^func [A-Z]|^type [A-Z]' pkg/*.go | rg -v '//'` 粗筛）。结果写入 design.md 末尾新节 `## 附录：接受的 API 现状`，格式为表格：符号 | 类别 | 处理（修改/接受）| 理由。

- [ ] **Step 3: 处理决定修改的项**

只允许：重命名未被外部引用的导出符号、取消导出、补注释。每项改动后 `GOWORK=off go build ./... && go vet ./...`。若某项涉及规格要求，暂停并报告，不自行加 delta。

- [ ] **Step 4: 提交**

```bash
git add -A
git commit -m "chore: public API inventory before the v1.0.0 freeze

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 9: HTTP 调用流程示例测试（task 6.1）

**Files:**
- Create: `examples/httpflows/doc.go`、`examples/httpflows/flows_test.go`
- Modify: `scripts/required-tests.txt`

**Interfaces:**
- Produces: 测试 `TestHTTPFlows` 及子测试 `first_sign_in`、`pending_completion`、`refresh_coordination`、`refresh_replay_outside_grace`、`reauthentication_keeps_refresh`、`revoke_others_and_delete_session`、`unbind_source_logs_out`；文档 Task 10 按子测试名引用。

- [ ] **Step 1: 写 doc.go**

```go
// Package httpflows 以 httptest 挂载内置终端用户 HTTP 面，走完客户端必须处理的协议层流程：
// 首次登录与 pending 补全、刷新协调与宽限、宽限外重放、重新认证、注销/撤销/解绑导致的退出。
// 它是 docs/http-client-flows.md 的可执行对照，只断言状态机与响应形状，不断言随机凭证值。
package httpflows
```

- [ ] **Step 2: 写失败的测试**

创建 `examples/httpflows/flows_test.go`：

```go
package httpflows_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/internal/testkeys"
	"github.com/bbxx111/accountkit/user/sender"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// capture 记录每个目标最近一次验证码与 code_id；真实客户端从短信/邮件取得验证码。
type capture struct {
	mu      sync.Mutex
	codes   map[string]string
	codeIDs map[string]string
}

func (c *capture) SendSMS(_ context.Context, to string, m sender.CodeMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.codes == nil {
		c.codes = map[string]string{}
	}
	c.codes[to] = m.Code
	return nil
}
func (c *capture) SendEmail(ctx context.Context, to string, m sender.CodeMessage) error {
	return c.SendSMS(ctx, to, m)
}
func (c *capture) code(to string) string { c.mu.Lock(); defer c.mu.Unlock(); return c.codes[to] }
func (c *capture) remember(target, id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.codeIDs == nil {
		c.codeIDs = map[string]string{}
	}
	c.codeIDs[target] = id
}
func (c *capture) codeID(target string) string { c.mu.Lock(); defer c.mu.Unlock(); return c.codeIDs[target] }

type env struct {
	t      *testing.T
	srv    *httptest.Server
	sent   *capture
	wechat *httptest.Server
}

type reply struct {
	status int
	header http.Header
	body   map[string]any
}

func key(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func newEnv(t *testing.T, grace time.Duration) *env {
	t.Helper()
	dsn := os.Getenv("SERVER_TEST_DB_DSN")
	if dsn == "" {
		t.Skip("SERVER_TEST_DB_DSN not set")
	}
	ctx := context.Background()
	schema := fmt.Sprintf("flows_%08x", rand.Uint32())
	pc, err := accountkit.PoolConfig(dsn, schema)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE"); pool.Close() })
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	// 合成微信提供方：任意 code 即为稳定的 unionid。
	wechat := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"access_token":"synthetic","openid":"open-%s","unionid":"%s"}`, r.URL.Query().Get("code"), r.URL.Query().Get("code"))
	}))
	t.Cleanup(wechat.Close)
	cfg := accountkit.Config{
		Schema:                schema,
		JWTKeys:               map[uint16]*ecdsa.PrivateKey{1: testkeys.JWT(1)},
		JWTActiveKey:          1,
		JWTIssuer:             "flows",
		JWTAudience:           "app",
		SubjectHMACKeys:       map[uint16][]byte{1: key(2)},
		SubjectHMACActiveKey:  1,
		SubjectCipherKeys:     map[uint16][]byte{1: key(3)},
		SubjectCipherActiveKey: 1,
		EnabledIdentityKinds:  []enum.IdentityKind{enum.IdentityEmail, enum.IdentityWeChat},
		RequiredIdentityKinds: []enum.IdentityKind{enum.IdentityEmail},
		WeChatApps:            []accountkit.WeChatApp{{AppID: "flows-wechat", Secret: "synthetic"}},
		WeChatAPIBaseURL:      wechat.URL,
	}
	if grace > 0 {
		cfg.RefreshGrace = grace
	}
	sent := &capture{}
	a, err := accountkit.New(cfg, accountkit.Deps{Pool: pool, Redis: rdb, SMSSender: sent, EmailSender: sent, HTTPClient: wechat.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	r := chi.NewRouter()
	r.Mount("/v1", a.EndUserHandler())
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return &env{t: t, srv: srv, sent: sent, wechat: wechat}
}

func (e *env) call(method, path, bearer, device string, body any) reply {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.srv.URL+"/v1"+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if device != "" {
		req.Header.Set("X-Device-Id", device)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if id, ok := out["code_id"].(string); ok {
		if m, ok := body.(map[string]any); ok {
			if target, ok := m["target"].(string); ok {
				e.sent.remember(target, id)
			}
		}
	}
	return reply{resp.StatusCode, resp.Header, out}
}

func (e *env) credential(target string) map[string]any {
	return map[string]any{"target": target, "code_id": e.sent.codeID(target), "code": e.sent.code(target)}
}

// signIn 走完验证码登录并返回 token pair。
func (e *env) signIn(email, device string) map[string]any {
	e.t.Helper()
	if r := e.call("POST", "/users:sendSignInCode", "", "", map[string]any{"channel": "EMAIL", "target": email}); r.status != 200 {
		e.t.Fatalf("sendSignInCode %d %v", r.status, r.body)
	}
	r := e.call("POST", "/users:signInWithCode", "", device, map[string]any{"kind": "EMAIL", "credential": e.credential(email)})
	if r.status != 200 || r.body["access_token"] == nil {
		e.t.Fatalf("signInWithCode %d %v", r.status, r.body)
	}
	return r.body
}

func str(m map[string]any, k string) string { s, _ := m[k].(string); return s }

func TestHTTPFlows(t *testing.T) {
	t.Run("first_sign_in", func(t *testing.T) {
		e := newEnv(t, 0)
		pair := e.signIn("first@example.test", "device-a")
		if pair["is_new_user"] != true || str(pair, "scope") != "user" || str(pair, "token_type") != "Bearer" || str(pair, "refresh_token") == "" {
			t.Fatalf("pair %v", pair)
		}
		me := e.call("GET", "/users/me", str(pair, "access_token"), "", nil)
		if me.status != 200 || str(me.body, "state") != "ACTIVE" {
			t.Fatalf("me %d %v", me.status, me.body)
		}
	})

	t.Run("pending_completion", func(t *testing.T) {
		e := newEnv(t, 0)
		pending := e.call("POST", "/users:signInWithIdp", "", "device-a", map[string]any{"kind": "WECHAT", "credential": map[string]any{"app_id": "flows-wechat", "code": "union-1"}})
		if pending.status != 200 || pending.body["access_token"] != nil || str(pending.body, "next_action") != "COMPLETE_IDENTITY" {
			t.Fatalf("pending %d %v", pending.status, pending.body)
		}
		ticket := str(pending.body, "sign_in_completion_token")
		if r := e.call("POST", "/users:sendSignInCompletionCode", "", "", map[string]any{"sign_in_completion_token": ticket, "channel": "EMAIL", "target": "pending@example.test"}); r.status != 200 {
			t.Fatalf("completion code %d %v", r.status, r.body)
		}
		done := e.call("POST", "/users:completeSignIn", "", "device-a", map[string]any{"sign_in_completion_token": ticket, "kind": "EMAIL", "credential": e.credential("pending@example.test")})
		if done.status != 200 || str(done.body, "access_token") == "" || str(done.body, "scope") != "user" {
			t.Fatalf("complete %d %v", done.status, done.body)
		}
		// 票据已消费：再次完成必须失败，客户端不能缓存票据重试。
		if r := e.call("POST", "/users:completeSignIn", "", "device-a", map[string]any{"sign_in_completion_token": ticket, "kind": "EMAIL", "credential": e.credential("pending@example.test")}); r.status == 200 {
			t.Fatal("consumed completion ticket accepted again")
		}
	})

	t.Run("refresh_coordination", func(t *testing.T) {
		e := newEnv(t, 0)
		pair := e.signIn("refresh@example.test", "device-a")
		old := str(pair, "refresh_token")
		// 多个请求同时发现 access 过期：所有并发刷新都拿到同一 pair。
		var wg sync.WaitGroup
		results := make([]reply, 4)
		for i := range results {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				results[i] = e.call("POST", "/token", "", "", map[string]any{"grant_type": "refresh_token", "refresh_token": old})
			}(i)
		}
		wg.Wait()
		for i, r := range results {
			if r.status != 200 || str(r.body, "access_token") != str(results[0].body, "access_token") || str(r.body, "refresh_token") != str(results[0].body, "refresh_token") {
				t.Fatalf("concurrent refresh %d: %d %v", i, r.status, r.body)
			}
		}
		if str(results[0].body, "refresh_token") == old {
			t.Fatal("refresh must rotate the refresh token")
		}
		if results[0].header.Get("Cache-Control") != "no-store" {
			t.Fatal("token response must be no-store")
		}
	})

	t.Run("refresh_replay_outside_grace", func(t *testing.T) {
		e := newEnv(t, time.Second)
		pair := e.signIn("replay@example.test", "device-a")
		old := str(pair, "refresh_token")
		fresh := e.call("POST", "/token", "", "", map[string]any{"grant_type": "refresh_token", "refresh_token": old})
		if fresh.status != 200 {
			t.Fatalf("refresh %d %v", fresh.status, fresh.body)
		}
		time.Sleep(1500 * time.Millisecond)
		replay := e.call("POST", "/token", "", "", map[string]any{"grant_type": "refresh_token", "refresh_token": old})
		if replay.status != 400 || str(replay.body, "error") != "invalid_grant" {
			t.Fatalf("replay outside grace %d %v", replay.status, replay.body)
		}
		// 重放视为泄露信号：整个会话被吊销，刚换到的 pair 也失效，客户端必须退出登录。
		me := e.call("GET", "/users/me", str(fresh.body, "access_token"), "", nil)
		if me.status != 401 || me.header.Get("WWW-Authenticate") == "" {
			t.Fatalf("session must be revoked after replay: %d", me.status)
		}
	})

	t.Run("reauthentication_keeps_refresh", func(t *testing.T) {
		e := newEnv(t, 0)
		const email = "reauth@example.test"
		pair := e.signIn(email, "device-a")
		access, refresh := str(pair, "access_token"), str(pair, "refresh_token")
		if r := e.call("POST", "/users/me:sendReauthenticationCode", access, "", map[string]any{"channel": "EMAIL", "target": email}); r.status != 200 {
			t.Fatalf("reauth code %d %v", r.status, r.body)
		}
		re := e.call("POST", "/users/me:reauthenticate", access, "device-a", map[string]any{"kind": "EMAIL", "credential": e.credential(email)})
		if re.status != 200 || str(re.body, "access_token") == "" || re.body["refresh_token"] != nil {
			t.Fatalf("reauthenticate must return a new access only: %d %v", re.status, re.body)
		}
		// 客户端保存新 access、保留原 refresh；原 refresh 仍可刷新。
		if r := e.call("POST", "/token", "", "", map[string]any{"grant_type": "refresh_token", "refresh_token": refresh}); r.status != 200 {
			t.Fatalf("original refresh must still work: %d %v", r.status, r.body)
		}
	})

	t.Run("revoke_others_and_delete_session", func(t *testing.T) {
		e := newEnv(t, 0)
		const email = "sessions@example.test"
		a := e.signIn(email, "device-a")
		b := e.signIn(email, "device-b")
		c := e.signIn(email, "device-c")
		list := e.call("GET", "/users/me/sessions", str(a, "access_token"), "", nil)
		sessions, _ := list.body["sessions"].([]any)
		if list.status != 200 || len(sessions) != 3 {
			t.Fatalf("sessions %d %v", list.status, list.body)
		}
		var otherName string
		for _, s := range sessions {
			m := s.(map[string]any)
			if m["is_current"] != true {
				otherName = str(m, "name")
				break
			}
		}
		// 单个撤销：按 name 末段 s_ ID 调用。
		id := otherName[len("users/")+len(str(a, "user_id"))+len("/sessions/"):]
		if r := e.call("DELETE", "/users/me/sessions/"+id, str(a, "access_token"), "", nil); r.status != 204 {
			t.Fatalf("delete session %d %v", r.status, r.body)
		}
		if r := e.call("POST", "/users/me/sessions:revokeOthers", str(a, "access_token"), "", nil); r.status != 200 {
			t.Fatalf("revokeOthers %d %v", r.status, r.body)
		}
		for _, other := range []map[string]any{b, c} {
			if r := e.call("GET", "/users/me", str(other, "access_token"), "", nil); r.status != 401 {
				t.Fatalf("other device must be signed out: %d", r.status)
			}
		}
		if r := e.call("GET", "/users/me", str(a, "access_token"), "", nil); r.status != 200 {
			t.Fatalf("current device must survive revokeOthers: %d", r.status)
		}
	})

	t.Run("unbind_source_logs_out", func(t *testing.T) {
		e := newEnv(t, 0)
		const first, second = "source@example.test", "second@example.test"
		pair := e.signIn(first, "device-a")
		access := str(pair, "access_token")
		if r := e.call("POST", "/users/me:sendBindCode", access, "", map[string]any{"channel": "EMAIL", "target": second}); r.status != 200 {
			t.Fatalf("bind code %d %v", r.status, r.body)
		}
		if r := e.call("POST", "/users/me/identities", access, "", map[string]any{"kind": "EMAIL", "credential": e.credential(second)}); r.status != 201 {
			t.Fatalf("bind %d %v", r.status, r.body)
		}
		current := e.call("GET", "/users/me/sessions/current", access, "", nil)
		sources, _ := current.body["authentication_identities"].([]any)
		if current.status != 200 || len(sources) != 1 {
			t.Fatalf("current session %d %v", current.status, current.body)
		}
		sourceName := str(sources[0].(map[string]any), "name")
		id := sourceName[len("users/")+len(str(pair, "user_id"))+len("/identities/"):]
		if r := e.call("DELETE", "/users/me/identities/"+id, access, "", nil); r.status != 204 {
			t.Fatalf("unbind %d %v", r.status, r.body)
		}
		// 解绑了当前会话的登录来源：成功即退出，客户端清理凭据并重新登录。
		if r := e.call("GET", "/users/me", access, "", nil); r.status != 401 || r.header.Get("WWW-Authenticate") == "" {
			t.Fatalf("session must be revoked after unbinding its source: %d", r.status)
		}
	})
}
```

- [ ] **Step 3: 运行并修正**

Run: `SERVER_TEST_DB_DSN=... GOWORK=off go test -count=1 ./examples/httpflows/ -run TestHTTPFlows -v 2>&1 | tail -40`
Expected: 全部子测试 PASS。可能需要修正的点：`PoolConfig` 签名、`Config` 字段名（以 `go doc accountkit.Config` 为准）、`WeChatApp` 字段、session/identity `name` 的切片方式（若 `user_id` 不在 pair 中，改用 `GET /users/me` 的 `name` 去掉 `users/` 前缀）。不得为了通过而放宽断言。

- [ ] **Step 4: 加入必需清单**

在 `scripts/required-tests.txt` 中与 `examples/` 其他条目同组插入 `github.com/bbxx111/accountkit/examples/httpflows/TestHTTPFlows`。

- [ ] **Step 5: 提交**

```bash
git add examples/httpflows scripts/required-tests.txt
git commit -m "test(examples): executable HTTP client flow walkthrough

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 10: HTTP 调用流程文档（task 6.2）

**Files:**
- Create: `docs/http-client-flows.md`

- [ ] **Step 1: 写文档**

结构固定如下，每节以 ASCII 时序图开头，再给合成值的请求/响应片段（token 用 `<access>`、`<refresh>` 占位，不写随机值），结尾一句“对应测试：`examples/httpflows` 的 `TestHTTPFlows/<子测试>`”。

```
# HTTP 客户端调用流程

（引言：面向直接调用内置终端用户 HTTP 面的客户端；只讲协议层状态机与时序，凭证存储、
  并发原语和平台 API 由各平台自行决定；路径以宿主挂载前缀为准，示例用 /v1。）

## 客户端状态机
  ASCII 状态图：SIGNED_OUT --signIn/complete--> SIGNED_IN(access, refresh)
                SIGNED_IN --401--> REFRESHING --200--> SIGNED_IN ; --invalid_grant--> SIGNED_OUT
                SIGNED_IN --reauthenticate 200--> SIGNED_IN(new access, same refresh)
  规则：只有 token/pending 响应更新 pair；reauthenticate 只换 access；任何 401 先尝试一次刷新，
  刷新失败才退出；pending 不是登录态。

## 首次登录与 pending 补全
  时序：sendSignInCode -> signInWithCode -> {TokenPair | PendingSignIn}
        PendingSignIn -> sendSignInCompletionCode -> completeSignIn -> TokenPair
  要点：按是否含 access_token 收窄 oneOf；next_action 未知值拒绝；票据一次性、有 expire_time；
        X-Device-Id 稳定。对应测试 first_sign_in、pending_completion。

## 刷新协调与宽限
  时序：N 个并发 401 -> 单一刷新者 POST /token，等待者复用结果；服务端宽限内重复刷新返回同一 pair。
  要点：客户端仍应只发一次（节流、单飞）；宽限只是容错，不是协议承诺的并发语义；
        响应 no-store；收到新 pair 立即替换并丢弃旧 refresh。对应测试 refresh_coordination。

## 宽限外重放与退出
  时序：旧 refresh 在宽限外再次提交 -> 400 invalid_grant -> 会话被吊销 -> 新 access 401。
  要点：这是泄露信号，客户端收到 invalid_grant 必须清理全部凭据并回到 SIGNED_OUT，
        不能用另一份缓存的 refresh 再试。对应测试 refresh_replay_outside_grace。

## 重新认证
  时序：sendReauthenticationCode -> reauthenticate -> AccessToken(无 refresh)
  要点：保存新 access、保留原 refresh 与其绝对期限；REAUTHENTICATION_REQUIRED(400) 触发该流程，
        不是 401。对应测试 reauthentication_keeps_refresh。

## 注销、撤销与解绑导致的退出
  时序：DELETE /users/me/sessions/{session} 204；POST sessions:revokeOthers 200 {}；
        DELETE /users/me/identities/{identity} 204 后当前会话可能已失效。
  要点：其他设备下一次请求 401 即进入刷新失败退出；解绑成功的 204 不能因随后 401 重试成失败；
        /revoke 对 refresh 的注销。对应测试 revoke_others_and_delete_session、unbind_source_logs_out。

## 错误处理约定
  引用 docs/openapi.md「客户端需要区分的响应」：错误体 reason、OAuth 错误体、429 Retry-After。
```

- [ ] **Step 2: 校验引用**

Run: `rg -n 'TestHTTPFlows/' docs/http-client-flows.md | wc -l; rg -n 'docs/openapi.md|openapi.md' docs/http-client-flows.md | head -2`
Expected: 至少 7 处子测试引用；指向 `openapi.md` 的链接存在。

- [ ] **Step 3: 提交**

```bash
git add docs/http-client-flows.md
git commit -m "docs: platform-neutral HTTP client flow walkthrough

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 11: 开发期发布历史压缩（task 7.1）

**Files:**
- Create: `docs/releases/pre-1.0-history.md`
- Delete: `docs/releases/v0.1.0.md` … `v0.7.2.md`（九份）

- [ ] **Step 1: 生成表格数据**

Run: `for t in v0.1.0 v0.2.0 v0.3.0 v0.4.0 v0.5.0 v0.6.0 v0.7.0 v0.7.1 v0.7.2; do printf '%s %s %s\n' "$t" "$(git rev-list -n1 $t)" "$(git log -1 --format=%cs $t^{commit})"; done`

- [ ] **Step 2: 写文件**

```markdown
# v1.0.0 之前的开发期版本

v0.1.0 到 v0.7.2 是 accountkit 的开发期版本。它们没有被任何产品正式使用：仓库自身无正式部署，
唯一接入方只在本地环境联调且未发布产品。v1.0.0 是第一个正式版本，这些开发期 tag 与对应的
GitHub Release 已删除，不再承认、不提供从它们升级的路径，迁移历史检查也不比对它们。
需要追溯时按下表的提交 SHA 从提交历史读取；`openspec/changes/archive/` 保留各次变更的原始记录。

| 版本 | 提交 | 日期 | 摘要 |
|---|---|---|---|
| v0.1.0 | <sha> | <date> | 从原项目抽取的首个独立包：验证码/微信/Apple 登录、JWT 与刷新、管理面、审计、accountsvc |
| v0.2.0 | <sha> | <date> | 宿主账号契约：注销前检查、同库匿名化回调、离线导入；验证码轮次 code_id |
| v0.3.0 | <sha> | <date> | 身份策略与登录补全、流程表改名、通用审计 UUIDv7、Go 集成与 OpenAPI 文档 |
| v0.4.0 | <sha> | <date> | 会话认证来源与客户端描述、数据库运行加固、标准化指标 |
| v0.5.0 | <sha> | <date> | 资料属性 JSON Schema、本人资料 HTTP、内置头像 |
| v0.6.0 | <sha> | <date> | 身份提供方注册表与统一凭证信封、Google/OIDC/微信小程序/Facebook/支付宝/QQ/微博 |
| v0.7.0 | <sha> | <date> | ES256 access 与 JWKS、严格认证与内省、已清理用户拉取、阿里云短信、ACCOUNTSVC_ 前缀、服务 Bearer 认证、邮件模板 |
| v0.7.1 | <sha> | <date> | accountsvc 开发模式明文 HTTP 签发方例外 |
| v0.7.2 | <sha> | <date> | 管理面 :reveal 响应 Cache-Control: no-store |

这些版本期间，初始迁移 0001 曾多次按用户授权直接改写而版本号保持 1。自 v1.0.0 tag 起 0001 字节冻结，
后续结构变更从 0002 递增；见 [迁移手册](../migrations.md)。
```

`git rm docs/releases/v0.*.md`。

- [ ] **Step 3: 校验**

Run: `ls docs/releases/ && rg -l 'releases/v0\.' README.md AGENTS.md docs --glob '!docs/superpowers/**'`
Expected: 只剩 `pre-1.0-history.md`；rg 列出的文件在 Task 12 到 17 中逐一处理（此时允许存在）。

- [ ] **Step 4: 提交**

```bash
git add -A docs/releases
git commit -m "docs(releases): collapse development-era release notes into one history page

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 12: 重写 docs/migrations.md（task 7.2）

**Files:**
- Modify: `docs/migrations.md`（整体重写）

- [ ] **Step 1: 重写**

保留原文第 3-5 行的总述（锁、顺序、密钥检查），其余按以下结构重写；保留或新建的锚点在括号内：

```
# 数据库迁移与恢复
（总述两段，原文保留）

## 当前初始基线（#当前初始基线）
  - 生产目录只有 0001 up/down；新空 schema 安装 version=1/dirty=false；八表清单（含 purged_user）。
  - 0001 自 v1.0.0 tag 起字节冻结，哈希记录在 internal/migrationcheck 的 initialFrozen；后续结构从 0002 递增，
    已发布版本不可修改、删除、重编号或 squash；测试 probe 用 latest+1，放 migrations/testdata/。
  - 数据库不设外键，引用一致性由领域写路径保证（一句，链接 AGENTS/README 对应节）。

## 预检规则（#预检规则）
  - 两条：dirty 优先返回引擎 dirty 错误；版本高于当前源返回 migrations.ErrIncompatibleBaseline。
  - 两种拒绝均在任何写入前发生，保留 catalog、数据、version/dirty，释放连接与锁；不 Force、不改版本、不清库。
  - 预检不反查结构；version=1 等价于当前 0001 由迁移引擎保证。

## 升级前（#升级前）
  原四条改写：核对目标 schema/版本/密钥/Redis 前缀/备份；预发布验证新空库安装 version=1、非空重复 Up、
  latest+1 升级、dirty 与版本超前拒绝、实例隔离；运行发布检查；迁移只追加。

## 诊断失败（#诊断失败）  原文保留，删除版本号叙述。
## 回退选择（#回退选择）  原文保留。
## BREAKING：全量 Down 默认禁用（#breaking全量-down-默认禁用）  原文保留。
## 历史检查与恢复演练（#历史检查与恢复演练）
  - check-migrations.sh：固定当前 initialFrozen，核验 v1.0.0 及以上全部可达正式 tag 的迁移不可改写、不消失、
    版本递增；主版本 0 的 tag 被忽略；浅克隆拒绝。
  - verify-recovery.sh：原文保留对两个独立空库、合成数据、三阶段、八表、会话来源与客户端描述核验的描述，
    删除“旧 v0.3.0 备份”“version 2 备份”等历史句子。
```

- [ ] **Step 2: 校验**

Run: `rg -n 'v0\.[0-9]|本轮|此前授权|直接改 0001|source-baseline|冻结源|b414fef|54afa4b|version=2|version 2' docs/migrations.md`
Expected: 无输出。

- [ ] **Step 3: 提交**

```bash
git add docs/migrations.md
git commit -m "docs(migrations): describe only the v1.0.0 baseline and preflight rules

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 13: 重写 docs/compatibility.md（task 7.3）

**Files:**
- Modify: `docs/compatibility.md`

- [ ] **Step 1: 重写**

新结构（保留锚点在括号内，供 README/accountsvc/openapi 等链接；其余旧锚点在 Task 15 的调用方改链）：

```
# 功能与兼容对照
（引言：本文列出 v1.0.0 的能力对照与长期契约边界；v1.0.0 之前的开发版之间升级说明已随
  docs/releases/pre-1.0-history.md 退役。）

## v1.0.0 调用者须知（#v100-调用者须知）
  - 已移除的六个开发期符号与替换名（表格）。
  - 公开 Go API 自 v1.0.0 起受 semver 约束；破坏性变化升 v2 模块路径。

## 宿主账号契约（#宿主账号契约）  原 362 节保留。
## 离线账号导入（#离线账号导入）  原 370 节保留。
## 通用审计公开接口（#通用审计公开接口）
  原 272 节四个 h3 的正文改写为现状描述（事件生产与写侧（#事件生产与写侧）、配置所有权与读侧、
  HTTP 资源与游标、外部隐私与模式切换），删除“迁移/此前/旧游标”措辞。
## 资料属性补丁语义（#资料属性补丁语义）  原 201 节保留。
## 功能对照（#功能对照）
  原 487 节表格保留；删除含 b414fef/v0.3.0 的行内历史措辞与 509 行“历史提取索引”引言。
```

删除全部 (b) 类章节（3、12、20、28、38、46、54、68、86、107、113、123、133、143、150、156、166、191、217、240、248、258、322、334、338、384、400、418、424、442、461、469、477、879）与三节“历史提取索引”（511、562、604）。其中原 38 节“启动时拒绝 `ACCOUNTKIT_` 变量”与原 86 节的凭证信封表若 README/openapi.md 未覆盖，分别并入 `docs/accountsvc.md`「配置」与 `docs/openapi.md`「统一凭证信封」（Task 15 核对）。

- [ ] **Step 2: 校验**

Run: `rg -n 'v0\.[0-9]|本轮|此前|迁移说明|source-baseline|manifest.json|b414fef|54afa4b|deprecated|Deprecated' docs/compatibility.md; wc -l docs/compatibility.md`
Expected: rg 无输出（“已移除符号”表除外，表内可出现符号名）；行数明显低于 300。

- [ ] **Step 3: 提交**

```bash
git add docs/compatibility.md
git commit -m "docs(compatibility): keep only the v1.0.0 contract map and caller notes

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 14: 重写 docs/release-checklist.md（task 7.4）

**Files:**
- Modify: `docs/release-checklist.md`

- [ ] **Step 1: 重写**

```
# 版本发布与产品上线检查
（module 与远程 URL 两行保留。）

## 当前状态（#当前状态）
  一段：最新正式版本及其发布说明链接（本变更时写“v1.0.0 候选，发布准备另起变更”）。
  开发期版本见 releases/pre-1.0-history.md。

## 发布前门禁（#发布前门禁）  通用、不带版本号的检查项（勾选框留空）：
  - [ ] 本版本功能变更已归档并合入 develop；盘点公开 Go API、HTTP/配置、指标/审计、依赖与 0001 差异。
  - [ ] 发布说明与升级/回退边界完成独立审查。
  - [ ] 准备快照：openspec strict、check-generated、check-migrations、check-openapi、Linux 镜像完整库/服务 race、
        必需门禁、恢复 seed/verify/source-unchanged，结果写入该版本准备变更的 verification.md。
  - [ ] --no-ff 合入 develop，再合入 main 并推送；记录最终候选 SHA。
  - [ ] 最终候选在线 CI 全部通过，head_sha 精确匹配。
  - [ ] 远端不存在同名 tag，也不存在任何低于 v1.0.0 的 tag（人工核对 `git ls-remote --tags origin`）。

## 发布动作与消费确认（#发布动作与消费确认）
  状态措辞约定段（原 251 行）原文保留；tag/Release/两宿主消费/补记三条通用化。

## 历史发布与产品责任（#历史发布与产品责任）
  只保留集成方责任表（原 216-226 行）与“归档不代表产品验收”一句。

## 嵌入式产品上线、## accountsvc 产品上线  原文保留。
```

删除 `当前待办与责任归属`、各版本门禁节（v0.1.0 到 v0.7.2、宿主账号契约新版本门禁）、`历史实现验证`。

- [ ] **Step 2: 校验**

Run: `rg -n 'v0\.[0-9]|\[x\]|actions/runs|releases/tag' docs/release-checklist.md`
Expected: 无输出。

- [ ] **Step 3: 提交**

```bash
git add docs/release-checklist.md
git commit -m "docs(release): reusable checklist without per-version history

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 15: README 与其余文档、OpenAPI 版本表述（task 7.5）

**Files:**
- Modify: `README.md`、`docs/features.md`、`docs/openapi.md`、`docs/go-integration.md`、`docs/gateway-integration.md`、`docs/accountsvc.md`、`docs/development.md`、`docs/metrics.md`、`docs/openapi/enduser.json`、`docs/openapi/admin.json`

- [ ] **Step 1: README**

按行号清单处理（行号为当前文件）：
- 第 9、15 行：删除“本轮/此前授权/撤回 v2/冻结源”句子，只留当前行为描述；15 行的迁移手册链接改为 `docs/migrations.md#当前初始基线`。
- 第 95 行：改为“本文描述 v1.0.0（候选）的公开契约；账号资源沿用 `/users`。”删除版本链。
- 第 99 行末句与 101 行（v0.2.0/v0.1.0 schema 默认值叙述）：改为“默认 schema 为 `account`，可用 `Config.Schema` 或带前缀的 `AUTH_SCHEMA` 设置”。
- 第 105、107 行：删除包名迁移与 enduser 迁移链接，只保留当前入口说明。
- 第 138、142 行：删除“HTTP 新增”“IdentityFlowContext 兼容”句子。
- 第 186、200 行：删除别名句子；200 行 `smtp.New` 的“原”字删除。
- 第 286、299 行：删除“旧成员 400”“旧裸 UnionID”句子（保留当前行为）。
- 第 317 行：改为“当前会话详情与客户端描述的协议见下文；迁移与部署协调见迁移手册”。
- 第 377、378、382、394 行：删除“兼容/旧库/旧任务名/旧游标”措辞。
- 第 474 行“见发布说明”改为链接 `docs/releases/pre-1.0-history.md` 或直接删除。
- 第 498 行：把“先将所有服务进程升级到支持必需 code_id”改为“确保所有进程使用相同密钥集合”。
- 第 517-529 行「仓库与发布状态」改写为：当前版本 v1.0.0（候选），`go get github.com/bbxx111/accountkit@v1.0.0` 待 tag 后可用；module 与远程 URL；发布与上线要求见检查表；开发期版本见 pre-1.0-history；公开 API 自 v1.0.0 起受 semver 约束。删除 519/523/527/529 的历史叙述。
- 所有 `## ...（阶段 Na）` 标题去掉“（阶段 …）”后缀；`docs/features.md` 中 `#身份绑定阶段-4b` 链接同步改为 `#身份绑定`。
- 在「HTTP 面」节加一句链接到 `docs/http-client-flows.md`。
- 保留第 275-283 行以 `{"kind": "` 开头的示例行（至少 3 行）。

- [ ] **Step 2: 其余文档**

- `docs/features.md`：第 1-5 行改为“更新日期：2026-10-30。Go 集成与 HTTP 文档面向 v1.0.0（候选）”；第 42-43、60 行删除 v0.7.x 引用；候选表“完整 HTTP 客户端调用示例”行改为“已提供平台中立版本，见 [HTTP 调用流程](../../http-client-flows.md)；平台特定示例待目标客户端”。
- `docs/openapi.md`：第 3、14 行改为描述 v1.0.0（候选）契约，删除版本链；第 48、72 行的 compatibility 锚点改为本文内节或删除；第 114、116 行历史验收引用删除。
- `docs/go-integration.md`：第 5 行版本链改为“本文对应 v1.0.0（候选）”；第 130、205 行历史引用删除。
- `docs/gateway-integration.md`：第 7、40 行版本引用删除；第 38、62 行 compatibility 锚点改为 README 或 accountsvc 对应节。
- `docs/accountsvc.md`：第 7、11、24 行版本/b414fef 叙述删除；第 57、63、173、319、369、410 行 compatibility 锚点改为本文或 README 对应节；若 Task 13 删除了“启动拒绝 `ACCOUNTKIT_` 变量”的说明，在「配置」节补一句。
- `docs/development.md`、`docs/metrics.md`：删除版本与历史验收引用（development.md 第 23、25 行，metrics.md 第 3、16 行）。
- `docs/openapi/enduser.json`、`admin.json`：`info.version` 改为 `"v1.0.0"`，`info.description` 改为一句现状描述（admin.json 删除“自 v0.5.0 起提供”）。

- [ ] **Step 3: 校验**

Run: `rg -n 'v0\.[0-9]+\.[0-9]+' README.md AGENTS.md docs --glob '!docs/releases/pre-1.0-history.md' --glob '!docs/superpowers/**'; node scripts/check-openapi.cjs && echo OPENAPI_OK; GOWORK=off go test -count=1 ./httpapi/enduser/ -run 'TestCredentialEnvelopeDocumentation|Test.*README|TestSessionOpenAPI' 2>&1 | tail -3`
Expected: rg 只剩 AGENTS.md（Task 17 处理）；`OPENAPI_OK`；httpapi 测试 `ok`（若 README 解析测试名不同，用 `rg -n 'README' httpapi/enduser/*_test.go` 找到并运行）。

- [ ] **Step 4: 提交**

```bash
git add README.md docs
git commit -m "docs: describe the v1.0.0 baseline instead of version increments

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 16: 修复归档与计划中的失效链接（task 7.6）

**Files:**
- Modify: `openspec/changes/archive/**/*.md`、`docs/superpowers/plans/*.md`（只改链接目标）

- [ ] **Step 1: 写链接检查脚本（放 scratchpad，不入库）**

`C:\Users\bbxx1\AppData\Local\Temp\claude\D--code-bbxx111-accountkit\6b023b97-ebab-433b-929a-e59f30e5df8e\scratchpad\checklinks.py`：

```python
import os, re, sys, unicodedata
root = sys.argv[1]
link = re.compile(r'\]\(([^)\s#]+\.md)(#[^)]*)?\)')
head = re.compile(r'^#{1,6}\s+(.*)$')
def slug(h):
    h = h.strip().lower()
    h = re.sub(r'[^\w\- ]', '', h, flags=re.UNICODE)
    return h.replace(' ', '-')
anchors = {}
def heads(p):
    if p in anchors: return anchors[p]
    s = set()
    try:
        for line in open(p, encoding='utf-8'):
            m = head.match(line)
            if m: s.add(slug(m.group(1)))
    except OSError: pass
    anchors[p] = s; return s
bad = 0
for base, _, files in os.walk(root):
    if '.test-output' in base or 'node_modules' in base: continue
    for f in files:
        if not f.endswith('.md'): continue
        p = os.path.join(base, f)
        for n, line in enumerate(open(p, encoding='utf-8'), 1):
            for m in link.finditer(line):
                target = os.path.normpath(os.path.join(base, m.group(1)))
                if not os.path.exists(target):
                    print(f'{p}:{n}: missing file {m.group(1)}'); bad += 1
                elif m.group(2) and m.group(2)[1:] not in heads(target):
                    print(f'{p}:{n}: missing anchor {m.group(1)}{m.group(2)}'); bad += 1
sys.exit(1 if bad else 0)
```

- [ ] **Step 2: 运行并修复**

Run: `python -I <scratchpad>/checklinks.py . `
Expected: 列出失效链接。修复规则：指向 `docs/releases/v0.*.md` 的链接改为 `docs/releases/pre-1.0-history.md`；指向 `compatibility.md#同类身份换绑扩展`、`#会话期限边界加固`、`#服务单监听与传输迁移` 的链接改为 `compatibility.md#功能对照`；`release-checklist.md#v040-发布门禁` 改为 `releases/pre-1.0-history.md`；`release-checklist.md#可选-accountsvc-发布` 改为 `#accountsvc-产品上线`；`migrations.md#会话认证依据初始整合`、`#当前头像初始定义version-1`、`#当前用户资料属性初始整合version-1`、`#登录补全流程命名调整` 改为 `migrations.md#当前初始基线`。只改链接文本，不改句子。重复运行直到退出码 0。

- [ ] **Step 3: 提交**

```bash
git add openspec/changes/archive docs/superpowers/plans
git commit -m "docs: repoint archived links after the documentation rewrite

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 17: 改写 AGENTS.md（task 8.1）

**Files:**
- Modify: `AGENTS.md`

- [ ] **Step 1: 改写**

- 「技术栈与目录」表：删除 `tests/testdata/source-baseline/` 行；新增 `examples/httpflows/` 行（“HTTP 调用流程的可执行对照”）；`tests/testdata/synthetic/` 不单列。
- 「迁移安全」节整体替换为：

```markdown
## 迁移安全

操作步骤以 [docs/migrations.md](docs/migrations.md) 为准。以下规则不能因版本尚处开发阶段而放宽：

- v1.0.0 是第一个正式版本。当前 `0001_init.up.sql` / `0001_init.down.sql` 自 v1.0.0 tag 起字节冻结，哈希固定在 `internal/migrationcheck` 的 `initialFrozen`；后续真实结构变更从 0002 递增，使用 `NNNN_name.up.sql` / `.down.sql`，已发布版本不可修改、删除、重编号或 squash。测试专用下一版本放 `migrations/testdata/`，不混入生产嵌入目录。
- v1.0.0 之前的开发期版本（v0.1.0 到 v0.7.2）未被正式使用，其 tag 与 Release 已删除：不再创建低于 v1.0.0 的 tag，不提供从它们升级的路径，历史检查忽略主版本为 0 的 tag。背景见 [开发期版本](docs/releases/pre-1.0-history.md)。
- 运行时预检只有两条：`schema_migrations` dirty 优先阻断；数据库版本高于当前源时返回 `migrations.ErrIncompatibleBaseline`。两者都在写入前发生并保留 catalog、数据与 version/dirty；禁止自动 Force、清除 dirty 或把失败标记为成功。预检不反查结构。
- `schema_migrations` 记录当前 version/dirty，不是完整历史账本。重复 Up 只忽略无新迁移。
- 保留从 schema 初始化开始的外层锁，以及与旧工具兼容的引擎锁。锁等待必须支持取消和超时，所有错误路径必须释放连接及锁；不能假定底层 WithInstance 初始化失败会自动释放借出的连接。
- `Down` 保留签名但无 I/O 返回 `ErrDestructiveOperation`；`UnsafeReset` 仅用于一次性开发测试数据，必须精确确认目标 schema。生产恢复不调用全量清库入口。
- 历史检查覆盖当前初始定义及全部可达的 v1.0.0 及以上正式 `vX.Y.Z` tag，包括合并分支和 HEAD；需要完整历史和 tags，不能只看第一父链或跳过浅克隆检查。
- 应用回退必须验证新结构兼容旧代码；数据库恢复先落到独立空库核验。备份不包含 Redis 和密钥系统，不能宣称零数据损失或通过清空 Redis 实现安全恢复。
```

- 「项目定位与边界」第二条“首版包含…”改为“v1.0.0 包含…”；「认证行为与兼容性」第 131 行附近删除“公开旧 `ids.IdentityFlow`…保留 deprecated 兼容入口”的句子；「代理操作约束」第 211 行“不修改源基线”改为“不改写已冻结的初始迁移”；「API 设计」第 46 行“旧 Go 入口迁移见兼容指南”改为“直接适配器使用 `httpapi/enduser`”。
- 「开发与验证命令」保持；若 `bash scripts/check-migrations.sh` 注释写“校验历史迁移（需要完整 Git 历史与 tags）”则保留。

- [ ] **Step 2: 校验**

Run: `rg -n 'source-baseline|v0\.[0-9]+\.[0-9]+|直接改 0001|冻结源|IdentityFlow' AGENTS.md`
Expected: 只剩「迁移安全」第二条里对 v0.1.0 到 v0.7.2 的一次提及。

- [ ] **Step 3: 提交**

```bash
git add AGENTS.md
git commit -m "docs(agents): migration-safety rules for the single v1.0.0 baseline

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 18: 完整验证与交付记录（tasks 9.1、9.2、9.3）

**Files:**
- Create: `openspec/changes/consolidate-pre-1-0-history/verification.md`
- Modify: `openspec/changes/consolidate-pre-1-0-history/design.md`（追加 Task 4 的偏离与 Task 8 的附录）、`tasks.md`（勾选）

- [ ] **Step 1: 本地门禁**

Run:
```bash
openspec validate --all --strict
bash scripts/check-generated.sh
bash scripts/check-migrations.sh
node scripts/check-openapi.cjs
gofmt -l .
python -I <scratchpad>/checklinks.py .
```
Expected: 全部退出码 0，`gofmt -l` 无输出。

- [ ] **Step 2: Linux 镜像完整验证**

按 `docs/development.md` 用 `tests/Dockerfile` 构建验证镜像，在容器内提供 `SERVER_TEST_DB_DSN`、`ACCOUNTKIT_RECOVERY_SOURCE_DSN`、`ACCOUNTKIT_RECOVERY_TARGET_DSN`（三个一次性空库），运行：
```bash
bash scripts/verify.sh
bash scripts/verify-accountsvc.sh
bash scripts/verify-recovery.sh
```
Expected: 三个脚本成功；输出留在 `.test-output/`。任何 skip 或失败都如实记录，不得改清单掩盖。

- [ ] **Step 3: 写 verification.md**

结构：
```
# consolidate-pre-1-0-history 验收记录
## 当前状态（日期、分支、HEAD SHA）
## 实际运行的检查（命令、结果、日志路径）
## 测试删除附表（测试名 | 类别 DELETE/SPLIT | 替代覆盖）
   —— 以 Task 3、4、5、6 的删除清单为准逐条列出
## 凭证格式覆盖核对（引用 .test-output/format-coverage.md 的四项结论）
## 公开 API 盘点结论（引用 design.md 附录）
## 未验证项
   —— 远端 v0 tag/Release 删除未执行（需用户单独确认）；v1.0.0 发布准备另起变更；
      六项真实环境验证缺口随版本发布。
```

- [ ] **Step 4: 更新 design.md 并勾选 tasks.md**

在 design.md「Decisions」末尾追加 `### D11 合成种子保留` 记录 Task 4 的偏离；追加 Task 8 的附录。将 tasks.md 中实际完成并验证的任务勾选为 `- [x]`。

- [ ] **Step 5: 提交**

```bash
git add openspec/changes/consolidate-pre-1-0-history
git commit -m "docs(openspec): verification record for consolidate-pre-1-0-history

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```
