# 验收记录：add-facebook-sign-in

日期：2026-10-23。被验证的提交为分支 `feat/add-facebook-sign-in` 的 `a6e9558`，与 develop 的合并基线为 `6e8c888`。

## 命令与结果

主机（Windows，Git Bash，无 CGO）在 `GOWORK=off` 下执行：

- `go build ./...`：通过。
- `go vet ./...`：通过。
- `go test -count=1 ./...`，`SERVER_TEST_DB_DSN` 指向一次性 Postgres 17 容器：通过，44 个包 `ok`，8 个包无测试文件，无失败。
- `bash scripts/check-generated.sh`（sqlc 1.31.1）：通过，生成结果与提交一致。
- `bash scripts/check-migrations.sh`：通过，输出 `Migration history is immutable; new versions are strictly increasing`。
- `openspec validate --all --strict`：通过，31 项通过，0 项失败。
- `git diff 6e8c888 --stat -- migrations tests/testdata/source-baseline audit/db`：无输出，没有新增迁移，`0001` 与冻结源字节不变。`user/db/query.sql.go` 因一条新增查询经 sqlc 重新生成而变化，属预期。

Linux 完整验证（镜像 `accountkit-verify:local`，golang:1.26.5 加 postgresql-client-17）：

- `bash scripts/verify.sh`：退出码 0。它依次执行 check-migrations、build、vet、`go test -race -json -count=1 ./...`、必需测试门禁（`scripts/required-tests.txt`）和恢复演练。
- race 测试：44 个包全部 pass；2896 个测试和子测试 pass，0 fail，2 skip。两个 skip 是 `TestRecoveryFixture`（普通套件预期跳过，恢复脚本分阶段强制执行）和 `TestProfileAttributesUnreadableFile`。
- 并发用例 `TestProviderDataDeletionConcurrentWithBind`、`TestProviderDataDeletionConcurrentWithUnbind`、`TestProviderDataDeletionConcurrentWithDeleteMe` 在 `-race` 与真实数据库下全部 pass，对应任务 4.4。
- 必需测试门禁通过。
- 恢复演练通过，使用 pg_dump/pg_restore 17.11，源库与目标库为两个不同的空库，输出 `Recovery drill passed`。

## 验证环境

- 一次性 Postgres 17 容器 `accountkit-facebook-signin-test`，用户 `postgres`，库 `accountkit_test`，密码为一次性测试口令。验证期间在其中新建 `recovery_source` 与 `recovery_target` 两个空库。
- 一次性 Redis 7 容器 `accountkit-facebook-signin-redis`，Docker 网络 `accountkit-facebook-signin-net`，供 accountsvc 的 Redis 测试使用。
- 容器内以只读方式挂载主仓库对象库，在容器内新建克隆并检出 `a6e9558` 后运行 `verify.sh`。这样所有可达 tag 都可用，且测试创建临时仓库时不受外部 `GIT_DIR` 影响。
- 日志与测试 JSON 留在被忽略的 `.test-output/`。

## 过程说明

- 被验证的代码提交是 `a6e9558`。本记录所在的 `deddf87` 只改文档（tasks.md 与 verification.md）。
- 证据文件是 `.test-output/task9-verify.log`，即通过的那次运行。`.test-output/tests.jsonl` 来自失败的第一次运行，不是证据。
- 第一次容器运行把 `GIT_DIR` 设为指向主仓库的工作树 gitdir。`internal/migrationcheck` 的临时仓库用例因继承该变量而失败，原因是验证方式，不是代码。该失败运行不计入上面的结果。它还留下两处副作用：
  - 在分支上产生了无用提交 `592b7c0`（"baseline"）。已用 `git reset --mixed a6e9558` 撤销，该提交现在只存在于 reflog。
  - 改写了共享仓库配置 `D:\code\bbxx111\accountkit\.git\config`：加入 `core.worktree=/src`，并把 `core.filemode` 设为 `true`。本会话的沙箱不允许还原，尚未还原，还原工作交给用户处理，待办。
