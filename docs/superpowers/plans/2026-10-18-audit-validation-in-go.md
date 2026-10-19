# 审计校验职责调整 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** 直接调整未发布 0001，将审计业务校验统一到 Go。

**Architecture:** 数据库保留基本结构和并发唯一性，Go 复用 event.Freeze。异步入队、Store 直写及 Go 脱敏写回分别守住自己的入口；SQL 批量清理沿用原事务和锁序。

**Tech Stack:** Go 1.26.5、PostgreSQL 17、pgx、sqlc 1.31.1。

**Spec:** [变更设计](../../../openspec/changes/archive/2026-10-18-audit-validation-in-go/design.md)及其 specs。

## Global Constraints

- 用户明确授权直接改 0001；初始化仍为版本 1，不新增生产迁移。
- 其他表 CHECK、全部索引、历史源快照、manifest 和正式标签保持现状。
- 完整事件 64 KiB、资源各 32 项、既有业务校验和异步失败语义保持。
- 不提交、推送、归档或操作既有数据库；仅清理本任务创建的一次性资源。

## Review Focus

- 数据库接受业务内容非法而形状合法的数据，Go Store 必须拒绝且批次无部分写入。
- 65536 字节合法、65537 字节拒绝，转义和数值规范化仍由 Go 保证。
- Go 清理后重新验证，SQL 清理输出仍可被 Freeze 接受。
- 无搜索路径的限定表名写入可用，多实例清理隔离保持。
- 已有同号数据库重复 Up 不重放 SQL；固定历史检查不放宽。

### Task 1: 初始 SQL 与数据库职责边界

**Files:** migrations/0001_init.up.sql、0001_init.down.sql、generic_audit_test.go、generic_audit_capacity_test.go、reset_test.go；internal/migrationcheck/main.go。

**Interfaces:** 保持全部表列、索引和生成 Go 类型；产出无审计自定义函数且仅保留基本结构 CHECK 的初始结构。

- [x] 在现有 TestGenericAuditDatabaseConstraints 中区分基本结构拒绝与业务内容接受；对非法 actor 内容、context、资源、source/action、超量容量与时间直接 SQL 写入预期成功。先以 PostgreSQL 运行并观察旧 CHECK 导致失败。
- [x] 删除五个函数及 down 清理语句；用 jsonb_typeof 保留 actor/context 对象、targets 数组，data 对象约束不变。保留 UUIDv7；移除来源/动作格式、时间范围及大小 CHECK。其他 SQL 保持原字节。
- [x] 调整现有容量与时间数据库测试为职责边界验证，保留 Go 64 KiB 精确断言；reset 隔离测试检查两实例均无本包审计函数。限定名写入测试保留正常写入验证。
- [x] 仅更新 initialFrozen 的 up/down SHA256，运行 migrations 和 internal/migrationcheck 测试；sqlc 由协调者统一生成。记录失败与通过证据至本任务报告，不提交。

### Task 2: Go 校验、文档和整体核验

**Files:** audit/store.go、audit/store*_test.go、user/service_audit_privacy_test.go（按已有覆盖选择最小补充）、scripts/required-tests.txt、README.md、docs/compatibility.md、docs/migrations.md、AGENTS.md、变更 verification.md。

**Interfaces:** 输入和输出沿用现有公开类型；依赖 Task 1 的数据库定义，不调整查询与锁序。

- [x] 覆盖 Store 直接写入非法 actor/context/资源/时间/超限容量的拒绝和无部分写入；复用现有 Go 校验，避免镜像实现测试。
- [x] 对 insertEvent 和 scrubExistingAuditEvent 的 Go 脱敏结果重新 Freeze，失败返回 ErrInvalid；测试受控投影和批量 SQL 清理得到合法事件，保留隐私竞态回归。
- [x] 文档明确 Go 校验入口、直接 SQL 责任、旧同号库不会移除旧函数；AGENTS 仅追加本次授权，避免改历史记录。新增必需顶层测试同步列表。
- [x] sqlc generate；检查生成差异、历史迁移、构建/vet、真实 PostgreSQL 全套 race 与恢复演练（环境可用时）、openspec validate --all --strict；逐项记录实际证据，独立审查后更新 tasks。
