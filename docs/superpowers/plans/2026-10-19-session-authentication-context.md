# 会话认证依据与客户端信息 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现会话当前认证依据、身份移除联动撤销、当前会话查询及可选客户端描述。

**Architecture:** 来源存于关联表，重新认证原子替换；INITIAL/LATEST 客户端快照独立存储。领域事务负责归属、锁序、撤销和清理，HTTP 负责可选头解析与明确 DTO；普通鉴权保持 JWT + Redis。

**Tech Stack:** Go 1.26.5、PostgreSQL 17、pgx/v5、sqlc 1.31.1、Chi、Redis/miniredis；UA解析库由任务1通过官方源码和样本选型。

**Spec:** [设计](../../../openspec/changes/add-session-authentication-context/design.md)及同change下全部六份spec；OpenSpec任务为验收清单，本计划将其组合成八个可独立审查的交付单元。

## Global Constraints

- 0001、已发布标签和 tests/testdata/source-baseline 保持原样；真实迁移从0002递增，无外键，枚举SMALLINT/sqlc override。
- GET /users/me/sessions/current允许user/user:bind，拒绝user:undelete；未知来源返回false与[]，具体身份仅name/kind。
- 来源命中任一身份或未知即撤销；包括当前和已过期未清理会话。重新认证替换来源，refresh/BIND不替换。
- 换绑仍撤销其他会话；当前来源未知或命中旧身份时也撤销。HTTP状态及JWT/refresh格式保持。
- client_type=WEB/NATIVE；device_type=DESKTOP/PHONE/TABLET；名称/型号64字符，版本32字符，描述总计2048字节；UA和Hints输入各2048字节。
- 可选描述缺失/自动解析失败不阻断；显式非法字段在凭证消费前400，/token为OAuth invalid_request。
- 用户锁先于会话锁；刷新仅锁会话时不得反向请求用户锁。Redis fail-open、异步审计、刷新宽限保持。
- 所有SQL在user/query.sql；生成代码只由sqlc更新；Go修改gofmt，GOWORK=off。
- 使用独立分支feat/session-authentication-context；每任务可形成本地检查点提交，不推送、不合并实施分支、不归档。
- 测试仅使用本次容器accountkit-session-context-pg-20261019及accountkit-session-context-redis-20261019；不得清理其他资源。

## Review Focus

- pending首次无描述或旧payload：完成时只补LATEST，不伪造INITIAL（任务5）。
- 重新认证与解绑交错：先提交者决定来源与撤销，后者不能复活会话（任务4）。
- 旧JWT仍有效但数据库会话撤销/过期：新详情401，普通Authenticate保留旧故障边界（任务3/6）。
- 来源未知与元数据缺失不可混淆：升级不退出，解绑撤销未知来源，缺设备字段不触发退出（任务2/4）。
- UA/显式字段组合不能把不同软件名称和版本拼在一起，也不能泄漏到审计、日志和指标（任务1/5/6）。

## Execution and Evidence

用户已明确要求apply，采用AGENTS.md默认子代理执行，无需再次选择执行方式。任务按依赖顺序实施，每任务独立审查；控制器负责验证环境、全量门禁和ledger。Windows隔离路径为C:/Users/bbxx1/.codex/worktrees/80fd/accountkit，所有工具命令显式workdir；此路径写入及依赖/Docker访问使用环境批准的提升权限。

测试环境：SERVER_TEST_DB_DSN=postgres://postgres:session-context-test@127.0.0.1:2136/accountkit_tests?sslmode=disable，ACCOUNTSVC_TEST_REDIS_URL=redis://127.0.0.1:2140/0（仅本地合成资源）。Linux完整门禁使用accountkit-verify:local镜像和本任务隔离网络/资源；恢复源目标另建空数据库。原始日志放.test-output/session-context/，持久结论进入change的verification.md。

### Task 1: 公共类型、客户端校验与本地解析

**Files:** Create enum/client.go、user/client_info.go、user/client_info_test.go、user/session_context_types.go、httpapi/clientinfo/{parse.go,parse_test.go}; Modify user/service.go的Meta、user/service_session.go的SessionInfo、go.mod/go.sum（仅选型后所需依赖）。