- 教训：容器内验证应针对容器内的干净克隆，不要让 `GIT_DIR` 指向宿主仓库。改用容器内克隆后重跑，全部通过。

## 未验证

- 未对 Facebook 真实服务端点做在线请求。校验器与回调均使用本地测试替身。
- 未做真实 Facebook 应用的数据删除回调联调，也未做浏览器或客户端 SDK 侧验证。
- CI 在线结果未核对。
- 未归档本变更。

## 终审修复

终审修复提交 `fix: align provider deletion with spec and correct status docs`：

- F1：`docs/go-integration.md` 的 DELETED/SCHEDULED/BLOCKED 三行含义按代码与 spec 重写；README 与其他 docs 无同类描述。
- F2：`DeleteProviderIdentityData` 把 PENDING_DELETION 判断移到“剩余有效身份”判断之后；冷静期账号仍有其他身份时抹除该身份并返回 DELETED，账号状态与 purge_time 不变。新增子测试“pending deletion with other identity”，先在旧代码下失败（返回 SCHEDULED），重排后通过。design.md 判定树同步。
- F3：`TokenResult.HintEmail` 注释不再声称邮箱已验证。F4：design.md Risks 记录 hint 口径不一致的后续事项。F5：`IssueTime` 加 `.UTC()`。

本次运行的命令（`GOWORK=off`，`SERVER_TEST_DB_DSN` 指向一次性库）：

- `gofmt -l .`：无输出。
- `go build -buildvcs=false ./... && go vet ./...`：通过（此环境 git 状态异常，默认 VCS 标记失败，故加 `-buildvcs=false`）。
- `go test -count=1 ./user/... ./user/idp/ .`：全部 ok。
- `openspec validate add-facebook-sign-in --strict`：valid。

## 合入 develop 后复验

- 在功能分支上以合并提交 `906dd0d` 合入 develop（`1ce789a`，微信小程序登录）。冲突出现在 `accountkit.go`、`internal/accountsvc/config.go`、`README.md`、`scripts/required-tests.txt`，均按保留双方解决：`identityProviders` 采用 develop 的带错误返回签名，Facebook 变量名并入 accountsvc 映射与 README 配置行，两组必需测试都保留。
- 宿主（`GOWORK=off`，`SERVER_TEST_DB_DSN` 指向一次性 PostgreSQL 17）：`go build ./...`、`go vet ./...` 通过；`go test -count=1 ./...` 44 个包通过、0 失败；`bash scripts/check-generated.sh` 退出码 0；`openspec validate --all --strict` 32/32 通过。
- Linux（镜像 `accountkit-verify:local`）：容器只挂载只读的 git bundle 并在容器内克隆 `906dd0d`，宿主仓库不进入容器。`bash scripts/verify.sh` 退出码 0：`-race` 下 2932 个测试通过、0 失败、2 个预期跳过（`TestRecoveryFixture`、`TestProfileAttributesUnreadableFile`）；`TestProviderDataDeletionConcurrentWithBind`/`WithUnbind`/`WithDeleteMe` 通过；必需测试门禁与恢复演练通过（使用新建的空库 `recovery_source_merge`、`recovery_target_merge`）。证据：`.test-output/merge-verify.log`。
- 前述共享 `.git/config` 中的 `core.worktree=/src` 已由用户删除；`core.filemode` 是否需要还原待用户确认。

## 支付宝归档后的注册表基线对齐 — 2026-10-24

归档支付宝变更时，将本变更 identity-provider-registry 的两条重叠 MODIFIED 要求与同步后的主规格对齐，保留 ALIPAY 和 FACEBOOK 类型、凭证及既有场景，防止后续归档较旧增量覆盖新基线。仅协调规划工件，没有修改 Facebook 实现、任务完成状态或将本变更归档。其他 Facebook 增量保持原状。
