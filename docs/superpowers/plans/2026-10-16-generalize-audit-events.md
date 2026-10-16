# 通用审计事件 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 accountkit 的审计升级为十字段 UUIDv7 通用事件，保留认证语义并支持宿主统一存储、查询与数据治理。

**Architecture:** 通用生产事件与持久化记录分离；认证输入经专用映射器转换，在首次投递前生成 UUIDv7。默认本地 Store/Async 处理去重、授权关联查询和迟到脱敏，宿主可替换写读；按最新用户指令直接在 0001 定义十列审计，不新增生产迁移或旧表转换工具。

**Tech Stack:** Go 1.26.5、PostgreSQL 17、pgx/v5、sqlc 1.31.1、Chi、miniredis、golang-migrate；计划采用 github.com/google/uuid v1.6.0 的 UUID/NewV7，并在任务一核验依赖及 pgx/sqlc 互操作。

**Spec:** [变更设计](../../../openspec/changes/generalize-audit-events/design.md)、[通用事件](../../../openspec/changes/generalize-audit-events/specs/generic-audit-events/spec.md)、[宿主集成](../../../openspec/changes/generalize-audit-events/specs/audit-host-integration/spec.md)、[兼容](../../../openspec/changes/generalize-audit-events/specs/embedded-auth-package/spec.md)、[存储迁移](../../../openspec/changes/generalize-audit-events/specs/versioned-auth-storage/spec.md)。本计划只定义实施接口和顺序，规格仍为需求来源。

**Inventory:** [26 种认证事件、字段兼容边界与锁序盘点](2026-10-16-generalize-audit-events-inventory.md)。其中锁图是需要并发测试验证的推演，不代表已复现或修复。

## Review Decisions

以下具体处理方案已于 2026-10-16 经用户“继续”确认，同步到 change 设计及受影响规格后实施：

1. 认证映射器对不可信或可选上下文采用安全省略规则：无效 IP、超长补全设备字段、超长自定义 request_id、超长可选 name 可省略，并单独观测；稳定主体 namespace/id 不截断、不哈希替换。已验证 issuer/sub 超过 512 字节时按明确的新审计输入边界拒绝该事件、记录安全计数/告警，认证结果不变。显式历史映射遇到不兼容字段拒绝。该上限影响此前合法宿主输入，必须在迁移说明中作为明确的兼容变化声明。
2. 早期 flow 事件保持原始主体和资源引用不可变；账号关联由 live flow 及已保存的归属事件形成查询闭包。外部读侧分页结果对仅通过 flow 匹配的事件同时提供具有该账号/flow 引用的归属事件证据，由管理适配层核对来源、动作和关联，避免把直接不含账号的合法事件误拒绝。该机制保留当前可用关联，不承诺在所有归属证据均丢失后重建历史。
3. 用户最新回复“继续，但不需要新增数据库迁移，直接修改原始数据库定义即可”：接受保留已审查的运行时代码及已披露流程偏差；将先前 3A 的增量/staging 方案替换为 Task3C 原始定义调整，不重做已通过的模型和运行时任务。后续功能仍先建立真实失败测试。

## Global Constraints

- GOWORK=off；不提交本地 replace、秘密、真实数据或测试输出。
- 主表固定 id/source/action/result/actor/targets/context/data/occur_time/create_time；原生 UUIDv7，source/action VARCHAR(128) ASCII，result 仅 1、2 或 NULL。
- actor 身份 namespace + id；资源身份 namespace + type + id；已知主体 namespace/id 各最多 512 字节，name 最多 256 Unicode 字符。
- 完整事件最多 64 KiB，targets/related_resources 各最多 32 项；拒绝非法/超限值，不截断身份。
- 不设置公共 schema_version；认证字段 data.accountkit；不引入可靠消息、任意业务 HTTP 审计 API 或跨库事务承诺。
- 本次明确授权修改当前 0001 审计定义及固定校验，不新增生产迁移；固定源 SQL/manifest、已发布 tags 原字节不变；不 Force、不自动清库，旧同号结构拒绝；sqlc 生成目录不手改。
- 写失败、生成失败、队列满不改变认证结果；默认有界异步及组件所有权保持。
- 管理认证和消费者分离；旧 HTTP 投影保持字段语义，审计 UUID/游标显式升级，其他 ID/令牌不变。
- 按任务先写并实际运行失败行为测试，再实现，通过相关测试后派独立 reviewer；任务有实质问题时先修复再执行依赖步骤。
- 项目默认使用子代理分任务实施与审查；当前用户要求在新 feat 分支 apply，当前工作区使用 feat/generalize-audit-events，不额外创建未经同意的 worktree。

