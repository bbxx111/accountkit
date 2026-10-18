# 标准化指标 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 accountkit 增加宿主可选的类型化观测接口，以及语义一致的 OTel 和原生 Prometheus 指标适配器。

**Architecture:** 叶子包定义六类观测值与安全记录边界，业务代码在固定位置产生事实；共享描述和投影驱动两个适配器。宿主拥有 Provider、Registry、Exporter 和指标端点，根门面及直接服务只接收可选 Observer。

**Tech Stack:** Go 1.26.5，现有 pgx／go-redis／miniredis，OTel Metrics API 与测试用 SDK，Prometheus client_golang，Linux CGO/race 与 PostgreSQL 17 恢复工具。

**Spec:** [提案](../../../openspec/changes/add-standardized-metrics/proposal.md)、[设计](../../../openspec/changes/add-standardized-metrics/design.md)、[核心规格](../../../openspec/changes/add-standardized-metrics/specs/standardized-metrics/spec.md)、[适配器规格](../../../openspec/changes/add-standardized-metrics/specs/metrics-adapters/spec.md)。

## Global Constraints

- 以规格为契约，不改变 SQL、Redis 键、HTTP、令牌、密文、公开原构造函数或既有审计 Stats；核心不导入观测后端。
- Observer 默认 no-op；实例标识由宿主显式提供，匹配 `[A-Za-z0-9][A-Za-z0-9._-]{0,63}`，所有指标附带 accountkit_instance。
- 13 个指标的名称、单位及标签严格采用适配器规格表；默认耗时桶为 0.001、0.005、0.01、0.025、0.05、0.1、0.25、0.5、1、2.5、5、10、30、60 秒。
- 原始错误、请求对象、凭证、用户／会话／流程 ID、IP、邮箱、手机号、baggage、trace exemplar 不进入指标；字段分类有限，未知输入不创建动态标签。
- 公开认证操作正常返回一次终态；内部重试不重复，原业务 panic 继续传播。发码是公开入口请求量，验码是实际存储逻辑调用量。
- no-op 快路径不建立标签或 goroutine，内置适配器只记录本地内存；库不关闭宿主设施、采集不访问业务依赖。
- 定时维护、手动维护和旧别名不重复计数，失败前的实际处理数量保留；自定义 Runner 名映射 other。
- GOWORK=off；真实依赖 skip 不算集成通过；全套验证及原始输出留在忽略目录，验收结论写入本变更 verification.md。
- 默认 SDD 执行，任务交付后独立规格／质量审查；仅不同文件且无接口前置依赖的任务允许并行。代理不自行派生子代理。

## Review Focus

1. 最终错误转换的 defer 顺序：补全不能在错误归一化前记错分类；Task 3 用无效票据／存储失败固定计数。
2. typed nil、宿主 panic 与日志 panic：Task 1 的 nil／故障记录测试固定安全边界，原业务 panic 测试固定不伪造成功。
3. Grace.Put 在事务内：Task 3 固定 commit 失败时无成功降级，实际依赖失败仍记录。
4. 同一 Provider／Registry 的两个实例及部分注册失败：Task 2 用真实聚合器验证隔离、回滚和关闭所有权。
5. Runner 到领域维护的 context 标记：Task 5 对直接、Runner、旧别名与自定义任务嵌套分别断言终态和处理数量。

## 文件与接口分工

| Task | 新增／修改文件 | 负责内容 |
|---|---|---|
| 1 | observability/{observation,classification,recorder}.go、对应测试；observability/internal/metricdata/{catalog,project}.go；internal/obstest/recorder.go | 核心事件、分类、安全记录、共享描述／投影、测试采集器 |
| 2 | observability/otel/{observer,observer_test}.go、observability/prometheus/{observer,observer_test}.go；observability/adapters_test.go；go.mod／go.sum | 两个适配器、真实聚合器一致性 |
| 3 | user/observability*.go 与测试、user/service*.go、user/Deps | 认证、验证码、依赖及成功提交相关降级 |
| 4 | audit/async.go、audit/authentication/authentication.go、audit/aliases.go（若有）、user/idp/apple.go 及测试 | 审计阶段增量、Apple 缓存回退 |
| 5 | maintenance/runner.go、user/service_{purge,rekey,signin_completion_store,session}.go、accountkit.go 及测试 | Runner／领域维护去重、根门面装配、关闭边界 |
| 6 | examples/metricsotel、examples/metricsprometheus、docs/metrics.md、README.md、docs/{features,go-integration,compatibility}.md；指标 benchmark | 示例、集成指南、性能、跨路径验收 |
| 7 | scripts/required-tests.txt、本变更 tasks／verification、必要修复 | 全库真实依赖、race、恢复、最终审查 |

