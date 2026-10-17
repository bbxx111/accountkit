## Why

审计事件的内容规则目前在 Go 和五个 PostgreSQL 函数中重复维护，其中容量计算还耦合 Go 的 JSON 编码细节。将完整校验集中到 Go，数据库仅保留基础结构和唯一性约束，可减少规则漂移。

## What Changes

- 移除五个 audit 校验函数及其业务 CHECK；保留 UUIDv7、非空、JSON 基本形状、列类型、默认值、主键和全部索引。
- source/action 内容格式、事件时间范围、主体与资源规则、上下文和 64 KiB 边界统一由现有 Go 校验执行，核对并覆盖脱敏后的写入。
- **BREAKING**：直接 SQL 写入不再获得上述业务校验；导入和修数须复用 Go 校验。公开 Go/HTTP 合法输入和错误语义保持。
- 用户明确要求直接修改当前未发布的 0001，初始化仍为版本 1，同步当前固定哈希；不新增迁移，不修改固定历史源快照和正式标签。
- 其他四张表的全部 CHECK、认证行为和锁序保持现状。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `generic-audit-events`：明确业务校验归属 Go，数据库仅承担基础结构约束。
- `migration-safety`：记录用户授权直接调整当前未发布初始审计校验定义的限定例外。

## Impact

涉及 migrations/0001_init.up.sql、down SQL、当前初始哈希、审计写入与测试、sqlc 生成核对、兼容与迁移文档。已有同号库重复 Up 不移除旧约束或函数，不自动重建。无新依赖、无新增外部服务。
