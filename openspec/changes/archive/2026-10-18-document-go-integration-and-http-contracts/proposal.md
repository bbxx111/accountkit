## Why

直接嵌入 Go 库的宿主需要集中了解装配、返回分支、调用层授权与事务责任，现有说明分散在 README 和源码。用户确认先完善 Go 集成文档与示例，再整理现有 HTTP API 的 OpenAPI 契约。

## What Changes

- 新增 Go 集成指南与可编译调用片段，明确合成示例和真实依赖验证的区别。
- 根据既有 handler、DTO、错误映射分别维护消费者与管理面 OpenAPI 文档，保留宿主挂载前缀和独立认证边界。
- 增加接入入口、契约维护说明和本次验证记录，更新功能清单。

## Capabilities

### New Capabilities

无运行时能力新增。此变更只记录既有契约，按文档维护设置 `skip_specs: true`，不人为增加行为规格。

### Modified Capabilities

无。Go/HTTP 接口、SQL、配置和认证行为保持既有定义。

## Impact

涉及 README、docs、examples/gointegration 及本变更工件。独立校验器仅安装在忽略目录；不增加 Go 运行时依赖、不发布、不归档、不操作产品资源。文档维护直接实施，不启动功能开发的 apply 流程。OpenAPI 不包含 accountsvc 专属内省/探针，也不生成客户端 SDK。
