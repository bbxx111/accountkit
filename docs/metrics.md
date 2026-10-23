# 指标与宿主观测

v0.3.0 提供可选的类型化 `observability.Observer`，以及 OpenTelemetry 和原生 Prometheus 适配器。未配置 `Deps.Observer` 时使用 no-op；包装 nil 的实现也视为未配置。核心领域不导入具体后端。项目仍为单 Go module，适配器依赖存在于根 `go.mod`，使用核心包不要求启动监控服务。

## 先运行合成示例

从仓库根目录运行：

```bash
GOWORK=off go run ./examples/metricsotel
GOWORK=off go run ./examples/metricsprometheus
```

PowerShell 先设置 `$env:GOWORK = 'off'`，再运行对应 `go run`。两者均构造本地设施、记录两个合成 access 校验成功事件、采集一次并退出；次数为 2，耗时分别为 0.125、0.375 秒，总和为 0.5 秒。示例没有执行真实认证，不需要 PostgreSQL、Redis、真实凭证或远程 Collector，也不启动 listener。OTel 使用 SDK `ManualReader`，Prometheus 使用独立 `Registry.Gather` 并输出文本格式。

代码与输出断言见 [OTel 示例](../examples/metricsotel/main.go)、[Prometheus 示例](../examples/metricsprometheus/main.go)。这些示例只验证合成事件的适配和采集；真实领域路径的验证状态见[本次验收记录](../openspec/changes/archive/2026-10-19-add-standardized-metrics/verification.md)。

## 真实宿主装配

适配器的构造函数分别为 `otel.New(metric.MeterProvider, instance)`、`prometheus.New(prometheus.Registerer, instance)`，均返回 `(*Observer, error)`。必需设施为 nil、包装 nil 或实例名非法时返回错误，不回退到全局对象。构造和本地记录不访问业务存储或远程导出端。

下列片段的完整编译示例见 [OTel 宿主装配](../examples/metricsotel/host_example_test.go)和[Prometheus 宿主装配](../examples/metricsprometheus/host_example_test.go)。这两个 `Example_hostObserver` 没有 `Output`，只参与编译，不执行依赖 I/O、迁移、监听或真实认证；其中 `cfg`/`deps` 的变量声明须替换为宿主真实配置和对象。`go run` 执行的仍是前述合成示例。

OTel 宿主创建 Reader/Exporter 和 Provider，再把 adapter 注入已有依赖。下面的 `reader` 由宿主提供：本地诊断可用 `sdkmetric.NewManualReader()`；生产可使用宿主选定 Exporter 的 `sdkmetric.NewPeriodicReader(exporter)`。Exporter 的连接、安全配置、周期和失败处理归宿主。

```go
import (
    "github.com/bbxx111/accountkit"
    akotel "github.com/bbxx111/accountkit/observability/otel"
    sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// cfg、deps 已按 Go 集成指南准备，reader 为宿主创建的 sdkmetric.Reader。
provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
observer, err := akotel.New(provider, "consumer-auth")
if err != nil { return err }
deps.Observer = observer
auth, err := accountkit.New(cfg, deps)
if err != nil { return err }
// 继续执行 Migrate、Start，再接入宿主请求。
// 宿主负责在退出或装配失败时关闭自己创建的 provider。
```

OTel instrumentation scope 固定为 `github.com/bbxx111/accountkit/observability`。库不调用全局 Provider。默认 SDK 累积聚合采用适配器的显式桶建议，宿主 View 可以覆盖聚合；若覆盖桶或 temporality，展示结果不再受默认桶等价保证。

原生 Prometheus 宿主创建 Registry 和 adapter，再注入根门面。需要 HTTP 采集时由宿主将 handler 挂到受控的管理网络：

```go
import (
    "github.com/bbxx111/accountkit"
    akprom "github.com/bbxx111/accountkit/observability/prometheus"
    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/promhttp"
)

registry := prometheus.NewRegistry()
observer, err := akprom.New(registry, "consumer-auth")
if err != nil { return err }
deps.Observer = observer
auth, err := accountkit.New(cfg, deps)
if err != nil { return err }
// hostAdminMux 是宿主已设置访问控制的管理路由，不在示例中启动 listener。
hostAdminMux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
// 继续执行 Migrate、Start，再接入宿主请求。
```

指标 handler 的监听地址、端口、TLS、身份验证和网络可达范围均由宿主决定。不要直接将其暴露在消费者公网入口。accountsvc 可选择启用同监听器的 Prometheus 入口，具体配置见下节。两个适配器不注册 Go/process 默认 collectors；宿主可以独立添加运行时指标。

