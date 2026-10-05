## Context

动机及范围见 [proposal.md](proposal.md)。目前 `internal/accountsvc/run.go` 创建两个 listener/server，`http.go` 分别挂载 public/internal 路由，production 在 `config.go` 强制装配 TLS；`migrate` 在读取服务专用配置前返回。`examples/remoteauth` 已有默认 false 的 AllowHTTP，但注释仅描述开发用途。代码及旧规格均明确依赖双端口隔离，需要一起迁移。

依据为 [运行时主规格](../../specs/accountsvc-runtime/spec.md)、[内省主规格](../../specs/consumer-token-introspection/spec.md)；本次改动的契约分别见 [运行时增量](specs/accountsvc-runtime/spec.md)、[内省增量](specs/consumer-token-introspection/spec.md)、[网关接入](specs/gateway-integration/spec.md)。用户已确认直接迁移，移除旧路径并对旧监听配置报错；不提供兼容双监听模式。

RFC 7662 不要求固定内省路径，定义 TLS 保护及调用授权；RFC 8414 的地址发现能力不属于本变更。部署侧终止 TLS 不代表将 Basic 或 token 跨主机明文传输视为合规部署。参考 [RFC 7662](https://www.rfc-editor.org/rfc/rfc7662.html#section-2)。

## Goals / Non-Goals

**Goals:**

- 以同一地址承载独立的消费者、管理员、内省和探针处理器，保持身份及领域边界。
- 显式表达 HTTP TLS 由本进程提供或由部署层提供，未指定开关时保持原 TLS 默认行为。
- 用部署文档与本地代理夹具验证访问策略和协议透传；实际网关/网格部署由宿主验收。

**Non-Goals:**

- 不实现网关、通用限流或网格产品配置，不自动识别 TLS 终止方，不增加第二个“信任明文”开关。
- 不改变库 Config/Deps、不增删认证字段或失败语义，不修改数据库、密钥回填、业务匿名化或依赖。
- 不修改历史归档设计以伪装旧版本已采用新架构；仅更新当前手册及明确的兼容迁移说明。

## Decisions

### 1. 一个监听地址，分别挂载认证处理器

保留 HTTP_ADDR 及默认 `127.0.0.1:8080`。serve 创建一个 net.Listener 和 http.Server，继续使用既有请求追踪、超时、体积限制、启动失败资源清理与关闭预算。减少 server 数量不改变 `Config/Deps → New → Migrate → Start → Close` 的库契约。

单路由树挂载 `/v1` 消费者和 `/admin/v1` 管理员，精确注册 `/v1/introspect` 与两个根探针。内省仍由原 handler 处理认证、方法和 Content-Type；不能仅通过外层 Post 注册改变原先的401/405、Basic challenge及 OAuth 错误语义。测试必须证明精确内省路由不会被消费者 `/v1` mount 吞掉，也不会绕过 Basic。

旧 `/internal/v1/introspect` 返回404，不做凭据请求重定向。serve 读取到非空 INTERNAL_ADDR 就报安全配置错误，提示移除并更新访问策略；不会忽略变量后悄悄改变网络暴露面。migrate 继续不读取这些服务专用变量。

替代方案：保留双监听器或旧路径别名可以减少短期调用方改动，但会保留两套部署语义。用户选择直接迁移，因此不实现兼容开关。

### 2. TLS 的三态输入与确定的有效值

新增服务侧 TLS_ENABLED，按现有 ADMIN_ENABLED 的布尔解析规则处理非空值；未设置/空值表示沿用旧默认。运行时只使用解析后的有效布尔值，不增加根库 TLS 配置。

| 输入 | mode | HTTP证书项 | 结果 |
|---|---|---|---|
| 未设置/空 | production | 完整有效 | HTTPS，缺失或无效则失败 |
| 未设置/空 | development | 均为空 | HTTP |
| 未设置/空 | development | 任一非空 | 必须为完整有效证书对，成功后 HTTPS |
| true | 任意合法mode | 任意 | 要求完整有效证书对；失败不降级 |
| false | 任意合法mode | 均为空 | HTTP，日志明确本进程未启用 HTTP TLS |
| false | 任意合法mode | 任一非空 | 配置冲突，拒绝启动且不忽略证书 |
| 非法布尔值 | 任意 | 任意 | 拒绝启动 |

启用时保持最低 TLS1.2 与现有证书验证规则；不新增客户端证书认证。关闭时不装配 tls.Config、不因 HTTP 请求头自动重新解释模式。日志只说明实际监听协议，不声称已验证网关或网格提供加密。

production 的 SMTP TLS、外部 OIDC HTTPS 和内省客户端必填约束保持；数据库、Redis 的 TLS 仍由原 DSN/URI 控制。本开关不提供全局“禁用所有 TLS”能力。

### 3. 内省地址与调用方传输配置

Basic 客户端 ID/秘密的静态登记、轮换及参数校验不变；内省体仍为表单，响应仍为 active 与最小授权信息，复用 Authenticate，不增加数据库状态查询。

更新远程示例及其说明至新路径和统一地址。AllowHTTP 仍默认 false，只有调用方显式开启才接受 HTTP URL；其文档用途扩展为本地开发或由受控代理/网格保障传输的应用连接。客户端不根据服务配置、Location或转发头自动降级，禁止重定向、不缓存成功结果和非200按依赖故障处理均保持。

推荐的直接部署仍为公网网关 HTTPS 终止后 HTTPS 回源，业务服务 HTTPS 调用。未来网格可在工作负载代理间提供 mTLS，应用段显式使用 HTTP。仅改开关不会自动建立这些保护；单监听器与外部 TLS 终止是独立选择。

### 4. 网关契约及验收职责

新增 `docs/gateway-integration.md`，README 和 accountsvc 手册只引用公共规则，发布检查表增加待验收项。内容按入口分类列出：

- 消费者公开路由、独立管理策略、内部内省与探针；禁止将 `/v1/*` 无条件作为公网透传规则，后端端口限制到网关及受信任内部调用方。
- 可信代理 CIDR、来源IP防伪、X-Request-Id；认证头、设备头、Content-Type、WWW-Authenticate、Retry-After、Cache-Control 透传及凭据日志保护。
- 网关负责请求频率、突发、在途并发；宿主记录每副本/集群计数方式与故障策略。库中验证码业务额度不变，不写统一生产阈值，不在服务里增加 Redis 限流依赖。
- 基于身份限流必须先验证身份。只有 accountsvc 才验证 Basic 时，前置网关不能直接相信 Basic 用户名；可使用可信来源或部署身份。
- 超限429与认证失败分开，普通业务与 OAuth 端点采用各自可识别的响应形状；不伪装401/invalid_grant/active:false。默认不由网关自动重试认证或写操作。
- 探针同端口且协议随有效 TLS 配置变化；就绪、优雅关闭、终止宽限与连接排空须同步配置。

真实网关、网络策略、证书链和限流阈值验证保持发布前置条件。本地测试代理只用于演示并核验 allowlist/内省隔离、HTTPS外部入口与 HTTP回源、可信来源及凭据透传，不发布实际网关配置，不将其测试结果作为产品部署通过。

### 5. 本地验证与兼容维护

- 配置矩阵覆盖两个 mode、开关未设/空/true/false/非法、证书完整/缺失/无效/冲突；migrate 忽略这些服务变量仍通过。生产 false 必须维持 SMTP TLS 与 Basic 要求。
- 路由测试覆盖一个地址上的所有处理器、新旧内省路径、无凭据/错误类型凭据/角色不足、认证优先于 token 校验、GET与其他方法/错误内容类型、请求体上限和健康状态。
- 实际 Linux 进程使用真实一次性 PG/Redis、本地 SMTP/OIDC，验证默认 HTTPS、显式 false 的 production HTTP、完整登录/刷新/内省及实际SIGTERM。外部终止用本地 TLS 代理模拟，不引入生产网络和实际服务商。
- 原有服务必需测试不能因双监听假设消失而删减验收范围；重写为单监听断言，保留邮箱生命周期、管理员、故障、换绑、过期、验证码轮换和正常/超时关闭验证。
- Compose 保留 `ACCOUNTSVC_PUBLIC_PORT` 作为统一端口映射变量（默认18080），删除内部端口映射和 INTERNAL_PORT 模板项，避免额外重命名；本地仍 development 和回环暴露。
- 新增必需顶层用例加入相应门禁；执行构建、vet、相关和完整 Linux race/恢复入口、服务严格入口以及 OpenSpec 严格校验。结果存本 change 的 verification.md，原始输出放 `.test-output/`。

## Risks / Trade-offs

- [端口合并取消原网络面隔离] → 明确 BREAKING，旧 INTERNAL_ADDR 拒绝启动，先部署公网路由策略和直连限制再切流量；服务不假装自动实现来源隔离。
- [显式 false 使凭据在应用段可为明文] → 默认行为不变，配置冲突拒绝，记录有效协议；跨主机加密和受控本地段由部署方验收，不能靠 X-Forwarded-Proto 自证安全。
- [凭据与角色在统一入口混淆] → 保持独立 handler 和认证链，并测试消费者 token 不能替代内省 Basic 或管理员 JWT。
- [旧客户端/探针访问地址失效] → 新旧地址与变量迁移表、服务和网关协调发布；无自动别名/重定向，不静默把失败当成用户凭据失效。
- [本地代理夹具被误认为产品网关验收] → 验收记录和发布清单分开，实际部署项保持未勾选。

## Migration Plan

1. 部署前清点所有内省调用、探针、端口映射、网关路由与环境注入；确认配置/回滚版本成套保存，不修改数据库。
2. 在网关侧先准备明确的公网路由允许范围，屏蔽新内省与探针入口，限制后端直连；不要在路径迁移时扩大 `/v1/*` 通配暴露。
3. serve 删除 INTERNAL_ADDR。Compose/自有部署删除旧内部端口映射，改用 HTTP_ADDR 的统一地址；将调用方 `/internal/v1/introspect` 改为 `/v1/introspect`，探针改为同一地址。
4. 未设置 TLS_ENABLED 的生产部署继续提供有效证书。选择外部终止时，先验证外部传输和网络边界，再显式设置 false 并移除服务端 HTTP 证书项；同步调用方 URL/AllowHTTP 和探针协议。所有配置均重启生效。
5. 完成本地新旧配置、真实服务及代理夹具验收。实际网关/网格、服务商和产品环境验证继续延期并保留发布清单待办。
6. 回退需同时恢复旧二进制、INTERNAL_ADDR/内部端口、旧内省路径和双监听探针；从生产 false 回退旧版本必须恢复服务端 TLS 证书。回退不执行 Down、UnsafeReset 或清空 Redis。
