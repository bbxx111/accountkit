## Context

动机见 [proposal.md](proposal.md)。当前 `examples/embedded` 只支持开发日志发送器，根门面已经具备依赖注入、相对路由、启动迁移、后台任务与关闭契约。以下三个已归档变更的规格是本设计的兼容依据：

- [嵌入式库](../2026-10-03-extract-auth-server/specs/embedded-auth-package/spec.md)
- [迁移安全](../2026-10-03-harden-migration-safety/specs/migration-safety/spec.md)
- [包名和可选服务](../2026-10-03-rename-accountkit-package/specs/accountkit-package-identity/spec.md)

用户已确认：SMTP 邮件先行；消费者使用远程鉴权；支持通用 OIDC JWT 管理员验证且默认关闭。以下端口、配置名、超时及协议细节是实施约定；实际验收状态见 [tasks.md](tasks.md)。

## Goals / Non-Goals

**Goals:** 用同一套领域实现交付可运行服务；让外部业务服务不持有消费者签名密钥；明确每个网络面的身份、故障行为与资源归属；让服务测试证明与嵌入式模式兼容。

**Non-Goals:** 不改变 HS256、刷新/吊销/审计故障策略，不建立管理员账号库或消费者授权码流程，不扩展跨库事务，不承诺邮件被用户实际阅读，不把单一实例的多副本部署扩展为动态多租户。

## Decisions

### 1. 单 module 的可选宿主

新增 `cmd/accountsvc/main.go`，运行装配放 `internal/accountsvc/`，通用 SMTP 实现放 `user/sender/smtp/`。外部宿主可单独选用 SMTP 子包；根包不导入服务包。服务设置监听器、连接池和发送器，业务动作仍交给 accountkit。

```text
embedded host ------> accountkit <------ internal/accountsvc <------ cmd/accountsvc
                          ^                     |
                          |                     v
                    sender contracts <------ SMTP adapter
```

相较复制示例成为第二套认证实现，这种布局保持业务规则唯一；相较拆出第二 module，不增加版本协同和本地替换需求。

### 2. 配置与启动

库配置继续调用 `ConfigFromEnv("ACCOUNTKIT_")`，原有变量及默认值不变。服务配置使用 `ACCOUNTSVC_` 前缀，避免监听、SMTP、管理员设置混入库 Config。所有密钥由运行环境注入，无内置固定密钥、无自动生成后持久化。错误点名配置项但不打印值、DSN 或证书私钥。

| 服务配置 | 首版约定 |
|---|---|
| `MODE` | `production`（默认）或显式 `development` |
| `DATABASE_URL` / `REDIS_URL` | 必填；Redis 使用 URI，支持认证、数据库编号和 TLS |
| `HTTP_ADDR` / `INTERNAL_ADDR` | 默认 `127.0.0.1:8080` / `127.0.0.1:8081`；不同监听地址 |
| `TLS_CERT_FILE` / `TLS_KEY_FILE` | 生产必填，两个监听器均使用 TLS 1.2 及以上；开发模式允许明文 |
| `TRUSTED_PROXY_CIDRS` | 默认空；仅显式可信代理可影响来源 IP |
| `STARTUP_TIMEOUT` / `SHUTDOWN_TIMEOUT` | 默认 60s / 30s；启动超时为正，关闭超时大于15s，预留库关闭预算 |
| `SMTP_HOST` / `SMTP_PORT` / `SMTP_FROM` | 必填；发件地址必须为单一合法地址，拒绝换行注入 |
| `SMTP_TLS_MODE` | 默认 `implicit`，可选 `starttls`；仅开发模式可用 `none` |
| `SMTP_USERNAME` / `SMTP_PASSWORD` | 成对配置；生产模式要求 SMTP 认证 |
| `SMTP_TIMEOUT` | 默认 10s，涵盖连接、TLS、认证和投递 |
| `INTROSPECTION_CLIENTS` | 必填 JSON 对象：客户端 ID 对应一组允许的随机秘密；支持重叠轮换 |
| `ADMIN_ENABLED` | 默认 false；false 时拒绝混入 ADMIN 专用凭据配置，避免误以为已启用 |
| `ADMIN_ISSUER` / `ADMIN_AUDIENCE` | 启用时必填；issuer 为 HTTPS URL，audience 为管理员 API 专用受众 |
| `ADMIN_ROLES_CLAIM` | 默认 `/roles`，使用 JSON Pointer，目标必须为字符串数组 |
| `ADMIN_USERNAME_CLAIM` | 默认 `/preferred_username`，可缺失；管理员唯一标识仍是 issuer + sub |

