# Identity Resolution Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** 实现手机号／邮箱的中立账号解析，提供独立服务权限、四字段快照与脱敏审计。

**Architecture:** 领域服务通过单个 sqlc LEFT JOIN 查询身份和账号；HTTP 服务入口复用统一认证，数据库查询只读。通用审计使用 SERVICE 主体，消费者和管理员 SDK 不接收服务凭据。

**Tech Stack:** Go 1.26.5、PostgreSQL 17、sqlc 1.31.1、Chi、pgx、现有 boundedjson 和 audit。

**Spec:** [变更设计](../../../openspec/changes/add-identity-resolution/design.md) 与同目录 specs 下三份增量规格。

## Global Constraints

- POST /v1/identities:resolve；独立权限 identities.resolve；仅 PHONE/EMAIL 单条解析。
- 结果固定 user_id、state、display_name、avatar_url，空资料为字符串空值；ACTIVE/FROZEN/PENDING_DELETION 可返回。
- 请求体最多 4 KiB，拒绝未知／重复字段、查询参数、非法编码、尾随 JSON；授权先于业务解析。
- 所有已配置摘要版本匹配；单语句快照；不增加账号锁、Redis 缓存、数据库迁移或第三方网络请求。
- 审计 Action=IDENTITY_RESOLVE、Actor.Type=SERVICE、Targets.Type=USER；不记录身份、摘要、资料或凭据；失败不改变业务结果。
- GOWORK=off；sqlc 生成代码不得手改；真实数据库／race 未运行不能计为通过；不提交或推送。

## Review Focus

- 非法 UTF-8、孤立代理项、重复 JSON 字段不能经宽松解码进入业务：Task 2 的协议边界测试。
- 查询等待连接池时也受 3 秒上限控制，连接断开与 SQL 缺陷应区分 503/500：Task 1 的故障测试。
- PHONE/EMAIL 被登录策略禁用后仍可解析存量身份：Task 1 的策略隔离测试。
- 异步审计晚于匿名化写入不会恢复个人资料，通用 Reader 可见而管理认证时间线不扩展：Task 1 的审计与生命周期测试。
- SDK 覆盖例外只能用于明确 service surface，不能让消费者缺方法／模型仍通过：Task 3 的检查器反例。

### Task 1: 领域解析、SQL、审计与宿主文档

**Files:** 新增 user/service_identity_resolve.go 与 user/service_identity_resolve_test.go；修改 user/query.sql，经 sqlc 更新 internal/userdb/；新增必要审计测试；修改 README.md。

**Interfaces:** Produces `ResolveIdentityInput{Kind enum.IdentityKind, Subject string, Actor audit.Actor, Meta Meta}`、`ResolvedIdentity{UserID string, State enum.UserState, DisplayName string, AvatarURL string}`、`(*Service).ResolveIdentity(context.Context, ResolveIdentityInput) (ResolvedIdentity,error)`。错误复用 ErrInvalidArgument/ErrInvalidTarget、ErrNotFound、ErrUnavailable；内部错误使用安全固定诊断。HTTP 传入 Actor Namespace 为配置 AuditNamespace。

- [x] 先补行为测试：PHONE/EMAIL 规范化相同值返回同 ID；未知 kind 和非法 SERVICE Actor 拒绝；未找到 errors.Is(ErrNotFound)；空资料为空字符串；三种账号状态返回真实 state。运行 `go test -count=1 ./user -run ResolveIdentity`，记录预期失败证据。
- [x] 新增最多两条 LEFT JOIN 查询与领域入口，执行 `sqlc generate`；查询连同连接获取限制 3 秒；零条未找到，多条、悬空或非法账号状态返回内部错误。
- [x] 先补审计行为断言再实现 Freeze+Recorder 投递，复用既有审计失败观测；覆盖成功、未找到、故障、空队列／失败、私密字段缺失，Actor.Name 不传播到审计。
- [x] 真实数据库测试轮换、多版本重复、解绑后重绑、删除／恢复／匿名化、实例隔离、只读时间字段和并发快照；禁用登录策略不影响解析。增加公开资料注销隐藏回归。
- [x] README 给出可编译调用形状与授权责任；运行相关测试、vet、生成差异检查；交付报告包含新增顶层测试名称、红绿证据、未验项。完成规格与质量审查再接 Task 2。