目录内可按单一职责拆小文件，不能借埋点修改领域事务。Task 1 接口验收后，Task 2 与 Task 3 可以在互不共享写入文件的情况下并行；Task 4 亦只写自己的域。Task 5 的 maintenance／内部标记部分仅依赖 Task 1，可先独立实施；修改 user／根装配前须等 Task 3／4 审查通过。Task 6 的合成示例、指标契约和适配器基准在 Task 2 审查通过后可独立实施，真实根装配说明及最终验收等 Task 5 就绪再核对。

### Task 1: 核心观测和共同投影（OpenSpec 1.2，2.1 的核心部分）

**Files:** 见分工表 Task 1；不修改领域服务或 go.mod。

**Interfaces:**
- 产生 `type Observer interface { Observe(context.Context, Observation) }`、封闭 `Observation` 接口，六个导出值类型 `AuthObservation`、`CodeObservation`、`DependencyObservation`、`DegradationObservation`、`AuditObservation`、`MaintenanceObservation`。
- 字段以各事件必要分类、Duration time.Duration、Count int64 构成。分类为独立 string 底型及有限常量，不接受任意标签 map 或 error；文档列清字段与常量供后续任务消费。
- 产生 `NewRecorder(Observer, *slog.Logger) *Recorder`，以及 `(*Recorder).Enabled() bool`、`(*Recorder).Observe(context.Context, Observation)`；nil／typed nil 归一为 disabled，传入有效 *Recorder 时返回同一指针，安全恢复只包围 observer 自身，并隔离故障日志 handler 的 panic。
- 产生内部 `metricdata.Descriptors() []Descriptor`、`DefaultBuckets() []float64`、`Project(Observation, func(Measurement))`。Descriptor 至少包含 ID、OTelName、PromName、Unit、Description、Labels、Histogram；Measurement 包含 Descriptor ID、Count、Value（秒）、有限 Labels。返回描述与桶副本，不泄露可变全局切片。
- 产生测试辅助 `internal/obstest.Recorder` 实现 Observer，提供线程安全 Snapshot／Reset；只存类型化值，供跨域行为测试使用。

- [ ] 先写外部测试 `TestObserverContract`、`TestRecorderFaultIsolation`、`TestMetricProjection`：typed nil 不调用；panic 合成秘密不泄露；并发安全；负耗时／数量拒收；未知枚举不进入标签；六类事件映射到规格 13 个指标；描述和桶修改不污染后续实例。
- [ ] 用 `go test -count=1 ./observability/... ./internal/obstest` 取得缺失 API 的 RED 证据，再实现最小代码；校验非法字段组合并保持低基数。
- [ ] GREEN 后运行上述测试与 vet；为 nil 快路径增加 benchmark／分配测试，避免单纯测试常量或镜像实现。
- [ ] 记录接口与 RED/GREEN 到任务报告；提交 `feat(observability): add typed observations and shared metric catalog`，交独立审查后冻结接口。

### Task 2: 两个指标适配器（OpenSpec 2.1–2.4）

**Files:** 见 Task 2。仅本任务修改 go.mod／go.sum。

**Interfaces:**
- 消费 Task 1 事件与 metricdata。
- 产生 `otel.New(provider metric.MeterProvider, instance string) (*otel.Observer, error)` 与 `prometheus.New(registerer prometheus.Registerer, instance string) (*prometheus.Observer, error)`，两种 Observer 实现公共接口；包名冲突用 import alias。
- 使用累积 Counter 与秒 Histogram；OTel instrumentation scope 固定 `github.com/bbxx111/accountkit/observability`，显式默认桶，记录不带请求上下文属性；Prometheus 不使用全局 Registry 或 MustRegister。

