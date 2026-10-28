# 验收记录：add-service-bearer-auth

日期：2026-10-28。分支 `feat/service-bearer-auth`。

## 实施方式

按[实现计划](../../../docs/superpowers/plans/2026-10-28-add-service-bearer-auth.md)在本会话内顺序实施，未使用逐任务子代理：oidcjwt → serviceauth → 处理器 → 配置/装配之间接口依赖紧密。功能部分先写失败测试（TDD），结束时由独立审查子代理对完整区间做一次代码审查。

## 设计偏离（已记入 design.md）

1. **`iat` 必需放在 serviceauth**：adminauth 原本只在 `iat` 存在时校验，为不改变管理员行为，公共层 `oidcjwt` 不要求 `iat`；服务令牌的「`iat` 必需」与 `exp − iat ≤ 1h` 在 serviceauth 执行。外部行为与规范一致。
2. **预加载改为就绪后后台执行**（审查发现，见下）；服务 verifier 单次刷新超时 5 秒，小于 10 秒冷却。

## 检查结果

| 检查 | 结果 |
|---|---|
| adminauth 回归 | `adminauth/*_test.go` 在区间内无改动；重构后全部通过（3 次重复运行） |
| Windows `GOWORK=off go build ./...`、`go vet ./...`、`go test -count=1 ./...` | 通过（无数据库 DSN，数据库用例 skip） |
| Linux 镜像 + 一次性 PG 17.4 / Redis 7：`scripts/verify-accountsvc.sh`（审查修复后） | 通过：race，700 个测试事件 pass，0 fail，必需测试门禁通过；`internal/accountsvc` 包耗时 218 秒 |
| 进程级集成 `TestServiceIntegrationServiceBearerTokens` | 真实二进制 + 本地 HTTPS 签发方：两接口 Bearer 端到端、跨接口 403、签发方故障时服务照常启动、Bearer 503（拉取带 `Retry-After: 1`）、Basic 可用、启动 WARN 日志；令牌由夹具秘密检查确认未出现在进程输出 |
| `openspec validate --all --strict` | 40 项通过 |
| `git diff --check develop..HEAD`、gofmt | 无问题 |

测试矩阵的覆盖位置：#1–#20、14b、14c 的判定与 challenge 在 `serviceauth` 测试逐条验证（含随机 kid 并发时对签发方请求次数的断言）；`oidcjwt` 测试覆盖冷却两分支、单飞、1 小时过期、延迟加载、issuer 不一致、不安全令牌；两个处理器的测试覆盖每种 Outcome 到各自错误格式、challenge 行、`Cache-Control`、`Retry-After` 的映射，未在处理器层重复整张矩阵（处理器只做映射）。配置测试覆盖 D4/D9 的全部拒绝条件且断言不回显值。

## 独立代码审查

结论：With fixes。无 Critical。

- **Important（已修复）**：签发方挂起时，启动期同步预加载会耗尽 `STARTUP_TIMEOUT` 预算导致 serve 启动失败（审查者在临时 worktree 中复现）。修复为就绪后后台预加载、关停时取消并等待；新增 `TestRuntimeStartsWhenServiceIssuerHangs`（修复前以 `startup timeout or cancellation` 失败，修复后通过），已加入必需清单。
- **Minor（已修复）**：
  - 刷新超时与冷却同为 10 秒，挂起时请求慢失败 → 服务 verifier 刷新超时 5 秒，新增 `TestHangingProviderFailsFastInsideCooldown`；
  - 启动时 issuer 不一致只记 WARN → 改记 ERROR（导出 `oidcjwt.ErrIssuerMismatch`），新增 `TestPreloadIssuerMismatchLogsError`；此测试还暴露并修复了「失败与关停竞争时日志被吞」的判断（改为依据错误本身是否为取消）；
  - 服务 issuer 与消费者 issuer 精确比较可被结尾 `/` 绕过 → 比较时忽略结尾斜杠，新增配置用例。
- **Minor（说明，不改）**：处理器层只覆盖矩阵子集（见上）；tasks/verification 待补（本文件）。
- 审查者列为不判的项：Basic 客户端 ID 与另一接口 azp 同名、`jwks_uri` 与 issuer 不同主机、内省 503 不带 `Retry-After`、403 日志不限频、不强制 `typ`、issuer 首尾空白被裁剪而非拒绝——均为规范明确选择或范围外，接受。