### Task 2: 服务权限、HTTP 与真实服务装配

**Files:** 新增 internal/accountsvc/identityresolve/handler.go、handler_test.go；修改 serviceauth/registry.go 与测试、service_clients 配置测试、run.go、http.go 和调用测试；新增 identity_resolution_integration_test.go；更新 docs/accountsvc.md、deploy/accountsvc/.env.example。

**Interfaces:** Consumes Task 1 的 ResolveIdentityInput/ResolvedIdentity/Service.ResolveIdentity。Produces `NewWithAuthenticator(auth *serviceauth.Authenticator, resolve func(context.Context,user.ResolveIdentityInput)(user.ResolvedIdentity,error), namespace string, requestID func(*http.Request)string) (http.Handler,error)`；若现有装配 Meta 需要可选 ClientIP，则保持可信注入且记录选择。Realm=`accountsvc-identity-resolution`、endpoint=`identities_resolve`。协议顶层测试固定 `TestIdentityResolveProtocol`（供 Task 3 引用）。

- [x] 先测试新权限当前被拒绝，再修改统一目录；组合权限、单数、通配符与未知值必须有反例，不改变旧两项能力。
- [x] 先写 TestIdentityResolveProtocol，覆盖 JSON 所有边界、四字段 DTO、空字符串、no-store、405 Allow: POST、415、413、400/404/500/503；用 fake 计数证明 401/403/未配置503 不读取正文和数据库。运行新测试确认失败，再写处理器。
- [x] 复用 Basic/Bearer 挑战；测试两方式同客户端生成同一 SERVICE Actor，未知 azp、未启用方法、多头、消费者／管理员 token 和 JWKS 不可用；输入请求不能注入 Actor。
- [x] 将独立路由装配到现有 /v1 路由层，扩展 serviceAuthenticators 与未配置警告；真实二进制用合成账号验证仅解析权限、404、状态、资料及原两项接口未授权行为。migrate 仍不校验服务配置。
- [x] 同步服务文档与模板，运行处理器、serviceauth、accountsvc 相关测试及 vet，评审后交付结果和新增必需测试名称。

### Task 3: 服务协议与覆盖门禁

**Files:** 新增 docs/openapi/service.json（仅新服务操作，与现有 enduser.json/admin.json 同目录）；修改 sdks/http-coverage.json、scripts/check-sdk-coverage.mjs、scripts/check-sdk-coverage.test.mjs 及相应说明；主代理集中维护 scripts/required-tests.txt。

**Interfaces:** Consumes Task 2 的 `internal/accountsvc/identityresolve/handler_test.go:TestIdentityResolveProtocol`。Produces service surface 的单条 `resolveIdentity` 登记，sdkApplicable=false，真实 Go 测试引用；保留原两类 SDK 强制校验。

- [x] 先补 Node 反例：缺 Go 测试、错误方法／路径、未知 surface、重复 operation、enduser/admin 使用 sdkApplicable=false 均失败；有效 service 记录通过。运行 `node --test scripts/check-sdk-coverage.test.mjs` 记录红灯。
- [x] 增加明确 service 协议与登记分支，验证测试文件及顶层函数确实存在，不伪造 SDK 文件，未知 surface 即使被动态文档传入也拒绝。
- [x] 更新覆盖说明并执行 `node --test scripts/check-sdk-coverage.test.mjs` 和 `node scripts/check-sdk-coverage.mjs`；审查是否放松原门禁，交付报告。

### Task 4: 集成与验收记录

**Files:** scripts/required-tests.txt、openspec/changes/add-identity-resolution/tasks.md、verification.md 与本计划。

**Interfaces:** Consumes前三项全部测试及报告；输出逐项证据。

- [x] 维护新必需顶层测试清单，核对每个名称真实存在并在数据库／服务依赖齐全时实际通过。
- [x] GOWORK=off 运行 go build ./...、go vet ./...、go test -count=1 ./...；在一次性 PostgreSQL/Redis 与可用 CGO 环境执行 race，检查 sqlc 生成、历史迁移。日志只保留 .test-output/。
- [x] 执行变更及全局 OpenSpec strict、git diff --check；全局既有 7 份主规格长度问题单列，不改写无关规格。
- [x] 最终独立审查全部差异，修复实质问题并复测受影响项；逐项更新 tasks 和 verification.md。提交、推送、归档与发布留待明确授权。
