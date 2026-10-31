# 网关与传输接入

本手册定义 accountsvc 和直接嵌入 accountkit 的宿主接入契约。accountsvc 的消费者、管理员、内省、探针和可选指标共用 `ACCOUNTSVC_HTTP_ADDR`；库宿主自行挂载路由，也可采用以下部署规则。网关与网络策略由部署方提供，服务继续执行认证、角色、scope、资源归属和验证码业务额度。项目不提供生产网关、网格或通用入口限流实现。

## 客户端描述透传与 CORS

只带X-Device-Id/Name的客户端可正常认证，客户端描述头均可选。描述只用于成功验证码/IdP登录、completeSignIn、reauthenticate和新轮POST /token；发码、绑定、换绑、当前GET、普通业务、Authenticate/内省不解析或写入它们。显式非法描述在消费凭证前400 INVALID_CLIENT_INFO，token为OAuth invalid_request；自动输入缺失/无法识别不阻断认证。

按使用场景将下列可选头加入代理透传与跨域Access-Control-Allow-Headers，并保留Authorization、Content-Type、X-Device-Id、X-Device-Name、X-Request-Id：

- X-Client-Type、X-Client-OS-Name、X-Client-OS-Version。
- X-Client-Browser-Name、X-Client-Browser-Version、X-Client-App-Name、X-Client-App-Version。
- Accept-Language：accountsvc 配置多语言邮件清单时据此选择验证码邮件的语言（见[邮件模板清单](accountsvc.md#邮件模板清单)）。它是 CORS 安全请求头，无需额外放行，但代理不得剥离或改写；缺失时服务使用默认语言。
- X-Device-Type、X-Device-Model。

默认解析也使用User-Agent与浏览器可用的Sec-CH-UA、Sec-CH-UA-Full-Version-List、Sec-CH-UA-Platform、Sec-CH-UA-Platform-Version、Sec-CH-UA-Model、Sec-CH-UA-Mobile；代理不要丢弃这些实际可用输入。浏览器控制的头由浏览器决定是否发送，不要求前端脚本强行设置、不主动请求额外高熵提示；字段可以缺省。UA和全部Sec-CH-UA*值分别限2048字节，超限/畸形只忽略相应自动解析部分，保留合法显式字段。

跨域预检由宿主/网关配置允许Origin、方法及这些实际使用的头，库不自动提供通用CORS策略。`/token`、`/revoke` 接受 `application/x-www-form-urlencoded` 后，这两个端点的表单 POST 属于 CORS 简单请求、不触发预检；端点不依赖 Cookie、凭据随请求体携带，不构成新的凭据暴露面，但允许的 Origin 与响应头暴露仍须由网关显式配置。按客户端读取需要Expose-Headers包含WWW-Authenticate、Retry-After、X-Request-Id；保留Cache-Control: no-store和错误体。当前会话GET所有响应都no-store；查询数据库依赖故障保持503，不能被代理改成401以触发用户退出。

优先级为合法显式值、Client Hints、UA。全部描述均自报，不能用于授权、限流身份或可信设备判断，也不代替可信ClientIP。默认不信任X-Forwarded-User-Agent等任意转发头；BFF由宿主Deps.ClientInfo明确适配原客户端信息，不能将BFF客户端库UA称为终端浏览器。原始UA、hints与完整描述不得进入日志/追踪/审计正文或指标标签；仅持久化有界结构化摘要，不保存原始UA、IP或位置。

这些输入只在同一请求内overlay；规范化非空描述作为单份观察整体替换，nil/空不写入，没有跨请求补字段。app/browser可并存，孤立version/build合法。制造商、build和extensions可由Go/宿主回调提供，没有对应显式头、任意JSON请求体或JSON头；网关不应自行引入此类透传协议。宿主仍须确定扩展允许来源、语义与隐私白名单，固定敏感键检查不能识别任意值中的隐私。nested响应与扩展限制见[README](../README.md#当前会话与客户端描述)及[Go指南](go-integration.md#当前会话解绑与客户端描述)。

## 入口与直连边界

| 入口 | accountsvc 路径 | 部署策略 |
|---|---|---|
| 消费者 | `/v1` 下的登录、身份、会话和生命周期路由 | 按实际消费者路由明确放行，保留各端点认证 |
| 管理员 | `/admin/v1` | 独立网络/入口策略，保留外部管理员 JWT 和逐路由角色检查 |
| 内省 | `POST /v1/introspect` | 仅授权内部调用方可达，仍要求独立 Basic 凭据或服务 Bearer 令牌 |
| 已清理用户拉取 | `GET /v1/purgedUsers` | 与内省相同：仅授权内部业务服务可达，要求独立 Basic 凭据或服务 Bearer 令牌；公网请求在网关拒绝，不随消费者 `/v1` 路由开放 |
| 公钥 | `GET /.well-known/jwks.json` | 只含验签公钥、无需认证；按业务服务所在网络选择对公网开放或仅内网可达，只放行 GET/HEAD |
| 探针 | `GET /healthz`、`GET /readyz` | 仅编排系统和运维来源可达；探针无消费者/管理员认证 |
| 指标 | `GET /metrics`（显式启用时） | 仅授权监控来源可达；无内建鉴权，禁止公网访问 |

JWKS 是公开信息，开放它不得连带开放内省、管理、探针或 `/v1/*` 通配路由；网关若缓存 JWKS，缓存时长不得超过服务端的 `Cache-Control: public, max-age=300`，否则密钥轮换的等待顺序会失效。业务服务全部在内网时可以不对公网开放 JWKS。禁止因内省位于 `/v1` 下就无条件公开 `/v1/*`。公网访问内省、探针和指标应在网关被拒绝，且不进入后端；是否允许管理员入口由单独策略决定。网络策略、防火墙或工作负载策略还须把后端端口限制为网关与受信任内部调用方，验证未授权来源不能直连绕过。路径名称、回环开发示例和 Basic 认证都不能自动建立生产网络隔离。

账号资源沿用 `/v1/users` 及其子资源和自定义动作；管理账号资源沿用 `/admin/v1/users`，响应 name 使用 users 前缀。公网清单须明确包含 `POST /v1/users:sendSignInCompletionCode` 与 `POST /v1/users:completeSignIn`，并分别配置匿名请求频率、突发与在途并发。客户端须处理登录的 token/pending 联合结果，见 [README](../README.md#身份策略与登录补全)。

公网清单还须明确包含 GET /v1/users/me/sessions/current（嵌入宿主替换实际前缀），保留消费者Bearer与user/user:bind scope检查；该GET不扩大内部内省/管理/探针开放范围。

网关认证不替代服务认证，也不把消费者 token 转换成管理员或内省身份。内部入口必须原样透传业务服务的 `Authorization: Bearer`，不得剥离、替换，也不得用网关自身的令牌代替业务服务调用内省或已清理用户拉取；`azp` 授权以原调用方令牌为准，见[服务调用认证](accountsvc.md#服务调用认证内省与已清理用户拉取)。直接嵌入库的宿主依原契约注入管理员验证和可信请求元信息，不应仅因请求经过代理就注入未经验证的 Principal。

## 指标访问

`ACCOUNTSVC_METRICS_ENABLED` 默认关闭。启用前在网关对 `/metrics` 设置独立来源允许清单，公网请求必须被拒绝且不进入后端；后端端口的防火墙、网络或工作负载策略也须限制直连来源。只允许授权监控网络抓取，验证未授权直连不能绕过网关。消费者 Bearer、管理员 JWT 与内省 Basic 不为指标入口提供保护，指标没有内建身份验证。

指标沿用业务单监听器和 TLS 配置，监控调用方使用有效协议并校验证书；没有独立指标端口。关闭时404，启用时仅 GET，其他方法405；保留响应 `Cache-Control: no-store`，不要缓存指标。采集错误和超出5个并发采集返回503；这是指标采集边界，不能统一改写为认证失败或通用入口429。采集只读本地聚合，不访问业务依赖，不因 readiness 为503就阻断抓取。

抓取目标应指向每个副本，保留 Prometheus 目标标签（例如 `instance`），避免只通过负载均衡地址轮流读取不同进程的累计值。同一逻辑账号实例可共用固定非秘密的 `ACCOUNTSVC_METRICS_INSTANCE`，不同逻辑实例必须使用不同值。指标配置、计数重置和停机尾部增量限制见[服务手册](accountsvc.md#prometheus-指标)。

## TLS 部署方式

| 方式 | 服务和调用方选择 | 部署方核验 |
|---|---|---|
| 服务自身 HTTPS | 服务有效 TLS 开启，完整有效证书对；调用方使用 HTTPS | 域名、证书链、期限、信任根及校验失败路径；探针同址 HTTPS |
| 网关终止并 HTTPS 回源 | 公网 HTTPS，网关到服务及业务内省调用使用经过证书校验的 HTTPS | 两段证书校验与后端直连限制，网关不能忽略回源证书错误 |
| 受控网关/网格提供外部 TLS | 服务显式 `TLS_ENABLED=false` 且移除 HTTP 证书项；应用段 HTTP，调用方使用 HTTP 时显式 `AllowHTTP=true` | 跨主机加密（例如工作负载代理间 mTLS）、受控本地应用段和直连隔离，确认部署身份与传输保护 |

生产默认仍要求服务端 TLS；未设/空开关和证书冲突矩阵见[服务手册](accountsvc.md#配置)。显式关闭只控制服务的 HTTP 监听，不影响生产 SMTP TLS、OIDC HTTPS、数据库或 Redis 连接协议，不会自动部署网关/网格。日志说明实际监听协议，不声明外部加密已经验证。第三种方式不能把 Basic 秘密和 token 跨主机无保护地传输，也不能仅凭客户端 `X-Forwarded-Proto` 声称安全；外部代理到应用的每一段都需明确边界和保护措施。

HTTPS 调用方必须校验证书；内省客户端默认拒绝 HTTP，只有调用方主动选择才允许受控应用段 HTTP。客户端不因服务开关、响应 Location 或转发头自动降级，也不跟随内省重定向。配置重启生效；回退须成套恢复二进制、监听/证书配置、调用方和网关策略，见[服务手册](accountsvc.md#邮件匿名化与停机)。

## 来源 IP 与请求标识

部署方核对实际代理链，仅把确实受控的直接代理网段加入 `ACCOUNTSVC_TRUSTED_PROXY_CIDRS`。边缘代理应移除客户端自报的转发头并按可信链重新生成；不可信直接对端或畸形链回退直接对端 IP。不要把所有地址设成可信代理来换取客户端 IP。嵌入式宿主通过 `Deps.ClientIP` 提供处理后的地址。

透传有效 `X-Request-Id`，无效或缺失时允许服务生成并回传；把它用于网关、业务与审计关联，不把未经验证的请求 ID 当作身份。使用伪造 `X-Forwarded-For` 的直连和经代理请求分别检查日志、审计和额度计数，证明攻击者不能选择自己的计数或审计 IP。

## 频率、突发与在途并发

入口请求频率、突发量和在途并发由网关/宿主配置。登录/发码、token/revoke、管理和内省分别选择预算；库继续执行验证码冷却、目标/IP UTC 日额度、校验次数、用途与防重放约束。通用入口限流不替代这些业务规则，也不在 accountsvc 中新增共享限流 Redis 状态。

每条策略应记录负责人、路由范围、计数维度、频率/突发/并发阈值、每副本或共享集群的计数范围及故障策略。IP、部署身份与已验证主体是不同维度；策略须说明 NAT、代理和网关多副本如何计数。分别验收持续频率、突发和在途并发，单副本测试不能证明集群总额度有效。项目不规定适用于所有产品的生产阈值。

按身份计数必须先验证该身份，不能相信自报 client_id、未验签 JWT 的 sub 或任意请求头。若只有 accountsvc 验证 Basic，前置网关就不能直接把 Basic 用户名视为可信客户端身份；可使用可信来源或经部署体系验证的工作负载身份，或显式装配并验证网关身份链。管理员和消费者身份仍按各自验证链处理。

超限返回可识别的429，并按策略提供 `Retry-After`；普通业务使用约定的业务错误体，OAuth 端点使用调用方可识别的 OAuth 错误形状。客户端和代理须保留状态/等待信息，不能将限流伪装成401、`invalid_grant` 或 `active:false`。网络、超时和依赖故障应拒绝业务访问并作为依赖不可用处理，避免触发用户刷新或退出登录。

## 协议、重试与日志

代理保留 `Authorization`（消费者/管理员 Bearer、内省与拉取的 Basic 或服务 Bearer 各自原样送到正确端点）、`X-Device-Id`、`X-Device-Name`、表单 `Content-Type` 和请求 ID；响应保留 `WWW-Authenticate`、`Retry-After`、`Cache-Control`（包括内省 no-store）、状态码及错误体。不要把表单内省改成 JSON，也不要让挑战或缓存语义在代理错误页中丢失。服务请求体上限和超时保持，网关另行核对自身限制与上游预算。

默认关闭认证和写请求的自动重试，包括发码、登录、刷新、撤销和管理操作。请求失败或客户端取消不证明上游尚未消费验证码、发送邮件或提交事务；需要恢复时由调用方按具体动作查询状态或执行已定义的流程。普通接口由业务服务用 JWKS 本地验签；关键接口每次请求重新内省，不缓存成功结果。业务服务继续验证 scope、近期认证与资源归属，分级方式见 [accountsvc 接入说明](accountsvc.md#分级校验)。

网关、服务、追踪/APM和错误采集均不得记录完整 Authorization、Basic 秘密、验证码、access/refresh token、SMTP 凭据或完整手机号/邮箱；内省表单和认证请求体不得原样采集。使用请求 ID 和脱敏元信息定位问题。实际检查访问日志、失败日志、追踪属性及审计输出，不能仅凭服务日志安全就认定全链路安全。

登录补全的 `sign_in_completion_token`、轮次标识、凭证和目标也只放请求体，不放 URL、日志、审计或追踪属性。代理保留200 pending与成功响应的 no-store，以及400 `SIGN_IN_COMPLETION_INVALID` / `SIGN_IN_ALREADY_COMPLETED`。结果未知时由客户端在原期限内以相同完成请求确认，服务复核账号、会话和 refresh 后才返回原 pair；代理不要自动重发或更改设备头。

## 探针、关闭与验收证据

探针同址同协议。healthz 不访问外部依赖；readyz 在启动完成、未停机且 PostgreSQL/Redis 两秒总预算内可用时为200，否则503。SMTP 和运行期 OIDC 故障不单独改变整体 readiness。编排系统同步更新探针端口/协议、网关摘流和连接排空；终止宽限必须大于 `SHUTDOWN_TIMEOUT`，给库收尾留出预算。

部署方按[发布清单](release-checklist.md#accountsvc-产品上线)提供真实网关/网格、网络隔离、证书、可信 IP、限流、并发、透传、不重试、日志和停机证据。本地 TLS 代理仅核验服务与代理夹具行为，不能代替真实产品策略验收；本次本地结果保存在[传输验收记录](../openspec/changes/archive/2026-10-06-simplify-accountsvc-transport/verification.md)，实际部署项继续待验收。直接嵌入库无需部署 accountsvc，但仍由宿主完成同样的入口保护职责。