直接构造 `user.NewService` 时给 `user.Deps.Observer` 传同一 adapter；直接构造公共审计、认证映射器或 Apple 验证器时通过各自 Options 的 Observer 字段接入。已有位置参数构造函数保持可用。根门面将其 Observer 归一为同一个安全 Recorder，再传给自建服务、本地 Async、认证映射器、Apple 和维护运行器；宿主注入的外部组件需自行接入观测。

## accountsvc 采集

accountsvc 默认不装配 Observer 或指标路由。设置 `ACCOUNTSVC_METRICS_ENABLED=true` 并提供 `ACCOUNTSVC_METRICS_INSTANCE` 后，服务创建应用专属 Registry 和原生 Prometheus Observer；沿用下表全部指标、标签和默认桶，不使用全局 Registry，不注册 Go/process 默认 collectors 或 HTTP 抓取指标。实例必须匹配 `[A-Za-z0-9][A-Za-z0-9._-]{0,63}`，固定且非秘密；关闭时忽略实例值，非法开关或启用时非法实例会在监听前失败。`migrate` 忽略两个设置，不创建观测设施。

同监听器的 `GET /metrics` 继承 TLS 与请求排空，无内建消费者/管理员鉴权。关闭返回404；启用后其他方法返回405。响应禁止缓存（`Cache-Control: no-store`），采集错误返回安全的503，最多5个并发采集、超限503。它只采集本地聚合，不访问业务依赖，也不受 readiness 失败阻断。先完成[网关及直连隔离](gateway-integration.md#指标访问)再启用；公网不得抓取。

无观测前允许没有序列或空输出。重启重置累计值；同一逻辑实例多副本可共用 `accountkit_instance`，抓取配置仍须保留每副本目标标签（例如 Prometheus 的 `instance`），不同逻辑实例使用不同值。服务退出时先排空 HTTP 再关闭 kit，不执行最终主动导出或额外刷新等待，关闭期间新增观测可能无法被抓取。完整配置与回退见[服务手册](accountsvc.md#prometheus-指标)。

## 实例与生命周期

每个适配器要求宿主提供非秘密的固定实例名，精确匹配 `[A-Za-z0-9][A-Za-z0-9._-]{0,63}`。实例名只在构造时确定，所有指标都带 `accountkit_instance`；不从 schema、Redis 前缀、issuer、连接串或请求内容自动推导，也没有新增核心环境变量。

共享同一 Provider/Registry 的不同逻辑账号实例使用不同实例名。同一逻辑实例跨进程可以复用该值，进程身份由采集目标或 OTel Resource 区分。同一进程中重复创建同一逻辑实例时复用已有 Observer；Prometheus 重复或不兼容注册返回错误，失败时只撤回本次已注册 collectors，保留宿主已有 collector 和数据。Registry 可能保留历史 descriptor 元数据。OTel 的同名 instrument 与属性可能合并，adapter 无法检测宿主所有误用。

嵌入式宿主可安排关闭顺序为：停止接入并排空业务请求 → `Auth.Close()` 停止维护和自建审计 → 排空宿主外部审计等仍会产生观测的组件 → 最终采集/刷新 → 关闭宿主 Provider、Reader、Exporter 和其他设施。共享 Provider 需等所有使用者停止后再关闭。库不会调用注入对象的 `Shutdown`、`Close` 或 `Unregister`，成功注册后的 collectors 由宿主管理；Registry 本身没有 Shutdown。

## 名称、单位和默认桶

以下表格与[公共指标规格](../openspec/specs/metrics-adapters/spec.md#requirement-公共指标契约)一致。所有行额外带 `accountkit_instance`；表中只列业务分类。计数器累积观测数量，耗时为秒；Histogram 的计数、总和和桶来自同一份耗时观测。

| OTel 名称 | 原生 Prometheus 名称 | 类型／单位 | 额外标签 |
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

默认有限桶统一为 `[0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60]` 秒，越界值仍进入 count/sum 和最后一桶。Prometheus 展示累积 `_bucket{le="..."}`、`_sum`、`_count`，包括隐含 `+Inf`；OTel SDK 表达为边界和非累积 bucket counts。比较两者时须先统一桶表示和 temporality，不能直接逐数组比较。

上表定义 OTel instrument 与原生 Prometheus 的映射。OTel 经其他 Exporter 导出后可能再次转换名称和单位；最终名称取决于宿主 Exporter 配置，不保证与原生 Prometheus 逐字相同。默认未出现的标签组合不预建序列；无序列可能表示尚无观测、未接入或覆盖边界外，不能直接解释为已采集到零。

## 有限分类与统计口径

`Observation` 是封闭接口，公开的六类值为 `AuthObservation`、`CodeObservation`、`DependencyObservation`、`DegradationObservation`、`AuditObservation`、`MaintenanceObservation`；使用结构体值及分类常量，内置适配器忽略指针形式。没有任意标签 map、原始 error 或业务对象字段。未知分类映射固定 `unknown`/`other` 或拒收；非法组合、负耗时/数量被拒收，零增量不增加计数。

| 分类 | 允许值与解释 |
|---|---|
| 认证 operation | `sign_in`、`complete_sign_in`、`refresh`、`reauthenticate`、`authenticate` |
| method | `phone`、`email`、`wechat`、`apple`、`facebook`、`google`、`alipay`、`qq`、`oidc`（全部配置 OIDC 类型）、`provider`（宿主登记的其他第三方类型）、`unknown`；refresh/authenticate 固定 `none`；具体类型名不作为标签 |
| 业务 result | `success`、`pending`、`rejected`、`dependency_error`、`internal_error`、`canceled`、`deadline_exceeded`；pending 仅登录/补全 |
| grant | `user`、`bind`、`undelete`、`none`、`unknown`；bind/undelete 对应受限 scope，pending/失败为 none |
| mode | 成功刷新为 `rotation` 或 `grace`；其他情况 `none` |
| 验证码 operation/channel/purpose | operation=`send`/`verify`；channel=`phone`/`email`/`unknown`；purpose=`sign_in`/`bind`/`reauth`/`sign_in_complete`/`unknown` |
| 依赖 component/operation | `code_store`：issue/verify/discard；`grace_cache`：get/put；`revocation`：check/revoke；`idp`：verify_wechat/verify_apple/verify_facebook/verify_google/verify_alipay/verify_qq/verify_oidc/verify_provider；`sender`：sms/email |
| 依赖 result | `success`、`miss`（grace get）、`rejected`（验证码 issue/verify 或 IdP）、`error`、`canceled`、`deadline_exceeded` |
| scenario | `revocation_check_fail_open`、`refresh_grace_write_failed`、`apple_cached_key_fallback`、`google_cached_key_fallback`、`facebook_cached_key_fallback`、`oidc_cached_key_fallback`、`alipay_profile_unavailable`、`qq_profile_unavailable` |
| 审计 stage/result | mapping/failed；admission/invalid 或 dropped；write/written、duplicate、failed、invalid、conflict |
| 审计 field | `request_id`、`device_id`、`subject_hint`、`actor_name`、`client_ip`、`unknown`；只表示被省略字段类别 |
| 维护轮次 result | `acquired`、`skipped`、`error`、`canceled`、`deadline_exceeded`；acquired 仅表示已取得锁 |
| 维护任务 result | `success`、`error`、`canceled`、`deadline_exceeded`、`panic` |
| task | `cleanup_sign_in_completion_flows`、`purge_users`、`cleanup_sessions`、`audit_retention`、`rekey_digests`、`reencrypt_subjects`；自定义名归入 `other` |

reason 为固定业务原因，不从错误文本生成：`none`、`invalid_argument`、`identity_kind_disabled`、`channel_disabled`、`invalid_credential`、`code_invalid`、`code_expired`、`code_attempts_exhausted`、`cooldown`、`target_limit`、`ip_limit`、`target_verify_limit`、`user_frozen`、`user_pending_deletion`、`invalid_account_state`、`identity_conflict`、`identity_required`、`identity_limit`、`flow_invalid`、`flow_expired`、`flow_conflict`、`unknown_token`、`session_revoked`、`session_expired`、`token_reuse`、`containment_failed`、`grace_read_error`、`grace_miss`、`grace_pair_expired`、`send_failed`、`dependency_unavailable`、`internal`、`canceled`、`deadline_exceeded`、`unknown`。保留常量不表示当前协议必然会返回该原因。分类常量以 [classification.go](../observability/classification.go) 为准；reason 不复制到耗时直方图。

认证按一次公开领域调用正常返回记录一次，耗时包含内部等待与重试。pending 和受限授权不能当作完整登录；业务 panic 不伪造成功。发码统计四个公开入口请求，包括前置拒绝；验码只统计实际存储逻辑校验，覆盖登录、绑定、换绑、重新认证和补全。发送成功仅表示发送器接受。

依赖次数表示一次逻辑调用，不能当成 SQL、Redis 命令或 HTTP 请求数。吊销 check 成功发现已吊销仍是依赖 success，业务随后拒绝。宽限写入失败的降级只在轮换最终提交成功后产生；Apple 缓存回退说明失败刷新后的实际缓存命中，最终凭证验证仍可能失败。依赖失败与业务结果应分别解释。

审计计数以既有 Stats 的增量为来源，按阶段解释；同一事件可能跨阶段出现，不能跨 stage 相加求唯一事件数。字段省略计字段数量。外部 Recorder 返回不代表落盘，库不虚构外部 write 成功。维护轮次与任务分开，直接领域维护不产生轮次；定时/手动/旧别名避免重复计数，失败前已完成数量仍保留，CAS 未命中与复核跳过不计处理量。

支付宝一次 Verify 包含换码和可选资料请求，仍只计一次 `verify_alipay` 逻辑调用及整体耗时。资料失败但身份可信时依赖可为 success，另计 `alipay_profile_unavailable`；最终认证 success/pending/rejected 由领域结果决定。正常缺昵称/头像或净化后省略不记资料故障；外层取消终止整体调用并计取消，不作为允许登录的降级。AppID、用户标识、code、token、密钥、URL 和上游原文均不进入标签。

QQ 一次 Verify 包含 `/oauth2.0/me` 校验和可选资料请求，只计一次 `verify_qq` 逻辑调用及整体耗时。资料失败、`ret` 非 0 或 `is_lost=1` 但身份可信时依赖可为 success，另计 `qq_profile_unavailable`；正常缺昵称/头像或头像因域名名单被丢弃不计降级；外层取消按取消计。AppID、openid、unionid、access_token、URL 和上游原文均不进入标签。

## 周期、查询和盲区

记录发生在业务边界，本地累计更新没有“每分钟才记一次”的周期。采集周期由 OTel Reader 或 Prometheus scrape 配置；查询窗口（例如 5 分钟）决定 rate/increase 的时间范围；面板 step 又决定展示分辨率。三者与维护任务执行间隔各自独立。

例如查询最近 5 分钟普通用户登录成功速率：

```promql
sum by (accountkit_instance) (
  rate(accountkit_auth_operations_total{operation="sign_in",result="success",grant="user"}[5m])
)
```

耗时分位数应从对应 histogram 计算，并保留所需聚合维度与 `le`；成功率的分母先明确是否包含 pending、受限授权、业务拒绝和设施故障。短于采集间隔的窗口、缺失样本和刚启动目标会影响查询；不要把缺失序列一律补零后宣称没有故障。

进程重启、替换 Registry/Provider 或重建聚合设施可能重置累计值。使用后端支持的 counter reset 处理；跨进程汇总时保留目标/Resource 的区分，不能先合并原始累计值再推断唯一业务次数。指标仅尽力记录当前进程观察事实；提交后、记录前或导出前退出可能漏计，不重放业务来补数，不作为账本或匿名化完成证据。

覆盖盲区包括进入领域前的 HTTP 解析/中间件拒绝、后续 scope 检查、全部管理/身份操作的请求总量、直接使用低层 Store 的任意调用，以及外部 IdP/审计内部行为。取消/超时只在可确认的返回错误或分支中分类；低层已抹去错误链时不会根据返回时 ctx.Err 猜测原因。指标采集只读本地聚合值，不查询数据库、Redis、发送器或 IdP，也不能代替依赖健康检查。

资源 ID、IP、目标、验证码、令牌、摘要、原始路径、连接串、错误原文和请求 context 值均不进入标签。内置 adapter 丢弃请求上下文属性，不导出 baggage 或 trace exemplar。宿主额外 SDK 富化属于自己的配置边界。

自定义 Observer 必须并发安全并快速返回；库的 Recorder 隔离其 panic，输出固定有界诊断，不打印 panic 内容，不改变业务结果。它无法中断阻塞回调，也不会为每个事件创建 goroutine。维护运行器在持有轮次锁时同步调用 Observer，回调不得同步重入同一个 Runner（例如调用其 `RunOnce`），否则可能死锁；事务中的回调也不能递归进入会等待同一锁的业务路径，或直接执行阻塞网络导出。

性能验证入口为 `go test -run '^$' -bench '^BenchmarkMetricRecording$' -benchmem ./observability`，覆盖 disabled、两个本地适配器及并发记录。基准分别报告 ns/op、B/op、allocs/op 和记录前后的 goroutine 差值；不包含构造、采集与导出，也不表示完整认证请求耗时。机器与实际结果保存在对应变更的验收记录中。
