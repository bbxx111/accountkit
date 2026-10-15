## Context

动机见 [proposal.md](proposal.md)。当前 audit.Event 是认证专用结构；audit.Store 在写入时生成 e_ TSID，Async 为有界尽力投递队列。accountkit.New 在自定义 Deps.Audit 存在时仍把本地 Store 接到管理查询和保留期任务。httpapi/admin 的读侧暴露 sqlc 行，审计与账号列表共用字符串游标。user/query.sql 的两条 scrub 查询依赖 user_id、flow_id；migrations/baseline.go 对所有版本都要求 audit_event.flow_id 存在。这些边界都需要同步调整。

本变更独立于未归档的 refine-verification-code-delivery，不修改其工件或验证码投递语义。迁移 0001、固定源快照、既有标签均保持。

## Goals / Non-Goals

**Goals:**
- 提供宿主能直接映射的通用审计契约，默认实现仍只管理本实例数据。
- 写侧事件、存储记录、HTTP 投影分离，保留认证审计语义与脱敏覆盖。
- 明确 UUIDv7、字段上限、事件重试、查询模式和历史升级验收。

**Non-Goals:**
- 不让 accountkit 成为宿主业务审计 SaaS，不增加任意事件写入 HTTP 路由或跨业务管理查询路由。
- 不引入消息队列、outbox 或默认至少一次投递，不保证零丢失或跨存储原子提交。
- 不修改其他资源的 TSID、JWT、凭证格式、认证结果或管理员身份系统。
- 不内置网络审计厂商适配器、多租户权限框架、代理身份链或多数据库适配。

## Decisions

### 1. 十列存储模型

| 字段 | PostgreSQL 类型 | 必填/默认值 | 备注 |
|---|---|---|---|
| id | UUID | 必填，无默认 | UUIDv7，全局主键；生产方首次投递前生成 |
| source | VARCHAR(128) COLLATE "C" | 必填，无默认 | 稳定、可信配置的来源命名空间 |
| action | VARCHAR(128) COLLATE "C" | 必填，无默认 | 可扩展 UPPER_SNAKE_CASE 动作，不集中枚举所有业务 |
| result | SMALLINT | 可空，NULL | 1=SUCCESS、2=FAILURE；未知/不适用为空，零值不存储 |
| actor | JSONB | 必填，无默认 | 结构化主体 |
| targets | JSONB | 必填，[] | 直接操作资源数组 |
| context | JSONB | 必填，{} | 通用请求与关联信息 |
| data | JSONB | 必填，{} | 业务专属对象 |
| occur_time | TIMESTAMPTZ(6) | 必填，无默认 | 实际发生时间，不用 UUID 时间替代 |
| create_time | TIMESTAMPTZ(6) | 必填，clock_timestamp() | 当前存储首次入库时间，重复投递不更新 |

不设置 schema_version、ingest_time、update_time、软删除状态或 flow_id 专属列。VARCHAR 上限按字符计；source/action 仅接受 ASCII，因此字符数与字节数相同。source 非空，格式采用 [A-Za-z0-9][A-Za-z0-9._/-]*；action 采用 [A-Z][A-Z0-9_]*。写入层与数据库分别校验，超限不截断标识。

选择 UUID 原生类型而非编码文本，保留 16 字节存储和工具互操作性。对外输出标准小写带连字符字符串，不增加 e_ 前缀；生成器验证版本/variant，使用可靠随机源并处理时钟回拨。生产环境 Go 生成，PG17 不依赖原生 uuidv7()；不使用 gen_random_uuid() 的 v4 结果直接冒充 v7。

### 2. 主体、资源与上下文

