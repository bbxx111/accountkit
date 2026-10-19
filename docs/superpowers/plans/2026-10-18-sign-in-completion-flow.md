# 登录补全流程重命名 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox syntax for tracking.

**Goal:** 按用户最新要求直接修改 0001 的登录补全流程表定义，保持初始化版本 1。

**Architecture:** 撤回新增 0002，仅调整当前初始 SQL 的表/索引/约束名称及固定哈希；预检拒绝旧表名实例。此前已完成的业务查询和 Go 兼容命名保留。

**Tech Stack:** Go 1.26.5、sqlc 1.31.1、PostgreSQL 17。

**Spec:** [OpenSpec design](../../../openspec/changes/archive/2026-10-18-rename-sign-in-completion-flow/design.md) 及其 specs/versioned-auth-storage/spec.md。

## Global Constraints

- 用户明确授权当前 0001 及其 current initialFrozen 哈希调整；legacy 哈希、源快照、历史 fixture、归档和 tags 保持。
- 不改变认证逻辑、密文 AAD、实例上下文序列化、f_ 或审计 IDENTITY_FLOW/flow_id。
- 不清用户数据库、不 Force、不自动重命名旧库、不提交或归档。
- 保留已有任务分支和暂存工作；生成代码仅通过 sqlc generate。

## Review Focus

- 新空库：只有 0001 pair，version=1，新流程表及全部新索引/约束名。
- 旧同号库：明确拒绝，五表记录/catalog/版本完全保持。
- 已撤回 v2：无论新旧表名都拒绝，dirty 仍优先。
- 历史：只调整 current 两项固定哈希，历史发布及源快照不变。
- 认证与恢复：新基线上完成/重放/注销/匿名化和备份恢复仍正常。

### Task 1: 初始定义修订

**Files:** migrations/0001_init.{up,down}.sql、删除 migrations/0002_rename_sign_in_completion_flow.{up,down}.sql、migrations/baseline.go、migrations/*_test.go、internal/migrationcheck/main.go、当前版本断言测试、scripts/required-tests.txt。

**Interfaces:** 当前表名 sign_in_completion_flow，最新生产版本 1；旧表名 v1 和源中不存在的 v2 返回 ErrIncompatibleBaseline。

- [x] 先更新新初始结构与旧基线拒绝行为测试并取得失败证据，保留完整数据/结构断言。
- [x] 直接修改 0001 表名/关联索引/约束及 down，删除本分支 0002，计算 LF 原始字节 SHA256 更新 current initialFrozen。
- [x] 预检要求新表且无旧表；更新旧十列审计 fixture 拒绝测试、catalog 名称归一化比较、当前版本断言与恢复用例，维护必需测试清单。
- [x] sqlc generate、gofmt；真实数据库 migrations 定向测试通过后独立审查。

### Task 2: 文档与交付

**Files:** README.md、AGENTS.md、docs/migrations.md、docs/compatibility.md、本 change 工件及 verification.md。

- [x] 文档统一为当前初始 version=1、后续增量从0002起，旧同号库拒绝且不自动接管；记录本次有限授权。
- [x] 完整 scripts/verify.sh、生成检查、历史检查、OpenSpec 严格检查。
- [x] 记录审查和本次新证据；旧 v2 方案测试只保留为历史，不视作当前验收；清理本次一次性资源。
