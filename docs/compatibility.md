# 功能与兼容对照

本文列出 v1.0.0（候选）的能力对照与长期契约边界：调用者须知、宿主账号契约、离线导入、通用审计接口和资料属性补丁语义。v1.0.0 之前各开发期版本之间的升级说明已退役，开发期版本只在[开发期版本记录](releases/pre-1.0-history.md)中留存摘要。HTTP 字段、状态码与配置以 [README](../README.md)、[OpenAPI 契约](openapi.md)和公开 Go 类型为准。

## v1.0.0 调用者须知

下列开发期符号在 v1.0.0 中移除或调整，旧调用需要按右列迁移。前六项只替换名称，其他项涉及公开边界或签名变化；不保留旧别名。

| 已移除符号 | 替换 |
|---|---|
| `ids.IdentityFlow` | `ids.SignInCompletionFlow` |
| `user.Deps.IdentityFlowContext` | `user.Deps.SignInCompletionFlowContext` |
| `(*user.Service).CleanupIdentityFlows` | `(*user.Service).CleanupSignInCompletionFlows` |
| `sender.SMSSender` | `sender.SMSCodeSender` |
| `sender.EmailSender` | `sender.EmailCodeSender` |
| `sender.Message` | `sender.CodeMessage` |
| `user.KeyBackfillBatchSize` | 无替换：密钥回填的内部批量大小，不可配置 |
| `user.PurgeBatchSize` | 无替换：到期匿名化的内部批量大小，不可配置 |
| `authentication.MapLegacy` | 不再提供开发期事件转换；在线认证事件使用 `Recorder` / `Map`，通用事件使用 `audit.Event`；既有私有快照的读取兼容保持 |
| `ids.AuditEvent` | 审计事件使用 `uuid.NewV7()`；不再用 `e_` 作为审计资源 ID |
| `code.ErrExhausted` | 移除不可达哨兵；实际最后一次错误尝试仍为 `ErrInvalid`，之后为 `ErrExpired` |
| `(*audit.Store).ListByUser` | 使用 `ListByResource(ctx, audit.Query{Source: source, Resource: audit.Resource{Namespace: namespace, Type: "USER", ID: userID}, After: after, Limit: limit})`，source/namespace 与记录器配置一致 |
| `audit.RetentionBatchSize`、`user.IsUniqueViolation` | 不再公开内部批量参数和数据库错误 helper；宿主诊断自身数据库错误可使用 `errors.As` / `pgconn.PgError`，业务冲突仍用领域错误 |
| `user.PurgeCoveredTables`（map 变量） | 改为 `user.PurgeCoveredTables()`，每次返回独立登记副本；修改副本不能改变库登记，也不改变匿名化 SQL |
| 旧 `idp.NewWeChat(apps, baseURL, httpClient, logger)`、`NewWeChatWithConfig` | 改为 `idp.NewWeChat(idp.WeChatConfig{Apps: apps, BaseURL: baseURL, ...}, httpClient, logger)` 并处理返回的 error；subject 模式与应用配置需按原配置明确提供 |
| `smtp.LegacyChineseRenderer` | `smtp.ChineseRenderer`；品牌、四用途标题/正文和 `smtp.New` 的默认行为保持 |
| `grace.Pair.AccessExpiresAt`、`RefreshExpiresAt` | `AccessExpireTime`、`RefreshExpireTime`；JSON `ae`/`re`、缓存加密和过期语义保持 |
| `Repo.Q`、`Repo.WithTx`、`Repo.WithTxRaw` | 不再提供原始查询/事务访问；使用 `user.Service` 领域操作，联合业务写入使用 `WithActiveUsers`、`BeforeDelete`、`Anonymizer` 等既有事务契约，不能以直接 SQL 替代领域约束 |
| `user/db`、`audit/db` | sqlc 模型已内部化，无公开替代包；按使用场景改用领域 DTO、服务或 `audit.Reader` |
| `httpapi/jsonbody`、`httpapi/reqid` | HTTP 内部工具不再供外部导入；宿主负责自身协议解析，通过 `Deps.RequestID` 提供可信请求 ID |

