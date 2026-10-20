# Proposal

## Why

数据库评审在 PostgreSQL 17 上复现了三个问题：宿主默认使用 Repeatable Read 时，认证事务锁后仍读取旧身份快照；单连接池被维护锁占满；密集资源的审计分页物化全部完整历史记录。用户已逐项确认修复方案，需要将这些运行契约固定为可回归验证的行为。

## What Changes

- 库内部依赖锁后复核的领域事务显式使用 READ COMMITTED，保留公开 Repo 的宿主隔离级别契约和现有锁序。
- **BREAKING**：`New` 无 I/O 拒绝 `Deps.Pool.Config().MaxConns < 2`，防止维护任务占用唯一连接；不新增独立连接或修改 pgx 默认容量。
- 审计查询按各匹配分支取有界的时间/ID 候选，合并去重后仅读取最终页完整事件；历史流程匹配在数据库内完成，Go 仅接收本页所需的去重归属证据。
- 保留账号/来源/命名空间隔离、动作过滤、历史流程归属、游标、异步写入和匿名化行为；不修改数据库结构或迁移。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `embedded-auth-package`：固定内部领域事务的隔离语义和初始化连接池容量要求。
- `generic-audit-events`：固定本地审计分页的候选及完整记录读取边界，保留归属授权和排序语义。

## Impact

涉及 `user` 事务入口及并发测试、`accountkit.New` 及初始化测试、`audit/query.sql` 与 sqlc 生成结果、`audit.Store` 及分页/性能测试。同步 README、兼容说明、必需测试清单和验收记录。不引入依赖，不改 0001、冻结源快照、公开 Repo 方法签名或 HTTP 协议。
