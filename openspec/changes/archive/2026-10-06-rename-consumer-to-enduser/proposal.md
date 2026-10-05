## Why

现有 `httpapi/consumer` 提供的是终端用户自己的登录、资料、身份、会话和账号生命周期接口，consumer 容易与 API 调用方或消费服务混淆。统一为 enduser 能与独立的 admin HTTP 面清楚对应，同时避免与领域包 user 重名。

## What Changes

- **BREAKING**：将 `httpapi/consumer` 迁移为 `httpapi/enduser`，Go package 改为 enduser，直接导入方需更新路径和包限定符；不保留旧包或兼容别名。
- **BREAKING**：将 `Auth.ConsumerHandler()` 改为 `Auth.EndUserHandler()`，不保留旧方法或转发包装。
- 迁移该包的 Handler、Deps、Service 和可选 IdentityReplacer，保持类型内容、方法签名及行为；同步本项目装配、示例、测试和与该 HTTP 面对应的内部命名。
- 更新当前接入文档、AGENTS 指南与必需测试清单，提供明确 Go 调用迁移表；历史归档和已完成实施计划保留原事实。
- HTTP 相对路由、宿主 `/v1` 挂载、管理员面、`/v1/introspect`、请求响应、凭据和存储契约全部保持。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `accountkit-package-identity`: 增加终端用户 HTTP 包与公开门面的统一命名、直接迁移及文档/门禁要求，补充运行行为保持范围。

## Impact

- 主要涉及原 `httpapi/consumer` 下19个 Go 文件、`accountkit.go`、accountsvc 装配、嵌入式示例、根包及服务测试；必需清单中目前37项原 HTTP 包测试需一对一迁移路径。
- 直接调用旧导入路径或旧门面方法的宿主必须调整后重新编译；仅调用 HTTP 的客户端不需要改 URL、字段或凭据。
- 不新增依赖、配置、端点、数据迁移或兼容层；module、根 package accountkit、领域包 user 保持不变。

## Non-goals

- 不重构领域服务或拆分账号功能，不合并消费者/管理员身份系统，不重命名协议枚举、scope、JWT claims、配置键或数据库对象。
- 不改变单监听器、TLS 开关、内省认证、网关职责、验证码/会话规则和维护调度。
- 不全仓机械替换所有 consumer 字样，不重写历史工件、固定源清单或已归档规格名称；不新增产品环境验证。