## Review Focus

- JSON 对象重排、时间精度与目标数组重排不得制造假冲突：任务一/三验证规范化与数据库 round-trip。
- 已脱敏事件原副本重放不得恢复隐私，真正标识冲突不可被脱敏比较吞掉：任务三验证白名单差异与不变字段。
- 同一 flow 关联多个用户，purge/完成/writer 不得倒置 user → flow 锁序：任务四以确定性并发屏障验证。
- 外部 reader 返回越界/无序/超量记录不得泄露或错误生成游标：任务六验证完整结果后才写响应。
- 原始 0001 直接安装通用审计；旧同号 v1 和旧 v2 不得被误接管，历史标签检查不可放宽：任务三验证 baseline 和历史格式。

## File Structure

| 范围 | 文件 | 责任 |
|---|---|---|
| 通用模型 | audit/event/、audit/audit.go、async.go | UUID 值、结构校验/副本、Record/RecordView、批量统计；先叶子包构建再切换门面 |
| 认证映射 | audit/authentication/ | 旧强类型认证输入转通用事件、认证数据投影；根包提供正式别名 |
| 存储 | audit/store.go、query.sql、store_test.go、sqlc.yaml | 规范化去重、锁、关联查询、生成映射 |
| 原始定义调整 | migrations/0001_init.*.sql、baseline.go、internal/migrationcheck/、testdata/ | 十列直装、固定校验、拒绝旧结构；移除本分支 0002/准备工具 |
| 领域 | user/service*.go、query.sql、对应测试 | 认证输入、明确关联、purge 锁序及 scrub |
| 门面 | config.go、accountkit.go、对应测试 | 来源配置、写读装配矩阵、生命周期 |
| HTTP | httpapi/admin/audit.go、audit_cursor.go、response.go、handler.go、测试 | 授权范围、DTO、审计专用游标、安全错误 |
| 交付 | examples/embedded、examples/audit、README、docs/、AGENTS、scripts/ | 接入示例、兼容/运维说明与验收门禁 |

## Task 1: 通用模型、批量统计与认证映射接口

**OpenSpec tasks:** 1.3 的对象突变/重放测试准备，2.1、2.2。

**Files:** Create audit/event/ 及模型行为测试；Modify go.mod/go.sum。旧 audit/audit.go、Async 与 Store 在任务三统一切换，确保每个阶段能构建。

**Interfaces:**
- Event：ID uuid.UUID、Source/Action string、Result *enum.Result、Actor Actor、Targets []Resource、Context EventContext、Data json.RawMessage、OccurTime time.Time；不含 CreateTime。
- RecordView：嵌入 Event 并含 CreateTime time.Time。
- Actor：Type ActorType、Namespace/ID/Name string；Resource：Type/Namespace/ID/Name string；EventContext 为设计中受控字段及 RelatedResources []Resource。
- Validate(Event) error；Freeze(Event) (Event,error)；DecodeEvent([]byte) (Event,error)；Scrub(Event) Event；Equivalent(Event,Event) bool。
- BatchResult：Written/Duplicate/Failed/Invalid/Conflicts int；Inserter.InsertBatch(context.Context,[]Event) (BatchResult,error)。Recorder.Record(context.Context,Event) 仍无错误返回。
- AuthenticationEvent：现有认证输入字段的强类型集合；AuthenticationData 解码 data.reason 与 data.accountkit 的明确字段；Projection(Event) (AuthenticationData,error)。此时保留旧域构造方式在下一任务一次性替换，避免共享字段同名碰撞。