`accountkit.Deps.SMSSender` / `EmailSender` 与 `user.Deps.SMS` / `Email` 是字段名，不在移除之列。

`user.Repo` 与 `NewRepo(pool)` 继续作为直接 `user.NewService` 的不透明装配依赖，`Deps.Codes/Revocation/Grace` 及两种 `CheckKeyVersions` 保留。独立迁移工具可检查密钥而不装配发送器或提供方。内部账号、身份与会话事务使用 READ COMMITTED；宿主自身连接池和业务事务的配置不被改写，传入领域回调的事务不得自行提交、回滚或另开连接替代。

IdP 统一调用入口是 `Provider.Verify(context.Context, json.RawMessage)`。具体 `VerifyApple`、`VerifyGoogle`、`VerifyIDToken` 等方法继续表达各协议的强类型凭证；构造器的错误返回与配置复杂度有关，不机械统一。Apple/Google/Facebook/OIDC 不返回 error 的构造器不承诺完整启动前校验，缺少 nonce registry 等依赖仍可能在验证时返回 `ErrMisconfigured`；根 `Config.Validate` 的部署校验职责保持。

公开 Go API 自 v1.0.0 起受语义化版本约束，后续破坏性变化须发布新主版本并改用 `/v2` 模块路径。数据库结构变化从 `0002` 迁移递增，见[迁移手册](migrations.md)。

## 宿主账号契约

`BatchPublicProfiles(ctx, ids)` 是 Go 批量查询，结果仅含 ID/DisplayName/AvatarURL/State。输入去重，非法 ID 返回参数错误，未知账号略去，待注销及已删除账号清空显示名和头像；宿主在调用前按业务权限过滤 ID。无任意用户资料 HTTP 端点。

`Deps.BeforeDelete` 在账号锁和状态检查后、改变状态前使用同一 `pgx.Tx`，消费者和管理员共享。返回 `user.ErrDeletionBlocked` 为 400 `FAILED_PRECONDITION`、reason `DELETION_BLOCKED` 和固定消息；其他错误为固定 500，同批账号、会话和业务写入回滚。其他回调错误不进入库的业务错误分类，即使包装 `ErrNotFound` 等哨兵也不会变成 404；调用者不要依赖对这些底层错误的 `errors.Is`/`errors.As`，明确业务拒绝使用 `ErrDeletionBlocked`。

`WithActiveUsers(ctx, ids, fn)` 要求非空账号集合和非 nil 回调，先验证/去重/排序锁定 ACTIVE 账号，再执行宿主回调；锁序为账号后业务资源。库拥有提交/回滚，回调不得自行结束事务；回调错误按原事务契约返回，panic 回滚并向上传递。未知或非 ACTIVE 账号拒绝，回调不运行。

## 离线账号导入

`ImportAccounts(ctx, tx, accounts)` 只用于受控停写工具，事务来自 schema 匹配的认证连接池。库不另开连接、不提交、不回滚；调用者负责空目标核验、批次记录、重复执行、业务引用迁移和最终提交。发生任何错误时必须回滚整个批次，不继续提交部分成功数据。

`user.ImportAccount` 保留合法 `u_` ID 和原创建/更新时间；账号和锚点创建/更新时间必需非零，允许历史时钟回退，不强制更新时间晚于创建时间。ACTIVE 至少包含一个 `user.ImportAnchor` 的 PHONE/EMAIL 合法目标，删除/匿名化时间为空；DELETED 只允许无显示名、无锚点的匿名墓碑，可保留删除/匿名化时间，不重新抢占旧地址。FROZEN、PENDING_DELETION、第三方身份和非法目标拒绝。归一化和版本化加密/摘要沿用库配置；重复 ID 或归一化身份冲突失败，不合并、不静默跳过。