命令支持无参数运行服务或 `serve`，以及独立 `migrate`。`migrate` 只校验库配置、数据库/Redis 连接和启动超时，不要求 SMTP、调用客户端或 OIDC 配置，不监听、不启动维护任务。`serve` 启动顺序：纯配置校验 → 创建依赖 → New → 有超时的 Migrate → 管理员 discovery/JWKS 初次校验（若启用）→ 绑定两个监听器 → Start → 对外服务。任一步失败均回收已创建资源并返回非零；不自动 Force、不执行 Down/UnsafeReset。

独立 migrate 可用于部署前置作业，serve 仍执行幂等 Migrate 和密钥版本检查，避免遗漏部署步骤；沿用现有迁移锁实现。migrate 构造门面时注入两个 Disabled 发送器并关闭管理员适配，只调用 Migrate/Close，因此满足既有非 nil 依赖要求且不会发送消息。首版不引入自动回滚或仅检查版本的第二套迁移路径。

### 3. HTTP 面、健康检查与关闭

公开监听器只挂 `/v1` 和 `/admin/v1`；内部监听器挂 `POST /internal/v1/introspect`、`GET /healthz`、`GET /readyz`。公开监听器不得路由到内部端点。生产客户端直接使用 HTTPS；如经反向代理，代理到服务仍使用 TLS，首版不提供生产明文豁免。服务不添加浏览器管理界面、Cookie 会话或默认宽松 CORS。

两个 HTTP Server 设置 `ReadHeaderTimeout=5s`、`ReadTimeout=15s`、`WriteTimeout=30s`、`IdleTimeout=60s`，请求体上限 64 KiB。可信代理使用 CIDR 判断直接对端，并从 X-Forwarded-For 右侧逐跳剥离可信代理；不可信对端忽略转发头，畸形链回退直接对端。请求 ID 复用既有格式约束，所有服务端点响应回传同一请求 ID。

`healthz` 为进程存活探针，不访问外部依赖；`readyz` 只有启动完成、未停机且 PostgreSQL/Redis 在 2s 总预算内成功探测时返回 200，否则 503。健康响应只给状态，不暴露地址或错误细节。SMTP/OIDC 故障不令整个消费者服务失去 readiness，各功能按自身错误语义拒绝；启用 OIDC 的初次 discovery/JWKS 加载失败仍阻止启动。

收到 SIGINT/SIGTERM 或任一监听器异常时先清除就绪状态，再停止接收请求并排空两个 HTTP 面；排空后关闭 accountkit，最后关闭自建 Redis/Pool。信号 ctx 不直接作为审计/维护 ctx，以免请求排空前停止审计。总关闭预算包含现有 Close 的维护等待（上限 10s）和审计刷新（上限 5s）；配置至少预留 15s，其余用于 HTTP 排空。超时强制断开残留连接并以非零退出，不宣称全部审计已写入；不在仍有活跃请求时提前关闭其连接池。

### 4. 邮件投递与显式禁用渠道

