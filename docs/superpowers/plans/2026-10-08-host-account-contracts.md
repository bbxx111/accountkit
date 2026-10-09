# accountkit 接入契约 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (- [ ]) syntax for tracking.

**Goal:** 为嵌入式宿主发布包含验证码轮次、跨轮次失败预算、公开资料查询、生命周期事务保护和受控离线导入能力的 accountkit 固定版本。

**Architecture:** 在本仓库修改共享领域与 HTTP 适配器，宿主只消费固定版本，不复制认证实现。验证码在 Redis 中原子校验和消费；业务扩展通过公开 Go 契约接入，同库事务由库持有。

**Tech Stack:** Go 1.26.5、Chi、pgx/v5、PostgreSQL、go-redis/v9、Lua、sqlc 1.31.1、miniredis。

**Spec:** [OpenSpec Change](../../../openspec/changes/archive/2026-10-09-add-host-account-contracts/proposal.md) 与 [技术设计](../../../openspec/changes/archive/2026-10-09-add-host-account-contracts/design.md)；全部规格位于该 Change 的 specs/，任务状态以 tasks.md 为准。

**工作仓库:** accountkit。下面 Files 均相对于本仓库；执行者读取本仓库 AGENTS.md 和 Change 的完整工件。宿主接入在宿主仓库单独处理。

## Global Constraints

- Schema 与 PoolConfig/search_path 一致；默认 account，合法显式配置保持。开发版默认 account 与 v0.1.0 默认 auth 的区别保留在兼容说明。
- code_id 为 crypto/rand 生成的 128 bit 随机值、32 位小写十六进制；验证码有效期 5 分钟、发送冷却 60 秒；每轮沿用默认 5 次，并支持宿主显式配置 3 次。
- 同一渠道、归一化目标跨 SIGN_IN/BIND/REAUTH 在首次错误起 15 分钟内最多 10 次错误；重发、正确验证码与后续错误不重置或延长窗口。
- 旧轮次不得消耗新轮次预算；验证码消费至多成功一次；Redis 故障返回 503，不提供无 code_id 校验旁路。
- 不修改冻结 SQL、已发布 tag、源基线和生成代码；查询变更通过 sqlc generate 生成。
- Anonymizer/BeforeDelete 使用库传入的同一 pgx.Tx；公开资料不返回联系方式或身份密文。
- 原 JWT、refresh、scope、管理员身份边界和审计失败不影响认证结果的契约保持。
- GOWORK=off 独立构建；不提交本地 replace、真实秘密或用户数据。发布必须使用新不可变版本。

## Review Focus

- 投递超时后另一轮已生成：按 code_id 比较后清理不能删除较新轮次，任务 1/2 覆盖。
- 同一密钥材料以多个版本配置：计数不能重复，也不能通过 active 轮换恢复失败预算，任务 1 覆盖。
- 两轮恰好产生相同 6 位数字：旧 code_id 仍须失败，任务 1 覆盖。
- 同一目标在两个账号/会话操作：BIND/REAUTH 的主体绑定不得交叉消费，任务 2 覆盖。
- 多个账号锁与团队锁交错：锁序一致，回调失败全事务回滚；导入冲突不得留下半批账号，任务 4/5 覆盖。

## 文件与接口边界

新增 user/code/challenge.go 放轮次参数和结果，challenge_scripts.go 放新 Lua；保留现有发送额度/HMAC 轮换职责，不把投递逻辑塞进 Store。user/code_credential.go 放公开验证码契约，service_profiles.go、service_host.go、service_import.go 分别负责批量公开资料、事务扩展、离线导入。根门面只传递新增配置和依赖；HTTP handler 继续只做协议转换。

## Task 1 验证码轮次和跨轮次失败预算

**Files:** Create user/code/challenge.go、user/code/challenge_scripts.go、user/code/challenge_test.go；Modify user/code/store.go、scripts.go、rotation_test.go、rotation_integration_test.go、rotation_fault_test.go、config.go、config_test.go、accountkit.go。

