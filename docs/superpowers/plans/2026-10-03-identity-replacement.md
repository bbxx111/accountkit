# Identity Replacement Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实施消费者同类手机/邮箱原子换绑，保持当前会话并撤销其他会话。

**Architecture:** 新领域入口先预检、在事务外消费 BIND 码，再按 user → session 锁序复核并提交身份/会话变更。HTTP 通过可选能力接口接入；登录/重新认证与批量撤销补齐并发保护。库与 accountsvc 共用实现。

**Tech Stack:** Go 1.26.5、pgx/v5、PostgreSQL 17、go-redis/v9、Chi、sqlc 1.31.1，沿用既有依赖。

**Spec:** [设计](../../../openspec/changes/add-identity-replacement/design.md)、[规格](../../../openspec/changes/add-identity-replacement/specs/identity-replacement/spec.md)、[任务](../../../openspec/changes/add-identity-replacement/tasks.md)。

## Global Constraints

- 工作限定在 feat/add-identity-replacement 当前任务 checkout；不直接修改集成分支。
- 用户 apply 已授权整份提案和默认子代理执行方式，不重复索要实施/计划确认。
- user_id 不变；PHONE→PHONE、EMAIL→EMAIL；新身份新 ID、旧身份软删除，复用 BIND 码和原 DTO。
- 近期认证默认5分钟、默认启用；当前会话保留，其他会话数据库撤销同事务提交；保留 access 吊销 fail-open 和异步审计。
- Redis 校验在数据库写锁外；回滚不恢复已消费码。不得扩大为找回、合并、通知或真实外部环境验收。
- 不改冻结 SQL/manifest；不手改生成代码；如查询变化，sqlc generate 后检查生成结果。
- 实现先写行为测试并观察失败，独立审查后进入依赖步骤；协调者维护 tasks、共享清单、文档和提交。

## Review Focus

- 新鲜度恰好位于边界或在预检后过期：Task1固定5分钟边界及事务内复核，不信任旧预检。
- 同目标、本人另一身份、其他账号占用：Task1分别验证400/409，后两者先消费正确新码且不泄露归属。
- 登录/重认证已经读到旧身份再等待锁：Task2用真实数据库锁编排交错，不能以随机并发代替。
- 多会话撤销与换绑持锁交错：Task2/4验证不存在 session→user 反向写锁以及撤销后刷新复活。
- 成功响应丢失及 Redis 提交后故障：Task1/4验证旧路径404、列表确认、已提交结果保留及 fail-open 告警。

### Task 1: 领域换绑、配置与审计（OpenSpec 1.2、2.1–2.4、4.2–4.3）

**Files:** 新增 user/service_identity_replacement.go、user/service_identity_replacement_test.go；修改 user/{service.go,errors.go}、enum/{enum.go,enum_test.go}、accountkit.go；增加 accountkit_replacement_config_test.go。仅必要时修改 user/query.sql 并生成。

**Interfaces:**
- 产出 `(*user.Service).ReplaceIdentity(ctx context.Context,p user.Principal,identityID string,channel enum.IdentityKind,target,plainCode string,meta user.Meta)(user.IdentityInfo,error)`。
- 产出 `user.ErrInsufficientScope`、`user.ErrReauthenticationRequired`、`user.ErrIdentityUnchanged`；复用 ErrIdentityConflict、ErrNotFound、ErrInvalidToken 等。
- user.Deps 增加 `ReauthMaxAge time.Duration`、`SensitiveOpVerification *bool`，0/nil 默认5m/true，负时长拒绝；accountkit.New 注入现有 Config 默认后的值。
- enum 追加 `RevokeIdentityReplaced` / `IDENTITY_REPLACED` 和 `EventIdentityReplaceRejected` / `IDENTITY_REPLACE_REJECTED`，旧编号不变。
- 消费已有归一化、摘要多版本、createAnchorIdentity、事务、revokeAllForUser/afterRevokeAll，不修改 Task2 所有的旧登录/会话文件。

- [ ] 写 TestReplaceIdentity 成功/预检/配置/新目标证明/审计测试，观察新方法缺失或行为断言失败；覆盖手机号和邮箱、同类上限1、时间边界、nil旧配置及显式false。
- [ ] 实现新方法与可选配置：预检 → Verify BIND → 事务持锁复核 → 唯一性/最终数量 → 软删/新建/撤销其他 → 提交后 Redis/审计；失败固定安全原因，验证码不会因数据库失败恢复。
- [ ] 写真实PG回滚/唯一冲突/摘要轮换/码计数/同旧身份并发测试；使用触发器或其他测试专用故障注入验证新建/撤销失败整体回滚，不添加生产测试钩子。
- [ ] 验证当前会话刷新与auth_time不变，其他current/previous refresh拒绝、正常access吊销，提交后Redis写失败保留成功并告警。执行 `go test -count=1 ./user ./enum .`、对应vet，报告实际DB执行和红绿证据；审查后由协调者提交。

