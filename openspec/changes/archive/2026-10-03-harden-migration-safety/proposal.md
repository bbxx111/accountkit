## Why

源实现已有迁移版本、dirty 标记和数据库锁，但生产升级、失败恢复与破坏性回滚需要更明确的保障。独立包首次用于生产前，应能验证旧数据保留、失败阻断及备份恢复，避免把全量 Down 当作安全回滚。

## What Changes

- 依赖 extract-auth-server，增加真实 PostgreSQL 的升级、重复执行、并发、失败及恢复验证。
- **BREAKING**：默认拒绝全量 Down；保留原函数签名作为返回明确错误的兼容入口，开发测试清库改用显式危险操作接口。
- 固定已发布迁移脚本，后续结构变更只追加新版本；源 0001 SQL 保持原样。
- 建立生产升级与恢复操作手册，并在隔离测试数据库演练备份恢复。
- P0 范围不包含完整迁移运维 CLI、运行时 checksum、全量历史审计或任意目标版本降级；这些分别作为后续 P1/P2 候选。

## Capabilities

### New Capabilities

- `migration-safety`: 失败阻断、并发串行、破坏性操作防护和生产恢复验收。

### Modified Capabilities

无。本能力补充 extract-auth-server 的 versioned-auth-storage，不重复声明或改写其基础迁移要求。

## Impact

主要影响 migrations 包、数据库集成测试、测试基础设施、发布检查和运维文档。认证 HTTP 协议不变。必须在 extract-auth-server 完成之后实施；两个变更均完成才具备首次生产发布条件。本变更不连接或修改任何产品生产数据库。