actor 的字段为 type（必填）、namespace、id、name（可选）。type 使用 USER、SERVICE、SYSTEM、ANONYMOUS、UNKNOWN；已知主体必须同时提供 namespace/id，匿名和未知主体省略二者。namespace/id 各不超过 512 字节，name 不超过 256 Unicode 字符。主体的稳定身份为 namespace + id；type 为分类，不因角色变化生成另一主体。外部管理员使用 USER + 完整 issuer + subject，与消费者的命名空间保持隔离。旧 ADMIN 的 HTTP 投影依据认证事件的受控 actor_kind 快照恢复，而非猜测姓名或任意角色。

targets 每项为 type、namespace、id、可选 name，资源身份为 namespace + type + id；type 为 1..128 字节 ASCII UPPER_SNAKE_CASE，其余长度同 actor。数组允许为空、按完整引用去重、顺序无语义。context.related_resources 使用同样结构，只表示相关但未被直接操作的资源。未确认身份的输入目标不能被升级为 actor。

| context 字段 | 类型/限制 | 语义 |
|---|---|---|
| request_id | string，最多 128 字节 | 请求关联；accountkit 原有 64 字符规则不放宽 |
| trace_id | string，32 位小写十六进制且非全零 | 已存在的 W3C trace ID |
| span_id | string，16 位小写十六进制且非全零 | 存在时必须有 trace_id |
| client_ip | string，合法 IPv4/IPv6 | 宿主可信代理解析的地址 |
| user_agent | string，最多 2048 字节 | 可选，不新增默认采集 |
| device_id | string，候选上限 256 字节 | 不用于可信身份认定；实施前检查现有接受范围 |
| related_resources | object[]，最多 32 项 | 资源关联，不代替授权过滤 |

source 与身份/资源 namespace 各有含义。新增 Config.AuditSource、Config.AuditNamespace；环境读取按宿主 prefix 提供 AUDIT_SOURCE、AUDIT_NAMESPACE。本地未配置时分别采用 accountkit/<schema>、accountkit/<schema>/resources，仅承诺当前数据库实例内一致；多个数据库向同一中心汇集时必须显式指定全局可区分的稳定值。自定义 Recorder 模式要求显式提供二者，New 只做验证且不联网。已持久化的 namespace 不随部署名改变，变更需独立数据迁移。

为保留外部模式的显式配置判定，ConfigFromEnv 对这两个新字段保留原始空/非空输入，本地默认值在 New 内应用；Config.Validate 使用有效默认值检查而不改变原字段。其他既有配置字段继续原有默认化行为。宿主明确配置与本地默认字符串相同的值仍是显式配置，不能通过与默认字符串比较来猜测来源。

#### To C 案例一：单个 App 使用自建账号体系

一个记账 App 使用 accountkit 管理消费者账号，宿主将 Config.AuditNamespace 配置为稳定的 `urn:identity:moneybook:prod`。所有生产用户共用该 namespace，通过各自的 id 区分，无需为每个用户创建 namespace。以下 ID 和域名均为合成示例。

```json
{
  "type": "USER",
  "namespace": "urn:identity:moneybook:prod",
  "id": "u_0000000000001"
}
```

手机号、邮箱和微信是登录身份；同一 accountkit 账号切换或绑定登录方式时，actor 的 namespace 和本地用户 ID 保持一致。宿主在装配时配置一次，由事件构建器补齐每条事件，无需业务调用点反复填写。

#### To C 案例二：多个产品共用统一账号中心

商城和会员 App 使用同一个消费者账号中心，并在业务审计中沿用该中心的主体映射：

```json
{
  "type": "USER",
  "namespace": "urn:identity:example:consumer:prod",
  "id": "u_0000000000001"
}
```

| 产生事件的产品 | source | actor.namespace |
|---|---|---|
| 商城 | shop/production | urn:identity:example:consumer:prod |
| 会员 App | membership/production | urn:identity:example:consumer:prod |

source 区分事件来源，namespace + id 识别同一个用户，便于跨产品关联操作历史。只有实际共享同一身份体系及主体标识时才复用 namespace；两个独立账号库不能因为用户编号相同就使用相同的主体映射。这是宿主业务事件的映射示例，不要求 accountkit 生成订单或会员业务事件。