普通创建和 ACTIVE/DELETED 导入的资料属性均为空对象 `{}`，复用账号原时间。`ImportAccount` 不接收用户资料属性；ACTIVE 导入提交后可另调 `PatchProfileAttributes`，DELETED 墓碑拒绝写入。导入和补丁是两个独立调用，不提供同批事务原子性保证。

导入先校验并归一化整批输入，再在写入前对整批 PHONE/EMAIL subject 按全部摘要版本一次性排序加事务级锁，因此任一账号的输入错误会先于任何数据库写入或冲突返回；误并行的批次即使身份顺序相反也只会排队，后到者得到身份冲突而不是死锁。锁持有到调用者事务结束，同一事务内的其他写入照常进行。

导入错误的 `Error()` 文本脱敏，身份冲突可用 `errors.Is(err, user.ErrIdentityConflict)` 分类；由唯一索引兜底发现的冲突（如与不经导入锁的并发写入相撞），`errors.As` 仍能获取数据库诊断。解包后的诊断可能包含原始详情，不能直接写入日志或对外输出。

此入口不发验证码、不创建会话、不签令牌、不调用 IdP，也没有导入 HTTP 路由。宿主停写、引用迁移和批次幂等在宿主工具中完成，不属于 accountkit 的认证行为。

## 通用审计公开接口