- [ ] 先以真实 SDK ManualReader／Registry 编写 `TestObserverMetrics`、`TestObserverInstanceIsolation`、`TestRegistrationRollback`、`TestAdaptersEquivalent`；断言 13 个描述、两实例数量、失败注册只撤回自身、桶边界／非整数秒／并发累计及原 Registry 保持。
- [ ] 根据官方 API 文档固定兼容版本，先看到 RED，再实现 adapters；原始依赖查证和下载结果记报告，不安装全局工具。
- [ ] 用带私密 context 的输入断言无 baggage／trace exemplar；宿主 View 覆盖只改变明确的聚合，不改核心口径；nil／typed nil 必需依赖报错。
- [ ] 运行 `go test -count=1 ./observability/...`、对应 vet 与 Linux race，提交 `feat(observability): add OTel and Prometheus adapters` 并独立审查。

### Task 3: 认证、验证码和依赖边界（OpenSpec 1.3 的领域部分，3.1–3.4，4.1–4.3 的服务部分）

**Files:** Task 3；允许新增 focused service helper 文件，不改 accountkit.go、审计／Runner 或 go.mod。

**Interfaces:**
- 为 `user.Deps` 增加 Observer，Service 持有 Task 1 Recorder；原 NewService 签名不变。
- 公开入口签名保持。通过内部帮助函数记录 code_store issue／verify／discard、grace get／put、revocation check／revoke、IdP verify、sender 调用；不改低层存储公共类型。
- 产生仅由服务使用的最终结果分类和安全终态帮助代码；新旧字段／方法兼容。

- [ ] 按设计入口表先写测试，再埋点每次公开正常返回。`TestAuthenticationObservations` 覆盖 pending、user／bind／undelete、无效输入、依赖故障、业务 panic 不记成功及上下文取消后的已提交成功。
- [ ] `TestCodeObservations` 覆盖四种公开发码的前置拒绝，登录／绑定／换绑／重新认证／补全的实际验码；存储一次逻辑调用只记一次，未执行依赖不记次数。
- [ ] `TestSessionObservations` 覆盖刷新 rotation／grace、CAS、到期、重放及吊销失败；真实 DB／Redis 验证 same pair、计数、不误吊销、Grace.Put 失败后 commit 成功才发降级，commit 失败只记依赖故障。
- [ ] `TestDependencyObservations` 覆盖 check fail-open、sender 普通错误及已知 ErrUnavailable、miss／rejected 与网络故障、discard 清理竞争；逐处 rg 复核调用覆盖但不以静态搜索代替行为证据。
- [ ] 运行 user 相关测试、vet、真实依赖用例及并发；提交 `feat(observability): instrument authentication and verification flows` 并独立审查。根门面注入留 Task 5。

### Task 4: 审计阶段和 Apple 回退（OpenSpec 4.2–4.3 的 Apple 部分，5.1）

**Files:** Task 4；不修改 user/service*.go、accountkit.go 或 go.mod，可先于 Task 5 并行。

**Interfaces:**
- 在 `audit.AsyncOptions`、认证映射 `Options`（以及根 alias 映射）、`idp.AppleOptions` 加可选 Observer，原构造参数不变；实例持安全 Recorder。
- audit 以 mapping failed／omitted fields、admission invalid／dropped、write BatchResult 各分类的实际数量发事件，不采样累计 Stats。
- Apple 在一次刷新已失败（lastRefreshOK=false）后的实际缓存命中时发 apple_cached_key_fallback，成功刷新恢复后不再发；回调在 Apple.mu 释放后执行。外层 IdP 逻辑调用由 Task 3 计数。

- [ ] 先写 `TestAuditObservations` 覆盖混合批、入队拒绝、非法 BatchResult、关闭排空与 Stats 对齐；省略字段数量不混成事件数，外部审计不伪造落盘成功。
- [ ] 先写 `TestAppleCachedKeyObservation`，在现有 HTTPS fixture 证明故障后缓存回退和后续验证失败可同时成立；不得为指标增加拉取或改变节流。
- [ ] 获得 RED 后实现最小代码；确保用户 Observer 和 Logger 回调不在 admission 锁内调用，观测 panic 隔离且无私密正文。
- [ ] 运行 audit、authentication、idp 的相关测试、vet、Linux race；提交 `feat(observability): expose audit stages and IdP fallback` 并独立审查。

