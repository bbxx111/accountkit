## Why

kit 已提供标准化观测与 Prometheus 适配器，但 accountsvc 尚未注入 Observer，部署服务无法采集这些指标。为独立服务提供显式启用的采集入口，使现有认证、验证码、依赖、审计及维护观测可直接用于监控。

## What Changes

- 增加默认关闭的 Prometheus 装配配置，启用时要求显式稳定实例标签。
- 每个服务应用创建独立 Registry，向 kit 注入既有 Observer，在现有单监听器提供 GET /metrics。
- 采集仅读取本地聚合值；关闭时不注册路由，migrate 不读取指标配置或创建指标组件。
- 更新部署模板、网关边界与验证；指标入口由部署网络和网关限制访问。
- 本次不增加 OTLP、额外监听器、运行时/HTTP 通用指标或远端审计。

## Capabilities

### New Capabilities
- `accountsvc-metrics`: 服务指标配置、装配、采集端点及访问与验证边界。

### Modified Capabilities
- `metrics-adapters`: 更新 accountsvc 的显式选择范围，默认行为保持。

## Impact

涉及 internal/accountsvc 的配置、运行装配、路由和测试，以及 deploy/accountsvc、服务与指标文档、网关及发布说明、服务必需测试清单。复用现有 client_golang 依赖；无 SQL、迁移、公开库 API 或认证协议变更。
