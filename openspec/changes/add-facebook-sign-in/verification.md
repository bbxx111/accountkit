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

- 第一次容器运行把 `GIT_DIR` 设为工作树指向的路径。`internal/migrationcheck` 的临时仓库用例因继承该变量而失败，原因是验证方式，不是代码。改用容器内克隆后重跑，全部通过。该失败运行不计入上面的结果。

## 未验证

- 未对 Facebook 真实服务端点做在线请求。校验器与回调均使用本地测试替身。
- 未做真实 Facebook 应用的数据删除回调联调，也未做浏览器或客户端 SDK 侧验证。
- CI 在线结果未核对。
- 未归档本变更。