### Task 2: 旧凭据竞态与一致锁序（OpenSpec 3.1–3.3）

**Files:** 修改 user/{service_signin.go,service_me.go,service_session.go,service_admin.go}；新增 user/service_identity_race_test.go；必要查询改动由协调者协调Task1，禁止并行编辑query.sql。

**Interfaces:** 保留现有方法签名；登录等待用户锁后发现旧identity不再活动/匹配时返回 code.ErrInvalid；Reauthenticate 在 user → session 锁内复核anchor，返回 ErrNotAnchor；批量撤销先取user写锁，保持原不存在用户语义。

- [ ] 写 TestSignInRejectsIdentityRemovedWhileWaiting 和 TestReauthenticateRejectsIdentityRemovedWhileWaiting，以真实PG用户锁和事务软删除旧身份重现竞态；先观察旧实现错误地登录/更新auth_time，再实现锁后复核。
- [ ] 为 RevokeOtherSessions/AdminRevokeAllSessions 补user锁，测试不存在用户/冻结用户等原语义不变，以及确定性的锁获取/等待行为。不要改变单会话刷新和撤销协议。
- [ ] Task1通过审查后，补 TestReplacementConcurrentMutations 等实际换绑与登录、重认证、解绑、冻结、注销、当前退出、用户/管理批量撤销、刷新竞争测试；断言合法结果和无死锁，不仅断言无错误。
- [ ] 执行 `go test -count=1 ./user`（真实PG）及相关race/vet，报告新增顶层测试名供必需清单；独立审查后提交。

### Task 3: HTTP 可选能力与响应（OpenSpec 1.2、4.1）

**Files:** httpapi/consumer/{handler.go,identities.go,identity_replacement_test.go}；必要时单独 replacement.go。不向现有 consumer.Service 添必需方法，不修改全局旧错误消息。

**Interfaces:** 产出可选 `IdentityReplacer` 接口，其 ReplaceIdentity 签名与Task1一致；在相对 `/users/me/identities/{identity}:replace` 注册 fullScope/RequireRecentAuth，使用既有 credential.anchor 和 identityResource。

- [ ] Task1独立审查后编写可选接口/真实路由失败测试，验证未实现接口的旧fake仍满足consumer.Service并返回503；观察404或未映射错误的RED。
- [ ] 实现单一端点：400 IDENTITY_UNCHANGED、403 INSUFFICIENT_SCOPE、400 REAUTHENTICATION_REQUIRED、409 IDENTITY_ALREADY_BOUND通用安全消息、503 IDENTITY_REPLACEMENT_NOT_CONFIGURED，其他复用既有映射；返回200掩码DTO，无token。
- [ ] 执行 `go test -count=1 ./httpapi/consumer` 与vet；覆盖已有DELETE路由、尾部action匹配、oneof/未知字段/超限、认证前拒绝和可选接口兼容；审查后提交。

### Task 4: 嵌入式与服务完整验收（OpenSpec 5.1–5.3）

**Files:** 新增 identity_replacement_test.go、internal/accountsvc/identity_replacement_integration_test.go；协调者维护 scripts/{required-tests.txt,accountsvc-required-tests.txt}。

**Interfaces:** 消费真实Auth门面和进程HTTP端点；复用现有root数据库fixture和service integrationFixture。SERVER_TEST_DB_DSN/ACCOUNTSVC_TEST_REDIS_URL 为本次隔离资源，SMTP/OIDC使用本地fixtures。

- [ ] Task1–3完成审查后写 TestIdentityReplacementEndToEndAgainstRealDB 和 TestServiceIntegrationIdentityReplacement；真实发码、两设备登录、必要reauth、换绑、旧路径404、新列表、新身份登录/旧地址不进入原账号。
- [ ] 验证当前refresh连续可用、其他refresh及access/内省拒绝、服务重启后新绑定仍生效；凭据不进入响应/日志/审计，服务短信禁用行为不变。
- [ ] 登记必需测试，执行 Linux `scripts/verify-accountsvc.sh`，确保新测试真实pass；协调者执行完整 `scripts/verify.sh`（含race和独立空库恢复）。独立审查后提交。

### Task 5: 文档与最终交付（OpenSpec 6.1–6.3）

**Files:** README.md、docs/{compatibility,accountsvc}.md、docs/identity-replacement-verification.md、当前change design/tasks。

- [ ] 记录最终接口/可选接入、认证语义、旧地址自动注册、码消费/回滚、fail-open、枚举兼容和不确定结果恢复，校验本地链接。
- [ ] 运行 build/vet、真实并发、Linux race、生成/迁移检查、完整库和服务严格入口、OpenSpec strict；清楚区分实际结果和暂缓外部联调。
- [ ] 整分支独立审查，修复并定向复核实质问题；仅在验证后勾选对应任务。提交实现和证据，保留任务分支，不自动推送、合并或归档。
