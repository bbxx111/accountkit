# 简化会话上下文 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** 删除来源已知标记与双快照，以必有认证来源和一份可扩展嵌套客户端描述简化会话模型。

**Architecture:** 领域保证来源非空并沿用事务锁序；客户端在业务层规范化，通过独立 JSON 编解码接口供存储与 DTO 使用。SQL 只持久化一份完整 JSONB，初始结构按本次明确授权整合且拒绝旧同号结构。

**Tech Stack:** Go 1.26.5、PostgreSQL 17、pgx/v5、sqlc 1.31.1、Chi、Redis/miniredis、OpenSpec。

**Spec:** [已批准设计](../../../openspec/changes/simplify-session-context/design.md)、[proposal](../../../openspec/changes/simplify-session-context/proposal.md) 及其中六份 delta；前一 [整合设计](../../../openspec/changes/consolidate-session-context-initial-schema/design.md) 保留历史。

## Global Constraints

- 只在 `refactor/simplify-session-context` 工作；基线 `b414fef`；用户已批准，不再重复请求设计确认，本次不提交/推送/归档。
- GOWORK=off；不手改 user/db、audit/db；修改 SQL 后 sqlc generate；不新增依赖、外键或生产0002。
- 初始化 version=1；旧 v0.3.0 v1、b414fef v1、撤回 v2 无写拒绝；三代历史、固定源和标签保持，只更新当前哈希。
- 来源缺失不是正常态；普通 JWT/内省不新增 DB；失败事务不恢复已消费验证码。
- JSON 总量2048字节；名称/型号/制造商64字符、版本32、build64；扩展key规则 `[a-z][a-z0-9_]{0,63}`、节点64、深度4、数组16、字符串256。
- 原生 app/browser 可并存；持久化整体替换、无描述保持；重放不更新；pending本次优先，否则首次描述与首次观察时间整体沿用。
- HTTP 不新增任意 JSON 输入；保留原错误协议、设备头、scope、JWT、refresh、异步脱敏审计和Redis故障边界。
- 验证结果统一写 `openspec/changes/simplify-session-context/verification.md`；缺真实数据库不能报告集成通过，全量 strict 的既有六项长段落失败单独记录。

## Review Focus

- 来源行被人为删除：查询/reauth/解绑/换绑应失败且不写入，普通 JWT 热路径仍不查库（任务4）。
- JSON 原始字节含重复键、非法UTF-8、嵌套敏感键或大整数：拒绝非法项且合法整数往返不失真（任务3）。
- 调用方在提交后修改扩展 map/RawMessage：结果不变，race 不暴露共享别名（任务3、5）。
- pending首次描述与完成描述部分重叠：整体选择及观察时间正确，重放不产生新活动（任务5）。
- 旧同号库含合法业务数据且第一次拒绝后再次Up：catalog、数据、dirty、版本保持且连接/锁释放（任务2）。

---

### Task 1: 固定已批准工件与执行接口

**Files:** 创建 `openspec/changes/simplify-session-context/{proposal,design,tasks,verification}.md`、六份 `specs/*/spec.md` 及本计划；不改主规格或前一变更。

**Interfaces:** 消费已批准设计；产出完整 OpenSpec apply-ready 工件及下述任务所有权。详细决定以 design 为准。

- [x] 读取相关主规格及前一未归档工件，保留受修改 Requirement 的所有既有 Scenario 标题，更新其行为。
- [x] 写齐 proposal/design/delta/tasks；用 `openspec validate simplify-session-context --strict` 验证，预期本变更无问题。
- [x] 记录 `openspec status --change simplify-session-context --json` 和 `instructions apply` 的就绪状态；不将就绪当作实现完成。
- [x] 自审接口与边界；通知实施代理允许开始。本轮不执行 commit。

### Task 2: 单行JSONB存储、生成代码与初始基线（根代理）

**Files:** 修改 `migrations/0001_init.up.sql`、`.down.sql`、`baseline.go`、`session_context_test.go`、`session_context_consolidation_test.go`、`initial_schema_test.go`、`user/query.sql`、`sqlc.yaml`、`internal/migrationcheck/main.go` 及其测试；创建 `migrations/simplified_session_context_test.go`；生成 `user/db/`、`audit/db/`。

**Interfaces:** 产出 `db.Session` 无 known 字段，`db.SessionClientInfo` 含 SessionID、ClientInfo []byte、ObserveTime/CreateTime/UpdateTime；UserID仅作为查询归属参数。GetSessionClientInfo 按账号/会话读取，UpsertSessionClientInfo 按 session_id 原子整体替换并保留所属校验。认证关联查询保持既有来源集合接口，删除 MarkSessionAuthenticationIdentitiesKnown。

