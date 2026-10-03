# Session Expiry Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 统一会话期限边界，修复列表、刷新和重新认证的到期处理。

**Architecture:** 领域内部提供UTC微秒时间/可续用判定；SQL过滤列表计数并保护写入。刷新及重新认证分别在锁后重新取时钟，宽限资格与缓存返回时刻分离。明确撤销与access验证保持原契约。

**Tech Stack:** Go 1.26.5、sqlc 1.31.1、PostgreSQL17、pgx/v5、Redis/go-redis，沿用现有依赖。

**Spec:** [设计](../../../openspec/changes/harden-session-expiry/design.md)、[期限规格](../../../openspec/changes/harden-session-expiry/specs/session-expiry/spec.md)、[兼容增量](../../../openspec/changes/harden-session-expiry/specs/embedded-auth-package/spec.md)。

## Global Constraints

- 仅fix/harden-session-expiry任务checkout；用户apply已授权完整实施和默认子代理执行，不重复询问计划许可。
- 等于期限即过期；UTC截断微秒；不增加JWT leeway、不改变15m access/30d滑动refresh/30s宽限默认值。
- 只过滤可续用列表/计数，不过滤明确撤销集合；自然到期不写撤销集，不改Authenticate/内省fail-open。
- 查询只改user/query.sql，由sqlc生成；冻结SQL、schema、go.mod和公开根Config/Deps/Service签名不变。
- 原角色、scope、身份复核、锁序、验证码一次消费和异步审计均保留；不新增历史会话API、最大设备数或外部部署验收。
- 每步TDD与独立审查；仅并行无共享写入文件的任务。协调者拥有文档、清单、任务状态和提交；不自动合并/推送/归档。

## Review Focus

- 微秒等号和亚微秒截断在Go/PG一致：Task1验证，不能使用测试真实wall clock替代已有fixture clock。
- 等锁后过期与有效滑动续期：Task2/3确定性行锁+原子可控时钟验证，不靠sleep猜测锁已获取。
- 缓存等待只跨宽限但未过会话期限：Task2仍返回同一pair；仅跨会话/pair期限才拒绝，不误吊销。
- 过期会话仍有有效access：Task4验证列表隐藏与正常Authenticate/内省并存，明确撤销随后仍生效。
- 共享查询升级不缩小批量撤销/重登/换绑集合：Task4覆盖旧行撤销和保留期；Task1不改相应未吊销查询谓词。

### Task 1: 时间判定、列表与SQL保护（OpenSpec1.2、2.1–2.2）

**Files:** 新增user/session_expiry.go、user/session_expiry_test.go、user/session_expiry_query_test.go；修改user/query.sql并生成user/db；更新user/service_session.go的ListSessions部分、user/service_admin.go计数部分及受生成签名影响的既有测试调用。仅该任务拥有SQL/生成结果及调用适配，完成后才进入Task2/3。

**Interfaces:**
- `sessionTime(t time.Time) time.Time` 返回UTC微秒截断；`sessionActiveAt(sess db.Session,at time.Time) bool` 判断未吊销且RefreshExpireTime.After(sessionTime(at))。
- 私有 `errSessionExpired` 包装公开ErrInvalidToken供Task3审计分类，不新增公开API。
- ListActiveSessionsByUser、CountActiveSessionsByUser 增加 `user_id`、`now::timestamptz` sqlc参数，调用处传sessionTime(s.now())。RotateSession已有Now、UpdateSessionAuthTime已有AuthTime分别作为SQL过期保护时刻。
- 不修改GetActiveSessionByIDAndUser/GetActiveSessionByUserDevice/RevokeSession/RevokeSessionsByUser的期限范围。

- [x] 写 TestSessionActiveAt 与真实PG TestSessionExpiryQueries：混合有效/到期/撤销/他人行、等号与±1微秒、亚微秒/时区、无读副作用和过期直接写入0行；观察现有实现RED。
- [x] 实现辅助规则及查询，sqlc generate；适配所有调用（包括既有测试），保留公开Service签名和旧未吊销查找的用途。
- [x] 运行真实PG `go test -count=1 ./user`、`go vet ./user` 和相关根编译，确认既有换绑/冻结/注销/会话测试无回归；独立审查后提交。生成一致性最终由协调者在提交后验证。

### Task 2: 刷新时刻与宽限（OpenSpec3.1–3.3）

**Files:** user/service_session.go刷新相关部分；新增user/service_session_expiry_test.go。使用Task1已审helper和SQL，不编辑shared query/generated。