### Task 5: 维护与根门面装配（OpenSpec 1.3 的根部分，5.2–5.4）

**Files:** Task 5；依赖 Task 3／4 的公共字段。

**Interfaces:**
- 根 `Deps.Observer` 向 user、Async、映射器、Apple、Runner 传递；不改变 Auth.New I/O 和 Close 所有权。
- 原 `maintenance.NewRunner(interval, locker, logger, tasks...)` 保留，新增明确的带观测构造入口；Task 1 的公共 MaintenanceObservation 负责轮次、终态、数量。
- 通过小型共享内部 context 标记协调 Runner 与直接领域维护方法，标记包含任务及 *observability.Recorder 所有者，仅启用观测的 Runner 写标记，只有匹配任务及所有者才抑制领域终态。根装配复用同一 Recorder；Task 5 定义精确函数并在报告记录，Task 1 类型不因标记增加业务字段。

- [ ] 先写 `TestRunnerObservations`，覆盖 acquired／skipped／锁错误／取消、任务 success／error／panic、故障 Observer 不影响结果及 custom task=other。
- [ ] 先写真实数据库的 `TestMaintenanceObservations`，覆盖直接和 Runner、手动与定时、旧别名只计一次、部分成功后失败、CAS 未命中、audit_retention 处理数量，原锁序和返回结果保持。
- [ ] 根门面 `TestObserverLifecycle` 覆盖无 I/O 构造、nil 可用、两实例共享采集设施、Close 不调用宿主 Close／Shutdown／Unregister；服务和根映射器都正确带 observer。
- [ ] 实现并通过相关 maintenance/user/root 测试、vet、真实依赖与 race；提交 `feat(observability): integrate maintenance and host lifecycle` 并独立审查。

### Task 6: 示例、指标指南和性能（OpenSpec 6.1–6.3）

**Files:** Task 6；依赖 Task 2 与 Task 5，不改运行时行为。

**Interfaces:** 仅消费已完成的公共 New／Deps／Observation 接口。

- [ ] 实现两个命令示例，输入合成事件并采集一次后退出；使用本地 ManualReader／Registry，不需要 DB／Redis／Collector，不启动 listener。
- [ ] `TestMetricsExamples` 或 Example 输出断言可核对的合成指标，不能镜像所有实现；运行两个示例、build/test/vet。
- [ ] 写 docs/metrics.md，引用唯一指标表并解释全部标签、默认桶、采集周期与查询窗口、重启重置、盲区、指标端点访问控制及宿主生命周期；更新四个既有入口文档的相关段落，检查链接和契约一致。
- [ ] 基准覆盖 no-op、两适配器和并发；保存环境与 ns/op、B/op、allocs/op，检查零观测快路径和 goroutine 数量；提交 `docs(observability): add host examples and metric contracts` 并独立审查。

### Task 7: 全量验收和最终审查（OpenSpec 6.4）

**Files:** 任务清单、验收记录、必需测试清单与审查修复。

- [ ] 整理新增必需顶层行为测试到 scripts/required-tests.txt，不删除旧项、不放宽 skip；SQL／生成文件及历史基线无意外差异。
- [ ] Windows GOWORK=off build／vet／全套快速测试；Linux 隔离真实 PostgreSQL／Redis 下执行 scripts/check-generated.sh、scripts/verify.sh、scripts/verify-accountsvc.sh 和 benchmarks，恢复源／目标为本任务新建空数据库。
- [ ] 独立全分支审查，按发现统一修复并定向复审；原始日志与恢复材料留忽略目录，实际命令／结果／环境／未验证边界写 verification.md。
- [ ] 执行 OpenSpec 全量严格校验、文档链接、gofmt、git diff --check 与任务核对；只有已实现并有证据的项目勾选。交付本实施分支，归档和再次合并分别由用户指令处理。
