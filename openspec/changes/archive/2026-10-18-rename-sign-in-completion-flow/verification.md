# 登录补全流程重命名验证

日期：2026-10-18。任务分支：refactor/sign-in-completion-flow。

## 当前范围

用户进一步明确要求“不新增 0002，直接改 0001 中的表定义”。本次验收以直接修改初始 SQL、固定校验更新、初始化版本 1 和旧库拒绝为准。

## 历史证据

上一轮新增 0002 方案曾通过 680 个顶层测试、race 和真实备份恢复，材料保留在 .test-output/rename-sign-in-completion-flow/ 及 .test-output/recovery.7YGtWj/。该方案已撤回，这些结果不代表当前修订已验证。

## 本次验证

使用本次独立 PostgreSQL 17、Redis 和 Linux 验证容器，Go 1.26.5、sqlc 1.31.1、PostgreSQL 客户端 17.11，设置 GOWORK=off。原始材料使用独立目录 `.test-output/rename-sign-in-completion-flow-initial/`，不覆盖上一轮报告。

- 实现前更新真实数据库测试，确认旧实现失败：0001 仍创建旧表名，并接受旧名 v1 与已撤回新名 v2。
- 实现后 `go test -count=1 ./migrations ./internal/migrationcheck` 通过；根包相关迁移/历史格式测试通过。本机服务定向测试因环境条件跳过，未计为服务集成通过。
- 独立审查发现一处把 cipher_version=2 误改为数据库版本 1 的测试断言，已恢复 2，生产密文逻辑未改。Linux 实际运行 `TestServiceIntegrationIdentityPolicy/active_flow_key_versions` 与 `TestServiceIntegrationStartupSafety` 均通过，无 skip。
- 最终 Linux `bash scripts/verify.sh` 退出码 0：历史迁移门禁、`go build ./...`、`go vet ./...`、`CGO_ENABLED=1 go test -race -json -count=1 ./...`、必需测试门禁和真实备份恢复全部通过。
- race 结果：681 个顶层测试通过，失败事件 0。普通套件唯一测试 skip 是预期的 TestRecoveryFixture，恢复脚本的 seed、verify、source-unchanged 三阶段均实际执行通过。
- pg_dump/pg_restore 17.11 演练验证当前 version=1、新流程表数据/密文/令牌及源库保持；材料位于 `.test-output/recovery.Q0Z46B/`。
- `bash scripts/check-generated.sh` 通过，生成结果一致；`openspec validate --all --strict` 21 项通过；本轮修改 Go 文件 gofmt 检查及 `git diff HEAD --check` 通过。
- 六份当前文档的 107 个本地链接及锚点全部通过。

独立审查复核上述断言修复，当前无未解决的 Critical / Important / Minor 发现；报告为本次目录中的 `review.md`。当前生产迁移目录仅包含 0001 up/down，源码、历史 fixture 和旧快照区分明确。

## 初始字节与历史保护

- 0001 up SHA-256：`5480fd1b7183b43cfc09792683627c9c41dfd691c3626c1b4ae3905b6b8cffa8`。
- 0001 down SHA-256：`53613c379dacd01f9739ce2824570f833060aca26548ff13b3ae8c42c49e63d6`。
- 两个文件均为 LF，匹配 current initialFrozen。legacy 固定哈希、精确标签提交、源快照、历史 fixture 及归档无修改。
- 全量日志为本次目录的 `verify.log`；最终 race JSON 为 `.test-output/tests.jsonl`。上一轮最终 JSON 已另存为旧目录的 `tests-final.jsonl`，不能混用。

## 交付边界

实施验收阶段未提交、推送或归档，未操作产品数据库；本地提交随后按用户明确的 commit 指令执行，推送与归档不在本次授权内。固定历史源快照与正式标签保持。旧表名同号库及已撤回 v2 不自动接管，拒绝时保留数据与版本。

未单独执行 `scripts/verify-accountsvc.sh` 服务发布门禁及其子进程 race 配置；主验证已实际执行 Linux 服务集成用例。未执行产品升级、真实服务商联调或线上 CI。新旧方案的测试结果均只证明各自本地合成资源验证。

提交包含最终初始定义、固定校验、全部配套改名及验收工件，生产目录仅有 0001 up/down。本次测试容器及网络已按核对的精确 ID 清理，日志和恢复材料保留在忽略目录。

## 归档核验（2026-10-18）

用户明确要求归档已完成 change，本次在 chore/archive-completed-changes 分支完成归档，目录为 openspec/changes/archive/2026-10-18-rename-sign-in-completion-flow。本节补记后续归档动作，前文实施、提交和验证边界保留为当时记录。

归档前将 versioned-auth-storage 的两个 MODIFIED requirements 同步到主规格，包含新流程表初始定义与旧库拒绝边界；保留其他要求和全部既有场景。

本批三个变更共 24 项任务均已完成，归档保留 .openspec.yaml、提案、任务及已有设计、规格和验收记录，修复迁移后的相对链接及外部引用。实际归档检查：

- 使用仓库忽略目录中的 OpenSpec 1.11.0 CLI 执行 validate --specs --strict 与 validate --all --strict，均为 19 项通过、0 失败；list --json 返回空活动变更列表。
- 逐项复核两份增量的 7 个要求均已同步，原有场景和未涉及要求保持。
- 归档后的 13 份非验收工件与 HEAD 原路径内容一致（按 Git 工作树换行转换规范化 CRLF/LF 比较）；检查 19 份 Markdown 的 45 个本地链接，目标均存在，未遗留旧活动目录引用。
- git diff --check 通过。

本次仅同步规格、移动工件及维护引用，未修改运行时代码、SQL 或固定源快照；未重跑 Go、数据库、race 或恢复演练，也不将历史验证记录视为本次运行结果。归档阶段未提交、合并、推送或发布。

## 合并前复核（2026-10-19）

用户明确要求本地合并。本轮重新执行 OpenSpec 全量严格校验（19 项通过、0 失败）、活动变更查询（空）、归档内容与本地链接检查（13 份非验收工件保持、19 份 Markdown 的 45 个链接均存在）及 git diff --check，均通过。develop 与任务分支基点同为 7a829c1，没有额外待整合的代码差异。按仓库约定提交归档改动后使用 git merge --no-ff 合入 develop；本次授权不包含推送或发布。纯文档和规格维护未重跑运行时测试。

## 中文记录恢复（2026-10-19）

后续标准化指标提案检查发现，前两节通过默认 us-ascii 的 PowerShell 管道传给 Node，新增中文已被替换为问号。此处按原操作与验收证据恢复中文，之前的实施记录保持；不代表重新执行了历史运行时测试。
