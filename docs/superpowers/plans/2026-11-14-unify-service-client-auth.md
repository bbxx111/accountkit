# 统一服务客户端认证 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 accountsvc 中以统一客户端注册表共享 Basic/Bearer 身份及接口能力授权，同时保留逐接口旧配置。

**Architecture:** 认证注册表位于 serviceauth，保存不可变的客户端方法、权限与秘密摘要；LoadConfig 解析环境变量并复用已有 OIDC 隔离检查。两个 handler 继续处理自己的协议，通过运行时传入的认证器使用统一授权。

**Tech Stack:** Go 1.26.5、现有 basicclients/oidcjwt、PostgreSQL 17、Redis、Go testing 与隔离 OIDC fixture。

**Spec:** [OpenSpec design](../../../openspec/changes/archive/2026-11-14-unify-service-client-auth/design.md)，同目录 proposal、tasks 与四份 specs 是需求权威。

## Global Constraints

- 不改消费者/管理员认证、数据库迁移、内省数据库吊销语义、feed 清理事务与分页。
- 权限只接受 `tokens.introspect`、`purged_users.read`；方法只接受 `basic`、`bearer`。
- 新旧配置互斥；旧路径原授权、错误及挑战兼容；migrate 不读取服务认证配置。
- 凭据、配置值不得进入错误或日志；重用 basicclients 常数时间摘要匹配与 oidcjwt 缓存。
- 用户已要求实施现有设计，不重新进行设计确认；在已有 `feat/unify-service-client-auth` 上保留全部未提交工件。此次不提交、推送或归档。
- 每个任务先运行失败测试，再做实现，再独立审查；测试日志在 `.test-output/`，验收结论在变更 verification.md。

## Review Focus

- 环境变量未设置与显式空值必须区分，Compose 不得给旧模式自动注入空 SERVICE_CLIENTS（任务 2、4）。
- 仅一个能力获授权时，另一个能力优先 503；已配置能力中缺权限才是 403（任务 1、3）。
- 已注册 Basic-only 身份提交有效 Bearer 时返回无 invalid_token 的 401（任务 1、3）。
- 下划线别名和字段后缀可能重叠，按完整预期变量名匹配，不猜分隔位置（任务 2）。
- 修改输入集合、重启后撤权及提供方故障不能扩大权限或使 Basic 依赖 OIDC（任务 1、3）。

### Task 1: 不可变注册表与统一认证决策

**Files:** 新增 `internal/accountsvc/serviceauth/registry.go`、`registry_test.go`、`unified_test.go`；修改 `serviceauth.go`。读取现有 serviceauth 与 basicclients、oidcjwt 测试。

**Interfaces:** 提供 `ClientConfig{ID string, AuthMethods []string, Permissions []string, BasicSecrets []string}`、`NewRegistry([]ClientConfig) (*Registry,error)`、`Registry.HasPermission(string) bool`、`Registry.BearerEnabled() bool`；Config 新增 `Registry *Registry` 与 `RequiredPermission string`。nil Registry 保持旧模式，非 nil 空 Registry 为统一空配置。

- [x] 先写注册表和认证矩阵失败测试：同一 ID 双方法双权限、未知/错误 Basic401、未知有效 azp403、关闭方法401、两方式缺权限403；快照深拷贝、两份秘密轮换与跨客户端重复拒绝。
- [x] 运行 `GOWORK=off go test ./internal/accountsvc/serviceauth -count=1` 并记录 RED。
- [x] 实现注册表构造与认证分支，复用 Matcher/Verifier；能力 Enabled 按权限判定，全注册表方法并集生成挑战；Basic403无挑战、Bearer403 insufficient_scope、503无挑战。
- [x] 增加表驱动 exact challenge、无效 claims/算法、日志脱敏测试，保持旧套件通过并记录 GREEN。
- [x] 独立审查本任务规格与代码；由主代理更新任务证据（OpenSpec 2.1–2.3 的 handler 部分留待任务 3）。

### Task 2: 环境变量解析与兼容模式

**Files:** 新增 `internal/accountsvc/config_service_clients.go` 与相应测试；修改 `config.go`、必要的 config 测试辅助函数。