SMTP 子包实现现有 EmailSender，不更改接口签名。使用固定中文模板区分登录、绑定和重新认证，内容含验证码及有效期；拒绝地址/头注入，日志不含完整收件地址、验证码、密码或正文。邮件安全传输参考 [RFC 8314](https://www.rfc-editor.org/rfc/rfc8314)：支持隐式 TLS 和强制 STARTTLS，验证证书；TLS 失败禁止降级。

SMTP 接受 DATA 后算本次提交成功，不把投递队列接受等同于最终到达。无后台重试，避免超时后状态不明导致重复发送。SMTP 通过新增 sender 叶子包的可识别不可用错误表达网络/超时/服务拒绝，领域层将其映射为既有依赖不可用（503）；其他旧发送器的普通错误映射不变。生成后的额度不退还，仍沿用既有验证码存储与冷却规则。

根库 New 仍要求两个非 nil sender。新增显式 Disabled 发送器以及可选的渠道可用性契约（如 `Enabled() bool`）；不实现该契约的现有发送器按已启用处理。在领域发码流程完成渠道/目标格式校验后、Issue 前检查禁用，返回新增 `sender.ErrDisabled`，消费者面映射 400 `CHANNEL_NOT_ENABLED` / `FAILED_PRECONDITION`，并记录无凭证的 CODE_SEND_REJECTED 审计。此检查覆盖登录、绑定、重新认证；不影响已有验证码校验、已绑定手机登录或身份数据。accountsvc 固定装配 SMTP 邮件和 Disabled 短信，不提供日志验证码模式。服务不以返回成功的空发送器代替禁用。

相较在服务 middleware 中解析三种请求体，领域发送前检查避免重复 DTO 和协议逻辑；相较变更 sender 必需方法，可选契约保持旧宿主编译和行为兼容。

### 5. 消费者远程鉴权

内省协议采用 [RFC 7662](https://www.rfc-editor.org/rfc/rfc7662) 的 POST 表单与 active 响应形式，不增加授权码或客户端令牌签发系统。调用者以 HTTP Basic 使用独立客户端 ID/秘密认证；至少 32 随机字节的秘密以 base64 编码配置，常量时间比较，允许一个 ID 的新旧秘密短期并存，重启后移除旧值。此凭据只授予本实例的内省权限，不能访问管理面；消费者 JWT 也不能当作调用凭据。

请求包含必需 `token` 和可选 `token_type_hint`；只内省 access token，refresh token 返回 `active:false`，hint 只作为提示、不决定是否尝试 access 验证。拒绝重复 token、空 token、超限请求和非表单内容。客户端认证先于令牌校验；无效调用凭据返回 401 `invalid_client` 和 Basic challenge，畸形表单 400 `invalid_request`，超限 413，不支持的内容类型 415。响应使用 OAuth 顶层错误格式，不套业务 error 对象。

成功调用 `a.Users().Authenticate(ctx, raw)`：无效、过期、其他实例、已确认吊销的 access 返回 200 `{"active":false}`；有效返回 `active:true`、`sub`、`scope`，以及扩展字段 `sid`、`auth_time`（Unix 秒）。首版不返回 exp、身份资料或 refresh 信息，不增加 claims 解析副本。响应 `Cache-Control: no-store`，接入示例每次受保护请求查询，不提供正结果缓存。业务服务仍负责 scope、近期认证和资源所属校验，不能将 `active:true` 等同于对所有业务的授权。

现有 Authenticate 只检查签名/claims、资源 ID 格式与 Redis 吊销集，不读取数据库重新计算账号状态或 scope；Redis 查询失败仍 fail-open 并记录警告。内省严格保留这一语义，不声称强一致或 fail-closed。readyz 在 Redis 故障时为 503，但已路由到实例的内省请求仍按现有认证规则处理；这是两种不同契约。调用端网络错误/超时/非 200/响应畸形必须拒绝业务请求并报告依赖不可用，不能当作登录过期或沿用旧结果。

相较分发 HS256 密钥，远程方式减少业务方拥有签发能力的风险；相较本轮引入非对称 JWT/JWKS，不改变已发行凭据。代价是每次鉴权增加网络往返，后续优化另立变更。

### 6. 管理员认证适配

启用后从配置 issuer 的 discovery 文档获取 JWKS，校验返回 issuer 精确相等、JWKS 为 HTTPS；固定超时和响应大小上限，不从入站 token 的 jku/x5u 获取地址。提供方元数据依据 [OpenID Connect Discovery](https://openid.net/specs/openid-connect-discovery-1_0.html)。只支持外部提供方签发给管理员 API 的 JWT access token，首版签名算法允许 RS256（RSA 至少 2048 位）；不支持 opaque token、管理员浏览器登录、ID token 作为 API 凭据或 Keycloak 专属依赖。

管理员 API audience 必须独立于消费者 audience 和 OIDC 登录客户端 ID，提供方需签发独立受众的 access token。验证签名、算法、issuer、audience、非空 sub、必需 exp，以及存在时的 nbf/iat，时钟容差 30s。用户名仅为可选展示字段；角色 JSON Pointer 仅从已验证 claims 中取值，不信任 HTTP 头。缺失角色视为空，类型错误拒绝凭据；super-admin 在适配层显式包含 operator，不依赖提供方的复合角色配置。

JWKS 缓存最多使用 1h；未知 kid 触发刷新、并发请求合并且最短刷新间隔 60s。仍有效缓存内的已知 key 可在提供方故障时继续验证；无可用 key 且拉取失败/被失败刷新节流时返回 503，成功刷新后仍未知 kid 返回 401。缓存到期不得无限使用旧 key。实现优先复用成熟验证组件，版本及其缓存行为在实施计划中核验；不复制 Apple 专用 nonce/受众策略。

装配调用认证 middleware，再挂 AdminHandler，并成对注入 AdminVerifier/AdminPrincipal。逐路由授权和 ADMIN_FORBIDDEN 审计仍走既有接口：未认证 401（Bearer challenge）、角色不足 403（一次拒绝审计）、依赖故障 503。关闭时完整挂载既有 Unconfigured handler 返回 503 ADMIN_NOT_CONFIGURED，不影响消费者。

### 7. 数据与验收边界

服务不注册宿主业务匿名化回调，只清理自己拥有的认证表。使用方有额外 user_id 关联数据时，应选嵌入式同库回调，或独立制定业务清理流程；服务部署手册不能承诺已覆盖业务表，也不把 HTTP webhook 放入认证数据库事务。

交付服务 Dockerfile、开发 Compose（PostgreSQL、Redis、仅本地邮件捕获器）、无真实密钥的环境变量示例及二进制运行手册。开发凭据由使用者生成，Compose 显式 development 且只绑定回环地址。生产 SMTP 凭据不得用于自动化测试。

验收分为：现有库回归；服务配置/协议/关闭测试；真实 PostgreSQL/Redis + 隔离 SMTP 接收器的邮件登录、刷新、内省、注销链路；受控 HTTPS OIDC/JWKS 测试的验证和轮换；构建实际二进制并检查信号退出、重启和迁移失败。服务集成入口缺依赖必须失败；外部 SMTP 真实投递和实际 OIDC 提供方联调记录为部署验收，不能由模拟测试代替。

## Risks / Trade-offs

- [Redis 吊销故障继续放行] → 保留兼容性、明确健康检查与请求认证区别，测试及接入手册说明限制；本轮不暗改策略。
- [SMTP 已接受但客户端观察到超时] → 不自动重发，保留既有额度/冷却限制，不宣称最终送达。
- [提供方 claims 结构各异] → 配置角色/用户名 JSON Pointer，支持边界明确限定为 RS256 JWT access token；其他算法或不透明 token 后续扩展。
- [OIDC key 轮换与撤销有缓存窗口] → 缓存硬过期、未知 kid 节流刷新并测试故障；手册说明最长缓存窗口。
- [服务退出预算不足导致审计丢失] → 预留 Close 时间，排空请求后关闭，超时非零退出并记录固定阶段信息。
- [独立部署被误认为清理全产品数据] → 明示认证数据范围，业务清理单独验收。
- [新服务依赖增加根 module 依赖数量] → adapter 放叶子包、运行逻辑放 internal，库不反向导入服务，独立宿主编译持续验收。

## Migration Plan

1. 实施前形成引用本 change 的详细计划并审查公开 sender 扩展；所有任务当前未完成。
2. 在一次性数据库验证启动建库、多副本迁移、旧数据接管、邮件投递、内省和管理员隔离；运行现有完整门禁及服务门禁。
3. 首次部署配置独立 schema/Redis 前缀、消费者 issuer/audience/密钥、SMTP 和内省客户端凭据。管理员可保持关闭。
4. 新实例部署先执行 migrate，再启动 serve；每个网络面用各自路由及访问策略，不自动向外发布内部端口。
5. 现有宿主无需迁移至服务。选择切换的宿主保持原存储、令牌配置和认证数据匿名化责任，经预发布验证后转移流量；不与带业务匿名化回调的宿主混跑维护任务，以免服务先取得锁而跳过业务回调。
6. 回退只回退二进制/流量，不执行数据库 Down 或清库；既有认证数据和 token 格式不变。撤销新增服务调用凭据前先停止对应业务调用方。
