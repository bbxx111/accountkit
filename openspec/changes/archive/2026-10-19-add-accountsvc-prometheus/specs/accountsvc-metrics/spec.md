## Purpose

为 accountsvc 提供可选择启用的 Prometheus 指标采集能力，复用账号库的统一观测契约，明确服务配置、实例隔离、网络访问和生命周期边界，使部署方能够在不改变认证语义的前提下监控服务运行。

## ADDED Requirements

### Requirement: 显式指标配置
serve SHALL 支持 ACCOUNTSVC_METRICS_ENABLED，未设置或空值默认为 false；非法布尔 SHALL 在监听前拒绝。启用时 ACCOUNTSVC_METRICS_INSTANCE SHALL 必填并匹配 `[A-Za-z0-9][A-Za-z0-9._-]{0,63}`，错误仅指出字段名；关闭时 SHALL 忽略实例标签。migrate SHALL 不解析指标设置、不创建指标组件。

#### Scenario: 默认与关闭配置
- **WHEN** 开关省略、为空或 false
- **THEN** 服务保持无指标入口和 no-op 观测，任意实例字段不影响启动

#### Scenario: 无效配置
- **WHEN** 开关非法，或启用时实例标签为空、超长或格式非法
- **THEN** serve 在监听前失败，错误不回显输入值

#### Scenario: 迁移命令独立
- **WHEN** migrate 收到非法指标设置而迁移所需配置合法
- **THEN** 设置被忽略，不注册指标或开启采集入口

### Requirement: 服务观测装配与隔离
启用后服务 SHALL 将 kit 的认证、验证码、依赖、降级、审计和维护指标输出到本应用独立的采集设施，沿用名称、单位、标签和桶契约。SHALL 不采用全局注册、不自动添加 Go/process 或 HTTP 指标。固定实例标签 SHALL 不从请求、schema、Redis 前缀或连接串推导。不同应用对象 SHALL 可独立构建和采集，彼此不污染或注册冲突。

#### Scenario: 真实服务行为产生指标
- **WHEN** 启用指标的实际服务执行认证操作
- **THEN** 采集得到对应操作次数和耗时及配置的实例标签，不出现凭据或用户标识

#### Scenario: 应用隔离
- **WHEN** 同进程创建两个启用指标的服务应用
- **THEN** 各自只导出自身观测，不使用全局默认 collectors

### Requirement: 指标 HTTP 与网络边界
启用时服务 SHALL 在现有单监听器挂载 GET /metrics，沿用 TLS 和请求排空；禁用为 404，启用时不支持的方法为 405。响应 SHALL 禁止缓存；采集错误 SHALL 返回不含内部详情的 503，超出最多五个并发采集请求 SHALL 返回 503。采集 SHALL 仅读取本地聚合值，不查询数据库、Redis、SMTP 或 IdP，也不因 readiness 不通过而被拒绝。入口 SHALL 不要求消费者或管理员凭据；部署方 MUST 限制网关与后端直连来源，只允许监控网络访问，不暴露到公网。

#### Scenario: 依赖故障时采集
- **WHEN** 已运行服务的业务依赖不可用
- **THEN** /metrics 仍可返回现有本地聚合指标，不触发依赖请求

#### Scenario: 路由和错误
- **WHEN** 请求关闭状态的 /metrics、开启状态的非 GET 方法，或采集失败
- **THEN** 分别返回 404、405、固定安全的 503

#### Scenario: 采集过载
- **WHEN** 已有五个未完成采集且另一个请求到达
- **THEN** 新请求得到 503，采集并发不再增加

#### Scenario: 公网隔离
- **WHEN** 公网请求经过符合接入说明的网关访问 /metrics
- **THEN** 请求被拦截且不进入后端，授权监控来源可以抓取

### Requirement: 生命周期与交付说明
服务 SHALL 继续先排空 HTTP 再关闭 kit 和连接；指标设施 SHALL 不增加后台导出任务。文档 SHALL 说明无观测前可没有序列、重启计数重置、多副本抓取标签区分及关闭尾部增量不保证被抓取。部署模板 SHALL 默认关闭；验证 SHALL 包含配置、路由、真实行为到采集、隔离、依赖故障和既有服务回归。

#### Scenario: 停机与回退
- **WHEN** 服务优雅退出或部署方关闭指标开关后重启
- **THEN** 不增加导出刷新等待，原停机顺序保持；关闭开关后入口消失，无数据库迁移
