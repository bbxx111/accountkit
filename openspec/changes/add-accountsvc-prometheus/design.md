## Context

动机见 [proposal](proposal.md)。现有服务已具备单监听器、TLS、请求排空和 kit 生命周期；Prometheus 适配器及 client_golang 已存在。用户选定先接 Prometheus，默认关闭，复用监听器并由网关限制指标访问。

## Goals / Non-Goals

以最小服务装配复用 kit 的全部现有指标契约。无需新库公共接口、数据库迁移、全局注册或后台导出任务。不实现 OTLP、独立指标端口、指标身份系统、运行时 collectors 和仪表盘。

## Decisions

1. 配置采用 `ACCOUNTSVC_METRICS_ENABLED`（默认 false，非法布尔拒绝）和 `ACCOUNTSVC_METRICS_INSTANCE`（启用时必填，`[A-Za-z0-9][A-Za-z0-9._-]{0,63}`）。不从 schema/连接串推导标签；关闭时忽略实例配置。migrate 在服务配置解析前返回，继续忽略指标设置。
2. 新增服务内部 `metrics.go`，创建独立 Registry 和既有 Prometheus Observer。每次 newApplication 独立创建，注入 deps.Observer，并把 handler 保存在 application。禁用时两者为 nil。Registry 无关闭任务；沿用现有 HTTP 排空及 auth.Close，不增加全局资源或 exporter 生命周期。
3. GET /metrics 只在启用时挂载到现有监听器，未启用为 404，其他方法为 405。使用 promhttp.HandlerFor 而非全局 Handler，不附带 Go/process collectors，不自计 HTTP 抓取。响应 no-store，采集错误固定安全 503，不把原始 collector 错误写入响应。最多 5 个并发采集，超限 503；沿用服务有限 HTTP 超时。
4. 指标不需要消费者或管理员凭据，依靠显式启用、网关允许清单和后端来源限制；沿用监听器 TLS。没有新建独立监听器，因为这会扩大既有部署模型。文档及代理 fixture 同时明确公网不能抓取 /metrics。
5. 重用既有标准指标、分类、实例标签和耗时桶；新进程计数重置，无观测前允许空输出。同一逻辑实例多副本可共用 accountkit_instance，由 Prometheus 抓取目标标签区分；不同逻辑实例必须不同。退出排空后可能无法再抓取最终增量，不承诺最后一次采集包含全部关闭事件。

## Risks / Trade-offs

- 单监听器入口误开放 → 默认禁用，并更新网关说明、部署清单及隔离测试；启用前配置网络策略。
- 监控依赖被误纳入认证关键路径 → 仅本地聚合，不增加存储访问；测试业务依赖不可用时仍能抓取。
- 实例标签错误合并 → 显式校验与部署说明；不使用请求数据、秘密或自动推导值。
- 进程退出或抓取间隔丢失观测 → 明确计数仅为运行观测，Prometheus 无最终主动导出保证。

## Migration Plan

原配置保持关闭。先配置网关和抓取目标，再设置 ENABLED=true 与 INSTANCE 并重启。回退只需关闭开关或回退二进制并移除抓取目标，无数据库操作。Compose 仅透传可选变量，默认关闭。