**Interfaces:**
- Produce enum.ClientType (ClientWeb=1, ClientNative=2)，enum.DeviceType (DeviceDesktop=1, DevicePhone=2, DeviceTablet=3)，enum.ClientInfoSnapshotKind (ClientSnapshotInitial=1, ClientSnapshotLatest=2)，零值Unspecified，String/Parse/Valid沿既有模式。
- Produce user.ClientInfo{ClientType enum.ClientType, OSName, OSVersion, BrowserName, BrowserVersion, AppName, AppVersion string, DeviceType enum.DeviceType, DeviceModel string}；Meta.ClientInfo *ClientInfo。
- Produce NormalizeClientInfo(*ClientInfo)(*ClientInfo,error)、MergeClientInfo(previous, observed *ClientInfo)*ClientInfo、ErrInvalidClientInfo；均不修改输入。
- Produce SessionInfo.ClientInfo *ClientInfo、ClientInfoUpdateTime *time.Time；AuthenticationIdentity{ID string,Kind enum.IdentityKind}；CurrentSessionInfo{SessionInfo; AuthTime time.Time; AuthenticationIdentitiesKnown bool; AuthenticationIdentities []AuthenticationIdentity; InitialClientInfo *ClientInfo}。
- Produce clientinfo.Parse(*http.Request)(*user.ClientInfo,error)，无I/O、可选输入，只定义解析不接入handler。

- [ ] **Step 1:** 先写TestNormalizeClientInfo和TestMergeClientInfo，断言nil保留、非法枚举/控制字符/65字符名称/33字符版本/2049字节描述被拒绝；输入不变，软件改名无版本清旧版本，类型切换清除不适用字段。
- [ ] **Step 2:** 执行go test ./user -run 'Test(Normalize|Merge)ClientInfo'，记录新增能力缺失的RED，再实现上述纯类型和函数。
- [ ] **Step 3:** 用官方源码比较mssola/useragent、mileusna/useragent等适当候选及Client Hints结构化解析方式，记录许可证/依赖/样本结果；增加TestParseClientInfo覆盖Windows Chrome、iOS Safari、Android Chrome、原生显式头、GREASE、多品牌、重复/畸形/超限自动头、显式非法头及BFF非浏览器UA。先RED后实现，不编造UA支持或版本。
- [ ] **Step 4:** go test ./enum ./user ./httpapi/clientinfo并go vet对应包；基线DB可用时运行项目完整测试一次，记录原有skip；选型与完整测试写task报告。
- [ ] **Step 5:** gofmt、差异检查后仅提交本任务文件，报告精确公开符号、测试证据和SHA；不修改OpenSpec任务勾选。

### Task 2: 递增迁移和sqlc查询

**Files:** Create migrations/0002_session_authentication_context.{up,down}.sql；Modify migrations/{migrations.go及相关预检/测试文件}、migrations/testdata、sqlc.yaml、user/query.sql、生成user/db和audit/db（如schema导致必要生成变化）、迁移/当前schema相关测试；不改冻结快照或0001。

**Interfaces:**
- Consume任务1的三个enum。
- Produce session.authentication_identities_known；session_authentication_identity(session_id,identity_id)；session_client_info所有design指定列。
- Produce q.ListSessionAuthenticationIdentities、q.AddSessionAuthenticationIdentity、q.DeleteSessionAuthenticationIdentities、q.MarkSessionAuthenticationIdentitiesKnown、q.RevokeSessionsByIdentity（包含未知、过期未清理及user_id范围）、q.UpsertSessionClientInfo、q.GetSessionClientInfo、q.ListSessionClientInfosByUser（批量）、q.DeleteSessionContextByUser。
- Produce q.LockCurrentSessionForRead（按user_id/id，FOR SHARE）供任务3在同事务读取来源/快照；已有写入路径在session锁下更新上下文，保证读一致性。
- 保留既有查询签名，新增可选上下文查询；查询生成的精确参数以报告为交接来源。

- [ ] **Step 1:** 编写TestSessionContextMigrationUpgrade，先在v1插入合成会话，证明新列/表缺失RED，再实现0002；验证升级后false、原token摘要不变，重复Up不重建。
- [ ] **Step 2:** 更新预检及测试专用下一版本，保留历史撤回v2/dirty拒绝测试；不删除门禁用例来满足新版本。
- [ ] **Step 3:** 编写跨账号和双schema查询测试后实现有界查询及必要索引，sqlc generate，验证所有新增引用无FK且格式、唯一性有效。
- [ ] **Step 4:** 运行go test ./migrations ./user及根迁移相关测试、check-generated.sh/check-migrations.sh；如现有集成测试对当前version=1有断言，改为当前已发布递增语义，冻结历史断言保持。
- [ ] **Step 5:** 提交并报告schema、查询参数和当前迁移测试结果。

### Task 3: 来源写入、重新认证及当前会话领域查询