**Interfaces:** 保持Refresh公开签名；私有rotate可去掉旧now参数，锁后检查直接产生ErrInvalidGrant和一次SESSION_EXPIRED拒绝审计。资格时刻在会话读取后；缓存返回时刻只复核会话/pair期限和计算剩余时间，不重新判定已合格的宽限请求。

- [x] 先写 TestRefreshExpiryAfterSessionLock、TestRefreshUsesLockedDecisionTime，真实PG锁等待并推进原子时钟；观察旧实现续用/期限偏移错误后修复，验证拒绝无字段/缓存写入，有效情况从锁后时间续期。
- [x] 写 TestRefreshGraceExpiryBoundaries：current/previous query等待、资格边界、CAS胜者pair、缓存等待跨期限或仅跨宽限、缺失/失败/过期pair；通过测试专用Redis Hook/代理编排，不增加生产钩子。
- [x] 修正时间采样/错误分类和剩余有效期，保证缓存失败/自然到期不误吊销、原宽限外重放处理保留；运行真实PG刷新和完整user测试、vet，独立审查后提交。

### Task 3: 重新认证与换绑对齐（OpenSpec4.1–4.2）

**Files:** user/service_me.go、user/service_identity_replacement.go；新增user/service_reauth_expiry_test.go。与Task2在Task1审查后可并行，写入文件不重叠。

**Interfaces:** 使用Task1 sessionTime/sessionActiveAt/errSessionExpired；保持Reauthenticate/ReplaceIdentity公开签名。REAUTH过期仍errors.Is(ErrInvalidToken)，审计SESSION_EXPIRED；auth_time/签发使用锁后时间。

- [x] 写 TestReauthenticateExpiryAfterLocks、TestReauthenticateUsesLockedDecisionTime，覆盖进入即到期、分别等user/session锁后到期、正常情况和显式false新鲜度开关；观察失败后最小实现。
- [x] 复用helper对齐换绑，保留完整授权/身份检查；确保过期拒绝无auth_time/update_time/expiry修改，无新token，已消费码不恢复；重跑换绑22场景及旧身份复核回归。
- [x] 运行真实PG `go test -count=1 ./user` 和vet，报告HTTP错误与审计结果的对应，独立审查后提交。

### Task 4: 协议、撤销保留和服务验收（OpenSpec5.1–5.3）

**Files:** 新增session_expiry_test.go（根门面）、user/service_expired_revocation_test.go、internal/accountsvc/session_expiry_integration_test.go；协调者维护scripts两份required-tests清单。

**Interfaces:** 复用根dbDSN/miniredis/captureSender与service integrationFixture；根测试只要求SERVER_TEST_DB_DSN，服务使用真实PG/Redis/SMTP/OIDC fixtures。SQL在测试隔离schema中构造期限，不增加生产时间钩子。

- [x] 按已确定协议先写黑盒TestSessionExpiryEndToEndAgainstRealDB、TestServiceIntegrationSessionExpiry，可独立观察旧端点行为RED；最终GREEN须Task1–3审查通过。
- [x] 验证消费者/管理员列表及active_session_count、400invalid_grant、401TOKEN_INVALID/challenge、access/内省仍有效但显式撤销后无效；DTO形状不变，过期拒绝不产生撤销写入。
- [x] 写 TestExpiredSessionsRemainRevocable 覆盖单个、双方批量、RFC7009、同设备重登、冻结/注销/换绑的到期行及revoked_count/审计；验证30天清理边界保持。
- [x] 定向真实依赖测试/vet并独立审查；注册必需测试，协调者运行全套Linux race、严格库/恢复和服务入口，不用skip代替。

### Task 5: 文档和交付（OpenSpec6.1–6.3）

**Files:** README.md、docs/compatibility.md、docs/accountsvc.md、openspec/changes/harden-session-expiry/verification.md、change tasks/design与本计划。

- [x] 写明确升级/回退说明：列表变化、过期reauth重新登录、独立JWT有效期、明确撤销覆盖、码消费及滚动升级边界；检查本地链接。
- [x] 运行独立build/vet、真实PG并发、Linux race、sqlc/迁移检查、verify.sh及verify-accountsvc.sh，保留实际日志和外部验收暂缓事实。
- [x] 最终整分支独立审查、修正及定向复核；OpenSpec全量严格校验，验证后勾选全部任务并提交，保留任务分支。
