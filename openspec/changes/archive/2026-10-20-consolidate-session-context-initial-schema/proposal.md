## Why

用户明确要求“把 0002 直接并入 0001”，使新安装仅执行一份完整初始迁移。这是对当前工作树的有限授权；v0.3.0 已发布 tag、其中的历史 SQL 及固定源快照继续保留，不能把此次整合描述成兼容旧库的增量升级。

## What Changes

- 将会话认证标记、认证身份关联、客户端快照及索引直接定义于 0001，删除未发布的会话 0002，初始化 version=1。
- **BREAKING**：拒绝不含会话上下文的旧 v0.3.0 v1，以及已经执行撤回 0002 的开发 v2，保留数据、结构和版本；需保留数据时另行制定迁移方案。
- 为 v0.3.0 的精确提交和旧初始哈希增加独立历史保护，更新当前初始固定校验；不提供跳过检查开关。
- 同步迁移测试、sqlc 生成、集成断言和升级文档；新基线后续真实增量从 0002 开始。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `versioned-auth-storage`：完整会话初始基线、旧版本拒绝边界及三代发布 SQL 的历史保护。

## Impact

涉及 migrations、internal/migrationcheck、生成代码、默认 schema 集成测试、迁移/兼容/发布指南及 AGENTS.md。会话业务、HTTP、JWT、refresh、密文及身份撤销语义保持。此次不提交、推送、合并、发布或归档，不操作现有数据库。

## Successor reconciliation

以上描述本变更在 b414fef 的原始整合范围。随后已实现的 `simplify-session-context` 继续移除来源标记和双快照；本次规格同步按这两个变更的顺序完成。为使尚未归档的重叠 delta 不遗漏后继场景或回写旧定义，本变更的 versioned-auth-storage delta 已重基到后继的最终契约；原始设计、实施证据和提交保留，不将后继行为追记为原始实现。