**Files:** Create user/service_session_context.go和对应测试；Modify user/service.go、service_signin_completion.go、service_me.go；仅必要私有签名调整。

**Interfaces:**
- Consume任务1模型与任务2查询。
- Produce (*Service).GetCurrentSession(ctx context.Context,p Principal)(CurrentSessionInfo,error)。
- Produce setSessionAuthenticationIdentities(ctx context.Context,q *db.Queries,sess db.Session,identityIDs []string)error，要求调用者持有账号/会话锁并复核具体身份有效归属/策略。
- createSession/establishSession等私有函数按需要接受已验证具体身份ID；公开登录方法签名保持。

- [ ] **Step 1:** 先写TestSessionAuthenticationSources覆盖普通单身份、账号有额外绑定、pending多身份/同类型去重，RED后实现同事务来源写入。
- [ ] **Step 2:** 先写TestReauthenticationReplacesSources覆盖多变单、未知变已知、失败回滚、同sid/refresh保留；再在既有reauth事务中替换来源。
- [ ] **Step 3:** 先写TestCurrentSessionContextDomain覆盖scope、user_id/sid归属、过期边界、revoked、未知false/[]、已知空集合错误及旧access读取DB最新auth_time；实现同事务FOR SHARE一致性读，依赖故障明确映射，不能把所有DB错误转401。
- [ ] **Step 4:** 测试refresh/grace/普通绑定不改变集合，以及来源写入失败整个会话/flow回滚；运行完整user和相关根集成测试。
- [ ] **Step 5:** 提交并报告接口及证据；来源与clientinfo存储职责分别由本任务和任务5维护。

### Task 4: 解绑、换绑及撤销并发

**Files:** Modify user/service_identity.go、service_identity_replacement.go、enum/enum.go、审计/观测原因映射；Create user/service_session_context_revocation_test.go及必要根HTTP集成测试，更新原测试中被新语义取代的期望。

**Interfaces:** Consume任务3来源关联和任务2撤销查询；新增enum.RevokeIdentityUnbound只追加。复用afterRevokeAll/revokeInSet。

- [ ] **Step 1:** TestIdentityUnbindRevokesAuthenticationSessions先RED，覆盖命中任一来源、未知旧会话、保留不命中已知会话、当前会话、到期会话、其他账号隔离。
- [ ] **Step 2:** 实现直接Go/HTTP一致的scope/近期认证配置/有效当前会话验证，以及用户锁后同事务软删除/撤销；不绕过剩余身份完整性。
- [ ] **Step 3:** TestReplacementCurrentAuthenticationSource先RED，覆盖当前未知/命中退出、先通过另一身份reauth后保留、其他会话仍撤销、BIND不迁移来源；实现并调整重试401/404契约。
- [ ] **Step 4:** TestSessionContextConcurrentRevocation用真实PostgreSQL屏障覆盖reauth先/后、普通登录/pending、refresh和换绑竞争；测试事务回滚、Redis故障、宽限及完成重放不复活。
- [ ] **Step 5:** 更新脱敏审计与有界指标原因映射，运行完整user及相关根HTTP测试，提交并报告锁序和每个并发结果。

### Task 5: 客户端快照和生命周期

**Files:** Create user/service_client_info.go、对应snapshot/lifecycle测试；Modify service_signin_completion_store.go、service_signin_completion.go、service_signin.go、service_idp.go、service_me.go、service_session.go、service_purge.go及必要user/query.sql（重新sqlc）。

**Interfaces:** ConsumeNormalize/Merge和q.Get/UpsertSessionClientInfo；将快照接入任务3建会话尾部及GetCurrentSession模型。
- Produce可复用的初始化/更新helper，服务器观察时间由调用者提供，写入前已有session锁；ListSessions批量加载LATEST。

- [ ] **Step 1:** TestSessionClientInfoSnapshots先RED，覆盖初始/最近、原生升级、缺字段合并、名称与版本、nil不更新时间；将显式Go元数据校验放凭证消费之前。
- [ ] **Step 2:** TestPendingClientInfoSnapshots先RED，覆盖首次保存、完成时更新、旧payload缺字段、首次无描述只建LATEST、设备头兼容和重放不更新；实现加密payload可选字段及持久化。
- [ ] **Step 3:** TestRefreshClientInfoGrace先RED，验证仅新轮换写LATEST，宽限/失败不写，来源/INITIAL保持；接入reauth及refresh事务。
- [ ] **Step 4:** TestSessionContextCleanupAndPurge先RED，覆盖30天保留、有界删除、历史会话及回调回滚；实施关联/快照与session清理，验证无孤儿。
- [ ] **Step 5:** 运行完整user、迁移和根快照/恢复相关测试，提交并报告，无每业务请求更新。