#### To C 案例三：直接使用外部 OIDC 主体

宿主 App 直接以已验证的 OIDC 主体作为用户身份时，namespace 使用完整 issuer，id 使用 subject：

```json
{
  "type": "USER",
  "namespace": "https://login.example.com/realms/consumers",
  "id": "8f42d719-3210-4c7a-9d21-6a2b405bc983"
}
```

namespace 和 id 来自验证通过的 iss/sub，不直接采信客户端声明，也不擅自改写 issuer 的大小写或尾部斜杠。如果外部身份最终映射为 accountkit 本地账号，则认证审计改用该本地账号的配置 namespace 和 `u_…`，使不同登录方式归到同一用户；不能仅因本次使用外部登录就切换审计主体。本案例说明通用宿主的身份映射，不新增 accountkit 消费者 OIDC 登录能力。

三个案例中的 namespace 均用于表示稳定的身份命名空间，可以是受控 URN 或身份提供方 URL，不要求可访问。它不是事件 source 或角色 role，也不继承 source 的 ASCII 格式规则；现有 namespace 长度限制仍适用。主体角色变化不改变 namespace + id，测试与生产的独立账号体系使用不同 namespace，已持久化的 namespace 变化仍需按前述迁移规则处理。

### 3. 业务数据与结构演进

data 采用 JSON 对象，可选 reason、changes；changes.<field>.before/after 是 JSON 值，省略与显式 null 区分。认证专属字段集中于 data.accountkit，包括 flow_id、identity_kind、subject_hint、仅作背景的 session_id、受控 actor_kind 和迁移时的 legacy_event_id。新增业务不需要改公共表或 enum.EventType。

历史管理员信息不完整时，专用历史映射入口以 UNKNOWN 主体加 actor_kind 快照表示，不伪造 namespace/id；已存在的单边 admin_issuer/admin_subject 可在 data.accountkit 的同名受控快照中保留，仍执行字段上限检查。旧投影仅在明确 legacy_event_id 路径恢复这些值。若保留 admin_username 展示快照，认证隐私清理必须同步删除；正常完整管理员事件从 actor 恢复信息，不重复存储这些快照。

直接调用领域服务时，调用者提供的 Principal 即使 user/session ID 格式合法，也不构成已验证主体证据。ReplaceIdentity 只有在 replacementPreflight 成功核验真实账号、活跃会话和身份归属后才设置 VerifiedSubject；更早的策略、近期认证或身份未变化拒绝事件保守使用 UNKNOWN，认证结果保持不变。本次不新增 Principal 可信来源机制。

accountkit 使用强类型事件构建器完成映射和脱敏，不能把任意请求体/claims/err.Error() 整体装入 data。通用 API 允许宿主自定义已校验 JSON 对象，避免公共结构硬编码全部业务字段。入队前验证并深拷贝可变对象，禁止调用方后续修改已排队事件。解码时拒绝重复 JSON 键和非法数值；默认完整事件不超过 64 KiB UTF-8 JSON，targets/related_resources 各最多 32 项。既有设备/管理员字段兼容边界须通过真实调用测试确定，不能静默截断或收紧 HTTP 输入。

2026-10-16 计划评审后明确兼容处理：认证映射器可省略无效 IP、超长不可信设备字段、超长自定义 request_id 和超长可选 name，并单独安全计数/告警；主体 namespace/id 不截断或哈希替换。已验证 issuer/sub 超过 512 字节时拒绝该审计事件并提供安全失败观测，认证结果不变。这是新审计输入边界的明确兼容变化，须写入调用迁移说明；历史准备遇到不兼容字段停止且保留原表。默认 request_id 的 64 字符规则不表示宿主自定义 resolver 过去已有相同上限。

