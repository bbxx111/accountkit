## Context

动机和范围见 [proposal.md](proposal.md)。本次基于 develop 合并提交 `4956edc`，只补充可观测契约，不改变认证行为。

现有代码提供以下可复用边界：

- `user.Service` 同时被 Go 与 HTTP 调用，登录／补全具有 token 和 pending 分支；部分 token 为 user:bind 或 user:undelete。
- 发码在四个公开方法中有不同前置条件，共用发送流程；登录直接使用 VerifyChallengeWithExpiry，其他验证路径也须纳入，不能只改 verifyCode 辅助方法。
- Refresh 的 CAS 回退、宽限读取和事务重试不能被当作多次业务请求。Grace.Put 在事务提交前执行，写入失败可以容忍，降级成功事件必须等最终提交成功。
- `audit.Async` 已有 Written、Duplicate、Invalid、Conflicts、Dropped、Failed，认证映射器已有 Failed／Omitted；不改变其定义或通过定时采样累加快照。
- 维护运行器处理锁和 panic，领域方法返回已处理数量；两层同时埋点需要避免重复。流程清理的旧别名继续转调新入口。
- `New` 无 I/O；Pool、Redis、外部审计及其他宿主组件所有权已有明确约定。

相关主规格： [嵌入契约](../../specs/embedded-auth-package/spec.md)、[验证码轮次](../../specs/verification-challenges/spec.md)、[投递](../../specs/verification-delivery/spec.md)、[补全](../../specs/sign-in-completion/spec.md)、[会话期限](../../specs/session-expiry/spec.md)、[宿主审计](../../specs/audit-host-integration/spec.md)。

## Goals / Non-Goals

**Goals:**

- 用核心定义的类型化事实与共享投影，让两个适配器在默认设置下产生等价指标。
- 以公开操作为业务计数边界，以实际逻辑调用为依赖计数边界，保留 early return、并发和部分完成信息。
- 同一进程可装配多个账号实例，不隐式使用全局设施；缺省观测不改变旧消费者使用方式。

**Non-Goals:**

范围沿用提案。尤其不把指标改造成审计事件总线，不新增后台观测队列、可靠投递或业务存储统计查询；不为额外指标重构事务、SQL 或所有依赖接口。

## Decisions

### 1. 包边界、接口和兼容入口

新增叶子包 `observability`，以及 `observability/otel`、`observability/prometheus`。核心包和领域包只导入叶子契约；共享描述与投影位于 observability 的内部实现，两个适配器不各自复制错误分类。保持单 module，适配器依赖仍记录在根 go.mod。

公共形状为 `Observer.Observe(context.Context, Observation)`，无 error 返回。Observation 使用本包封闭的接口和六类导出值类型表示 Auth、Code、Dependency、Degradation、Audit、Maintenance；每类字段仅有类型化分类、time.Duration 和非负数量，禁止 any、error、任意标签 map、凭证或资源对象。Maintenance 内以固定 kind 区分轮次、任务终态和处理数量，Audit 内区分事件结果与字段省略。新增事件类型不增加 Observer 方法；内置实现忽略无法识别的事件，自定义实现也应有默认忽略分支。

`accountkit.Deps.Observer` 和 `user.Deps.Observer` 可空；增加结构体字段沿用仓库 keyed literal 用法，文档提醒外部未命名字段字面量不具备新增字段兼容性。nil／typed nil 的可选 Observer 归一化为 no-op。根门面向自建服务、认证映射器、Async、运行器和需要观测的内置 IdP 传递同一 observer。

现有可扩展 Options 增加可选 observer 字段；位置参数构造函数保持原签名，确需直接接入时增加命名的带观测构造入口，由原入口转调 no-op。不修改 sqlc 类型，不把领域枚举转成反向依赖。

不选择分别暴露 OTel／Prometheus 字段到核心，避免核心了解后端；不选择给 Observer 增加六个必需方法，避免每次扩展破坏宿主实现。

### 2. 记录隔离和分类所有权

核心在调用 adapter 前形成最终分类，适配器只做检查、投影及聚合。维护固定分类表，不用 err.Error 字符串匹配；数据库异常只在已知来源边界区分依赖错误，其他未知错误归 internal。业务返回值、错误包装和 HTTP 映射保持原状，指标分类不反向驱动业务。

结果集按事件约束：业务 `success/pending/rejected/dependency_error/internal_error/canceled/deadline_exceeded`；依赖 `success/miss/rejected/error/canceled/deadline_exceeded`；维护使用下述独立集合。未发生依赖调用不产生依赖结果。取消／超时只在该调用实际失败原因对应时分类，不能因返回前 context 恰好取消而把已成功提交操作改为取消。

