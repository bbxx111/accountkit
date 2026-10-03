## Why

accountkit 已能作为 Go 库嵌入宿主，但仓库只有开发宿主示例，缺少可直接部署的正式服务及真实验证码投递装配。提供可选 accountsvc，让使用方可以选择库集成或独立服务，并为独立部署明确消费者鉴权、管理员认证及匿名化边界。

## What Changes

- 增加 `cmd/accountsvc` 和服务内部装配，复用 accountkit 生命周期、领域规则及消费者/管理面；保持单一 Go module，库不依赖服务进程。
- 增加服务配置、PostgreSQL/Redis 连接管理、启动迁移、健康检查、可信代理处理、请求关联、TLS 和优雅关闭。
- 首版提供 SMTP 邮件验证码；短信默认禁用，后续单独增加服务商。显式禁用渠道在生成验证码和扣除额度前拒绝，已启用渠道继续沿用原规则。
- 提供受服务调用凭据保护的消费者令牌远程内省接口，复用现有认证规则；业务服务不需要消费者 JWT 签名密钥。保留并说明吊销查询失败时的 fail-open 语义。
- 提供可选外部 OIDC 提供方 JWT 管理员认证适配，默认关闭；管理角色、身份和审计与消费者分离。
- 增加二进制/容器运行说明、配置示例和真实依赖验收。服务只负责自身认证数据匿名化，跨服务业务数据清理不纳入本轮。

## Capabilities

### New Capabilities

- `accountsvc-runtime`: 可选服务的装配、配置、生命周期、HTTP 边界、健康检查与部署验收。
- `verification-delivery`: 可复用 SMTP 发送器、显式渠道禁用和邮件登录闭环。
- `consumer-token-introspection`: 受保护的消费者令牌远程内省及业务服务接入契约。
- `accountsvc-admin-authentication`: 默认关闭的 OIDC JWT 管理员认证及角色适配。

### Modified Capabilities

无。沿用已归档变更中的 `embedded-auth-package`、`migration-safety` 和 `accountkit-package-identity` 契约；新渠道禁用行为只适用于显式选择该能力的宿主，已有发送器、默认配置和既有认证行为保持兼容。

## Impact

- 预计新增 `cmd/accountsvc/`、`internal/accountsvc/`、`user/sender/smtp/`、显式禁用发送器及相应测试；消费者错误映射和发送前检查增加可选渠道能力，不改变现有发送器接口签名。
- 新增服务专用 `/internal/v1/introspect`、`/healthz`、`/readyz`；消费者 `/v1` 与管理面 `/admin/v1` 复用现有路由。库 handler 仍返回相对路由。
- 服务运行需要 PostgreSQL、Redis 和 SMTP；启用管理面时依赖外部 OIDC 提供方。具体第三方依赖在实施计划中核验并固定版本，不引入另一套账号系统。
- 新增部署文件、服务操作手册和必需集成测试，更新 README、AGENTS 目录说明与发布检查。无生产 SQL 迁移，无冻结基线修改，无现有令牌格式变化。
- 不包含短信服务商、消费者非对称签名、公钥发布、跨服务匿名化、账号找回/换绑、管理前端、完整 OAuth 授权服务器或自动发布部署。