- [ ] Step 1：新增 TestEventValidation 表测试，断言 v4/Nil/非 RFC variant 拒绝、128/129 来源和动作边界、已知主体 namespace/id 缺失、256/257 名称字符、32/33 资源和 64 KiB 边界；新增 TestEventFreezeOwnership、TestDecodeEventDuplicateKeys、TestEventEquivalentPrecisionAndResourceOrder。
- [ ] Step 2：运行 `go test -count=1 ./audit -run 'Test(Event|DecodeEvent)'`，确认缺失接口或行为导致预期失败，并记录基线。
- [ ] Step 3：按上述接口实现模型/规范化/JSON 解码，时间统一到 PostgreSQL 微秒精度；采用 google/uuid v1.6.0，测试 NewV7 版本及错误，不依赖全局随机池。
- [ ] Step 4：定义供 Async/Store 消费的 BatchResult、生产记录器和读侧 Query/Page/FlowLink 接口，并测试冻结对象和受控脱敏投影。Async/Memory 的迁移及对应先失败测试在任务三启用新模型时完成，不能提前标记这部分完成。
- [ ] Step 5：运行 `go test -count=1 ./audit/event ./audit`、`go vet ./audit/event`；记录结果并由独立 reviewer 审查接口、容量和深拷贝边界。

## Task 2: 认证映射、配置与领域调用迁移

**OpenSpec tasks:** 1.2、2.3、2.4。

**Files:** audit/authentication/ 及映射测试；config.go、config_test.go。领域和根包公开类型在任务三 schema 切换时统一启用。

**Interfaces:**
- AuthenticationOptions：Source、Namespace string；NewAuthenticationRecorder(Recorder,AuthenticationOptions,*slog.Logger) (*AuthenticationRecorder,error)。
- AuthenticationRecorder.Record(context.Context,AuthenticationEvent) 负责默认 actor/time、UUID 生成、强类型映射/校验/安全失败观测；内部 UUID 生成函数可在包内测试替换，不公开测试钩子。
- user.Deps 增加 AuditSource/AuditNamespace，领域构造使用 AuthenticationEvent；公共审计 Recorder 只收到 Event。
- Config.AuditSource/AuditNamespace 及前缀化 AUDIT_SOURCE/AUDIT_NAMESPACE；外部 recorder 模式要求显式配置，本地默认规则遵循设计。

- [ ] Step 1：依据盘点文档新增 TestAuthenticationEventMapping 表，逐个 EventType 断言 action、主体、直接/关联资源、reason/flow/session/identity 投影；测试未知/失败登录不把目标当已认证主体，旧 ADMIN 信息可恢复。
- [ ] Step 2：新增 TestAuditConfiguration 和 TestAuthenticationIDFailureDoesNotChangeResult，先运行相关 audit/accountkit/user 测试确认预期失败。
- [ ] Step 3：在 authentication 叶子包实现映射器，调用时绑定可信 source/namespace，逐种动作验证主体分类与资源关系。先保持旧领域编译；任务三将 user 的旧 Event 构造切换为 AuthenticationEvent 别名并更新测试投影，不扩大序列化字段。
- [ ] Step 4：检查 maxDeviceField=64 与管理员名称输入实际接受范围；任何与 512 字节/256 字符限制冲突记录为明确兼容裁决，不能截断或静默丢弃原可用审计。
- [ ] Step 5：运行 `go test -count=1 ./audit ./user ./httpapi/enduser ./...` 和相关 vet；Reviewer 复核事件映射矩阵及消费者/管理员隔离后再进入存储。

## Task 3: 原始数据库定义与通用存储

**OpenSpec tasks:** 3.1～3.5，4.1 的读侧基础。

已完成历史记录：3A 曾实施增量/staging，3B 激活根包/领域/Store/Async/HTTP 和生成/夹具，两步代码已审查。最新用户明确改变数据库策略，新增 3C 将 3A 方案替换为原始定义；保留先前提交及验证历史，但最终任务验收以 3C 和后续实际结果为准。

**Files:** migrations/0001_init.up.sql/.down.sql、baseline.go、迁移测试/testdata、internal/migrationcheck；移除 internal/auditmigration、cmd/accountaudit-migrate、0002 及 accountkit.Migrate 预检；调整兼容/恢复/服务版本断言、AGENTS 和迁移操作说明；audit/store/query/sqlc 已激活逻辑保持。

