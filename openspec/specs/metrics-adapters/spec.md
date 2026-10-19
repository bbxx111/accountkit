# metrics-adapters Specification

## Purpose

让宿主在启动装配时以相同观测契约选择 OpenTelemetry 或原生 Prometheus，通过显式依赖与统一指标描述保持业务统计语义一致，同时把实例识别、导出设施和生命周期责任留给宿主。

## Requirements

### Requirement: 两种适配器与显式装配
项目 SHALL 提供 OTel 和原生 Prometheus 两种适配器，实现相同的类型化观测契约；核心领域包 SHALL 不依赖两种具体实现。适配器 SHALL 显式接收宿主 Provider／Meter 或 Registerer，不隐式采用全局对象。缺失或包装 nil 的必需依赖 SHALL 在装配时明确失败；未启用观测由核心 no-op 表达。项目 SHALL 保持单一 Go module，说明适配器依赖仍存在于根 go.mod。

#### Scenario: 切换实现
- **WHEN** 宿主在下一次启动把 OTel 适配器换为原生 Prometheus
- **THEN** 无需修改领域调用或认证配置，指标语义保持，时间序列可因新进程或新实例重新开始

#### Scenario: 显式缺失依赖
- **WHEN** 宿主选择某适配器但未提供其有效依赖
- **THEN** 构造返回配置错误，不回退全局 Provider 或 Registry，不产生网络 I/O

### Requirement: 公共指标契约
系统 SHALL 提供下表所列指标并保持名称映射、单位及允许标签。所有指标 SHALL 附带固定 accountkit_instance 标签；表中仅列额外标签。计数器 SHALL 累计真实观测数量，耗时直方图 SHALL 不复制计数器的细分 reason 标签。未出现的标签组合不要求预创建；零与未产生序列的区别 SHALL 在文档说明。

| OTel 名称 | Prometheus 名称 | 类型／单位 | 额外标签 |
|---|---|---|---|
| accountkit.auth.operations | accountkit_auth_operations_total | Counter / {operation} | operation, method, result, grant, mode, reason |
| accountkit.auth.operation.duration | accountkit_auth_operation_duration_seconds | Histogram / s | operation, method, result |
| accountkit.code.operations | accountkit_code_operations_total | Counter / {operation} | operation, channel, purpose, result, reason |
| accountkit.code.operation.duration | accountkit_code_operation_duration_seconds | Histogram / s | operation, channel, purpose, result |
| accountkit.dependency.operations | accountkit_dependency_operations_total | Counter / {operation} | component, operation, result |
| accountkit.dependency.operation.duration | accountkit_dependency_operation_duration_seconds | Histogram / s | component, operation, result |
| accountkit.degradations | accountkit_degradations_total | Counter / {event} | scenario |
| accountkit.audit.events | accountkit_audit_events_total | Counter / {event} | stage, result |
| accountkit.audit.omitted.fields | accountkit_audit_omitted_fields_total | Counter / {field} | field |
| accountkit.maintenance.rounds | accountkit_maintenance_rounds_total | Counter / {round} | result |
| accountkit.maintenance.runs | accountkit_maintenance_runs_total | Counter / {run} | task, result |
| accountkit.maintenance.run.duration | accountkit_maintenance_run_duration_seconds | Histogram / s | task, result |
| accountkit.maintenance.items | accountkit_maintenance_items_total | Counter / {item} | task |

#### Scenario: 指标描述一致
- **WHEN** 宿主分别使用两个默认适配器采集覆盖全部事件类别的合成输入
- **THEN** 指标名称、单位与标签符合本表，平台特有格式转换按明确映射解释，不增加包含请求数据的动态标签

