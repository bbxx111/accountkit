# accountsvc 单监听器与可选 TLS Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实施 simplify-accountsvc-transport，保留认证边界并简化服务入口和 TLS 部署。

**Architecture:** 一个 HTTP server 挂载独立消费者、管理员、内省与探针处理器。TLS_ENABLED 在加载配置时解析有效值，未设保留旧行为；显式关闭仅影响 HTTP listener。网关职责以文档和本地代理 fixture 验收，不新增生产网关或限流模块。

**Tech Stack:** Go1.26.5、Chi、现有 pgx/Redis/SMTP/OIDC；无新增依赖、SQL或根库接口。

**Spec:** [proposal](../../../openspec/changes/simplify-accountsvc-transport/proposal.md)、[design](../../../openspec/changes/simplify-accountsvc-transport/design.md)、[runtime](../../../openspec/changes/simplify-accountsvc-transport/specs/accountsvc-runtime/spec.md)、[introspection](../../../openspec/changes/simplify-accountsvc-transport/specs/consumer-token-introspection/spec.md)、[gateway](../../../openspec/changes/simplify-accountsvc-transport/specs/gateway-integration/spec.md)。

## Global Constraints

- 用户确认直接迁移：只监听 HTTP_ADDR（默认127.0.0.1:8080），新路径 /v1/introspect，旧路径404，无别名/重定向；serve 非空 INTERNAL_ADDR 报错，migrate 继续忽略服务配置。
- TLS_ENABLED 未设/空沿用旧默认；true要求有效证书对；false在production也使用HTTP，但与任一证书配置冲突则失败。非空值按strconv.ParseBool规则解析，错误不回退。
- SMTP、OIDC、PG/Redis传输策略及三套认证处理器、库配置/生命周期、领域规则、audit和Redis失败语义不变；不新增运行时依赖、运维动作、mTLS或服务内通用限流。
- TLS外部终止和单端口由部署方保护，服务不检查网格存在，不信任客户端X-Forwarded-Proto自动降级。内省认证先于token/方法/体积处理；保留其OAuth错误形状。
- GOWORK=off；改Go运行gofmt/相关测试/vet。保存RED/GREEN；不为纯文档套TDD。每步独立审查，最终全分支审查；实际网关/网格/服务商/产品环境延期。
- 本分支 feat/accountsvc-transport 从已完成维护HEAD d4456aa继续；集成分支develop为b84f418。当前change最终审查基线d4456aa，不重复审查此前已批准的维护提交；不擅自合并、归档或推送。

## Review Focus

- TLS=false但残留一个证书路径：任务1在读证书前拒绝冲突，错误只指出配置名。
- 精确/v1/introspect与/v1 mount冲突：任务1用真实内省handler断言Basic challenge、认证先于token校验及认证后405，保留请求体边界。
- migrate继承废弃/非法服务环境：任务1验证仍可加载数据库迁移配置，不因TLS/INTERNAL_ADDR/SMTP设置阻断。
- 单listener启动或停止失败导致连接/审计未收尾：任务1保留原回收、排空、总预算测试，任务2实际SIGTERM复核。
- HTTPS代理向HTTP后端转发时来源与认证头混淆：任务2测试可信IP防伪、Basic/Bearer边界、协议头及拒绝路由不进入后端。

### Task 1: 配置、单监听器与旧用例迁移

**Files:**
- Modify: `internal/accountsvc/config.go`, `run.go`, `http.go`
- Modify: `internal/accountsvc/config_test.go`, `run_test.go`, `http_test.go`；`shutdown.go`/`shutdown_test.go` 仅在单listener行为确有需要时修改。
- Modify: `internal/accountsvc/fixtures_integration_test.go` 与已有 `*_integration_test.go` 的端口/路径调用点、`internal/accountsvc/introspection/handler_test.go` 的示例路径。
- Modify: `scripts/accountsvc-required-tests.txt`（新增单元顶层必需项）。

**Interfaces:**
- Consumes: `LoadConfig(command string) (Config,error)`、`runConfig`、`newHTTPServer`、既有独立consumer/admin/introspection handlers。
- Produces: `Config.TLSEnabled bool` 为解析后有效值，删除 `InternalAddr`；私有 `serviceHandler(cfg Config, consumer, admin, introspect http.Handler, state *healthState) http.Handler` 返回统一树；`serviceProcess.baseURL string` 为唯一服务地址，后续测试沿用 `f.start(extra) / f.call / f.introspect / p.stop`。

- [ ] 先补 `TestConfigTLSMode`、`TestConfigRejectsInternalAddress`，断言完整TLS矩阵及旧配置拒绝；migrate携带非法TLS、旧内部地址、服务凭据缺失仍不读取服务设置。执行 `go test -count=1 ./internal/accountsvc -run 'TestConfig'` 记录预期RED。
- [ ] 实现有效TLS值及安全错误：未设production仍TLS、development无证书HTTP；显式false有任一证书报错、无证书HTTP。HTTP关闭日志说明本进程未启TLS，不声称外部加密已验证。保持SMTP生产TLS/OIDC和客户端配置校验。
- [ ] 写/更新 `TestHTTPBoundaryAndHealth` 与 `TestRuntimeSingleListener` 行为测试，再实现单listener/server与serviceHandler；断言绑定一次、/v1/introspect正确到达真实Basic handler、旧路径404、所有面在同址、没有角色或scope绕过。沿用现有超时与64KiB上限。
- [ ] 复核/保留原启动顺序、失败回收、监听异常、health/readiness与SIGTERM排空单元测试；不要删除测试绕过双端口行为变化。HTTP流量排空覆盖全部处理器。
- [ ] 机械迁移全部已有服务fixture和调用点到baseURL，按TLS开关构造http/https地址；删除内部地址环境注入。将原“公开口内部路由404”断言改为新同址协议行为及旧路径404，不删已有生命周期、认证、故障、换绑、过期、轮换验收内容。
- [ ] `go test -count=1 ./internal/accountsvc/...` 和 `go vet ./internal/accountsvc/...` 通过；实际Linux既有服务回归由任务2执行。将新增必需顶层测试加入清单，记录测试名、RED/GREEN和未执行项。
- [ ] 自审并提交 `feat(accountsvc): unify listener and make http tls explicit`，交独立审查；覆盖OpenSpec2.1、2.2、3.1、3.2及4.2的用例迁移（4.2待真实服务通过后勾选）。

