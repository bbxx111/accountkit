## Why

accountkit 当前依靠分散的日志与局部审计 Stats 观察运行状态，尚不能以统一口径区分登录待补全、刷新宽限返回、依赖降级及维护部分失败。宿主需要可直接用于监控的指标，并能在 OpenTelemetry 与原生 Prometheus 之间选择，而不改变账号领域代码或认证行为。

## What Changes

- 新增可选、类型化的 Observer 注入，覆盖认证、验证码、依赖调用、降级、审计和维护六类观测事件；未配置时 no-op。
- 明确每种指标的单位、触发时机、有限标签、默认耗时桶和实例隔离方式，区分业务结果与依赖结果，不通过异步审计推算认证调用量。
- 提供 OTel 与原生 Prometheus 两个可选适配包，由宿主在启动装配时选择；Provider、Registry、Exporter、指标端点及关闭流程归宿主管理。
- 保留现有构造入口、审计 Stats、认证协议、Redis 故障策略及维护语义；新增观测不增加业务存储查询，不参与事务提交或业务错误返回。
- 提供两个可运行的合成观测宿主示例、指标契约文档及适配器一致性验证，明确指标属于尽力运行观测，不是精确业务账本。

## Capabilities

### New Capabilities

- `standardized-metrics`: 六类观测的公共契约、统计边界、隐私、基数约束、故障隔离与既有业务兼容。
- `metrics-adapters`: OTel／Prometheus 的宿主选择、统一指标映射、多实例装配、生命周期和可运行示例。

### Modified Capabilities

无。现有认证、验证码、审计、维护及宿主生命周期要求保持；本次新增的可观测行为由上述两个能力描述。

## Impact

- 预期涉及 `accountkit.Deps`、`user.Deps`、`observability/` 及两个适配包，认证与验证码领域路径、审计组件、维护运行器和相关依赖调用边界。
- 单一 Go module 保持；核心包不导入 OTel 或 Prometheus 实现，适配器及示例所需依赖仍列入根 `go.mod`，不承诺模块级依赖隔离。
- 同步 README、功能清单、Go 集成文档、兼容说明及新增指标文档；测试记录保存在本变更 `verification.md`。
- 无 SQL、迁移、生成查询、HTTP DTO、令牌或密文格式变更。

## Non-Goals

- 本次不添加 accountsvc 指标端点或服务导出配置，不添加全量 HTTP／逐条 SQL／连接池指标、Tracing、监控面板或告警部署。
- 不提供运行时热切换、全局默认注册、自动安装监控设施、额外持久化队列或跨进程精确计费统计。
- 不改变宿主自定义审计组件的责任，不把调用外部 Recorder 等同于外部落盘成功。
