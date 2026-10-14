# 身份策略与登录补全 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development 执行；行为变更使用 superpowers:test-driven-development，每个工作包完成后使用 superpowers:requesting-code-review。

**Goal:** 按已确认 OpenSpec 实现部署身份策略、首次登录延后建号、手机号/邮箱自动关联或注册，以及 accounts HTTP 契约。

**Architecture:** 领域规则集中在 user.Service；配置解析与不可变策略分离；验证码沿用 Redis 原子轮次，补全状态、加密证明和最终结果由 PostgreSQL identity_flow 持久化；HTTP 只转换协议。

**Tech Stack:** Go 1.26.5、pgx/v5、PostgreSQL 17、Redis、sqlc 1.31.1、Chi。

**Spec:** [proposal](../../../openspec/changes/add-identity-policy-sign-in-completion/proposal.md)、[design](../../../openspec/changes/add-identity-policy-sign-in-completion/design.md)、[tasks](../../../openspec/changes/add-identity-policy-sign-in-completion/tasks.md) 及该 change 的八个 specs；冲突以规格为准。

## Global Constraints

- 当前实施分支为 feat/identity-policy-sign-in-completion，起点 6d8f3ec；提案已按用户要求提交并用 no-ff 合入 develop。实施完成不自动再次合并、推送、归档或发布。
- Required 仅 PHONE/EMAIL、any-of，nil 为默认 PHONE+EMAIL，显式 [] 不限制类型；Enabled 非空、Required 为其子集。默认装配与 Disabled 兼容，不把瞬时依赖健康状态当作身份策略。
- 仅新增 accounts:sendSignInCompletionCode、accounts:completeSignIn。独立验证码用途；唯一票据字段 sign_in_completion_token；不添加 prepare、第三方补全、defer_registration 或恢复端点。
- SignInResult 为 Tokens/Pending 联合，恰好一个有效成员；未知身份不足时没有 user_id、identity 或 session。已知账号按全部可用身份推导 scope。
- 冻结 0001 和源基线不可修改；新增递增迁移，无外键。SQL 只写查询源，通过 sqlc generate 更新生成文件。
- 锁顺序已有 user 排序 → flow 排序 → identity/session，等待后重查身份、期限、策略；不自动合并两个账号。
- Redis 消费不因 SQL 回滚恢复；成功证明先持久化再最终事务，提交未知时允许同请求受限重放。流水及结果 TTL 不续期。
- 凭证随机至少 256 bit，内部 f_ TSID 不是凭证。快照/结果版本化加密并绑定实例、流程；秘密、完整目标和摘要不进入日志审计。
- 保留刷新宽限同 pair、轮换/重放、同设备替换、已有恢复、异步审计和 Redis 故障边界。
- 测试、检查、审查和未验证项最终汇总到 change/verification.md；原始记录放 .test-output/identity-policy/。所有任务须实际完成且验证后才勾选。

## Review Focus

重点检查未验证归属泄露、证明消费与持久化故障、锁后归属变化、重复建号/会话、刷新后的幂等结果、跨实例密文/票据重放、匿名化后快照回写、旧密钥移除、旧 users 路径与两个 HTTP 面的隔离。配置只做无 I/O 校验，migrate 不依赖认证运行装配。

## Task 1: 身份策略与配置基础

对应 OpenSpec 2.1–2.3；后续工作包使用同一策略对象。

**Files:** config.go/config_test.go、accountkit.go/accountkit_test.go、新增 user/identity_policy.go 和测试、user/service.go、user/errors.go。

1. 先增加表驱动配置和纯策略测试：缺省/显式空、非法枚举、重复、第三方 Required、子集、外部切片修改、禁用历史身份不计入完整性。
2. 运行 `GOWORK=off go test ./... -run 'IdentityPolicy|IdentityKinds' -count=1`，记录预期失败。
3. 实现统一不可变策略（Enabled/Required 的复制、允许判断、完整性和规范化指纹），根配置使用同一集合校验。Env 用 LookupEnv 保留存在性，[] 是唯一显式空表示。
4. 为直接 user.NewService 注入相同配置语义；New 保持无 I/O。显式 Enabled 检查真实能力、禁用可省略，省略 Enabled 保留 SMS/Email 必填及 Disabled 兼容。
5. 相关测试与 vet 通过后审查；记录导出的策略接口供后续包使用，不在本包改 SignIn 返回类型。

## Task 2: 补全验证码与存储基础

对应 OpenSpec 3.1、4.1–4.2，以及 3.3 的密文基础。

**Files:** enum/enum.go、ids/ids.go、user/code/challenge.go、user/code/challenge_scripts.go、对应测试、user/sender/smtp/、pii/cipher.go、migrations/0002_identity_flow.*.sql、user/query.sql、sqlc.yaml、生成的 user/db 与 audit/db。