一次性容器、网络、卷与镜像已删除；未触碰其他项目的 `postgres`、`redis` 容器。

## 配置改名（用户决定）

按认证方式成对命名（见 design.md D9 偏离记录）：`SERVICE_ISSUER`、`SERVICE_AUDIENCE`、`INTROSPECTION_BASIC_CLIENTS`、`INTROSPECTION_BEARER_CLIENTS`、`PURGE_FEED_BASIC_CLIENTS`、`PURGE_FEED_BEARER_CLIENTS`（均带 `ACCOUNTSVC_` 前缀）。改名覆盖代码、测试、文档、部署模板及本变更的 proposal/design/specs；已归档变更保留原名作为历史记录，主规格经本变更的 MODIFIED 需求在归档时同步。

| 检查 | 结果 |
|---|---|
| Windows `go build ./...`、`go vet ./...`、`go test -count=1 ./internal/accountsvc/... ./cmd/... ./examples/...`、gofmt | 通过 |
| Linux 镜像 + 一次性 PG 17.4 / Redis 7：`scripts/verify-accountsvc.sh` | 通过：race，700 个测试事件 pass，0 fail，必需测试门禁通过 |
| `openspec validate --all --strict` | 40 项通过 |
| 非归档文件中旧变量名残留 | 无 |

## 内省调用认证改为可选（用户决定）

内省与拉取规则一致：两种调用认证都未配置时 serve（含 production）照常启动，`/v1/introspect` 先于认证返回 503 `introspection_not_configured`（OAuth 顶层 `error`）。本地 Compose 不再强制 `ACCOUNTSVC_INTROSPECTION_BASIC_CLIENTS`。见 design.md D9 偏离记录。先写失败测试再改实现：`TestIntrospectionNotConfiguredWithoutAnyMethod`、`TestNewRejectsInvalidConfiguration`（空凭据）、`TestConfigIntrospectionOptional`（production/development）在修改前均按预期失败；新增进程级 `TestServiceIntegrationIntrospectionNotConfigured` 并加入必需清单。

| 检查 | 结果 |
|---|---|
| Windows `go build ./...`、`go vet ./...`、`go test -count=1 ./...`、gofmt | 通过 |
| Linux 镜像 + 一次性 PG 17.4 / Redis 7：`scripts/verify-accountsvc.sh` | 通过：race，703 个测试事件 pass，0 fail，必需测试门禁通过；新增进程级测试已运行并通过 |
| `openspec validate --all --strict` | 40 项通过 |

## 未配置接口的启动警告与内部标识符改名（用户要求）

- serve 监听就绪后为每个未配置的可选接口记一条 WARN（内省、已清理用户拉取、管理员 API、用户资料属性），只写变量名。先写 `TestWarnUnconfiguredInterfaces`（修改前编译失败），进程级 `TestServiceIntegrationIntrospectionNotConfigured` 增加对启动日志的断言；前者加入必需清单。
- 配置相关 Go 标识符与变量名对应：`Config.IntrospectionBasicClients`、`Config.PurgeFeedBasicClients`、`Config.Service`（`ServiceConfig`：`IntrospectionBearerClients`、`PurgeFeedBearerClients`）、`serviceauth.Config.BearerClients`。描述令牌本身的名称（如 `preloadServiceTokens`）不变。逐标识符核对改名差异，无行为变化。随后 `serviceauth.Config.Tokens` 改为 `BearerVerifier`，与 `BearerClients` 成对；纯改名（12 处），`go vet` 与 accountsvc 全部单元测试通过，未重跑 Linux 完整验证。

| 检查 | 结果 |
|---|---|
| Windows `go build ./...`、`go vet ./...`、`go test -count=1 ./...`、gofmt | 通过 |
| Linux 镜像 + 一次性 PG 17.4 / Redis 7：`scripts/verify-accountsvc.sh` | 通过：race，706 个测试事件 pass，0 fail，必需测试门禁通过；两个新测试均已运行并通过。首次运行因容器内下载 Go 模块 TLS 握手超时未进入测试，重跑通过 |
| `openspec validate --all --strict` | 40 项通过 |

## 未验证项

- 真实 Keycloak 端到端（客户端凭据、audience mapper、密钥轮换）：由接入方（ai-food）负责。
- Windows 本机无 CGO，race 只在 Linux 镜像中运行。
- 未运行库全量 `scripts/verify.sh` 与恢复演练：本变更不改库代码与 schema。