- [x] 新建 `TestSimplifiedSessionClientStorage`：空安装 version=1、七表、client_info JSONB NOT NULL、session_id单列主键、无known/snapshot_kind/平坦列，JSON业务约束不复制到数据库。
- [x] 新建 `TestSimplifiedSessionRejectsPreviousSnapshotBaseline`：从基线 SQL 构造旧双快照非空v1，连续Up均拒绝且catalog/数据/版本不变，dirty优先，连接/锁可复用；保留旧v030/v2拒绝覆盖。
- [x] 运行 `go test -count=1 ./migrations -run '^TestSimplifiedSession'`，配置任务专属DSN，预期RED并记录原因。
- [x] 调整0001与预检、当前固定哈希；保留历史哈希；把前一catalog等价测试改为明确授权差异清单，不移除历史拒绝检查。
- [x] 修改查询移除标记/快照分类和逐列字段，保留user scope、清理、锁序及来源引用校验；删除sqlc快照枚举override并运行 `sqlc generate`。
- [x] 运行 `go test -count=1 ./migrations ./internal/migrationcheck` 和 `bash scripts/check-generated.sh`、`bash scripts/check-migrations.sh`，预期通过；独立审查后通知任务4准确生成接口。本轮不commit。

### Task 3: 客户端模型、编解码、解析与DTO（独立客户端代理）

**Files:** 修改 `user/client_info.go`、`user/client_info_test.go`、`httpapi/clientinfo/parse.go`、`parse_test.go`、`httpapi/enduser/response.go`、`httpapi/admin/response.go`、两面的 `client_info_test.go`/`session_client_info_test.go`；需要时创建 `user/client_info_json.go`、`user/client_info_json_test.go`。不改存储、query、current_session或来源文件。

**Interfaces:** 产出 `NormalizeClientInfo(*ClientInfo)(*ClientInfo,error)`、`MarshalClientInfo(*ClientInfo)([]byte,error)`、`UnmarshalClientInfo([]byte)(*ClientInfo,error)`。保留Go平坦字段，增加DeviceManufacturer/AppBuild/Extensions map[string]json.RawMessage；与根代理固定nested JSON。MergeClientInfo最多为同请求parser辅助，禁止历史合并。

- [x] 添加 `TestClientInfoJSONRoundTrip / TestClientInfoJSONRejectsInvalidInput`：全部字段嵌套往返、app/browser并存、零值省略、未知核心key拒绝、空描述nil、名称变化无旧版本、版本单独输入不拼接旧名称。
- [x] 添加 `TestClientInfoExtensionsBoundsAndPrivacy / TestClientInfoJSONEncodedByteBoundary`：总JSON2048/2049字节、64/65节点、深度4/5、数组16/17、字符串256/257、key合法/非法，包含null/布尔/数值/对象/数组与大整数无损往返。
- [x] 添加 `TestClientInfoJSONRejectsInvalidInput` 和 `TestClientInfoCopiesAndEncodedSize`：重复键、非法UTF8、控制字符、敏感键及嵌套变体拒绝；输入map/RawMessage后续修改不改变规范化或DTO结果。
- [x] 以模型显式文件测试确认app/browser排他及build清理的行为RED；新codec接口的部分首轮编译断点单独记录，不冒充行为RED。
- [x] 实现校验/编解码与显式DTO；新增精确限制集中常量，同请求输入保持合法显式→Hints→UA优先级，制造商/build/扩展不新增任意JSON HTTP输入。
- [x] 更新两面DTO断言为nested JSON；运行 `go test -count=1 ./user ./httpapi/clientinfo ./httpapi/enduser ./httpapi/admin`，检查未触碰他人文件，独立审查后通知根代理接口可用。不commit。

### Task 4: 认证来源必有及当前HTTP（独立来源代理）

**Files:** 修改 `user/service_session_context.go`、`user/session_context_types.go`、`user/service_identity_replacement.go`、`user/service_session_context_test.go`、`user/service_session_context_revocation_test.go`、`user/service_session_context_internal_test.go`、`httpapi/enduser/current_session.go`、`current_session_test.go`；共享 `user/service.go` 中解绑/reauth调用点由根代理整合。

**Interfaces:** 消费任务2生成来源查询；`GetCurrentSession(context.Context,Principal)(CurrentSessionInfo,error)`签名保持，删除公开known/initial字段；来源集合非空、固定安全500映射。向根代理提供共享service.go的最小修改说明，不并发写该文件。