审计契约定义在叶子包 `audit/event`，`audit` 包以类型别名导出同一组类型；认证专用映射位于 `audit/authentication`。审计事件 ID 是 UUIDv7，账号、身份、会话与流程沿用 `u_`/`i_`/`s_`/`f_` 资源 ID。宿主侧装配方式见 [README 宿主审计装配](../README.md#宿主审计装配与清理责任)。

### 事件生产与写侧

`audit.Event`（即 `audit/event.Event`）包含 `ID uuid.UUID`、`Source`、`Action string`、`Result *enum.Result`、`Actor`、`Targets`、`Context`、`Data json.RawMessage`、`OccurTime`；`audit.RecordView` 在事件外增加存储方首次写入时生成的 `CreateTime`。存储为十列，认证专属快照位于 `data.accountkit`，不设 `user_id/session_id/flow_id/identity_kind` 专属列或公共 schema_version。

认证事件使用 `audit.AuthenticationEvent`（`audit/authentication.Input`）和 `audit.NewAuthenticationRecorder`。直接生产通用事件的宿主先用 `uuid.NewV7()` 生成 ID，设置实际发生时间及可信 source，调用 `audit/event.Freeze` 校验、规范化并冻结可变值，再交给 `Recorder.Record(ctx, event)`。认证输入已有 UUIDv7 时映射器保留，缺失时首次生成；重投递保持 ID、OccurTime 和逻辑内容。生成或校验失败不改变认证动作结果。

`Inserter.InsertBatch` 返回 `(audit.BatchResult, error)`，分别统计 Written、Duplicate、Failed、Invalid、Conflicts。同一 ID 的同一事实不新增记录、不更新 CreateTime；真正冲突返回可识别的 `audit.ErrConflict`，不能重新生成 ID 或 upsert 覆盖。合法清理后的重放不能恢复已删除隐私。宿主记录器必须自行冻结输入、避免阻塞业务路径并提供安全的失败观测；默认 `audit.Async` 在入队前冻结并有界批处理，但不提供持久化队列或可靠投递。

source/action 最长 128 个 ASCII 字符，source 匹配 `[A-Za-z0-9][A-Za-z0-9._/-]*`，action 匹配 `[A-Z][A-Z0-9_]*`；非 nil Result 只能是 SUCCESS/FAILURE。已知主体为 namespace + id，资源为 namespace + type + id；namespace/id 各最多 512 字节，name 最多 256 个 Unicode 字符。完整事件不超过 64 KiB（`event.MaxEventBytes`），targets/related_resources 各最多 32 项（`event.MaxResources`），超限和重复 JSON 键拒绝，不截断稳定标识。

业务内容规则统一由 Go 校验：默认 `Async.Record` 在入队前调用 `event.Freeze`，本地 `Store.InsertBatch` 在写入前独立调用同一入口，Go 脱敏投影写回前也经过校验。数据库只保留 UUIDv7、JSON 基本形状、非空、列类型、默认值、主键和索引，不用自定义函数校验主体、资源、上下文、事件大小或业务时间范围。直接 SQL 可以写入形状合法但业务内容非法的数据；导入和修数必须复用应用校验，读侧仍可能拒绝这些非法记录。

认证映射可省略无效 IP、超长设备字段、自定义 request_id 或可选 name，并单独安全计数/告警；这些边界不收紧认证 HTTP 输入。已验证管理员 issuer/sub 超过 512 字节时拒绝该审计事件并安全观测，认证结果不变。库不再提供开发期事件写侧转换或旧库自动升级；在线认证映射不生成 `legacy_event_id`。既有带该标记的私有快照保持原读取及隐私清理兼容，这不表示存在真实生产历史数据或提供旧库迁移承诺。

### 配置、所有权与读侧

本地默认 source/namespace 分别为 `accountkit/<schema>`、`accountkit/<schema>/resources`。`ConfigFromEnv(prefix)` 对 `AUDIT_SOURCE/AUDIT_NAMESPACE` 保留原始空值，`New` 才应用本地默认；`Config.Validate` 检查有效默认而不修改调用方字段。注入 `Deps.Audit` 时必须显式提供 `Config.AuditSource/AuditNamespace`，允许明确填写与默认字符串相同的值。直接构造 `user.NewService` 时，每个缺失值分别从 Repo 池配置的首个 search_path schema 推导，池为空或首项为空回退 account；直接服务不判断外部接管，跨库中心审计仍须显式配置二者。

`audit.NewStore(pool)` 同样只推导本地默认；自定义配置使用 `audit.NewStoreWithOptions(pool, audit.StoreOptions{Source: source, Namespace: namespace})`，与事件生产方保持一致。namespace 表示稳定身份体系，可以是受控 URN 或完整 issuer URL，无需可访问；source 区分事件来源，role 和显示名不构成稳定身份。实际共享同一主体体系时才复用 namespace，独立数据库汇聚时须明确隔离；已持久化记录的 namespace/source 一经变化，须另行处理既有记录。消费者应用案例见[通用审计设计](../openspec/changes/archive/2026-10-17-generalize-audit-events/design.md#to-c-案例一单个-app-使用自建账号体系)。

`Deps.AuditReader` 与管理适配器的读侧使用 `audit.Reader.ListByResource(ctx, audit.Query) (audit.Page, error)`。`Query` 指定 Source、完整 Resource、可选 Actions、After 和 Limit；Reader 按 `(occur_time, UUID)` 严格升序返回不超过 Limit 的 RecordView，所有记录严格大于 After，CreateTime 必须非零且为可序列化的有效时间。不得重复 UUID，包括同 UUID 不同发生时间；管理面复核全页及用于判断下一页的额外记录。

`Actions` 为空表示该资源的全部动作，通用 Reader 可查询业务事件；非空集合必须在排序和 Limit 前过滤。`Query.NormalizedActions()` 校验动作标识、返回排序去重的独立副本，不修改调用方输入，不将小写或空白改写为合法值。管理路由固定使用 `authentication.Actions()` 导出的完整认证动作集合，不提供业务审计 HTTP 接口或客户端动作筛选参数。宿主 Reader 必须执行这一条件；返回值违反条件时 HTTP 整页拒绝为 503，不先取页再静默跳过业务事件。`FlowLinks` 的归属证据独立查询，即使其动作不在记录筛选集合中也须保留必要证据。

默认无注入时库管理本地写读；注入 Audit + AuditReader 时管理查询走宿主读侧；仅注入 Audit 时已授权审计查询返回 `503 AUDIT_QUERY_NOT_CONFIGURED`；只注入 Reader 在 `New` 被拒绝。读侧故障、越界记录、顺序或记录结构不合规返回安全的 `503 AUDIT_QUERY_UNAVAILABLE`。`ADMIN_NOT_CONFIGURED`、角色检查、目标账号存在性检查和独立管理员身份系统不受审计配置影响。

查询范围是本来源、本 namespace 下账号作为主体、直接目标或关联资源的认证事件。只引用流程而未引用账号的记录由 `Page.FlowLinks` 提供归属证据：Evidence 须为同源、成功的 `SIGN_IN_COMPLETION_PROOF_VERIFIED` 或 `SIGN_IN_COMPLETION_COMPLETED`，并有可复核的 USER/FLOW 资源引用；`Live=true` 时 Evidence 必须为空，由管理面调用领域 FlowOwner 独立核验。已明确归属其他账号的事件不能通过共享 flow 混入结果。资源后来被删除不使有效证据失效；证据不直接序列化，任意业务 data 不进入认证 HTTP DTO。本地 Reader 的 `FlowLinks` 只覆盖当前页所需的去重流程归属，不是账号全部流程清单。

库只管理自建 worker，不会启动、刷新或关闭宿主组件；宿主先排空业务、关闭 Auth，再关闭自建审计组件与连接。三种可编译装配见 [examples/audit](../examples/audit/main.go)，默认嵌入宿主见 [examples/embedded](../examples/embedded/main.go)。示例中的宿主 Store 与认证同库，远端 Recorder/Reader 由宿主实现；不能把阻塞网络调用直接放入 Record 并声称不影响认证。

### HTTP 资源与游标

审计资源 name 的末段是标准小写带连字符的 UUIDv7，不带前缀，例如 `users/u_0000000000001/auditEvents/0199ed00-0000-7000-8000-000000000001`。认证 DTO 提供 event_type、actor_kind、用户/会话关联及受控管理员快照；result 为 `SUCCESS`、`FAILURE` 或 JSON `null`，其他可空字段输出 null；任意业务 JSON 不原样暴露。

审计列表使用独立的游标，绑定 source/resource、规范化的认证动作集合和 `(occur_time, UUID)`，客户端将其视为不透明字符串。格式不符或跨账号、来源、动作集合的游标返回 `400 INVALID_PAGE_TOKEN`，客户端清空 page_token 从第一页重新开始；认证动作枚举扩展导致动作集合变化时同样须重新开始分页。用户列表等其他游标各自独立，不能通用解码或当作授权凭据。

### 外部隐私与模式切换

本地匿名化保留事件事实及稳定资源引用，清理 actor/targets/related_resources 的 name、context 中 client_ip/user_agent/device_id 和 `data.accountkit.subject_hint`，保留 request/trace/span 关联；覆盖账号和流程历史，迟到及重放不能恢复隐私字段。外部设施须实现对应的资源关联、保留期和清理完成核验。

宿主必须建立独立于尽力审计的持久化清理流程，保存待清理资源、失败重试和完成状态。同库匿名化回调可用传入的 `pgx.Tx` 写入宿主自己的清理任务，不可等待远端服务或另开连接提交同批数据。只收到 `USER_PURGED` 不构成完成证据；清理未完成时不得声称外部副本已匿名化，也不能回滚已提交的本地事务或丢弃清理责任。库不内置可靠消息/outbox，也不承诺跨库原子提交。

切换到外部写读不会自动搬移或删除本地已有记录，本地保留和匿名化继续覆盖配置范围内的这些记录；外部历史回填须独立受控执行。改变 source/namespace 前须安排原范围的持续清理，不能用新配置下查不到记录证明原有副本已删除。外部备份、副本及回填数据也须纳入宿主治理。

## 资料属性补丁语义

资料属性只经 Go 接口和本人 `/users/me/profile` 路由读写，不进入其他账号或认证 HTTP 响应。直接调用 Go 方法时宿主负责目标账号与字段授权，包含父对象删除和数组整体替换；Schema 的 `readOnly`、`writeOnly`、`default` 等仅是注解，不实施权限或填充值。接口与本人路由见 [README 可选用户资料属性](../README.md#可选用户资料属性go-与本人-http-接口)。

补丁遵循 RFC 7396，顶层必须是对象。对象成员的 `null` 删除该成员；数组整体替换，其中及其对象成员不能保留 `null`。递归对象补丁保留未提及成员：对已有对象提交 `{}` 保留内容，对缺失对象提交 `{}` 则创建空对象并检查子字段 `required`。删除父对象可以删除整个子树；删除不存在但已经声明的属性无变化。未知属性即使以 `null` 删除也拒绝，数组内对象的字段同样检查声明目录。合并后始终验证完整资料，包括未改动部分和条件约束。

Schema 固定为 Draft 2020-12 的受支持范围：主结构树每层对象声明固定 `properties` 与 `additionalProperties: false`，节点具有单一明确类型；数组声明单一 `items`。条件、组合及其本地复用定义可使用仅含约束或布尔的 Schema，但只约束主树已有字段，不引入新字段或改变结构类型。仅支持本文件 `$defs` 中无环引用，不加载远程或本地文件资源；未知关键字、未知 format、动态属性、类型联合和嵌套 `$id` 在初始化时拒绝。format 为 `date`、`date-time`、`email`、`uri`、`uuid`，均启用断言；pattern 使用 Go `regexp` 子集。

Schema 原始输入限 64 KiB、32 层；展开后的主树属性声明限 128，验证节点限 1024。同一属性的条件约束不增加声明计数，主树不同位置复用的结构按各位置计数。补丁原始输入与规范化最终资料分别限 16 KiB、深度 8、512 个值节点（含根和容器）。重复键、尾随文档、非法 Unicode、NUL 和 PostgreSQL JSONB 无法存储的数字均拒绝；资料不保存 JSON `null`。数字精确处理，`1`、`1.0`、`1e0` 按相同值比较。

宿主保证同一数据库实例的 Schema 配置一致。库不保存逐行 Schema 身份，不提供 SchemaMismatch 错误。读取只检查通用存储边界；更新按当前 Schema 校验完整合并结果，无变化请求也不得绕过。新增可选属性、放宽约束、修改注解可直接更新配置。收紧规则可能使已有资料的完整结果不合法，此时更新拒绝且原资料与账号 update_time 保持；同次补丁可修复后保存。移除声明属性后，遗留属性仍不能以未知属性 null 删除，须通过过渡 Schema 或专项数据处理清理；库不自动丢弃字段。设计与验证见[变更设计](../openspec/changes/archive/2026-10-21-add-schema-validated-user-extensions/design.md)及[验收记录](../openspec/changes/archive/2026-10-21-add-schema-validated-user-extensions/verification.md)。

冻结、解冻、注销冷静期与恢复保留资料。最终匿名化在原事务中清空 JSON，复用账号原时间语义，不区分曾使用与未使用；配置移除或 Schema 不兼容不阻断清理，回调失败整体回滚。

资料路径的校验器及存储错误不保留能恢复原始资料的底层错误链，审计动作 USER_PROFILE_ATTRIBUTES_UPDATED 只保存固定服务 actor、账号目标、请求 ID 等受控事实，不保存资料值、补丁、属性名、Schema 版本、指纹、资料时间元数据或 Schema 全文。导入和匿名化错误保留诊断 cause；宿主匿名化回调不得在 error 中拼接资料或隐私数据，维护日志可能记录该错误。

## 功能对照

| 规格能力 | 生产入口/路径 | 验证 |
|---|---|---|
| 独立消费、完整功能 | 根门面、全部子包、sqlc.yaml | `GOWORK=off` 独立构建、[examples/embedded](../examples/embedded/main.go) 宿主编译 |
| 手机/邮箱验证码、限流 | user/code、user/service_signin.go、httpapi/enduser | user/code/store_test.go、user/service_test.go、httpapi/enduser/signin_test.go、TestConsumerEndToEndAgainstRealDB |
| 验证码 HMAC 轮换 | user/code/store.go、user/code/challenge_scripts.go，库及 accountsvc 共用 | 多版本状态/并发测试、TestCodeKeyRotationIntegration、TestCodeRotationRedisIntegration、TestServiceIntegrationCodeKeyRotation；见[轮换验收记录](../openspec/changes/archive/2026-10-04-harden-code-key-rotation/verification.md) |
| 微信/Apple | user/idp、user/service_idp.go | user/idp/wechat_test.go、apple_test.go、nonce_test.go、user/service_idp_test.go、TestWeChatSignInEndToEndAgainstRealDB |
| Facebook 登录（Limited/Classic） | user/idp/facebook.go、user/service_idp.go、config.go | user/idp/facebook*_test.go、user/service_facebook_test.go、httpapi/enduser/signin_facebook_test.go、config_facebook_test.go、internal/accountsvc/config_facebook_test.go |
| 支付宝 App 极简登录与空字段补填 | user/idp/alipay*.go、user/service_idp.go、user/service_signin_completion.go、config.go | user/idp/alipay*_test.go、user/service_alipay_test.go、config_alipay_test.go、accountkit_alipay_test.go、accountkit_alipay_http_test.go、internal/accountsvc/config_test.go；真实 App/应用分组属于宿主接入清单 |
| Facebook 数据删除入口 | accountkit.go `HandleFacebookDataDeletion`、user/service_provider_deletion.go、user/idp/facebook_signed_request.go | accountkit_facebook_test.go、user/service_provider_deletion_test.go、user/idp/facebook_signed_request_test.go、audit/authentication/authentication_test.go |
| 身份绑定解绑 | user/service_identity.go | user/service_identity_test.go、httpapi/enduser/identities_test.go、TestIdentityBindingEndToEndAgainstRealDB |
| 同类身份换绑 | user/service_identity_replacement.go、httpapi/enduser/replacement.go | 领域回滚/竞态、嵌入式及服务 E2E，见[换绑验收记录](../openspec/changes/archive/2026-10-03-add-identity-replacement/verification.md) |
| JWT、刷新、会话、重新认证 | tokens、session、user/service_session.go | tokens/tokens_test.go、session/*/*_test.go、user/service_test.go、TestConsumerEndToEndAgainstRealDB |
| 资料、注销恢复、冻结 | user/service_me.go、user/service_lifecycle.go、user/service_admin.go | user/service_lifecycle_test.go、user/service_admin_test.go、TestAccountLifecycleEndToEndAgainstRealDB |
| 管理接口、角色、审计 | httpapi/admin、audit | httpapi/admin/*_test.go、audit/*_test.go、TestAdminSurfaceEndToEndAgainstRealDB |
| 加密、旧密钥、维护 | pii、maintenance、user/service_rekey.go | pii/pii_test.go、maintenance/runner_test.go、user/service_rekey_test.go、TestKeyRotationBackfillEndToEnd |
| 业务同事务匿名化 | anonymize、user/service_purge.go | user/service_purge_test.go、TestHostContracts（宿主先写入再失败） |
| 产品隔离 | Config.Schema/KeyPrefix、JWTIssuer/JWTAudience | TestIndependentInstances（故意复用 ID、refresh、目标与 HMAC 密钥暴露缺少命名空间） |
| 包内 schema/版本/重复迁移 | migrations、Auth.Migrate | migrations/migrations_test.go、TestConsolidatedInitialSchema、TestUpgradePreservesDataAndIsRepeatable、TestFailedMigrationBlocksRetry、TestVersionBeyondSourceRejected；规则见[迁移手册](migrations.md) |

HTTP 请求响应、状态码、错误码和环境配置约定见 README；当前必需测试选择器以 `scripts/required-tests.txt` 为准。Redis 吊销检查 fail-open、审计失败不阻断、刷新宽限故障拒绝请求等失败语义见 [README Redis 键与失败模式](../README.md#redis-键与失败模式)。第三方真实发送和设备联调由宿主负责。