既有低层组件部分不可用包装只保留 ErrUnavailable 而未保留 context 错误链；此类输入按可确认的依赖故障计数。观测不根据返回时 ctx.Err 猜测被抹去的原因，也不为指标修改低层公开错误契约。因此 canceled／deadline_exceeded 表示从返回错误或明确执行分支能够确认的原因，并非对所有底层取消的完整追踪。

reason 为固定枚举，按需复用已有语义：none、invalid_argument、identity_kind_disabled、channel_disabled、invalid_credential、code_invalid、code_expired、code_attempts_exhausted、cooldown、target_limit、ip_limit、target_verify_limit、user_frozen、user_pending_deletion、invalid_account_state、identity_conflict、identity_required、identity_limit、flow_invalid、flow_expired、flow_conflict、unknown_token、session_revoked、session_expired、token_reuse、containment_failed、grace_read_error、grace_miss、grace_pair_expired、send_failed、dependency_unavailable、internal、canceled、deadline_exceeded、unknown。实施时若已有分支不能正确映射，应先更新本设计和对应验收，不动态透传新字符串。保留 code_attempts_exhausted 兼容映射不表示当前挑战协议必然返回它。

安全调用边界只 recover Observer 自身 panic，固定脱敏诊断按进程实例限频，不打印 panic 对象、不递归产生观测。该边界不持有 admission 锁或运行器内部统计锁；事务中实际依赖调用的观测仍可能处于业务事务内，因此宿主回调快速返回是硬性契约。内置适配器只做本地记录；不通过每事件 goroutine 提供虚假的超时隔离。未知／非法事件拒收或映射固定 unknown，负耗时／数量拒收，零数量不增加计数。

关闭或未配置观测时使用快路径，不构造指标标签。耗时使用 time.Now／time.Since 的单调部分，包含领域调用内等待与重试，记录前取终点，不把 Observer 本身耗时累加到业务耗时。

### 3. 认证和验证码覆盖清单

| 事件 | 入口／边界 | 分类 |
|---|---|---|
| Auth | SignInWithCode、SignInWithIdp | operation=sign_in，method=phone/email/wechat/apple/unknown |
| Auth | CompleteSignIn | operation=complete_sign_in，method 为本次补充证明渠道；pending 或结果幂等返回均为一次调用 |
| Auth | Refresh | operation=refresh，method=none；mode=rotation/grace/none |
| Auth | Reauthenticate | operation=reauthenticate，method=phone/email/unknown |
| Auth | Authenticate | operation=authenticate，method=none；不包含随后 scope 中间件拒绝 |
| Code send | SendSignInCode、SendBindCode、SendReauthenticationCode、SendSignInCompletionCode | 从公开方法入口到最终返回，涵盖前置拒绝，按各自用途计数 |
| Code verify | 领域层实际调用 VerifyChallenge／VerifyChallengeWithExpiry | channel=phone/email/unknown；按 SIGN_IN/BIND/REAUTH/SIGN_IN_COMPLETE 转成固定小写 purpose |

授权标签 grant 取 `user/bind/undelete/none/unknown`，来自最终 TokenResult 或 Principal，不从调用方字符串原样复制。返回 pending 或失败时为 none。刷新 mode 仅最终成功轮换／宽限领取时为 rotation／grace，失败为 none，具体失败由 reason 表达；不额外增加一份容易漂移的轮换总数，可按 mode 过滤计数。

观测 defer 必须位于输入校验之前，并在 flowStorageError 等最终错误转换之后读取返回结果；只在正常返回时产生业务终态，原业务 panic 不被伪造为成功或被观测代码吞掉；共用 sendCodeUntil、signInVerified 不另计业务次数。验码使用统一的内部观测帮助入口覆盖所有直接存储调用，不能给 public wrapper 和底层 Store 同时增加同一业务验码计数。

认证指标不包含身份管理或管理 API 的全部调用；其中实际发生的验证码、IdP、发送器调用仍进入对应细分指标。没有开启第二套产品行为埋点。

### 4. 依赖清单和降级边界

| component | operation | 边界及解释 |
|---|---|---|
| code_store | issue / verify / discard | 领域发起的一次验证码存储调用；校验错误为 rejected，不是 Redis 故障 |
| grace_cache | get / put | 一次宽限缓存调用；get 缺失为 miss，返回 pair 后到期属于业务 reason |
| revocation | check / revoke | 一次吊销集读写；被吊销是正常 check 完成，业务随后拒绝 |
| idp | verify_wechat / verify_apple | 一次验证器调用，可能包含缓存或多次 HTTP；非法证明为 rejected |
| sender | sms / email | 一次实际发送器调用，包含渲染和传输，成功不等于最终收件 |

为保持原类型和构造方式，优先在服务的固定调用边界统一帮助函数，不给宿主 Pool／Redis 安装全局 hook，也不包装成另一套数据访问层。首版不声称覆盖直接使用 code.Store 等低层组件的任意调用；这些指标的覆盖对象是账号领域业务路径。