### Task 6: HTTP、宿主装配与服务集成

**Files:** Modify httpapi/enduser/{handler,signin,signin_completion,me,oauth,response}.go；Create current_session.go及测试；Modify httpapi/admin/{response,sessions}.go、accountkit.go、internal/accountsvc集成测试。

**Interfaces:** 新增可选CurrentSessionService{GetCurrentSession(context.Context,user.Principal)(user.CurrentSessionInfo,error)}；Deps.ClientInfo func(*http.Request)(*user.ClientInfo,error)，nil使用clientinfo.Parse；纯authMeta助手支持错误返回，仅登录/补全/reauth/token调用。

- [ ] **Step 1:** TestCurrentSessionEndpoint先RED，覆盖user/user:bind/user:undelete、缺能力503、DB失效401挑战、503/500、no-store、资源名、RFC3339及身份类型；接入新GET和专用DTO，保留旧Service接口。
- [ ] **Step 2:** TestAuthenticationClientInfoHeaders先RED，验证每个认证入口传Meta、非法显式字段在消费前400、/token OAuth、宿主回调及旧客户端；接入装配，普通Authenticate/内省保持无DB。
- [ ] **Step 3:** TestSessionClientInfoDTO验证消费者/管理员分别脱敏、字段省略和批量列表不N+1；新详情包含INITIAL/LATEST，列表仅LATEST。
- [ ] **Step 4:** 根HTTP真实DB流程与accountsvc代理集成验证登录→查询→reauth→解绑→当前失效，以及可选头透传/旧客户端；数据库故障不能返回伪401。
- [ ] **Step 5:** 运行对应HTTP/服务包及项目完整测试一次，提交并报告。

### Task 7: 文档、OpenAPI与恢复测试

**Files:** Modify README.md、docs/{compatibility,go-integration,migrations,accountsvc,gateway-integration,openapi}.md（按实际已有路径）、docs/openapi/*.json、examples/gointegration、契约测试、recovery_test.go、scripts/required-tests.txt及服务必需测试清单。

**Interfaces:** Consume任务1–6已实现的实际Go/HTTP契约；保持原token和身份列表字段。

- [ ] **Step 1:** 更新OpenAPI和真实契约测试，覆盖当前会话和新头/客户端摘要，严格状态码/oneOf检查通过；只修改公开文件或既有生成源，不手工改生成Go。
- [ ] **Step 2:** 更新可编译示例，显示重新认证后重查、来源未知保守提示和成功解绑后退出；说明位置式Go初始化及直接Go解绑Principal前置条件。
- [ ] **Step 3:** 更新迁移/部署/网关说明，明确协调升级、旧v2拒绝、回退限制、CORS头、初始/最近观测范围及无原始UA/IP持久化。
- [ ] **Step 4:** 扩展恢复fixture验证新增来源和快照的备份/恢复、旧来源未知；将新必需顶层测试加入清单，保留TestRecoveryFixture正常skip规则。
- [ ] **Step 5:** 运行文档/OpenAPI入口、go test ./examples/gointegration以及对应fixture普通阶段，检查链接，提交。

### Task 8: 全量门禁、执行计划和最终复核

**Files:** Modify本change tasks.md/verification.md及本计划勾选；只为发现的真实缺陷修改对应范围，禁止放宽门禁。

**Interfaces:** Consume所有实现；用精确Git快照构造Linux验证副本，包含完整Git历史/tags和本任务代码。

- [ ] **Step 1:** 在一次性PG17生成代表性会话/来源数据，EXPLAIN按身份撤销和当前会话读取，验证索引、用户范围及列表无N+1，保存规模/计划。
- [ ] **Step 2:** GOWORK=off执行build/vet/go test；Linux镜像执行race、scripts/verify.sh、accountsvc严格入口和独立空源/目标恢复；保存实际日志，不以skip通过冒充集成通过。
- [ ] **Step 3:** check-generated.sh、check-migrations.sh、openspec validate --all --strict、diff检查；按实际覆盖勾选32项验收任务。
- [ ] **Step 4:** 提供最终代码审查包（基线8190167至实施HEAD），解决实质发现后重跑受影响测试；把证据/限制写verification.md。
- [ ] **Step 5:** 保留实施分支和工作区供用户审阅；仅清理本任务明确创建的测试资源，未经另行授权不合并、推送、发布、归档。