### Requirement: 统一描述与适配器一致性
两个适配器 SHALL 使用共同的指标描述和分类投影，固定单位、允许标签、累计计数含义及默认显式直方图桶。默认桶 SHALL 为 0.001、0.005、0.01、0.025、0.05、0.1、0.25、0.5、1、2.5、5、10、30、60 秒，超范围值仍被统计。平台特有名称和直方图表达形式 SHALL 有明确映射；在相同默认聚合条件下，归一化后的次数、标签、耗时数量／总和及桶分布 SHALL 一致。

#### Scenario: 相同合成输入
- **WHEN** 对两个适配器输入相同的认证、发码、依赖、降级、审计和维护事件
- **THEN** 通过 OTel SDK 采集和 Prometheus Gather 得到等价值；非整数秒、桶边界、零耗时及并发累计均被验证

#### Scenario: 宿主调整聚合
- **WHEN** 宿主显式修改 OTel View、时间窗口或桶边界
- **THEN** 文档明确展示值不再受默认桶等价保证，但事件单位和业务分类保持，不能将自定义配置结果当成默认适配器不一致

### Requirement: 实例隔离与注册冲突
每个启用适配器的账号实例 SHALL 使用宿主提供的非秘密、固定实例标签；该标签 SHALL 不从请求、schema、Redis 前缀或连接串自动推导。共享采集设施时宿主 SHALL 为不同账号实例选择不同标签，标签语法及长度 SHALL 有界。原生 Prometheus 的重复／不兼容注册 SHALL 明确返回错误，不能静默复用其他实例的 collector；构造失败 SHALL 清理本次已成功注册的 collector，不移除宿主既有 collector。OTel 的相同 Meter 与标签可被 SDK 聚合，宿主 SHALL 保证不同逻辑实例不误用同一标签，不宣称适配器能检测所有外部注册。

#### Scenario: 同进程两个实例
- **WHEN** 两个账号实例以不同固定标签接入同一观测后端
- **THEN** 分别采集各自数量，一方 Close 不影响另一方和宿主设施

#### Scenario: Prometheus 注册冲突
- **WHEN** 注册中途出现与既有 collector 的冲突
- **THEN** 构造失败并撤销本次新增注册，既有 collector 和数据保持

### Requirement: 宿主生命周期与采集边界
Provider、Registry、Exporter、Reader 和 HTTP 指标端点 SHALL 由宿主创建、配置和关闭；库关闭 SHALL 不调用注入组件的 Shutdown、Close 或 Unregister。宿主 SHALL 先排空业务并停止库维护／自建审计，再最终刷新与关闭观测设施。记录路径 SHALL 不因采集请求访问数据库、Redis、发送器或 IdP；默认不从上下文导出 baggage、请求属性或 exemplar。

#### Scenario: 关闭顺序
- **WHEN** 宿主停止业务请求、关闭 accountkit 后刷新并关闭其 Provider
- **THEN** 库关闭过程中的审计与维护终态仍可记录，其他共享同一 Provider 的组件由宿主统一管理

#### Scenario: 采集期间业务依赖故障
- **WHEN** 数据库或 Redis 不可用但监控设施仍能采集
- **THEN** 采集只读取本地聚合值，不触发认证存储读写或健康探测

### Requirement: 宿主示例与指标文档
项目 SHALL 提供分别使用两个适配器的可运行合成观测示例和指标契约文档。示例 SHALL 显式创建本地采集设施、输出合成观测并执行一次采集，默认无需账号数据库、Redis、真实凭证或远程监控服务。文档 SHALL 描述真实宿主注入、固定实例标签、依赖所有权、导出配置、默认桶、名称映射及观测盲区，不将示例称为真实认证联调。

#### Scenario: 独立运行示例
- **WHEN** 开发者分别运行两个示例
- **THEN** 得到可核对的合成指标，进程正常退出，不打印真实隐私、不自动监听外网或依赖产品配置

#### Scenario: accountsvc 边界
- **WHEN** 本次变更完成后按现有方式运行 accountsvc
- **THEN** 现有路由、探针、配置和监听方式保持，不自动新增指标端点或导出网络连接
