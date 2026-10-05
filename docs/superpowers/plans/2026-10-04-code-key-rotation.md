# 验证码密钥轮换连续性 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 完成 harden-code-key-rotation，使正常轮换下验证码、目标冷却和额度连续。

**Architecture:** 保持现有 Redis 键和 hash 字段，多版本目标键去重后传给 Lua。发码和校验各保留一个原子边界，新写使用 active，旧记录原地校验并保留期限和计数。

**Tech Stack:** Go 1.26.5、go-redis/v9、Lua、miniredis、PostgreSQL 17；不新增依赖。

**Spec:** [proposal](../../../openspec/changes/archive/2026-10-04-harden-code-key-rotation/proposal.md)、[design](../../../openspec/changes/archive/2026-10-04-harden-code-key-rotation/design.md)、[spec](../../../openspec/changes/archive/2026-10-04-harden-code-key-rotation/specs/code-key-rotation/spec.md)、[tasks](../../../openspec/changes/archive/2026-10-04-harden-code-key-rotation/tasks.md)。

## Global Constraints

- 不新增运维动作、端点、热加载、配置字段、公共方法或依赖；保留周期性数据库回填及默认间隔。
- 不修改数据库 SQL、生成文件、迁移与冻结基线，不改变 JWT、refresh、access 吊销或审计失败模式。
- 仅对程序已升级、完整相同密钥集合、相同验证码策略的 active 滚动切换承诺连续性。
- 验证码 TTL/次数、发送冷却/UTC 日额度、用途/渠道/实例隔离保持原契约；发送失败不退额。
- 验证码脚本失败、畸形返回和未知状态 fail-closed；多份物理码冲突原子作废并返回 ErrExpired。
- Go 操作在 GOWORK=off 下执行；基线提交 055b542，工作分支 fix/code-key-rotation；实际服务商/产品环境验证延期。
- 各步子代理自行完成 TDD、gofmt、覆盖测试及提交，禁止再派子代理；协调者独立派审查代理。最终统一运行完整门禁，避免各步骤重复全量恢复演练。

## Review Focus

- 同密钥材料的多个版本生成重复物理键：任务 1 验证计数只增加一次、记录不会被当作冲突。
- 冷却键多份且剩余时间不同：任务 1 验证返回最长剩余冷却，不依赖 active 顺序；秒级返回覆盖实际剩余时间。
- Redis 返回空数组、错误类型或未知状态：任务 1 验证 ErrUnavailable 且不 panic、不返回成功。
- 错误尝试与轮换/重新发码交错：任务 1 验证原次数及有效期不重置，旧记录不复活。
- 退役时仍有旧进程/回填在途：任务 4 文档明确 T0 与数据库查询条件，不能以启动成功或固定 5 分钟判断完成。

### Task 1: 多版本验证码存储原子处理

**Files:**
- Modify: `user/code/store.go`, `user/code/scripts.go`
- Create: `user/code/rotation_test.go`, `user/code/rotation_fault_test.go`
- Modify if needed: `user/code/store_test.go`（仅消除本次涉及的随机碰撞伪失败）

**Interfaces:**
- Consumes: `pii.Digester.AllDigests(string) []string`、既有 Store/options/error 类型。
- Produces: 公共 `Issue(ctx context.Context, channel enum.IdentityKind, purpose enum.CodePurpose, target, ip string) (string, error)` 与 `Verify(ctx context.Context, channel enum.IdentityKind, purpose enum.CodePurpose, target, code string) error` 签名不变。
- 内部可使用 `targetDigests(target string) []string` 去重、`scriptStatus(result []interface{}) (string, error)` 验证结果；保持文件职责，不引入公共抽象。

- [x] 写 `TestCodeRotationVerify`、`TestCodeRotationLimits` 行为测试；使用两把合成密钥及同 Redis 的两个 active。核心断言：`err == nil`（K1 发码/K2 校验），`errors.As(err, &limited) && limited.Dimension == "COOLDOWN"`（切换后重发），部分额度跨切换累计达到上限返回 TARGET_LIMIT。
- [x] 执行 `go test -count=1 ./user/code -run TestCodeRotation`，记录旧实现的预期 RED；不得先修改生产代码。
- [x] 扩展键构造和 issueScript，按 design 的顺序先检查后写入；目标计数去重求和，只向 active 加一，IP 只加一，替换全部版本同用途旧码。冷却若用 PTTL，向上取整为秒以覆盖剩余期限，保留最少 1 秒。
- [x] 写并执行 `TestCodeRotationState` / `TestCodeRotationConcurrent` RED：旧格式 h/n 部分计数及原 TTL、双向验证、别名、冲突多记录、20 个不同 active 并发请求一胜、Issue/Verify 可串行化；随后实现 verifyScript 原地计数与原子消费，不续 TTL。
- [x] 写 `TestCodeRotationUnavailable`，通过 Redis hook 的命令返回注入验证空值/畸形/未知返回及 Redis 断开；实现明确的错误映射，无需新增生产测试钩子。正确码、密钥、目标不得出现在测试失败日志。
- [x] 验证 PHONE/EMAIL、SIGN_IN/BIND/REAUTH、不同前缀、UTC 跨日、投递前占额及 IP 拒绝不占目标额度；随机生成新码测试使用可区分的旧夹具，不依赖百万分之一不碰撞的假设。
- [x] 执行 `go test -count=1 ./user/code ./pii`、`go vet ./user/code ./pii`；测试报告记录 RED/GREEN、顶层用例名称和未执行的真实依赖检查。
- [x] 提交 `fix(code): preserve verification state across key rotation`，交独立任务审查；对应 OpenSpec 1.2、2.1、2.2、3.1、3.2、3.3。

### Task 2: 库接入与真实 Redis 验证

