# 整合初始数据库结构 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将当前最终数据库定义整合为一个初始版本并完整验证明确的基线边界。

**Architecture:** 当前最终结构进入 0001，未来增量从 0002 开始。运行迁移拒绝旧基线，历史门禁分别校验固定历史发布和新初始结构，格式兼容夹具装载到新结构。

**Tech Stack:** Go 1.26.5、PostgreSQL 17、Redis、sqlc 1.31.1、golang-migrate。

**Spec:** [proposal](../../../openspec/changes/archive/2026-10-15-consolidate-initial-schema/proposal.md)、[design](../../../openspec/changes/archive/2026-10-15-consolidate-initial-schema/design.md)、两项 delta specs 与 tasks。

## Global Constraints

- 不改原 source-baseline、历史标签、认证 Go/HTTP 契约、密文格式或无外键规则。
- 本次用户要求授权一次迁移基线整合；没有日常 squash 开关，不操作现有数据库。
- 旧库明确拒绝，dirty 保留，不 Force、清库或改版本；公开 Down 仍无 I/O 拒绝。
- 只修改各任务负责文件；不提交、合并、推送、归档或发布；协调者独占 tasks/verification。

## Review Focus

- 旧版 version=1 与新版同号但缺结构，不能 no-op 成功；旧 version=2 不降版本。
- 后续测试版 2 的正常升级及 dirty 优先级不得被基线检查误伤。
- 历史 tag 被移动或新标签携带旧 SQL 不得获得例外，完整 Git 历史不能省略。
- 列顺序、NULL、默认值、CHECK、部分索引及 collation 必须与整合前最终结构一致。
- 旧密文/令牌格式与恢复结果仍需验证，不能用旧库不再支持为由删除这些断言。

### Task 1: 初始 SQL 与运行迁移

**Files:** migrations/*.sql、migrations/*.go、migrations/testdata/*。
**Interfaces:** 保留 Up/Down/UnsafeReset；新增旧基线拒绝只在原锁范围内进行；新版本为 1。

- [x] 先写生产源仅一对初始 SQL、version=1 和旧基线拒绝测试，在真实 PG17 看见 RED。
- [x] 直接定义 identity_flow 与 audit flow_id，保留列顺序；撤回生产0002并恢复测试0002/0003。
- [x] 加入锁内基线检查，测试原始旧 SQL 建库和旧开发 version=2 均拒绝且数据不变；dirty 和测试增量不误伤。
- [x] 比较基线 511ac6e 的两段 SQL 与新 SQL 的 PG 目录，运行 migrations 全套真实测试与 vet，提供 RED/GREEN 和 diff 供审查。

### Task 2: 双基线历史门禁

**Files:** internal/migrationcheck/*.go。
**Interfaces:** check(root string) error 保持；使用 Task1 最终 SQL 的独立哈希，不读写源快照。

- [x] 在原历史测试基础上补固定历史 tag/commit 和新基线行为 RED；原来的篡改/合并/HEAD/删除场景保留。
- [x] 精确历史发布映射及新版初始哈希分别核验；未知新标签一律要求新基线；保留全历史和递增校验。
- [x] 测试无标签新库、未来增量、移动标签、混入旧基线、合并历史和浅克隆；运行 go test -count=1 ./internal/migrationcheck 与历史脚本，提供结果供审查。

### Task 3: 集成、文档与完整验收

**Files:** compatibility_test.go、recovery_test.go、default_schema_test.go、host_contract_test.go、host_lifecycle_db_test.go、isolation_test.go、internal/accountsvc/*test.go、scripts/required-tests.txt、README.md、AGENTS.md、docs/{compatibility,migrations}.md；sqlc 生成文件仅由工具生成。
**Interfaces:** 历史格式数据通过 seedFormatFixture 装入当前完整结构，保留原密码学和认证断言；新版 schema_migrations=1。

- [x] 原 TestSourceDatabaseTakeover 一对一更名为 TestHistoricalFormatsOnInitialSchema；夹具通过新迁移建立结构再装历史合成数据；更新服务/默认 schema 的版本断言。
- [x] 同步文档的当前结构及旧开发库重建边界，保留旧发布说明/归档记录原事实；同步 required-tests 的更名与新增顶层门禁。
- [x] sqlc generate 和 check-generated.sh；完成独立规格/代码审查；运行 scripts/verify.sh，全部必需测试和 PG17 三阶段恢复实际通过。
- [x] 严格 OpenSpec、链接及差异检查，写入当前 verification.md，任务只按已验证勾选；回收本轮独立资源和临时执行目录。