**Interfaces:** 新增 code.Binding { UserID string; SessionID string }、code.Issued { CodeID string; Code string; ExpireTime time.Time }、code.Credential { Channel enum.IdentityKind; Purpose enum.CodePurpose; Target string; CodeID string; Code string; Binding Binding }。Store.IssueChallenge(ctx context.Context, channel enum.IdentityKind, purpose enum.CodePurpose, target, ip string, binding Binding) (Issued, error)；Store.VerifyChallenge(ctx context.Context, credential Credential) error；Store.DiscardChallenge(ctx context.Context, channel enum.IdentityKind, purpose enum.CodePurpose, target, codeID string) error。DiscardChallenge 只删除仍匹配 codeID 的轮次。任务 1 使用新增方法和独立 challenge 数据键，保留旧 Issue/Verify 供现有调用者编译；任务 2 完成所有调用迁移后移除旧方法，本计划完成并发布时不保留旁路。发送冷却/日额度和失败预算独立于轮次数据键。

**配置:** Config 和 code.Options 新增 CodeFailureLimitPerTarget/FailureLimitPerTarget int、CodeFailureWindow/FailureWindow time.Duration；默认 10、15 分钟。新增 CODE_FAILURE_LIMIT_PER_TARGET、CODE_FAILURE_WINDOW 环境变量，沿用宿主前缀。CodeMaxAttempts 仍可配置，需要 3 次策略的宿主显式设置，不更改其他宿主已有默认 5。

- [ ] 写 TestChallengeIsolation：Issue A→重发 B→Verify A 失败，B 的次数为 0；令两轮 code 相同，A 仍失败；目标、渠道、用途和绑定变化均拒绝。
- [ ] 写 TestChallengeAttemptBoundary/AtomicConsume：第三次正确允许成功，第三次错误立即作废；并发校验恰好一次成功；旧标识不扣新次数，不能先计数再在 Go 中检查标识。
- [ ] 写 TestChallengeFailureBudget：每轮 3 次、重新发码后累计预算继续；第 10 次错误后发码/校验受限；第一次错误后第 15 分钟恢复，重发/正确码不延长窗口。验证 RetryAfter 等于剩余时间。
- [ ] 写 TestChallengeDiscardRace、TestChallengeRotationBudget：清理 A 不删 B；不同 HMAC 版本合并计数，相同材料别名去重；Redis 不可用和损坏状态均不绕过限制。
- [ ] 运行 go test -count=1 ./user/code ./ -run 'TestChallenge|TestConfig'，先确认因缺接口/行为失败。
- [ ] 实现上述接口和 Lua。目标已有最新轮次 hash 中存 code_id/主体绑定，验证码 HMAC 输入包含 code_id；不新增明文 target。轮次匹配、限流、摘要比对、计数、消费同一次 Lua 完成；失效轮次统一 ErrExpired，累计预算拒绝使用 RateLimitedError Dimension=TARGET_VERIFY_LIMIT。
- [ ] 对原发送额度/轮换测试补充新参数并保持原断言；运行完整 user/code 测试及真实 Redis 轮换集成用例，检查没有 skip 才记录集成通过。
- [ ] gofmt，复核没有将 raw CodeID/Code 写入日志；提交 feat(code): add isolated verification challenges。

任务 1 的核心断言示例（使用 maxAttempts=3 的合成 fixture）：

```go
if len(issued.CodeID) != 32 { t.Fatalf("code_id length = %d", len(issued.CodeID)) }
if err := store.VerifyChallenge(ctx, oldCredential); !errors.Is(err, code.ErrExpired) { t.Fatalf("old round: %v", err) }
if err := store.VerifyChallenge(ctx, currentCredential); err != nil { t.Fatalf("current round: %v", err) }
```

## Task 2 领域和 HTTP 使用完整轮次协议