- [x] 添加/更新 `TestMissingAuthenticationSourcesRejectSensitiveActions / TestIdentityUnbindChecksUnrevokedSourceInvariants` 表驱动：手工移除当前或解绑涉及的来源后，查询/reauth/解绑/换绑拒绝；身份、auth_time、来源、会话状态不变，不把unknown改为空数组成功。
- [x] 更新正常登录/补全非空来源、reauth整体替换、解绑命中及换绑保留/撤销测试；保留当前撤销成功204、过期但未清理、并发竞争与Redis失败覆盖。
- [x] 运行来源及当前HTTP测试确认RED；依赖生成代码未就绪时先落测试，不手改生成代码。
- [x] 删除known写入/分支与InitialClientInfo；使用来源的读写动作显式拒绝缺失；当前HTTP去除字段且保持稳定排序/一致快照、401/403/503与固定500安全边界。
- [x] 运行 `go test -count=1 ./user -run 'SessionContext|AuthenticationIdentities|IdentityReplacement'`、`go test -count=1 ./httpapi/enduser -run 'CurrentSession'`；结合真实DSN/并发测试GREEN后独立审查，不改JWT热路径、不commit。

### Task 5: 持久化、pending、集成/恢复与文档（根代理，依赖2–4）

**Files:** 修改 `user/service_client_info.go`、`user/service.go`、`user/service_session.go`、`user/service_signin_completion.go`、`user/service_signin_completion_store.go`、`user/service_client_info_test.go`、`enum/client.go`及枚举测试、`accountkit_session_context_test.go`、`recovery_test.go`、`internal/accountsvc/session_context_integration_test.go`、`httpapi/enduser/session_openapi_test.go`、`docs/openapi/{enduser,admin}.json`、`scripts/required-tests.txt`、README/AGENTS及相关长期文档。

**Interfaces:** 消费任务3编解码及任务4CurrentSessionInfo；单份存储通过Marshal/Unmarshal，pending保持加密首次描述和服务器观察时间，公开只client_info/client_info_update_time。

- [x] 更新 `TestServiceClientInfoWholeObservationReplacement`：旧A字段被新B整体替换，缺失字段不保留；nil观察不写时间，app/browser共存；非法显式输入在凭证消费前拒绝。
- [x] 更新 `TestSignInCompletionClientInfoSelection`：首次有/无×完成有/无完整矩阵、完成优先、不拼接、fallback保留首次观察时间；加密payload仍不泄漏原始信息。
- [x] 保留并验证refresh胜者/宽限重试、完成重放、事务回滚、匿名化回滚和有界清理测试；旧快照测试接口迁移时的编译断点与迁移后真实PG通过分别记录。
- [x] 用任务3接口重写单份持久化，移除读取旧描述合并、快照枚举及标记；协调来源代理共享调用点；gofmt所有修改Go文件。
- [x] 调整恢复和历史格式合成夹具：显式建立合法来源与一条嵌套JSON，或验证损坏来源异常；保留三阶段恢复与引用完整性断言，不宣称旧库升级。
- [x] 更新OpenAPI两面nested schema、未知key拒绝与输出样例，移除known/initial；执行 `node scripts/check-openapi.cjs` 与HTTP schema测试。
- [x] 更新README、docs/compatibility.md、docs/migrations.md、docs/development.md等实际涉及文字，说明旧字段移除、整体替换、扩展白名单责任、旧同号拒绝和回退风险；只为新增必需顶层测试追加required-tests。
- [x] 运行真实DB定向集成及普通全包测试/vet，独立审查并修复实质问题；保存RED/GREEN证据，不commit。

### Task 6: 完整验证与独立审查（根代理）

**Files:** 更新本变更tasks/verification；原始输出在 `.test-output/`。不改无关主规格以掩盖CLI警告。

**Interfaces:** 消费全部集成结果，产出每项检查的实际退出状态、当前代码对应证据和未验证边界。

- [x] 执行最终Linux verify.sh内的GOWORK=off build/vet/完整race测试；Windows首轮全仓两处失败及修复另行追溯，未执行DB的skip必须明确标出。
- [x] 运行生成检查和历史检查；完整Windows无race工具链时用tests/Dockerfile的Linux镜像执行 `bash scripts/verify.sh`，需普通DB及两个实际不同空恢复库。
- [x] 执行 `ACCOUNTSVC_TEST_RACE=1 bash scripts/verify-accountsvc.sh`，核验库/服务必需测试、race与恢复seed/verify/source-unchanged，普通套件TestRecoveryFixture预期skip除外不接受必需项skip。
- [x] 运行 `openspec validate --all --strict`，本变更单独strict必须通过；如仍为既有六份主规格长段落警告，原样记录而不放宽strict/修改无关规格。
- [x] 独立只读审查规格、实现、JSON边界、来源锁序及迁移无写拒绝；修复实质问题后只重跑受影响门禁，禁止沿用变更前日志。
- [x] 更新verification与实际完成tasks，检查diff、文件引用与敏感数据；仅清理本任务测试资源。交付明确未提交/未归档及产品迁移未验证，不将任务完成等同发布。