不设置独立版本列：同一 action 只增加可选字段；类型、单位或语义变化使用新字段，动作含义变化使用新 action；确需格式版本的业务在自己的 data 命名空间内声明。消费者忽略不认识的可选字段，保留历史缺失语义。

### 4. 写入、去重与并发隐私处理

区分生产事件与已存储记录：生产事件不允许伪造存储生成的 create_time；已存储记录附带 create_time。Go 使用 UUID 值类型；具体依赖在实施步骤中核对并选定，公共 Event 的变更明确视为源码迁移。

生成 ID、验证和脱敏先于投递；单次生成失败只计数并丢弃，不改变认证结果。同一事件重投递保持 ID、occur_time 及原始逻辑内容。Store 以 id 唯一约束阻止并发重复，重复不更新 create_time；禁止无条件 upsert 覆盖。

去重比较采用明确规范化后的结构值（非原始 JSON 字节），统一时间精度、空对象和资源顺序。真正相同 ID 不同事件报告可识别冲突并记录安全计数，不重生成 ID 伪装成新事件。合法隐私清理字段单独列入规范：对已持久化脱敏记录比较双方经过相同清理规则的投影；忽略的仅是明确允许清理的字段，不忽略 action/source/主体稳定标识/资源引用/occur_time。已有行始终优先，不能恢复被删除的 name、client_ip、device_id、subject_hint 等。

本地迟到事件与 purge 并发另行处理：在同库事务中按稳定顺序锁定关联账号，复核 DELETED 后剥离隐私字段；插入先持锁则 purge 在其后清理，purge 先提交则插入先脱敏。尚无账号的 flow 事件必须使用与流程清理一致的 flow 锁顺序，锁后确认流程仍存在且归属有效；流程已消失时保守删除隐私字段。不能只做无锁存在性查询。先建立完整账号/flow 关联，再清理流程，保留已过期流程通过历史事件定位的既有能力。各事件被哪些账号影响，由认证映射器明确提供，不能从不可信 data 任意推断授权。

默认 Async 仍是有界尽力投递，保留 Written/Dropped/Failed 类观测；新增重复/校验/冲突分类不把重复计为新的成功写入。写入错误日志不再假定所有错误都来自 pgx，禁止直接记录宿主返回的可能含秘密的错误文本。

### 5. 宿主写读装配与权限边界

沿用 Deps.Audit 的记录器注入概念，事件参数升级；新增不依赖 sqlc 行的公开读接口和审计专用游标值。建议入口名 Deps.AuditReader，具体方法以领域资源引用和 source 范围查询，不让宿主实现管理 DTO。

| 装配 | 写入 | 读侧/维护 |
|---|---|---|
| 未注入 Audit/AuditReader | 库创建本地 Async + Store | 管理查询本地 Store，执行本地 retention |
| 注入 Audit 和 AuditReader | 宿主管理 | 管理查询外部 reader；库仅保留旧本地数据维护，不能删除外部记录 |
| 仅注入 Audit | 宿主管理 | 认证正常；审计查询显式 503 AUDIT_QUERY_NOT_CONFIGURED，不返回本地空列表 |
| 仅注入 AuditReader | 拒绝 New | 防止默认本地写入配外部读取而无明确来源 |

自定义模式的实例标识须显式配置。库管理自建 worker 的 Start/Close；宿主管理注入组件的生命周期，库不隐式调用任意接口的 Close。支持宿主自行用 Async 包装批量后端，但不会自动管理该对象。accountsvc 默认仍使用本地模式，本次不增加远端网络协议配置。

管理路由继续先验证管理员、角色、账号存在性，按本实例 namespace 和账号资源引用查询。外部 reader 只能返回匹配授权范围的认证事件；适配层复核 source/资源引用，不信任跨账号结果。读取失败返回固定安全 503，未知账号仍 404；列表权限和读取不产生审计事件的规则保持。用户列表游标不受影响。

### 6. 查询、HTTP 投影与索引