**Files:**
- Create: `code_key_rotation_test.go`, `user/code/rotation_integration_test.go`
- Modify: `scripts/required-tests.txt`, `scripts/verify.sh`, `.github/workflows/ci.yml`, `docs/development.md`（仅新增真实 Redis 测试接入所需内容）
- 可新增 `scripts/code-required-tests.txt` / `scripts/verify-code.sh`，仅在沿用现有测试门禁模式更清晰时使用，并确保 CI 实际调用。

**Interfaces:**
- Consumes: 任务 1 不变的 Store API；根 `accountkit.New/ Migrate/ Start/ Close`、`ConsumerHandler` 和既有 captureSender/HTTP 夹具模式。
- Produces: `TestCodeKeyRotationIntegration`（根门面）；`TestCodeRotationRedisIntegration`（真实 Redis）。复用 `ACCOUNTSVC_TEST_REDIS_URL` 作为已有真实 Redis 测试地址；仅操作唯一任务前缀，禁止 FLUSHDB。

- [x] 使用两个配置仅 active 不同的 Auth，共享测试 schema/Redis/发送器，先写跨重建的根门面用例：旧码登录、BIND 绑定及换绑、REAUTH，断言正确 user_id/身份变更/access 结果，错误码与审计原因保持兼容。
- [x] 补充投递失败后另一 active 重试仍限流、实例隔离、PHONE/EMAIL 三用途路径；调用真实库，不复制验证码算法来代替行为断言。
- [x] 使用真实 Redis 单独前缀写旧 h/n 夹具，验证原 TTL、计数、双向消费、发送冷却与日限、并发一胜；客户端关闭时局部清理仅本测试键。
- [x] 配置缺失时普通单测允许明示 skip，但完整验证和 CI 必须提供真实 Redis 并强制检查新必需用例 pass。把任务 1 新增必需顶层测试及本任务用例加入清单；如修改验证入口，测试缺少 Redis 配置时门禁拒绝，不允许静默缺验。
- [x] 执行真实 PG/Redis 下 `go test -count=1 . ./user/code -run 'TestCode(KeyRotationIntegration|RotationRedisIntegration)'` 及 `go vet . ./user/code`；证明未 skip。集成测试已有功能可能直接 GREEN，任务 1 的先行 RED 是行为变更依据，不回退生产代码制造 RED。
- [x] 提交 `test(code): verify rotation through library and real redis`，交独立任务审查；对应 OpenSpec 4.1、4.2 及部分 5.2。

### Task 3: accountsvc 滚动切换验收

**Files:**
- Create: `internal/accountsvc/code_key_rotation_integration_test.go`
- Modify only if needed: `internal/accountsvc/fixtures_integration_test.go`
- Modify: `scripts/accountsvc-required-tests.txt`

**Interfaces:**
- Consumes: `newIntegration(t)`、`f.start(extra map[string]string)`、`f.call`、`f.smtp.mail`、`p.stop`、`f.assertSafe`，沿用 Linux 实际二进制测试设施。
- Produces: `TestServiceIntegrationCodeKeyRotation`，加入严格服务必需测试清单。

- [x] 测试两个进程都持有 K1/K2、active 不同，跨进程验证码登录；重新启动并仅切换 active 后旧码仍可用，原身份复用而非重复注册。
- [x] 验证冷却、目标累计额度和 IP 计数不随 active 分开；使用有界轮询 Redis TTL 或测试夹具控制冷却，避免任意长 sleep；记录收件目标/码/token 为秘密并检查日志无泄露。
- [x] 如需小幅增强夹具，仅在测试代码中实现，不新增服务生产配置/端点，不修改其余服务行为。
- [x] 执行 Linux + 真实 PG/Redis + 本地 SMTP 下 `go test -race -count=1 -timeout=5m ./internal/accountsvc -run '^TestServiceIntegrationCodeKeyRotation$'`，开启 `ACCOUNTSVC_TEST_RACE=1`；报告实际进程及无 skip 结果。
- [x] 提交 `test(accountsvc): cover verification key rotation`，交独立任务审查；对应 OpenSpec 4.3。

### Task 4: 文档与完整交付核验

**Files:**
- Modify: `README.md`, `docs/compatibility.md`, `docs/accountsvc.md`, `docs/development.md`（按实际接入变化）
- Create: `openspec/changes/archive/2026-10-04-harden-code-key-rotation/verification.md`
- Modify after verified: change `tasks.md`、本计划的任务状态。

**Interfaces:**
- Consumes: 前三步实现和报告、协调者完整门禁日志/测试计数、审查结论。
- Produces: 持久验收记录与长期轮换操作说明，链接不重复存档报告。

- [x] 删除 README 中验证码/目标限额因正常轮换失联的旧限制，明确 IP 日额度本来就独立于 HMAC；说明程序升级、先分发全部密钥再切 active、重启生效及历史冲突处理。
- [x] 写清 T0、未删除 identity 的旧版本引用归零、验证码/冷却/UTC 日界线退役条件、备份保留以及周期回填方向仍可能在混合 active 期间变化；不增加用户已排除的运维动作。
- [x] 协调者执行并提供 `scripts/verify.sh`、`scripts/verify-accountsvc.sh`、生成检查、OpenSpec strict 的实际日志，代理据此记录计数/skip/恢复三阶段和真实环境延期，不能把尚未执行的命令写成通过。
- [x] 检查 Markdown 链接与差异，确认根库仍可独立构建、不依赖 accountsvc、无 SQL/依赖/公开配置变化；更新实际完成的 tasks。
- [x] 提交 `docs(code): document continuous key rotation and verification`，交独立任务审查；对应 OpenSpec 5.1、5.2、5.3。最终整体审查由协调者另行发起，最终报告补录后才宣告完成。