**Interfaces:**
- audit.Reader.ListByResource(context.Context,Query) (Page,error)，Query{Source string, Resource Resource, After *EventCursor, Limit int}，EventCursor{Time time.Time, ID uuid.UUID}；Page{Records []RecordView, FlowLinks []FlowLink}，FlowLink{Flow Resource, User Resource, Evidence RecordView, Live bool}。Live 与 Evidence 互斥；Live 归属必须在管理面通过领域回调独立核对本地流程，不能只信 reader；FlowLinks 仅供查询范围复核，不修改 Event 或增加主表字段。
- Store 保留 NewStore(*pgxpool.Pool) 兼容默认，并新增 NewStoreWithOptions(*pgxpool.Pool,StoreOptions{Source,Namespace})(*Store,error)；默认从连接池配置 schema 得到本地命名空间，不做 I/O，根门面显式传配置以限定本实例读/维护范围。消费任务一 BatchResult；DeleteOlderThan(context.Context,time.Time)(int64,error)。事务内查询 SQL 全部放 query.sql 并生成。
- 不再提供 Prepare、staging 或自动旧表升级；保留通用 Store、读侧和非阻塞认证映射接口。

- [ ] Step 1：先写真实 PG 测试，断言仅 0001 直装 version=1 且十列 UUID，旧同号 v1 结构被拒绝且数据/版本不变；观察当前 0001 旧结构/当前 baseline 接纳旧结构造成的真实 RED。
- [ ] Step 2：将已审查十列定义、约束和索引合入 0001，更新当前固定校验，删除本分支 0002/准备工具及根预检。保留双锁、dirty、Down 禁用和显式测试清库边界，不操作已有产品库。
- [ ] Step 3：相对下一版本测试迁移、旧同号 v1/v2 拒绝、取消/并发/非空重复 Up、固定历史校验均通过；将旧“全目录完全相同”断言改为非审计目录保持加十列主动变化断言，不删除有效门禁。
- [ ] Step 4：历史/恢复夹具直接装入新定义；保留旧非审计 ID、密文/摘要、有效令牌与明确合成审计映射，不修改源快照、不再经过 Prepare 或假称旧库升级。同步服务 version=1 断言。
- [ ] Step 5：运行 sqlc、真实 PG/Redis 全库测试、build/vet、check-generated/check-migrations；独立 reviewer 核对初始定义授权边界、历史门禁、旧结构拒绝及非审计兼容。
- [ ] Step 6：对已有 3B Store/读取证据作接口衔接核验，保留规范化、幂等/冲突/受控重放、flow 隔离和整批回滚行为，后续隐私锁/严格 HTTP 继续由任务四/六验收。

## Task 4: 匿名化、流程锁序与迟到写入

**OpenSpec tasks:** 5.1、5.2、5.3。

**Files:** user/query.sql、service_lifecycle.go、service_purge.go、service_signin_completion*.go 及测试；audit/query.sql、store.go、并发测试；所有生成文件通过 sqlc。

**Interfaces:**
- 新增用户领域 SQL LockIdentityFlowsForUser（按 flow id 稳定排序 FOR UPDATE），在 scrub audit 之前锁定需删除的关联流程；沿用排序 user → flow → audit 的一致顺序。
- Store 的批量事务先读取整批 flow/账号快照，按稳定顺序锁整批全部已知关联账号，再按 id 锁 flow，发现快照新增账号则退出并在事务外重试，绝不持 flow 再补 user 锁。
- flow 已消失/未知时保守 Scrub；DELETED 账号记录先 scrub 再入库。保留既有整批写入失败全部回滚的语义，不改成每事件独立提交；整批排序避免不同事件顺序形成反向账号锁。

- [ ] Step 1：写 TestAuditInsertVersusPurgeBothOrders、TestAuditSharedFlowLockOrder、TestAuditMissingFlowLateEvent、TestPurgeRollbackPreservesAudit，使用两个真实连接和屏障控制先后；先记录未实现保护的失败。
- [ ] Step 2：在用户路径引入 scrub 前 flow 锁定并改 JSON 清理 SQL；覆盖 actor/target/related name、client_ip/device_id/subject_hint 和历史 flow 关联，保留稳定 ID 及事件事实。
- [ ] Step 3：Store 实现快照/排序锁/复核/重试或保守脱敏；上下文取消和重试耗尽明确失败，不改变认证结果；禁止用无锁状态查询冒充并发保证。
- [ ] Step 4：运行 sqlc、真实数据库领域/审计并发测试及 race，验证过期/清理 flow、softDelete 和 purge 不产生锁环。
- [ ] Step 5：验证 retention 仍按 occur_time、批次/取消和全局审计锁序；Reviewer 专项检查锁图与脱敏重放。外部接管后的旧本地副本清理及外部组件不被删除的根装配测试由任务五接续，OpenSpec 5.3 在该测试实际通过前不勾选。