存储返回通用记录，管理面继续投影旧 event_type、actor_kind、result、reason、session_id、flow_id、identity_kind、subject_hint、IP/设备/管理员字段；不直接输出任意 data。缺失历史信息仍为原有 null/省略语义，不伪造身份。name 仍为 users/{user}/auditEvents/{id}，末段明确升级为 UUID。source 范围内的认证 action 保留旧事件名称，不能把不认识的业务 action 伪装为已有认证类型。

审计专用游标包含格式版本、查询范围和 (occur_time, UUID)，旧 e_ 游标返回原 400 INVALID_PAGE_TOKEN，提示重新开始分页；游标不是授权凭证。按 (occur_time,id) 稳定排序，不承诺并发迟到事件下的快照导出完整性。

基础索引为 id 主键、(occur_time,id)、(source,occur_time,id)。actor namespace/id 表达式、targets/related_resources GIN、request_id 和 data.accountkit.flow_id 表达式按必需查询增设；默认账号审计查询和 scrub 的计划须以真实 PostgreSQL 验证，不能以 GIN 支持过滤推断排序高效。索引不是增加业务专属列的理由。

2026-10-16 计划评审后明确 flow 关联：早期事件不追加后来发现的账号到原始资源引用；查询利用 live flow 或已保存的证明/完成归属事件构造账号关联闭包。公开读侧 Page 将 Records 与 FlowLinks 分开，FlowLink 携带 flow/user 引用及归属事件 Evidence；管理适配层检查同源、认可的归属动作及完整引用后才接受间接匹配事件，不把这些查询证据输出到 HTTP DTO。所有归属证据均丢失时不承诺重建历史。

实施接口补充：仍存在的本地流程可使用 FlowLink.Live 标记，不伪造 Evidence 审计事件。管理面对此必须通过库持有的领域查询回调，独立核对本地 identity_flow.user_id/proof_user_ids 归属；外部读侧的 Live 声明本身不构成授权。已删除流程必须使用真实保存的 Evidence；Live 与 Evidence 不混用，缺少回调或无法核对时按查询依赖故障拒绝。这样保留“归属事件尚未入库但 live flow 已有归属”的查询能力，同时不改原事件或新增主表字段。

多账号流程的查询隔离：flow 闭包只补回没有明确账号引用的早期事件；仅明确关联账号 B、没有关联请求账号 A 的事件，不能仅因共享 flow 就出现在 A 的列表。直接关联 A 的事件仍按原资源关系返回。SQL 查询与外部结果复核使用同一规则，并以 A/B 证明账号共享一个 flow 的场景验证。

### 7. 外部数据治理边界

外部存储的 retention 和清理由宿主管理，不在同步认证/匿名化事务中调用远端网络。文档提供账号/flow 资源关联与清理字段契约；同库宿主可以沿用传入的 pgx.Tx 做业务清理，跨库宿主自行提供持久化清理请求、重试和完成核验。仅收到异步 USER_PURGED 事件不等于外部数据已清理，也不能依赖可丢弃审计流作为唯一清理通知。

默认本地仍按 occur_time 和 AuditRetentionDays 清理；切换外部模式不自动迁移历史副本、不自动删除本地表，原保留与本地匿名化继续覆盖历史数据。脱敏后重复投递不得恢复隐私，删除后超过接收窗口的旧投递必须由宿主接收策略拒绝，不能宣称去重记录过期后仍永久幂等。

保留期按配置 source 限定生产实例，不要求事件包含消费者 namespace：匿名发码没有主体命名空间，ADMIN_FORBIDDEN 只有外部管理员命名空间，也应遵循该生产来源的保留策略。多个独立生产实例必须选择不同 source；同一 source 内的不同身份命名空间共享保留期。账号查询与定向匿名化仍按 source 和完整资源 namespace/id 过滤；身份命名空间不作为额外租户保留策略。

## Migration Plan

