# 用户资料属性并入初始结构 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development。测试先行、独立审查；不自动提交、推送、发布、归档或操作已有数据库。

**Goal:** 按用户本次明确授权，把当前未发布 0002 的单列 profile_attributes 定义并入工作树 0001，撤回生产 0002；无需旧数据迁移。

**Architecture:** 新空 schema 由 0001 创建完整七表及资料列，version=1。迁移预检要求完整当前结构；不转换旧数据库、不重放旧同号版本、不 Force/改版本/清库。当前初始哈希与已发布 v0.4.0 tag 的旧初始定义分别固定，历史 tag 和冻结源不改。

**Tech Stack:** Go 1.26.5、PostgreSQL 17、sqlc 1.31.1、既有 golang-migrate 与历史检查工具。

**Spec:** openspec/changes/archive/2026-10-21-add-schema-validated-user-extensions/ 当前 proposal/design/specs/tasks；本计划覆盖本轮数据库定义整合，之前行为和验收仅为历史。

## Global Constraints

- 只将 `profile_attributes JSONB NOT NULL DEFAULT '{}'::jsonb` 加入 user_account 的 0001 CREATE TABLE；撤回生产 0002 up/down；0001 down 仍删除整张 user_account，无单独遗留对象。
- 用户明确授权修改当前 0001；只更新当前 initialFrozen 哈希，不更新冻结源、v0.1/v0.2/v0.3/v0.4 tag、publishedInitialReleases 中的精确 commit/hash 或历史 SQL；不能新增跳过检查开关。
- 0001 其他列/索引/约束原样。新安装完整 version1，重复 Up 无变化。当前合并结构与原 0001+0002 catalog/数据默认语义等价。
- 预检在 source 可达版本上要求完整当前资料结构，不再以 version1 表示缺资料的发布旧结构。旧 v0.4.0 v1、旧会话/流程/审计 v1及撤回各代 v2均无写拒绝；dirty优先、锁/取消/资源释放保护保持，不实现迁移转换。
- 后续增量仍从2开始，测试成功/失败 probe 使用 latest+1，testdata 脚本保持仅测试用途；不能机械地将所有版本2断言改1（历史拒绝/测试未来版本2仍保留）。
- 不改资料公共API、JSON Schema、账号行锁、账号时间、认证/生命周期/审计隐私行为；sqlc仅生成。
- 更新 AGENTS.md 和活跃规格/迁移手册中的本次有限授权；归档/发布说明历史原文保持。验收不得将旧增量升级结果当新整合结果。
- 使用独立任务标签的测试资源，不触碰已有数据库；本轮没有旧数据迁移实现/承诺。

## Review Focus

1. 新建v1包含资料列且重复迁移无变化；旧v0.4同号v1不被接受为新结构。
2. 当前initialFrozen可变更不代表放宽已发布tag校验；删除/移动v0.4tag或改其旧SQL仍失败。
3. 撤回v2无源时拒绝，未来测试probe v2有源且完整新基线时仍正常；dirty与历史拒绝无写。
4. 只新增资料一列，当前schema catalog与原两步结构等价，生成结果不得漂移业务行为。
5. 全库/可选服务/恢复夹具中的迁移版本预期一致；结构/ID/时间/认证隐私测试保持。

## Task 1: 更新授权、规格和文档

**Files:** AGENTS.md、当前change工件、README、docs/migrations.md、docs/compatibility.md；旧计划仅补历史替代提示。

- [x] 明确本轮当前初始定义整合授权，旧发布字节与tag仍独立固定，当前新安装v1且无生产0002。
- [x] 取消旧v0.4→v2自动升级承诺，写出不提供旧数据迁移、旧结构无写拒绝；新增C1–C4任务和本轮待验收区。

## Task 2: 实施与行为验证

**Files:** migrations/0001_init.up.sql、0001_init.down.sql（必要时）、撤回0002、baseline.go/profile_attributes_baseline.go及相关测试；internal/migrationcheck/main.go及测试；其他实际依赖版本断言；生成user/db与audit/db。

- [x] RED：用测试专用fixture构造包含资料列的version1，重复Up应成功且无变化，旧实现应因version1资料残留拒绝；不把fixture构造当生产迁移。
- [x] 将单列定义合入0001，撤回0002，更新当前哈希与必要预检，保留已发布各代精确pin。
- [x] 真实DB验证新安装/重复/并发/多实例，合并与原两步catalog等价、旧v0.4 v1与撤回v2无写拒绝、dirty/取消/失败锁释放。
- [x] 将旧升级测试改为当前初始定义测试，保留必需顶层入口及未来probe的升级/回滚隔离；只修真正依赖生产最新版本的断言。
- [x] sqlc generate、相关全量migrations/history测试、vet及生成/历史检查通过；独立审查。

## Task 3: 综合验收

- [x] GOWORK=off build/vet、全量普通测试、Linux race及必需门禁。
- [x] 两独立空库执行真实恢复seed/verify/source-unchanged，确认版本1、资料JSON、认证及匿名化/源不变。
- [x] 严格OpenSpec、格式/差异、当前hash及历史tag固定检查、独立最终审查通过；记录实际证据。
- [x] 核对任务标签后仅清理本轮资源，保留日志/备份；不自动提交或归档。