### Task 2: 远程调用、实际进程与外部终止验收

**Files:**
- Modify: `examples/remoteauth/client.go`, `client_test.go`、相关例子路径（如有）。
- Create: `internal/accountsvc/transport_integration_test.go`, `gateway_integration_test.go`。
- Modify only as needed: `internal/accountsvc/fixtures_integration_test.go`, `scripts/accountsvc-required-tests.txt`。

**Interfaces:**
- Consumes: 任务1 `serviceProcess.baseURL`、`Config.TLSEnabled`、新路由；原有 `f.start`, `f.call`, `f.smtp.mail`, `f.assertSafe`, `f.assertAuditPrivate`, `p.stop`。
- Produces: `TestServiceIntegrationTransportModes`、`TestServiceIntegrationGatewayBoundary` 及对应门禁项；remoteauth公开接口不变，AllowHTTP仍默认false。

- [ ] 扩充远程客户端测试：HTTPS默认证书校验、HTTP显式允许、拒绝重定向、逐次调用不缓存、非200/畸形响应为ErrUnavailable；更新文档注释允许受控外部终止，保持Go调用接口。
- [ ] 新实际进程测试验证未设开关的production HTTPS和显式false的production HTTP，错误Basic/Bearer仍拒绝；完整邮件登录/刷新/内省可用，同址health/ready和旧路径404；子进程旧INTERNAL_ADDR/证书冲突/非法开关以非零退出且不泄露配置值。
- [ ] 建立只在测试中的TLS反向代理（stdlibhttputil/httptest），连接本地HTTP服务；公网上下文allowlist拒绝内省/探针且计数确认未进入上游，授权内部调用经TLS可内省。验证网关清理伪造转发头、服务仅信任配置代理、认证头/设备头/请求ID/challenge/no-store/Retry-After透传及秘密不泄露。
- [ ] 保持代理fixture用途有限：不用它实现生产限流，不配置真实网关/网格。fixture的路由策略不改变accountsvc本身的认证处理；检查请求取消/代理退出及测试拥有资源清理。
- [ ] 真实一次性PG/Redis下运行 Linux `ACCOUNTSVC_TEST_RACE=1 go test -race -count=1 -timeout=5m ./internal/accountsvc/... ./examples/remoteauth`，确认既有及新增服务必需用例无skip；相关vet通过。协调者提供环境；完整恢复门禁不在本步重复跑。
- [ ] 自审并提交 `test(accountsvc): verify unified transport and gateway boundary`，交独立审查；覆盖OpenSpec4.1、4.2、4.3、4.4。

### Task 3: 部署模板、迁移文档与完整交付

**Files:**
- Modify: `deploy/accountsvc/Dockerfile`, `compose.yaml`, `.env.example`。
- Create: `docs/gateway-integration.md`, `openspec/changes/simplify-accountsvc-transport/verification.md`。
- Modify: `README.md`, `docs/accountsvc.md`, `docs/compatibility.md`, `docs/release-checklist.md`, `docs/development.md`（按现有验证说明需要）。

**Interfaces:**
- Consumes: 已批准运行时行为和两个任务报告、协调者完整门禁证据。
- Produces: 单端口本地开发部署；产品无关接入契约与未勾选的实际部署检查；完整verification记录。

- [ ] Docker仅EXPOSE8080；Compose删除内部端口映射与INTERNAL_ADDR注入，保留PUBLIC_PORT默认18080及development/回环绑定；模板给出TLS_ENABLED显式选择和证书冲突规则，不提交秘密。
- [ ] 用独立Compose项目、ignored合成.env渲染配置、构建并本地启动，验证统一地址健康/就绪和旧路径404；只清理该项目拥有的容器/网络/volume，不触碰既有postgres/redis。记录实际命令和退出结果。
- [ ] 写网关职责、入口暴露/直连防绕过、TLS三种部署方式、可信IP、身份可信前提、限流与并发范围、协议/凭据透传、不自动重试及日志保护；README/服务手册引用一份规则，不复制出冲突版本。
- [ ] 文档列旧配置/端口/路径到新配置迁移及整体回退，解释migrate继续忽略服务设置、AllowHTTP需要调用方明确选择、HTTP开关不影响出站协议；保留历史归档文档，不声称产品环境已验证。
- [ ] 协调者运行 GOWORK=off build/vet/完整Linuxrace/恢复脚本、服务严格门禁、生成/历史检查、根库依赖图和OpenSpec strict；据实际日志填verification，不以旧证据或skip冒充本次验证。纯文档不强制TDD。
- [ ] 核对文档链接和diff，记录各步审查与未验收项；提交 `docs(accountsvc): describe unified listener deployment`，交独立审查。OpenSpec5.1、5.2、6.1在实际验证后完成，6.2留到最终整体审查补录。