## Task 5: 宿主写读装配和生命周期

**OpenSpec tasks:** 1.3 的错配测试、2.1 的 UUID 并发补充、4.2、5.3、5.4。

**Files:** accountkit.go、config.go、相关测试；audit/async.go、async_test.go；httpapi/admin/handler.go；生命周期验证发现的 internal/accountsvc/run.go 自建监听器关闭兜底及对应测试。

**Interfaces:** Deps.Audit audit.Recorder、Deps.AuditReader audit.Reader；admin.Deps 增加 AuditSource/AuditNamespace，并允许 Audit 为 nil 表示明确未配置查询。

- [ ] Step 1：补全 TestAuditAssemblyModes，断言默认本地、外部读写、只外部写和非法只外部读；TestInjectedAuditLifecycleOwnedByHost 断言 Auth.Close 不关闭宿主记录器。3B 已实现且已获用户接受的行为只记录实际回归结果，不删除保护制造 RED；新增或未满足行为先观察真实失败。
- [ ] Step 2：完成装配矩阵和根模式切换测试，配置仅写侧时认证正常；稳定 source/namespace 下旧本地副本继续 retention/匿名化且不调用宿主组件删除，覆盖取消、边界及默认 worker 生命周期、New 无 I/O。
- [ ] Step 3：让默认观测区分 Written/Duplicate/Dropped/Failed/Invalid/Conflicts，测试队列满、关闭、写入超时及含秘密宿主错误日志；按 TDD 核实不改变认证动作结果。
- [ ] Step 4：运行根包/audit/maintenance/internal/accountsvc 相关测试和 vet；Reviewer 复核配置前缀、失败语义和连接所有权。
- [ ] Step 5：对已观察到的早期停机监听器关闭时序，使用现有 runtimeFactory 和测试 listener/logger 构造确定性 RED，最小保证自建监听器在关闭库/连接及成功返回前完成关闭，保持既有 accountsvc-runtime 的排空顺序、总预算和异常退出契约；不新增公共接口或服务架构。

## Task 6: 管理查询、DTO 与审计专用游标

**OpenSpec tasks:** 1.3 的旧游标测试、4.1 完成、4.3、4.4。

**Files:** httpapi/admin/audit.go、audit_cursor.go、response.go、handler.go、audit_test.go、fake_test.go；保留 page.go 的用户列表游标。

**Interfaces:** encodeAuditCursor(Query,EventCursor) string；decodeAuditCursor(string,Query)(*EventCursor,*apierror.Error)；JSON 游标版本与 source/namespace/user 范围绑定；newAuditEventResource 接受 RecordView 和明确认证投影。

live flow 归属接口采用 user.Service.AuditFlowBelongsToUser(context.Context,userID,flowID string)(bool,error)，验证资源格式并通过领域 SQL 核对 user_id/proof_user_ids。根门面将其作为 admin.Deps.FlowOwner 回调注入；直接使用管理适配器但未提供回调时，不接受 Live 证明。

- [ ] Step 1：写 TestAuditLegacyCursorRejected、TestAuditCursorScope、TestAuditExternalReaderBounds、TestAuditQueryUnavailable；断言旧 e_ 游标 400、跨账号拒绝、仅写侧 503 AUDIT_QUERY_NOT_CONFIGURED、依赖错误安全 503。
- [ ] Step 2：实现独立游标与范围查询，先保留管理员角色及账号存在性检查，验证 Reader 返回的每项 source/直接关联或 FlowLink 证据/动作、数量和顺序后再序列化；越界、证据缺失或无序结果按依赖故障处理，不静默丢项生成错误游标。
- [ ] Step 3：使用认证 Projection 保留旧 HTTP 字段/null 语义，资源末段输出 UUID，未知业务 data 不外露；测试 operator/super-admin 拒绝、USER/ADMIN/SYSTEM、同时间分页及用户列表游标不变。
- [ ] Step 4：运行 `go test -count=1 ./httpapi/admin ./...`，Reviewer 检查授权、凭证错误类别和任意扩展数据泄露。

## Task 7: 示例、兼容说明与操作契约

**OpenSpec tasks:** 4.5、6.1。

