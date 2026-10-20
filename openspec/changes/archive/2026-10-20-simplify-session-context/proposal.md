# Proposal

## Why

当前尚未发布的会话上下文通过来源已知标记、INITIAL/LATEST 两类快照和按字段合并表达过多状态。用户已确认简化为必有认证来源与一份最新完整客户端描述，使用有界 JSONB 承载可扩展信息。

## What Changes

- **BREAKING**：移除 `session.authentication_identities_known` 及公开对应字段，认证来源必须非空；缺失为内部不变量错误，不再兼容 unknown 正常态。
- **BREAKING**：`session_client_info` 以 `session_id` 为主键，每会话至多一条 `client_info JSONB NOT NULL`；删除 `snapshot_kind`、INITIAL、公开 `initial_client_info` 和 `enum.ClientInfoSnapshotKind`。
- 成功登录、补全、重新认证与新一轮刷新有有效描述时整体替换，无描述保持；重放不更新。pending 完成本次描述优先，否则沿用加密首次描述及其观察时间。
- Go `ClientInfo` 保留平坦字段，增加制造商、App build 和有界 `Extensions`；JSON 持久化与 HTTP 输出使用嵌套结构，允许 app/browser 并存。
- 业务层统一校验核心字段、JSON 总量与扩展结构；扩展仅通过 Go/宿主回调进入，数据库保留基本结构约束。
- 用户本轮明确批准在当前未发布 0001 中整合；不新增生产 0002。旧 v0.3.0 v1、前一整合 v1 和撤回 v2 均无写拒绝，三代历史校验保持。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `session-authentication-context`：来源必有、缺失拒绝、移除 known 与 unknown 分支。
- `session-client-information`：单份嵌套描述、整体替换、有界扩展及输入输出契约。
- `identity-replacement`：当前会话按必有来源精确判断，缺失拒绝。
- `versioned-auth-storage`：简化的当前初始结构、同版本旧结构拒绝。
- `migration-safety`：限定初始定义授权和三代历史保护。
- `embedded-auth-package`：未发布 Go/HTTP 调整与宿主迁移说明。

## Impact

涉及 migrations、user/query.sql、sqlc 配置与生成代码、客户端解析/领域/DTO、公开枚举、OpenAPI、恢复夹具、迁移检查和长期文档。不增加依赖、生产迁移、JWT 数据库热路径或任意 JSON HTTP 输入。前一 `consolidate-session-context-initial-schema` 工件保留历史，本变更 delta 在其上覆盖。具体实施见 [详细计划](../../../../docs/superpowers/plans/2026-10-20-simplify-session-context.md)。