**Interfaces:** 消费任务 1 的 ClientConfig/Registry；Config 增加 `ServiceClients *serviceauth.Registry`（nil 为旧模式）。ServiceConfig 增加统一模式 Bearer 启用状态供 Enabled 使用；配置需保留原 issuer/audience 检查。弃用变量名保存在 Config 的内部字段供任务 3 记录。

- [x] 用 LoadConfig 行为测试固定 LookupEnv 模式、显式空、孤立字段、新旧冲突（含 []/{}）、旧空白占位、旧路径回归；运行失败测试。
- [x] 实现声明别名完整变量名集、严格 CSV/ID/方法/权限/秘密验证；测试下划线别名、空项/重复、所有非法枚举、秘密长度和规范 Base64、错误不含值。
- [x] 复用信任配置验证；测试 Basic-only、无 Bearer 的多余 issuer、缺 issuer/audience、消费者/管理员/第三方与客户端受众冲突、production/development、migrate 忽略全部新增字段。
- [x] 运行 `GOWORK=off go test ./internal/accountsvc -run 'Test.*Config|Test.*ServiceClient|Test.*Issuer' -count=1`，确认旧配置套件通过。
- [x] 独立审查接口和兼容行为，完成 OpenSpec 1.1–1.4（运行时 WARN 覆盖由任务 3 收口）。

### Task 3: 接口、启动日志与真实服务集成

**Files:** 修改 `run.go`、`run_service_token_test.go`、`run_unconfigured_test.go`、两 handler 的测试、`service_token_integration_test.go`、`purge_feed_integration_test.go`；仅在必要时修改 handler；新增必需顶层测试登记到 `scripts/accountsvc-required-tests.txt` / `scripts/required-tests.txt` 的适用清单。

**Interfaces:** 消费 Config.ServiceClients、Registry.HasPermission/BearerEnabled 和 serviceauth.Config.RequiredPermission；同一注册表与 verifier 装配两个接口，保持原返回签名。

- [x] 先写失败测试：同 ID 两方式访问两个 handler、Basic/Bearer403零业务调用、未配置503优先于认证/参数、禁用方法401、JWKS503/Retry-After、no-store。
- [x] 修改装配和警告：统一注册表共享、每接口权限独立、旧配置每启动一次变量名级弃用 WARN、未配置能力 WARN；Basic-only 不创建 verifier。
- [x] 增加启动快照、撤权重启、Basic-only 无 I/O、提供方故障与 readiness 隔离、共享刷新测试。已有 handler 支持行为时以集成证据证明，无须无意义重写。
- [x] 扩展真实 PostgreSQL/Redis 集成矩阵，保持数据库吊销、feed 分页与清理事务；先跑定向测试再执行 Linux `scripts/verify-accountsvc.sh`。
- [x] 独立审查并完成 OpenSpec 2.1–2.3、3.1–3.4、4.1 的实际覆盖项。

### Task 4: 部署文档与综合验收

**Files:** 修改 `deploy/accountsvc/.env.example`、Compose、`docs/accountsvc.md`、`docs/compatibility.md`、`examples/remoteauth` 相关示例说明；新增显式启用的 `deploy/accountsvc/compose.service-clients.yaml`（env_file 指向同目录 `.env`，文档必须要求与 CLI --env-file 使用同一文件）；检查 `README.md` 和 `sdks/http-coverage.json`；更新 change 的 tasks 与 verification。

**Interfaces:** 使用前三任务的真实配置语法；示例明确 Basic-only、Bearer-only、双方式、权限最小化、重叠轮换及旧模式回退。

- [x] 以合成秘密解析三类示例；先建立配置示例测试，再更新模板与 Compose 透传。确认旧模式不会因默认空变量被误选为统一模式。
- [x] 更新决策表、TLS、IdP secret 与 Basic secret 区别、重启和排空副本、旧模式迁移回退；不承诺热更新或提供方禁用即时撤销。
- [x] 核对 SDK 内部端点排除登记，无消费者或管理员协议变化则保持 SDK 不变。
- [x] 运行 GOWORK=off build/vet/test；使用一次性 PG/Redis 和 Linux race 执行 verify-accountsvc，条件具备时执行完整 verify.sh 恢复门禁。保留原始输出，skip 或缺依赖单列。
- [x] 运行 OpenSpec 本变更与全库严格校验，区分已知 7 份主规格基线警告；独立全量审查，修复实质问题后更新 tasks/verification，不自动归档或提交。