**Files:** Create user/code_credential.go、user/service_challenge_test.go、httpapi/enduser/challenge_test.go；Modify user/service_signin.go、service_me.go、service_identity.go、service_identity_replacement.go、相关 user/*_test.go、httpapi/enduser/{handler,request,response,signin,me,identities,replacement,fake_test}.go、对应 HTTP 测试、compatibility_test.go、examples/embedded/main.go、internal/accountsvc/*integration_test.go。

**Interfaces:** user.CodeChallenge { CodeID string; ExpireTime time.Time }；user.CodeCredential { Channel enum.IdentityKind; Target string; CodeID string; Code string }。

发码方法 SendSignInCode(ctx, channel, target, meta)、SendBindCode(ctx, p, channel, target, meta)、SendReauthenticationCode(ctx, p, channel, target, meta) 都返回 (CodeChallenge,error)。消费方法统一为 SignInWithCode(ctx context.Context, cred CodeCredential, dev Device, meta Meta) (TokenResult,error)、BindWithCode(ctx context.Context,p Principal,cred CodeCredential,meta Meta) (IdentityInfo,bool,error)、Reauthenticate(ctx context.Context,p Principal,cred CodeCredential,meta Meta) (TokenResult,error)、ReplaceIdentity(ctx context.Context,p Principal,identityID string,cred CodeCredential,meta Meta) (IdentityInfo,error)。其余方法保持。

- [ ] 写 TestCodeChallengeHTTPContract：三种发码成功含 code_id/expire_time 和 Cache-Control:no-store；phone/email oneof 必须含合法 code_id；缺失、跨用途、已消费标识返回 400；错误码不产生 401；格式错误的 code_id 返回 400 INVALID_ARGUMENT，未知/失效合法标识统一 CODE_EXPIRED；累计预算为 429 + TARGET_VERIFY_LIMIT + Retry-After。
- [ ] 写 TestCodeChallengePrincipalBinding：BIND 固定 UserID，REAUTH 固定 UserID/SessionID；跨主体不能消费。换绑沿用 BIND 但保留原近期认证、账号/身份归属与最后身份约束。
- [ ] 写 TestCodeChallengeDeliveryCleanup：provider 成功才返回标识；失败不退发送额度，Discard 不误删新轮次；依赖异常返回 503，投递失败路径日志/审计不含 CodeID、完整 target、验证码；开发 LogSender 的既有显式调试行为保留。
- [ ] 运行 go test -count=1 ./user ./httpapi/enduser -run 'TestCodeChallenge' 确认新增行为失败。
- [ ] 使用任务 1 的 IssueChallenge/VerifyChallenge/DiscardChallenge，在领域层归一化目标、确定 Purpose/Binding，再传 Store；将私有 sendCode 返回 CodeChallenge。HTTP credential 增加 code_id，发码写明确 DTO；enduser.Service、IdentityReplacer 和 fake 实现同步更新，不能保留旧签名旁路。
- [ ] 更新现有测试和示例的调用参数，保留 scope/刷新/注销/发送失败原语义；覆盖独立 accountsvc 相同协议。运行 go test -count=1 ./user ./httpapi/enduser ./internal/accountsvc，再运行对应真实依赖顶层测试。
- [ ] gofmt，记录公开 Go/HTTP/Redis 格式破坏性变化；提交 feat(user): require verification challenge credentials。

任务 2 的核心 HTTP 断言示例：

```go
if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "CODE_EXPIRED") { t.Fatal(w.Code, w.Body.String()) }
if limit.Code != http.StatusTooManyRequests || limit.Header().Get("Retry-After") == "" { t.Fatal(limit.Code, limit.Header()) }
```

## Task 3 批量公开资料

**Files:** Create user/service_profiles.go、user/service_profiles_test.go；Modify user/query.sql 和 sqlc 生成的 user/db/query.sql.go。

**Interfaces:** user.PublicProfile { ID string; DisplayName string; State enum.UserState }；(*Service).BatchPublicProfiles(ctx context.Context, ids []string) (map[string]PublicProfile,error)。仅内部 Go 调用，不新增公开 HTTP 任意用户查询。

- [ ] 写 TestBatchPublicProfiles：空输入返回空 map，重复 ID 去重、未知 ID 不出现在 map，ACTIVE 返回显示名；PENDING_DELETION/DELETED 返回空显示名；结果没有身份/联系方式字段。
- [ ] 运行 go test -count=1 ./user -run TestBatchPublicProfiles 确认失败。
- [ ] 新增单次 WHERE id=ANY 查询，只选 id/display_name/state；验证用户 ID 格式，去重后一次批量查询。实现 BatchPublicProfiles，单次查询，无逐用户 GetMe。
- [ ] sqlc generate、gofmt，运行资料测试；用真实库检查查询次数为一次，随后执行 bash scripts/check-generated.sh。
- [ ] 提交 feat(user): add batch public profiles。

任务 3 的核心结果断言示例：

```go
if _, exists := got[unknownID]; exists { t.Fatal("unknown profile leaked") }
if got[deletedID].DisplayName != "" { t.Fatal("deleted profile retained a name") }
```

## Task 4 删除前置检查和账号事务保护

**Files:** Create user/service_host.go、user/service_host_test.go、host_lifecycle_db_test.go；Modify accountkit.go、user/service.go、user/service_lifecycle.go、user/errors.go、httpapi/enduser/response.go、httpapi/admin/response.go、host_contract_test.go。

**Interfaces:** user.BeforeDelete func(ctx context.Context,tx pgx.Tx,userID string) error；根 Deps 与 user.Deps 新增 BeforeDelete user.BeforeDelete。新增 user.ErrDeletionBlocked，两个 HTTP 面映射 400 FAILED_PRECONDITION，reason=DELETION_BLOCKED，固定消息。(*Service).WithActiveUsers(ctx context.Context,userIDs []string,fn func(pgx.Tx) error) error：去重并按 ID 排序，通过 Repo.WithTxRaw 开事务、依序锁账号、要求 ACTIVE，再调用 fn；库负责提交/回滚，回调禁止自行提交。

- [ ] 写 TestBeforeDeleteSharedTransaction：DeleteMe/AdminDeleteUser 都在锁后、状态改变前调用 hook；ErrDeletionBlocked 不改变账号、会话或宿主数据；其他错误传播为内部错误。
- [ ] 写 TestWithActiveUsers：非 ACTIVE/未知/非法 ID 拒绝且 callback 不运行；重复 ID 只锁一次；参数逆序的并发调用不死锁；callback error/panic 回滚。
- [ ] 写 TestHostDeleteCreateRace：创建宿主 owner 关系与注销竞争，用同一账号锁；最终只能是 ACTIVE+owner，或 PENDING_DELETION 且无新增 owner，不出现 PENDING_DELETION+新 owner。
- [ ] 运行对应 user 和根顶层测试确认失败。
- [ ] softDelete 改用 WithTxRaw，锁账号并校验后调用 BeforeDelete，然后按原规则软删除/吊销；实现 WithActiveUsers，不暴露 Repo 的私有成员、不在 callback 外开另一事务。
- [ ] 运行单元与一次性 PostgreSQL 并发测试，显式确认真实库用例执行且未 skip；检查原匿名化回滚测试仍通过。
- [ ] 提交 feat(user): add host lifecycle transaction hooks。

任务 4 的拒绝断言示例：

```go
if !errors.Is(err, user.ErrDeletionBlocked) { t.Fatalf("delete result: %v", err) }
if account.State != enum.UserActive || session.RevokeTime != nil { t.Fatal("blocked delete mutated account/session") }
```

## Task 5 受控存量账号导入

**Files:** Create user/service_import.go、user/service_import_test.go；Modify user/query.sql、生成的 user/db/query.sql.go；补充 docs/compatibility.md 的离线导入说明。

**Interfaces:** user.ImportAnchor { Kind enum.IdentityKind; Target string; CreateTime time.Time; UpdateTime time.Time }；user.ImportAccount { ID string; State enum.UserState; DisplayName string; CreateTime time.Time; UpdateTime time.Time; DeleteTime *time.Time; PurgeTime *time.Time; Anchors []ImportAnchor }；(*Service).ImportAccounts(ctx context.Context,tx pgx.Tx,accounts []ImportAccount) error。仅离线 Go 工具使用，无 HTTP 路由，tx 来自 schema 匹配的认证连接池，由调用者提交。

- [ ] 写 TestImportAccounts：ACTIVE 至少有一个 PHONE/EMAIL 身份，保持 ID/原时间，使用库的归一化/加密/摘要；DELETED 墓碑没有锚点和显示名；禁止 FROZEN/PENDING_DELETION、非法 ID 和重复归一化身份。
- [ ] 写 TestImportAccountsRollback：第二个账号身份冲突，调用者回滚后第一个账号和业务引用均不存在；接口不创建会话、不发码、不另开事务、不输出明文数据。
- [ ] 运行 go test -count=1 ./user -run TestImportAccounts 确认失败。
- [ ] 添加专用插入查询保存原时间/墓碑状态，实现导入验证；只复用库内部 normalizeTarget/Cipher/Digester，拒绝自动合并。非空目标账号表的再次导入由外部工具的迁移记录检查控制，函数对重复 ID 明确失败。
- [ ] sqlc generate、gofmt，运行真实库导入/回滚测试，执行生成一致性检查。
- [ ] 提交 feat(user): support controlled legacy account import。

任务 5 的事务断言示例：

```go
if imported.State != enum.UserDeleted || imported.DisplayName != nil { t.Fatal("invalid legacy tombstone") }
if countAfterRollback != 0 { t.Fatal("failed import left rows behind") }
```

## Task 6 完整验证和固定版本发布

**Files:** Modify README.md、docs/compatibility.md、docs/release-checklist.md、docs/releases/（新增实际发布版本说明）、scripts/required-tests.txt、scripts/accountsvc-required-tests.txt；顶层真实依赖契约测试按 tasks 1–5 补充。

**Interfaces:** 发布产物为包含以上公开接口的固定新版本；后续宿主计划读取实际版本号与提交 SHA。不得预先把未发布版本写进宿主 go.mod。

- [ ] 将新增真实依赖顶层测试加入 required-tests 门禁，保持历史清单项目；核对所有修改调用者和示例。
- [ ] 在 GOWORK=off 下执行 go build ./...、go vet ./...、go test -count=1 ./...；随后 bash scripts/check-generated.sh、bash scripts/check-migrations.sh。
- [ ] 准备三个明确的一次性 PostgreSQL 17 数据库、真实 Redis及恢复工具，设置 SERVER_TEST_DB_DSN、ACCOUNTKIT_RECOVERY_SOURCE_DSN、ACCOUNTKIT_RECOVERY_TARGET_DSN；运行 bash scripts/verify.sh、bash scripts/verify-accountsvc.sh。记录原生退出码、必需用例全部执行的证据；skip 不能算完整验证。
- [ ] 对五类 Review Focus 逐项核对测试，复核无明文 CodeID/PII 泄露、无旧校验旁路、无 tag/冻结迁移变更。
- [ ] 更新破坏性升级手册：同步客户端、停用旧验证码写/校验实例、旧轮次 TTL 淘汰、HMAC 轮换条件及回退限制。
- [ ] 提交并形成可审阅发布结果；确认发布授权后创建新 tag/发布。公开发布另需用户明确授权，不由“接入库”自动推定。
- [ ] 在全新临时宿主中从实际固定版本拉取，GOWORK=off 编译 EndUserHandler/CodeCredential/BatchPublicProfiles/WithActiveUsers/ImportAccounts；保存真实版本号、SHA和消费证据，由各宿主仓库记录并消费，不在 accountkit 执行宿主接入任务。

## 自审与交接

需求以本仓库 openspec/changes/archive/2026-10-09-add-host-account-contracts/ 的 proposal、design、全部 specs 和 tasks 为准。任务 1/2 实现 verification-challenges、verification-delivery 和 code-key-rotation 增量；任务 3/4 实现 host-account-contracts；任务 5 实现 legacy-account-import；任务 6 完成 embedded-auth-package 的独立消费、兼容说明和发布门禁。每轮默认 5 次保持，测试中的 3 次为显式宿主策略。

本计划只定义实施步骤，不表示任何代码、数据库或发布任务已完成。验收统一写入该 Change 的 verification.md，不在其他仓库保留本库实施结果。