降级 scenario 固定为 `revocation_check_fail_open`、`refresh_grace_write_failed`、`apple_cached_key_fallback`。第一个在实际放行时发出；第二个将事务中的写失败事实暂存，在最终轮换提交成功后发出，回滚只保留依赖失败；第三个指内置 Apple 已经历失败刷新（lastRefreshOK=false）后，后续验证实际命中缓存密钥，直到成功刷新恢复；不要求同一次失败刷新找到原本未知的 kid，不增加主动刷新或网络调用，不宣称最终登录一定成功。Apple 观测回调在释放缓存锁后执行。外部 IdP 内部的降级是宿主责任。

缓存故障不更改现有 invalid_grant；发生故障与最终 HTTP 状态分开解释。宽限读取的已确认故障使用认证 result=dependency_error、reason=grace_read_error，依赖 result=error；缓存 miss 或 pair 到期使用认证 result=rejected 和各自 reason。不能仅按最终 ErrInvalidGrant 抹去已知故障原因。指标不会为了区分一个原本不可观测的原因额外访问 Redis 或数据库。

### 5. 审计和维护的准确边界

审计在既有原子 Stats 更新处同时发出增量，避免定时读取累计快照再 Add。映射器以 stage=mapping、result=failed 计事件；字段省略使用独立字段计数。Async 入队阶段以 admission/invalid 或 admission/dropped 分类；写入阶段以 write/written、duplicate、failed、invalid、conflict 分类，数量来自通过既有一致性校验的 BatchResult。正常入队和正常映射不额外计为“写入成功”。同一事件可能经过多个阶段，指标不能跨 stage 求和当作唯一事件总量。

关闭排空沿用原 Stats 所记录的事实，不以队列长度猜测额外丢弃或重复增量。外部 Recorder 没有接收确认／落盘接口，因此不输出其 writer 指标；单独使用公共 audit 组件的宿主可通过其观测 Options 接入。认证映射器的 allowed field 类别由已有脱敏字段集合投影，未知名统一 unknown。

维护分三个投影：轮次结果、任务终态、已处理数量。轮次 result=`acquired/skipped/error/canceled/deadline_exceeded`；acquired 只说明锁已获得，不表示全部任务成功。任务 result=`success/error/canceled/deadline_exceeded/panic`。六个内置 task 名保持现状，其余公共 Runner 名称映射 other。

Runner 记录锁结果及任务终态；仅启用观测的 Runner 在执行任务时通过内部叶子包的私有 context 标记当前内置任务及观测所有者。直接领域维护方法仅在任务名和共享安全 Recorder 所有者均匹配时抑制重复终态，否则自行记录；根装配通过幂等 NewRecorder 复用同一指针，直接装配不同观察者不互相吞计数。Runner 负责其外层耗时与 panic。未知宿主任务不伪造内置标记。处理数量始终在真正拥有返回数量的边界记录，根门面闭包不再次累计；audit_retention 的数量由该任务闭包独占记录。旧 CleanupIdentityFlows 转调新方法，不增加第二次记录。

直接调用被支持的领域维护方法不产生维护“轮次”指标。失败前的已完成数量保留；CAS 未命中、被恢复账号的状态复核跳过不计处理成功。Observer panic 被单独隔离，不能把任务成功改成 panic。

### 6. 指标描述与名称映射

所有指标均有固定 `accountkit_instance` 标签。计数累计，耗时使用秒，名称和标签作为版本化公共观测契约。counter 的原因标签不复制到耗时直方图，降低序列数。

完整的 13 项公共指标名称、类型、单位与标签见 [适配器规格](specs/metrics-adapters/spec.md)中的“公共指标契约”，由共同描述表实现；设计不另存第二份名称表。

Histogram 默认有限边界统一为 `[0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60]` 秒，适配器都统计越界值。OTel 设置默认显式桶建议，示例和一致性测试的 SDK 明确使用对应聚合配置；宿主 View 可覆盖，文档需注明默认一致性成立的配置条件。Prometheus 侧使用相同边界，展示时带隐含 +Inf；OTel 桶表示归一化后再比较。首版不提供任意动态标签扩展或自定义指标改名配置。

内置适配器不把请求 context 的属性或 trace 信息带入指标记录，使用不含请求值的记录上下文，默认不启用 exemplar；宿主自行启用额外 SDK 富化不属于本适配器默认输出。

OTel 接收显式 MeterProvider，使用固定 instrumentation scope `github.com/bbxx111/accountkit/observability`，不调用全局 Provider。原生 Prometheus 使用宿主 Registerer 创建自己的 collectors，不使用默认 Registry、不使用 MustRegister。构造错误撤回本次已完成注册，宿主已存在的 collectors 保持；成功注册后的移除由宿主设施生命周期管理。

