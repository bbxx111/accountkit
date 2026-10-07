## Why

accountkit 已覆盖账号、身份、会话与生命周期，默认 schema 使用 account 更符合账号领域。用户确认 v0.1.0 尚未实际使用，要求在开发任务分支直接修改，不增加数据迁移或旧默认值兼容逻辑。

## What Changes

- **BREAKING**：未指定 Schema 时默认从 auth 改为 account，库及 accountsvc 共用此默认值。
- 更新当前配置说明、操作示例、代理指南及开发 Compose 的 schema。
- 继续支持显式自定义 Schema；环境变量名 AUTH_SCHEMA 和 Redis 默认前缀 auth: 保持既有契约。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `embedded-auth-package`: 更新产品隔离要求中的默认 schema，并覆盖默认/显式配置行为。

## Impact

涉及 config.go、配置/根门面/服务测试、当前文档和开发 Compose。冻结 SQL、生成查询、历史工件、已发布 v0.1.0 tag 和认证行为不改；不增加数据库搬迁、自动检测旧 schema 或新运维入口。