**Files:** examples/audit/main.go、examples/embedded/main.go、internal/accountsvc 装配、README、docs/compatibility.md、docs/migrations.md、docs/accountsvc.md、AGENTS.md。

- [ ] Step 1：给出默认本地、宿主写读、仅写入三个可编译装配示例；复用明确 source/namespace，宿主控制 Close，避免示例暗示网络调用可靠或跨库原子。
- [ ] Step 2：补充公开 Event/Reader 迁移、UUID 标准输出、旧游标重启、原始定义直装和旧库拒绝/独立备份恢复边界、namespace 稳定性与 To C 案例链接。
- [ ] Step 3：明确外部匿名化必须有独立持久化清理流程及完成核验，切换后旧本地副本仍保留责任；只收到 USER_PURGED 不能当清理完成证据。
- [ ] Step 4：更新 AGENTS 的审计 ID 与本次明确授权初始定义例外，保留其他迁移/ID 规则；运行示例独立 build、检查路径和内容差异，Reviewer 复核说明与实际 API 一致。

## Task 8: 必需测试门禁、恢复与交付

**OpenSpec tasks:** 6.2～6.5。

**Files:** scripts/required-tests.txt、必要的迁移/恢复 fixture 和 tests/Dockerfile 使用说明；openspec/changes/generalize-audit-events/verification.md、tasks.md。

- [ ] Step 1：把实际新增必需顶层测试加入清单，不删除旧门禁；执行 GOWORK=off build/vet/test、sqlc 和迁移历史检查，保存原始输出至本任务 .test-output 目录。
- [ ] Step 2：创建可识别的本任务 PostgreSQL 17 一次性资源及独立空恢复源/目标库，在 Linux CGO 验证环境执行 scripts/verify.sh；不复用未经核验的产品/现有测试库，不以禁用 race 代替验证。
- [ ] Step 3：用直接装载当前定义的历史合成数据核对明确旧 ID 映射、记录数、账号/身份/会话/flow ID、解密与有效 access/refresh，执行恢复 seed/verify/source-unchanged；TestRecoveryFixture 按既有特殊 skip 规则处理，不假称原地升级。
- [ ] Step 4：真实数据库 EXPLAIN 账号关联查询、flow scrub、retention，记录所选索引及数据规模，不给出无测量的性能承诺。
- [ ] Step 5：完成全分支独立代码审查及修复，运行 openspec validate --all --strict 和 verification-before-completion；更新任务与证据，清理仅本任务创建的资源。实施分支的提交、合并、推送、归档按用户后续授权分别处理。

## Coverage and Execution Order

| 规格/风险 | 任务 |
|---|---|
| 十字段、UUID、来源/动作/结果、容量、结构演进 | 1、2 |
| 主体与资源、认证隐私映射 | 2、6 |
| 幂等、冲突、排序 | 1、3、6 |
| 匿名化、迟到与多用户 flow 锁序 | 4 |
| 默认/外部装配、生命周期、外部数据责任 | 5、7 |
| Go/HTTP 兼容边界 | 2、6、7 |
| 原始定义、历史冻结、非空重复/测试升级、同号旧库拒绝 | 3、8 |
| 独立构建、race、备份恢复与发布证据 | 8 |

任务 1 → 2 → 3A/3B（已完成历史实施）→ 3C（最新原始定义调整）→ 4 → 5 → 6 → 7 → 8 顺序执行；仅只读盘点或无共享写入的验证可并行。每个任务由新 implementer 执行，独立 reviewer 复核后再进入依赖任务。继续实施授权包含任务内部提交，不扩展为推送、最终合并或归档。

## Plan Self-Review

- 四份规格均能映射到以上任务；26 个 OpenSpec 任务保持原验收条件，计划分组不缩减范围。
- 公共接口统一使用 Event、RecordView、Query、EventCursor 和 BatchResult；生成行只在 Store 内部映射。
- 锁序需先完成任务二事件归属盘点，再由任务四实现并发保护，不能仅按当前 UserID 锁一行。
- Ruling: 通用模型和认证映射先作为叶子包实施，任务三统一切换根包/领域/存储/旧管理编译边界，避免以中间编译失败作为任务验收。额外包结构增加少量别名维护成本，公共入口仍为 audit。
- 用户已评审计划；实施仍按逐任务 TDD 与独立审查推进。