1. 在本变更评审后实施；先补行为测试和迁移预演。默认以 0002 作为下一版本，若实施时已有生产增量则使用下一未占用序号；测试专用迁移编号同步改为相对当前最新版本，不改历史快照。
2. 非空旧审计表采用受控预备转换：提供一次性内部 Go 工具，停止旧版本写入和维护后，在明确 schema 与最终 AuditSource/AuditNamespace 配置下准备转换 staging。为每个旧事件生成并持久化一个 UUIDv7 映射及新结构记录；存下原行快照供 SQL 验证，旧 ID 保留在 data.accountkit.legacy_event_id，旧缺失信息不推测。预备工具只准备 staging，不更改迁移版本或原审计行；重复准备复用映射，配置或原数据变化须拒绝并报告。
3. 新递增 SQL 在迁移锁内验证 staging 与全部旧行一一对应、原行未变、UUID/字段有效，事务性复制并切换表、建立约束/索引、清理 staging。新 create_time 表示迁移入新存储时间，occur_time 保留；未知未来枚举不能被静默丢弃。空旧表直接升级无需 staging；非空缺少完整准备时失败，禁止自动丢弃数据或改版本绕过。
4. 升级失败按现有 dirty 规则人工处置，不自动 Force。staging 的寿命、敏感数据权限与清理纳入操作说明；只有成功迁移才删除，不写真实审计快照到 Git 或普通日志。UUID 映射在成功切换前后均可核对，重跑不二次分配。
5. baseline 检查按 version 分别验证冻结旧结构和新结构，不再对高版本无条件要求 flow_id 列；旧撤回基线仍拒绝。空库顺序应用 0001 和新增量到最新版本，固定初始 SQL/hash 不变。
6. 新应用与新表共同部署，更新 Recorder、Reader、配置、审计资源名和游标消费；旧应用不能直接运行新表。恢复方式为独立空库还原升级前备份并核验后切换，或前向修复；不提供破坏性 Down 或零损失回退承诺。
7. 交付更新 README、docs/compatibility.md、docs/migrations.md、必要的 AGENTS 审计例外、sqlc 生成结果、必需测试清单及 verification.md。普通账号/身份/会话/流程 ID、密文、令牌和认证行为保持。

## Risks / Trade-offs

- [JSONB 过滤与时间排序成本] → 按真实账号规模检查 EXPLAIN 和并发清理，必要时采用可重建关联索引；不预先扩展为复杂多表审计平台。
- [新写入模型和 UUID 的破坏性变化] → 明确发布迁移说明、旧 ID 映射、独立审计游标和非审计协议回归。
- [非空升级需要准备窗口] → 选择可核对的 staging 转换，避免 PG17 自定义随机 SQL 与身份来源猜测；预演中记录窗口和容量。
- [匿名化后迟到/重放] → 同库锁序与投影去重验证；外部宿主承担持久化清理和接收窗口，禁止假称跨库原子。
- [source/namespace 默认值跨库冲突] → 外部模式要求显式稳定配置，迁移配置一致性校验；namespace 不等同于租户授权。
- [泛型 metadata 泄露] → 强类型认证映射器、限制与脱敏测试，HTTP 只输出明确投影，宿主错误不原样记录。

## References

- [RFC 9562 UUIDv7](https://www.rfc-editor.org/rfc/rfc9562.html#section-5.7)：标识布局及趋势排序边界。
- [PostgreSQL 17 字符类型](https://www.postgresql.org/docs/17/datatype-character.html)：VARCHAR 长度语义与存储成本。
- [PostgreSQL 17 JSONB](https://www.postgresql.org/docs/17/datatype-json.html)：结构校验与索引能力。
- [WorkOS Audit Logs](https://workos.com/docs/audit-logs)：actor/action/targets 事件建模参考，非其内部 SQL 表结构声明。
- [OpenID Connect 主体稳定性](https://openid.net/specs/openid-connect-core-1_0.html#ClaimStability)：issuer 与 subject 的身份边界。