1. 先测新增 PurposeSignInComplete 与 Binding.FlowID 的跨流程/跨用途拒绝、旧轮次不扣预算、最后一次尝试、共享目标/IP额度及失败预算、HMAC 两种 active 的并发接管。
2. 运行 `go test ./enum ./ids ./user/code ./user/sender/smtp ./pii -count=1` 观察预期失败。
3. 追加枚举值；Binding 不复用 UserID。提供受流程原截止时间限制的发码入口，保留普通 IssueChallenge 签名与用途语义，Lua 继续原子计数。
4. SMTP 新用途说明完成登录和身份关联。为流程提供可绑定上下文的加解密方法，旧 Encrypt/Decrypt 格式与调用不变。
5. 新表包括 token digest 唯一索引、状态、策略指纹、加密快照及版本、revision、可清理的账号/会话关联、时间；不保存原始凭证。按核心流程使用的操作添加查询并运行 sqlc generate。
6. 用真实 PG 验证新迁移/ID/索引和回滚，执行生成及迁移检查。测试专用迁移若编号冲突，迁移其测试版本而不触碰冻结基线。

## Task 3: 领域登录与补全事务

对应 OpenSpec 1.2、3.2–3.4、4.3–4.4、5.1–5.6、7.3。

**Files:** user/service_signin.go、user/service_idp.go、新增 user/service_signin_completion.go 与专门测试、user/service.go、user/errors.go、user/service_purge.go、user/service_lifecycle.go、user/service_rekey.go、accountkit.go 中密钥核验/维护装配；必要查询由本任务继续通过 sqlc 生成。

1. 先写真实数据库行为测试：微信未知返回 pending 且三表不增；Required=[] 直接注册；PHONE 不足只 pending；已有 B 完成沿用 ID；新目标一次建号；scope、is_new_user、设备绑定及 no 假 token。
2. 定义 `SignInResult{Tokens *TokenResult, Pending *PendingSignIn}`，Pending 包含 SignInCompletionToken、ExpireTime、EnabledIdentityKinds、RequiredIdentityKinds。SignInWithCode/Idp 返回此联合；成功业务仍通过 TokenResult 的现有签发逻辑。
3. 定义 `SendSignInCompletionCode(ctx, token, channel, target, dev, meta)` 和 `CompleteSignIn(ctx, token, cred, dev, meta)`；dev 允许为空表示未重复提供头，提供则必须和首次一致。HTTP 与直接调用共享验证。
4. 生成 32 随机字节票据，只存摘要；加密快照保存规范化证明、首次设备、已知归属、秘密绑定请求指纹及最终结果。TTL=min(5m, 可获知的证明期限)，策略/实例变化拒绝。
5. 追加验证码证明前再次检查 flow；消费后持久化遵守用户→流程锁序/CAS，失败不退码。只使用已持久化证明进入最终事务。
6. 所有权预读→排序锁用户→锁流程→重新查身份。无法安全证明的归属变化拒绝或整体重试；不反向申请用户锁。目标已有 B 则关联未占用来源；无归属且满足条件才创建用户。验证 per-kind 数量与所有账号状态。
7. 成功结果、身份和会话同事务提交；最终请求指纹相同且账号/会话/refresh仍有效才返回同 pair。分别测试 flow失效和 SIGN_IN_ALREADY_COMPLETED。
8. 故障和并发测试覆盖消费后写失败、最终提交失败、响应丢失、两个流程争抢、冻结/换绑/匿名化交错、IdP锁后所有权检查；不出现死锁/重复用户/未提交成功审计。
9. 活跃流程加入密钥版本核验、到期有界清理、删除及匿名化清理。同事务回滚保留语义；禁止匿名化后回写证明。
10. 精确范围测试和 vet，审查后移交 HTTP 与装配接口；对新签名的测试调用者按实际成功/pending 分支更新，不添加生产兼容旁路。

## Task 4: 全部身份入口及生命周期策略

对应 OpenSpec 2.4、6.1–6.3。

**Files:** user/service_identity.go、user/service_identity_replacement.go、user/service_session.go、user/service_me.go、user/service_lifecycle.go、user/service_idp.go、相关测试。

1. 先测显式禁用 Kind 在消费/外部调用前拒绝、普通第三方绑定不受 Required 限制、存量禁用身份可列表。
2. 所有发码、登录、绑定、换绑、重新认证和补全使用相同 Enabled 检查。
3. 解绑/换绑事务用最终身份集合验证至少一个可用身份和 Required any-of；保留普通占用409与每种身份数量限制。
4. 新登录、刷新轮换、原手机/邮箱重新认证和恢复使用完整性判断；宽限返回缓存原 pair，重新认证不延长 refresh。
5. 测第三方同设备重新登录更换会话及近期认证、原手机/邮箱与管理员恢复、IdP冷静期拒绝；运行原会话到期/身份替换/生命周期回归。
6. 相关单元/真实数据库并发通过后审查。