平台导出可能再转换名称和单位，尤其 OTel 经 Prometheus Exporter 的最终命名由其配置决定。规格表定义 OTel instrument 与原生 Prometheus 的映射，不承诺所有 Exporter 默认配置输出逐字相同名称。验收比较归一化数据；不根据名称字符串替换猜测单位。

### 7. 实例、生命周期和示例

两个适配器要求显式 Instance，规则为 `[A-Za-z0-9][A-Za-z0-9._-]{0,63}`，宿主自行选择非秘密、稳定标签，不用原始数据库名或 issuer 推导。共享设施的不同逻辑账号实例使用不同标签；同一逻辑实例跨进程可复用该值，进程身份由采集目标／OTel Resource 区分。核心不复制该标签进入业务 Config，不新增环境变量。

同一进程为同一逻辑实例重复 New 时应复用已有 Observer；新建 Prometheus adapter 的重复注册明确报错。OTel 同名 instrument／属性可合并，适配器不维护全局注册表或虚假地承诺检测外部误用。

先停止接收和排空业务请求，再 Close accountkit 以停止维护并处理自建审计，最后由宿主最终采集、刷新和关闭观测设施。库不关闭 Observer、Provider 或 Registry，不自动注销 Collector。实例标签只在构造时固定，不热更新。

计划新增 `examples/metricsotel` 与 `examples/metricsprometheus`：使用合成 Observation、显式本地 SDK／ManualReader 或 Registry，完成一次采集并退出，不需要数据库、Redis或真实令牌。默认不启动 HTTP，也不自动导出网络。文档演示把同一个 adapter 注入真实 Deps，并由宿主自行配置 Exporter／指标 handler，明确示例并未执行真实认证。

参考：[OTel 库埋点边界](https://opentelemetry.io/docs/concepts/instrumentation/libraries/)、[OTel Metrics API](https://opentelemetry.io/docs/specs/otel/metrics/api/)、[OTel Go 导出](https://opentelemetry.io/docs/languages/go/exporters/)、[Prometheus 命名](https://prometheus.io/docs/practices/naming/)。实施时选择并固定与本仓库 Go 版本兼容的依赖版本，先核验 API／默认聚合行为；不在提案阶段修改 go.mod。

### 8. 验证顺序

先完成外部包兼容、分类、非法输入、隐私、panic 和 no-op 快路径测试；再以相同合成事件比较两个真实本地聚合器；最后通过真实 PostgreSQL／Redis 场景验证认证和维护计数时机。覆盖 pending、多种授权、刷新 CAS／宽限、提交失败、发码前置拒绝、跨用途验码、投递失败、吊销 fail-open、部分回填失败、锁竞争、panic 和旧别名。

基准分别记录 no-op、两个 adapter 和并发操作的 ns/op、B/op、allocs/op，在同一环境比较，保存原始结果；不为跨机器时延设置武断门槛。no-op 快路径验证额外分配及 goroutine 情况，若有显著开销须在验收前处理并记录。独立构建、vet、race、必需测试、迁移保护和恢复按仓库流程执行，未验证项明确保留，不以旧报告替代。

## Risks / Trade-offs

- [自定义 Observer 阻塞] → 类型契约明确快速返回；内置实现仅本地记录，panic 单独隔离，不谎称能中断任意宿主代码。
- [多层重复计数] → 固定公开边界、内部统一帮助函数和维护标记，加入事务重试与别名的计数断言。
- [指标基数膨胀] → 有限分类、固定实例标签、直方图减少维度、只为实际组合创建序列，非法值不透传。
- [两后端默认聚合不同] → 统一描述与默认桶，并用实际 SDK／Gather 比较；宿主覆盖配置另行标注。
- [未来扩展接口与标签] → 单入口、封闭类型、未知分支处理，指标契约变更纳入 OpenSpec 和兼容说明。
- [追加依赖成本] → 单 module 明示依赖范围，核心不 import 具体后端，不安装 Collector 或管理宿主监控服务。
- [进程退出导致漏计] → 只承诺尽力观测；成功率查询分别看业务拒绝和设施故障，不把指标作账本或可靠清理证明。

## Migration Plan

1. 发布前按任务完成实现和兼容验证；旧宿主不配置 Observer，行为保持。
2. 宿主选用一种适配器并配置固定实例标签，注入已有 Deps；真实 SDK／Registry 和导出设施由宿主装配。
3. 初次部署先验证无隐私标签、实例隔离和采集结果；告警或面板由宿主另行配置。
4. 回退时移除 Observer 注入或回退宿主依赖版本，不涉及 SQL、Redis 键、令牌或密文迁移。不同进程／适配器的计数重置按正常监控语义处理。
