# v1.0.0 发布 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans 顺序执行发布步骤，并使用 superpowers:requesting-code-review 独立审查发布说明。执行顺序由既有发布检查表及本次发布授权确定。

**Goal:** 发布 accountkit 的第一个正式 Go module 版本 v1.0.0。

**Architecture:** 本次只维护发布工件和状态，不修改运行时。候选、main CI、annotated tag 与远端消费逐级固定证据，发布后补记不移动 tag。

**Tech Stack:** Go 1.26.5、sqlc 1.31.1、PostgreSQL 17、Redis 7、Docker、GitHub Actions、gh CLI。

**Spec:** [发布设计](../../../openspec/changes/prepare-v1-0-0-release/design.md)与 [发布检查表](../../release-checklist.md)。任务进度以 OpenSpec tasks 为准。

## Global Constraints

- 不修改生产代码、冻结 0001、依赖、公开契约；纯发布不套用 TDD。
- 任务→develop→main 均保留 merge commit。用户已确认 v1.0.0 发布与消费完成后删除 v0，精确限定九个版本。
- 验证/恢复仅用本轮新建测试资源；日志和备份放 `.test-output/v1-release/` 或恢复脚本输出目录。
- v1.0.0 tag 固定最终 CI 成功的候选；不移动、删除或重建。

## Review Focus

- 第一个正式版本不是已有正式版本的增量；发布说明完整列出能力与运行边界。
- 不把 API 调用替换说明描述成开发期数据库升级路径。
- 旧 tag 删除状态、在线 CI 与远端消费均以实际证据为准。
- 内省、服务 Bearer、管理面和消费者身份边界不能混淆。
- 无真实提供方/网关验收不能写为产品上线通过。

### Task 1: 发布文档与开发期盘点（OpenSpec 1.1）

**Files:** 创建 docs/releases/v1.0.0.md；更新 docs/release-checklist.md、README.md；删除完成后更新 AGENTS.md、docs/releases/pre-1.0-history.md。核对 docs/compatibility.md 的全量替换表，引用现有长期文档避免复制协议。独立 reviewer 检查本计划 Review Focus；用远端标签/Release 列表确认清理范围，运行文档链接/差异检查。提交为 `docs(release): prepare v1.0.0`。

### Task 2: 准备快照完整验证（OpenSpec 1.2）

使用 tests/Dockerfile、新网络、PostgreSQL/Redis 容器和三个空库；运行 openspec validate --all --strict、check-generated.sh、check-openapi.cjs、verify.sh、verify-accountsvc.sh，补非 root 权限测试。统计 981/62 必需门禁和恢复三阶段，验证无依赖缺失 skip，写入 verification.md。只清理本轮容器和卷。

### Task 3: 候选集成、CI、tag/Release（OpenSpec 2.1、2.2）

先提交准备证据，再 --no-ff 合入 develop/main 并推送。通过 gh run list/view 核对最终 main head_sha、job/step 全部 success；失败先处理。检查 v1.0.0 不存在，按用户明确顺序暂时保留旧 v0 标签，创建 annotated tag 并推送，核对远端解析 SHA，再用 --notes-file 发布 GitHub Release。

### Task 4: 远端消费与状态补记（OpenSpec 2.3、3.1、3.2）

在 ignored 目录创建两个独立模块，GOWORK=off、无 replace、go get 远端 v1.0.0，分别编译根门面和直接 enduser.New，并用 go list -m -json/SQL 哈希核对。消费确认后保存旧标签/Release 元信息并精确删除九个 v0 标签和现存八个 Release，核对剩余列表。将 README、功能/兼容/Go 集成/OpenAPI/发布说明/检查表的当前状态改为已发布，保留真实环境未验证事实；补记 tasks/verification，合入 develop/main 并推送。重新核对 tag 对象未变，发布准备变更不自动归档。