## Task 5: accounts HTTP 协议

对应 OpenSpec 7.1–7.2，联动 1.2。

**Files:** httpapi/enduser/*、httpapi/admin/*、httpapi/apierror/* 及 HTTP 测试；根集成测试与服务测试内相关调用者。

1. 先测两面 accounts 成功、users404、token/revoke及身份面授权不变、所有资源 name 引用一致。
2. 统一路由及资源字符串，不改 user_id、u_、JWT、scope、Auth.Users() 或 Go user 包。
3. SignIn DTO 保持顶层 RFC6749 token响应；pending 仅有 next_action/票据/期限/策略集合，HTTP200且 no-store。缺失联合成员或双成员拒绝内部错误。
4. 新增两个精确独立端点，新增可选能力接口；未实现返回503。严格 phone/email oneof，拒绝 IdP、purpose、通用票据别名和普通发码中的补全票据。
5. 缺票据/无效/过期、已完成不可重放、禁用Kind按设计400；Bearers保留401、scope403、依赖503、内部错误固定文本。
6. 按新签名更新 fake、示例和测试调用；验证 X-Device-Id/Name、两面隔离及既有 OAuth 错误契约。运行 HTTP 全套并审查。

## Task 6: accountsvc 装配及网关

对应 OpenSpec 7.4–7.6。

**Files:** internal/accountsvc/config.go/run.go、cmd/accountsvc/、服务与网关集成测试、deploy/accountsvc/。

1. 先测试显式纯 IdP 且无 SMTP可以装配、EMAIL关闭忽略非法SMTP配置、显式PHONE报错、原默认SMTP/TLS不变。
2. 按 Enabled 选择 SMTP；migrate 使用专用迁移必要装配，不构造发送器/IdP/HTTP handler，不用假发送器绕过。
3. migrate仍校验静态策略、数据库/search_path/锁/dirty与有效密钥版本，运行相应迁移故障和锁测试。
4. 网关清单明确列出两个 accounts 补全端点，旧 users不映射；内省、探针、管理不被通配开放。测试可信IP/设备头/限流和秘密日志边界。
5. 测试与 vet 后审查。

## Task 7: 集成、恢复、文档

对应 OpenSpec 8.1–8.3、8.5。

**Files:** 新增根级 identity_policy_signin_test.go、recovery_test.go、isolation_test.go、scripts/required-tests.txt、README.md、docs/compatibility.md、docs/accountsvc.md、docs/gateway-integration.md、部署模板和 examples/embedded。

1. 先写根级真实依赖测试：EMAIL+WECHAT自动关联、仅EMAIL、Required=[]、策略收紧、两实例；维护 required-tests，避免只skip。
2. 恢复夹具覆盖 identity_flow 的有效加密证明/完成结果、旧密钥、Redis丢失拒绝；seed/verify/source-unchanged使用独立空库及PG17客户端，不使用产品库。
3. 所有活跃产品文档迁移 accounts；保留历史记录不篡改。写清 Go联合返回迁移、配置nil/[]、自动分流、专用票据、近期认证会话切换及纯第三方恢复限制。
4. 真实微信/Apple环境只在可用时联调；无实际外部凭据时明确未验证，不拿模拟提供方测试替代真实联调结论。
5. 独立宿主编译、集成及恢复检查后审查。

## Task 8: 完整验证与交付

对应 OpenSpec 8.4、8.6，汇总全部任务。

1. 在 GOWORK=off 下执行 go build ./...、go vet ./...、全部 go test；真实依赖执行 scripts/verify.sh（含 race、必需测试及PG17备份恢复）。
2. 运行 sqlc generate 的一致性检查、scripts/check-generated.sh、scripts/check-migrations.sh 和 openspec validate --all --strict；不改变门禁以通过。
3. 整分支审查覆盖全部增量规格；处理实质问题并用针对回归测试复验。测试已有skip和环境缺口如实记录。
4. 更新 OpenSpec tasks 和 verification.md 的当前证据、资源与日志引用；只勾选完成且验证的项目，真实外部联调未完成则保持未勾选。
5. 交付提案提交/合并哈希、当前feat分支、实施范围、实际检查和未验证项；不自动归档、push或再合并。

## Execution Dependencies

Task 1 与 Task 2 可在互不写同一文件时并行；Task 3 依赖两者。Task 4 依赖策略及核心登录接口；Task 5 可在 Task 3 类型确定后只修改 HTTP 范围。Task 6 依赖根配置；Task 7 依赖最终协议，Task 8 收口。同一文件始终只有一个写入者，接口变化先通知依赖任务。
