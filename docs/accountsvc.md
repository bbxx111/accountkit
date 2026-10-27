# accountsvc 服务

accountsvc 是 accountkit 的可选官方宿主。同一个 Go module 同时支持直接嵌入库和运行服务；使用库的项目不需要部署或调用 accountsvc。

## 会话上下文部署

v0.4.0 提供GET /v1/users/me/sessions/current，允许user/user:bind的Bearer，返回数据库最新的非空具体身份来源，以及可选单份nested client_info和client_info_update_time；所有响应no-store。user:undelete不允许；数据库无效会话401 TOKEN_INVALID，依赖故障503；来源缺失或失效、已存客户端描述非法均为固定500内部错误，不能统一映射为401或正常空集合。普通access验证仍只做JWT/Redis验证，不增加数据库热路径，Redis故障边界保持；内省按 sid 读取会话吊销状态（见[内省](#内省)），不读取来源或客户端描述。

默认HTTP解析已装配，无新增必需配置、任意JSON输入或制造商/build显式头；显式可选头、UA与Client Hints由[网关/CORS清单](gateway-integration.md#客户端描述透传与-cors)按实际使用透传。只在成功登录、补全、重新认证和新轮refresh观察；规范化非空描述整体替换单行JSONB，nil/空描述保留已有值与时间，孤立version/build合法，app/browser可以并存。pending完成本次非空描述优先，否则整体沿用加密首次描述及原观察时间；失败、回滚和重放不更新。业务读写校验而非数据库JSON对象/业务CHECK。不收集业务请求活动，不保存原始UA/IP/位置；原设备头仍有独立设备合并与pending一致性语义，描述不构成可信设备证明。嵌入式宿主可通过Deps.ClientInfo覆盖解析并提供Extensions，但须管理来源、语义和隐私白名单，详见[Go指南](go-integration.md#当前会话解绑与客户端描述)；accountsvc不提供任意转发UA信任配置。

v0.4.0 的0001包含完整会话上下文；部署须按[迁移手册](migrations.md#会话认证依据初始整合)人工核验新空目标，安装完整七表且version=1/dirty=false，并协调切换全部认证副本。旧v0.3.0 v1、前一`b414fef`双快照v1与撤回会话v2均拒绝直接接管，需要保留数据时另行制定迁移方案。旧v0.3.0 Migrate可能接受同号新结构，不能依赖旧预检防止旧来源/撤销规则混跑，部署方须显式阻止。解绑命中任一来源仍会撤销相应会话；同账号任一未吊销会话缺少有效来源时整事务报内部错误并回滚。换绑检查当前来源，成功204/200仍为操作成功，客户端重查并处理退出。恢复先落独立空库核验，不能清Redis、改版本或Down代替。当前验证状态见[简化验收](../openspec/changes/archive/2026-10-20-simplify-session-context/verification.md)。

## 两种集成方式

| 方式 | 使用方负责 | 适用边界 |
|---|---|---|
| 嵌入 accountkit | 连接、发送器、HTTP 挂载、管理员验证和业务匿名化回调 | 需要同库业务事务或定制装配 |
| 运行 accountsvc | 运行配置、TLS、SMTP、外部管理员提供方、业务端授权 | 通过消费者 API、JWKS 本地验签及关键路由内省接入 |

服务提供可选 SMTP 邮件验证码；显式身份策略启用 EMAIL 时要求 SMTP，禁用 EMAIL 时不解析或要求 SMTP。未设置新策略保留原 SMTP 装配，短信显式禁用；禁用渠道请求返回 400 `CHANNEL_NOT_ENABLED`，不生成验证码、不扣发送额度。库宿主原有短信/邮件发送器继续可用。

服务默认使用库管理的本地有界异步审计及本地管理查询，`Auth.Start/Close` 管理 worker，服务管理连接。`ACCOUNTKIT_AUDIT_SOURCE` / `ACCOUNTKIT_AUDIT_NAMESPACE` 可显式设置，默认分别为 `accountkit/<schema>` / `accountkit/<schema>/resources`；同库共享审计时保持实例范围稳定。服务未内置远端审计适配器或任意业务审计 HTTP 写入接口。嵌入宿主的读写接管与仅写入模式见 [examples/audit](../examples/audit/main.go) 和[库装配说明](../README.md#宿主审计装配与清理责任)。

当前审计使用UUIDv7标准字符串，管理HTTP仍返回认证投影；result未知时为null。旧审计page_token返回400，客户端清除后重新翻页。已发布v0.3.0和v0.4.0均安装为version=1，但v0.4.0的0001额外包含简化后的完整会话上下文，不能根据版本号直接接管旧库。旧identity_flow、旧十七列审计、双表并存、缺少会话结构的v0.3.0 v1、前一`b414fef`双快照v1和撤回v2均拒绝；重复migrate不自动改名、转换或补表。新部署使用经人工核验的新空资源，保留数据需求另行制定迁移方案，见[迁移手册](migrations.md#会话认证依据初始整合)。外部清理须有持久化流程和完成核验，`USER_PURGED`不能独立证明清理完成；切换后旧本地副本仍受保留和匿名化责任约束，详见[审计迁移说明](compatibility.md#通用审计公开接口与协议迁移)。

## 构建与运行

在仓库根目录运行：

```bash
GOWORK=off go build -o bin/accountsvc ./cmd/accountsvc
./bin/accountsvc migrate
./bin/accountsvc serve
```

PowerShell 使用 `$env:GOWORK = 'off'`，构建目标可使用 `bin/accountsvc.exe`。无参数等同 `serve`。命令不自动读取 `.env` 文件，应由 shell、容器或进程管理器注入环境变量。

`migrate` 仅要求库配置、数据库/Redis URL 和启动超时，不要求 SMTP、TLS 监听配置、内省客户端或管理员参数；也不解析两个指标设置、不创建指标组件，即使指标设置无效也不影响迁移。它不监听、不运行维护任务。`serve` 仍执行幂等迁移、依赖连通性和身份密钥版本检查。迁移 dirty 或密钥缺失会阻止启动，不自动 Force 或清库。恢复流程见 [迁移手册](migrations.md)。

开发环境可使用 [Compose](../deploy/accountsvc/compose.yaml)：

```bash
cp deploy/accountsvc/.env.example deploy/accountsvc/.env
# 填入各个独立随机测试密钥后运行
docker compose --env-file deploy/accountsvc/.env -f deploy/accountsvc/compose.yaml up --build -d
docker compose --env-file deploy/accountsvc/.env -f deploy/accountsvc/compose.yaml down
```

默认统一服务地址为 `http://127.0.0.1:18080`，邮件捕获界面为 `http://127.0.0.1:18025`。`ACCOUNTSVC_PUBLIC_PORT` 保留为统一端口映射变量；数据库和 Redis 不映射宿主端口。Compose 明确使用 development、`TLS_ENABLED=false`、隔离邮件捕获及回环端口，只用于本地开发。这个示例显式选择 HTTP，服务未设置开关时的默认行为见下表；模板不提供证书挂载，使用 true 时需另行装配证书。`down` 保留开发数据库 volume，清理数据需由使用者明确选择。

同一地址检查探针：`curl -f http://127.0.0.1:18080/healthz` 和 `curl -f http://127.0.0.1:18080/readyz`。启用服务 TLS 时，两者改用同址 HTTPS 并校验证书。探针访问范围见[网关接入手册](gateway-integration.md)。

## 身份策略与登录补全

服务读取 `ACCOUNTKIT_ENABLED_IDENTITY_KINDS` 和 `ACCOUNTKIT_REQUIRED_IDENTITY_KINDS`。省略 E 沿用原装配，省略 R 为 PHONE/EMAIL any-of；非空 R 只能包含 PHONE/EMAIL 且为 E 子集，`[]` 显式不限定类型。仅邮件可设 E=EMAIL、R=EMAIL；纯微信可设 E=WECHAT、R=[] 并配置微信应用，无需 SMTP。仅小程序部署可配置 `ACCOUNTKIT_WECHAT_MINI_PROGRAM_APPS=appid:secret`，OAuth 应用使用 `ACCOUNTKIT_WECHAT_APPS`，两组不能重复 AppID。全局 `ACCOUNTKIT_WECHAT_SUBJECT_MODE` 缺省为 `UNIONID`，显式只接受 `UNIONID` / `OPENID`，空值非法；管理员 audience 不能等于任一组的 AppID。客户端统一提交 `{"kind":"WECHAT","credential":{"app_id":"wx…","code":"…"}}`。小程序 code 来自小程序登录，OAuth code 来自对应授权流程；接口由服务端应用分组决定。subject 使用 `unionid:<unionid>` 或 `openid:<appid>:<openid>`；与历史裸 UnionID 不兼容，不自动转换旧数据或合并账号，也无旧数据迁移工具。模式应固定，切换模式或分组可能产生不同身份并使已有 pending 失效，所有副本须一致。配置的 OIDC 类型同理：在 `ACCOUNTKIT_OIDC_PROVIDERS` 列出类型名，并为每个类型设置 `ACCOUNTKIT_OIDC_<KIND>_ISSUER`、`_JWKS_URL`、`_CLIENT_IDS`（可选 `_NONCE_TTL`、`_NAME_CLAIMS`、`_PICTURE_CLAIMS`，后两者设为空表示不读取）；缺失或非法时启动失败并指出具体变量名，不输出配置值。显式 E 中未配置的非内置类型指向 `ACCOUNTKIT_ENABLED_IDENTITY_KINDS`。未设置任何 OIDC 变量时 migrate 不受影响；已列出类型但配置不完整或非法时，migrate 与 serve 同样失败并指出变量名。类型名本身由 OIDC 配置定义，因此引用未定义类型的静态策略在 migrate 中同样无效。未在 `ACCOUNTKIT_OIDC_PROVIDERS` 列出的类型，其 `ACCOUNTKIT_OIDC_<KIND>_*` 变量被忽略，列表拼写错误会使该提供方保持关闭。显式启用 PHONE 报错，因为服务没有 SMS 服务商。配置在重启后生效，所有同实例副本须一致。

消费者沿用 `/v1/users`，管理面沿用 `/admin/v1/users`；资源 name 使用 users 及其子资源。此前未发布的 accounts 路径返回 404，不注册别名或重定向。未知第三方身份不满足 R 时返回 pending，尚不建号。客户端调用 `/v1/users:sendSignInCompletionCode` 和 `/v1/users:completeSignIn`，只用 `sign_in_completion_token` 和 PHONE/EMAIL 轮次；验证后关联已有活动账号或注册，详见 [库协议](../README.md#身份策略与登录补全)及[协议升级说明](compatibility.md#身份策略与登录补全协议升级)。

普通第三方绑定从已登录账号设置发起。纯第三方近期认证要重新登录并替换 token pair，核对 user_id 后继续；普通 IdP 仍不能恢复冷静期账号，手机/邮箱和管理员恢复保留。migrate 只检查静态 E/R 与迁移依赖，不构造发送器、IdP 或认证 handler；serve 才校验显式启用类型的真实运行能力。

## 配置

库配置按 [README](../README.md) 的变量表使用 `ACCOUNTKIT_` 前缀。例如 `ACCOUNTKIT_AUTH_SCHEMA`、`ACCOUNTKIT_AUTH_KEY_PREFIX`、`ACCOUNTKIT_JWT_KEYS`、`ACCOUNTKIT_JWT_ISSUER`。当前版本的 `ACCOUNTKIT_AUTH_SCHEMA` 未设置或为空时默认 `account`，Compose 也显式使用 `account`；合法的显式 schema 仍按配置使用。已有宿主的环境变量契约不变。`ACCOUNTKIT_PROVIDER_PROFILE_BACKFILL`（默认 `true`）控制已有账号在第三方登录、绑定或补全关联时是否补填空的显示名与头像；非法值使 `serve` 与 `migrate` 启动失败并指出该变量名，不回显取值。

Facebook 登录使用 `ACCOUNTKIT_FACEBOOK_APP_ID`、`ACCOUNTKIT_FACEBOOK_APP_SECRET`，可选 `ACCOUNTKIT_FACEBOOK_GRAPH_API_VERSION`（默认 `v26.0`）、`ACCOUNTKIT_FACEBOOK_NONCE_TTL`（默认 `10m`），以及仅供测试或代理部署的 `ACCOUNTKIT_FACEBOOK_GRAPH_API_BASE_URL`、`ACCOUNTKIT_FACEBOOK_JWKS_URL`。App ID 为空表示未启用，非空时 App Secret 必填；App Secret 只通过环境或密钥注入，不写入镜像、Compose 文件或日志。accountsvc 不提供 Facebook 数据删除回调路由：部署方须在 Meta 后台配置数据删除说明页，或另行用嵌入式库的 `HandleFacebookDataDeletion` 实现回调，见 [Go 集成指南](go-integration.md#facebook-数据删除回调)。

支付宝 App 极简登录使用 `ACCOUNTKIT_ALIPAY_APP_IDS`（逗号分隔的唯一 16 位数字 AppID），每个应用分别提供 `ACCOUNTKIT_ALIPAY_<APPID>_PRIVATE_KEY` 应用私钥和 `ACCOUNTKIT_ALIPAY_<APPID>_PUBLIC_KEY` 支付宝公钥的真实多行 PEM。至少 2048 位 RSA，只支持普通公钥，不能传文件路径或字面 `\n`。`ACCOUNTKIT_ALIPAY_SUBJECT_MODE` 未设置时 UNIONID，显式仅接受 UNIONID/OPENID；`ACCOUNTKIT_ALIPAY_GATEWAY_URL` 默认生产网关。serve 缺应用或密钥会在监听前指出具体变量名，不输出内容。migrate 无需配置支付宝、不访问上游，但已配置的非法材料仍拒绝。

纯支付宝部署设 `ACCOUNTKIT_ENABLED_IDENTITY_KINDS=ALIPAY`、`ACCOUNTKIT_REQUIRED_IDENTITY_KINDS=[]`，无需 SMTP；默认 Required 仍需手机/邮箱补全。客户端完成极简 SDK 的 auth_user 与一次性 state 核对后提交 `POST /v1/users:signInWithIdp` 的 `{"kind":"ALIPAY","credential":{"app_id":"2026000000000001","code":"synthetic-code"}}`，本服务无 authInfo 或回调端点。默认 UNIONID 的实际跨 App 共享由宿主核对真实应用分组；OPENID 按 AppID 隔离，不自动迁移。配置指纹改变会拒绝旧 pending，所有副本须一致。资料降级及后续成功登录空字段补填、Flutter 桥接与宿主上线前真机清单见[支付宝接入指南](alipay-sign-in.md)。本地 Compose 默认 EMAIL，须用 override 或实际部署环境显式传入上述变量并覆盖 E/R，单改 `.env` 不会自动传递任意变量。

QQ App SDK 登录使用 `ACCOUNTKIT_QQ_APP_IDS`（逗号分隔的唯一 1–20 位数字 AppID）、`ACCOUNTKIT_QQ_SUBJECT_MODE`（默认 `UNIONID`）和测试用 `ACCOUNTKIT_QQ_API_BASE_URL`；不需要 AppKey。显式 Enabled 含 QQ 却未配置应用时启动失败并指出 `ACCOUNTKIT_QQ_APP_IDS`，非法值只报告变量名。

纯 QQ 部署设 `ACCOUNTKIT_ENABLED_IDENTITY_KINDS=QQ`、`ACCOUNTKIT_REQUIRED_IDENTITY_KINDS=[]`，无需 SMTP；默认 Required 仍要求手机号或邮箱补全。migrate 不需要 QQ 配置，但已提供的非法 QQ 配置会被拒绝。

微博 App SDK 登录使用 `ACCOUNTKIT_WEIBO_APP_IDS`（逗号分隔的唯一 1–20 位数字微博 AppKey）和测试用 `ACCOUNTKIT_WEIBO_API_BASE_URL`；不需要 AppSecret，也没有标识模式变量。显式 Enabled 含 WEIBO 却未配置应用时启动失败并指出 `ACCOUNTKIT_WEIBO_APP_IDS`，非法值只报告变量名。服务不提供授权回调或取消授权通知路由。

纯微博部署设 `ACCOUNTKIT_ENABLED_IDENTITY_KINDS=WEIBO`、`ACCOUNTKIT_REQUIRED_IDENTITY_KINDS=[]`，无需 SMTP；默认 Required 仍要求手机号或邮箱补全。migrate 不需要微博配置，但已提供的非法微博配置会被拒绝。

以下名称均加 `ACCOUNTSVC_` 前缀：

| 变量 | 默认/要求 |
|---|---|
| `MODE` | production；开发显式 development |
| `DATABASE_URL` | 必填 PostgreSQL DSN |
| `REDIS_URL` | 必填 Redis URI，支持认证、数据库编号及 rediss |
| `HTTP_ADDR` | 单一监听地址；127.0.0.1:8080 |
| `INTERNAL_ADDR` | 已移除；serve 读取到任意非空值即报错，必须删除旧注入 |
| `TLS_ENABLED` | 未设置/空沿用原模式默认；true 强制 HTTPS；false 显式 HTTP |
| `TLS_CERT_FILE` / `TLS_KEY_FILE` | 有效 TLS 模式启用时要求完整有效证书对；false 与任一证书项冲突 |
| `METRICS_ENABLED` | 未设置、空或 false 时关闭；true 启用 Prometheus；非法布尔在监听前拒绝 |
| `METRICS_INSTANCE` | 指标启用时必填的固定非秘密标签，匹配 `[A-Za-z0-9][A-Za-z0-9._-]{0,63}`；关闭时忽略 |
| `PROFILE_ATTRIBUTES_SCHEMA_FILE` | 可选本地普通文件，仅 serve 启动时读取；Schema 最大 64 KiB，空设置关闭资料能力 |
| `TRUSTED_PROXY_CIDRS` | 空；逗号分隔可信代理网段 |
| `STARTUP_TIMEOUT` / `SHUTDOWN_TIMEOUT` | 60s / 30s；关闭预算需给库收尾预留15秒 |
| `SMTP_HOST` / `SMTP_PORT` / `SMTP_FROM` | serve 在 EMAIL 启用时必填；一个发件地址 |
| `SMTP_TLS_MODE` | implicit；可选 starttls；none 仅限 development |
| `SMTP_USERNAME` / `SMTP_PASSWORD` | 成对配置，生产必填 |
| `SMTP_TIMEOUT` | 10s，包含连接、TLS、认证和提交 |
| `INTROSPECTION_CLIENTS` | serve 必填；JSON：客户端 ID → base64 秘密数组 |
| `ADMIN_ENABLED` | false |
| `ADMIN_ISSUER` / `ADMIN_AUDIENCE` | 启用时必填；issuer 是 HTTPS URL，audience 为管理员 API 专用标识 |
| `ADMIN_ROLES_CLAIM` / `ADMIN_USERNAME_CLAIM` | /roles / /preferred_username，JSON Pointer |

身份摘要、身份加密和服务调用秘密分别生成独立随机值，例如 `openssl rand -base64 32`，不复用。消费者 JWT 签名密钥是 ES256（P-256）私钥，`ACCOUNTKIT_JWT_KEYS` 的值为 PKCS#8 DER 的 base64：

```bash
openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 | openssl pkcs8 -topk8 -nocrypt -outform DER | base64 -w0
```

旧的 HS256 随机字节密钥会在启动时被拒绝并指出变量名。内省 JSON 示例中的值是占位符，须替换为至少32随机字节的 base64 编码：

```json
{"business-service":["<base64-secret>"]}
```

调用内省时 HTTP Basic 的 password 使用同一 base64 文本。轮换时先同时配置旧、新秘密并重启，再更新调用方，最后移除旧秘密重启。签名私钥只留在 accountsvc，业务服务只通过 JWKS 取得公钥。

`TLS_ENABLED` 的输入与有效协议如下，非法布尔值拒绝启动，不自动降级：

| 输入 | mode | HTTP 证书项 | 有效行为 |
|---|---|---|---|
| 未设置或空 | production | 任意 | 要求完整有效证书对，使用 HTTPS |
| 未设置或空 | development | 均为空 | HTTP |
| 未设置或空 | development | 任一非空 | 要求完整有效证书对，使用 HTTPS |
| true | 任一合法模式 | 任意 | 要求完整有效证书对，使用 HTTPS |
| false | 任一合法模式 | 均为空 | HTTP；日志明确本进程未启用 HTTP TLS |
| false | 任一合法模式 | 任一非空 | 配置冲突，拒绝启动 |

启用服务 TLS 时最低 TLS1.2。显式 false 可供受控外部 TLS 终止使用，但开关本身不证明网关或网格提供了加密。启用 EMAIL 时生产 SMTP 仍要求认证与经过证书验证的 TLS，外部 OIDC 仍使用 HTTPS；PostgreSQL/Redis 的 TLS 分别由 DSN/URI 控制。`migrate` 不读取监听地址、TLS 开关/证书、SMTP、内省或管理员设置，因此这些服务设置即使无效也不影响它。传输部署方式、可信代理与验收职责统一见[网关接入手册](gateway-integration.md)。

## 身份密钥与验证码轮换

服务使用库的 [密钥轮换规则](../README.md#密钥轮换)：保持旧 active 时先升级全部进程，再向所有进程分发相同完整的 `ACCOUNTKIT_SUBJECT_HMAC_KEYS`，确认验证码策略一致后，滚动修改 `ACCOUNTKIT_SUBJECT_HMAC_ACTIVE_KEY` 并重启。配置在进程启动时读取，不新增轮换命令、端点或热加载；实例 schema、Redis 前缀和既有策略保持一致。

满足此前提时，不同 active 进程和重启后的服务可消费旧码，共用目标冷却及累计 UTC 日额度；IP 额度原本独立于 HMAC。旧码不会延长寿命或重置错误次数，历史多份冲突码返回原 `CODE_EXPIRED` 并作废，重新发码仍受原冷却和额度约束。

服务继续周期性回填数据库，默认每5分钟运行；混合 active 期间回填方向仍可能变化。最后一个旧 active 进程及其在途请求、维护结束后记录 T0，同时满足 README 中未删除身份旧摘要引用归零及 Redis 业务窗口条件，才移除旧 HMAC 并重启。AES 单独按旧密文引用退役，不要求与 HMAC 版本编号一致；启动期密钥检查不代替 Redis 退役检查，旧备份恢复所需历史密钥须受控保留。本地实际服务进程与 SMTP fixture 验证见[轮换验收记录](../openspec/changes/archive/2026-10-04-harden-code-key-rotation/verification.md)。

## HTTP 面

| 监听地址 | 路径 | 身份 |
|---|---|---|
| HTTP_ADDR | /v1 | 原消费者登录、身份、会话与生命周期接口 |
| HTTP_ADDR | /admin/v1 | 独立外部管理员 JWT，关闭时503 |
| HTTP_ADDR | POST /v1/introspect | 独立服务调用 Basic 凭据 |
| HTTP_ADDR | GET /.well-known/jwks.json | 无认证；只含消费者 access 验签公钥，可公共缓存 5 分钟 |
| HTTP_ADDR | GET /healthz、GET /readyz | 无认证探针；访问范围由部署策略限制 |
| HTTP_ADDR | GET /metrics | 指标开关启用时提供，无内建鉴权；仅允许监控来源 |

服务只绑定一个监听器，路径不构成网络隔离。公网明确允许消费者路由、分别设置管理员策略并阻断内省、探针和指标；不得直接公开全部 `/v1/*`，后端端口也须限制直连来源。具体接入规则见[网关接入手册](gateway-integration.md)。旧 `/internal/v1/introspect` 返回404，无别名或重定向；旧配置、端口、调用方与整体回退见[兼容迁移说明](compatibility.md#服务单监听与传输迁移)。服务无管理前端，不提供 Cookie 登录或默认宽松 CORS。

healthz 只表示进程存活。readyz 要求启动完成、未停机且 PostgreSQL/Redis 在两秒内可用；SMTP 或运行期 OIDC 故障不影响整个服务就绪，但对应功能会失败。请求/响应携带 X-Request-Id 用于关联；不要记录 Authorization 或请求体中的凭据。

## Prometheus 指标

默认关闭。先按[网关接入手册](gateway-integration.md#指标访问)配置网关允许清单与后端端口来源限制，再设置 `ACCOUNTSVC_METRICS_ENABLED=true` 和 `ACCOUNTSVC_METRICS_INSTANCE=consumer-auth`，重启后抓取同址 `GET /metrics`。Compose 透传这两个变量，默认仍关闭，不另开端口或部署 Prometheus。启用服务 TLS 时采集使用同址 HTTPS 并校验证书。

实例值是部署时确定的非秘密标签，不从 schema、Redis 前缀、连接串或请求推导；配置错误只指出字段名，不回显输入。不同逻辑账号实例使用不同标签；同一逻辑实例的多副本可以共用该值，Prometheus 仍须按抓取目标标签（例如 `instance`）区分进程。

关闭时 `/metrics` 返回404，启用时只接受 GET，其他方法返回405。入口不要求消费者或管理员凭据，不能直接暴露到公网。响应带 `Cache-Control: no-store`；采集错误返回不含内部详情的503，最多允许5个并发采集，超限返回503，并沿用服务 HTTP 超时。

每个应用拥有独立 Registry，复用[既有指标与默认耗时桶](metrics.md#名称单位和默认桶)，不注册 Go/process 默认 collectors 或 HTTP 抓取指标。采集只读本地聚合值，不访问 PostgreSQL、Redis、SMTP 或 IdP；readiness 失败不阻断已运行服务的采集。尚无观测时可以没有序列或输出为空，不能据此判定采集失败或全部为零。

进程重启后累计值重置；指标不是业务账本。停机仍先排空 HTTP，再关闭 kit 和连接，不增加后台导出任务或最终刷新等待，关闭尾部增量不保证被最后一次抓取。回退可关闭开关并重启、移除抓取目标，无需数据库迁移。库宿主的自主管理与采集语义见[指标指南](metrics.md)。

配置、真实服务采集和网关夹具验证见[指标装配验收记录](../openspec/changes/archive/2026-10-19-add-accountsvc-prometheus/verification.md)；实际 Prometheus 与产品网络策略仍需部署验收。

## 更换邮箱或手机号

服务复用 [库的换绑端点](../README.md#手机号与邮箱换绑)：先完成必要的重新认证，向新地址请求 BIND 码，再调用 `POST /v1/users/me/identities/{identity}:replace`。保留当前会话，撤销其他会话；旧身份 ID 失效，客户端应保存响应中的新资源名。服务默认只启用邮件投递，短信禁用策略不变。

数据库明确回滚时旧绑定保持，但已经消费的验证码不能恢复；结果未知时先查询身份列表确认。旧邮箱之后的新登录可能建立另一个账号，不会获得原账号的数据。业务服务继续执行自身 scope/资源归属校验，access 吊销的 Redis fail-open 限制不变。实现的本地检查见[换绑验收记录](../openspec/changes/archive/2026-10-03-add-identity-replacement/verification.md)。

## 会话到期与重新登录

会话列表和管理员活跃计数不展示已到刷新期限的会话。到期refresh返回400 `invalid_grant`；到期重新认证返回401 `TOKEN_INVALID`并带Bearer challenge，应重新登录。有效重新认证不会延长refresh期限，显式关闭新鲜度检查也不会让到期会话恢复可用。

原access可能仍在自身有效期内：本地验签和内省都不按刷新期限拒绝，自然到期也不会写入吊销集。需要使这些access失效时，仍可按原接口明确撤销未清理的会话；撤销提交后内省立即返回 `active:false`，本地验签仍要等 access 过期。期限判断与码消费细节见 [README](../README.md#会话过期语义)。

## 业务服务远程鉴权

### 分级校验

业务服务按接口对撤销实时性的要求分两级接入：

| 级别 | 做法 | 撤销生效 | 依赖 |
|---|---|---|---|
| 普通接口 | 用 JWKS 本地验签 | 最长 `ACCESS_TOKEN_TTL` 加验签偏差（默认约 15 分 30 秒） | 仅缓存的 JWKS |
| 关键接口 | 先本地验签，再每次调用内省，不缓存结果 | 撤销事务提交后立即生效 | accountsvc 与其数据库 |

哪些接口属于关键接口由业务服务认定并写入其接入说明，例如支付、资金、权限或安全设置变更。两级都由业务服务继续校验 scope、必要的近期认证（`auth_time`）及资源所属；`user:bind`、`user:undelete` 不能访问普通业务。

本地验签必须全部满足：只接受 `alg=ES256` 与 `typ=at+jwt`；按 header `kid` 从 JWKS 选公钥；`iss`、`aud` 与服务配置的 `ACCOUNTKIT_JWT_ISSUER`、`ACCOUNTKIT_JWT_AUDIENCE` 完全相等；`exp` 必须存在，时钟偏差不超过 30 秒；`sub`、`sid` 分别为 `u_`、`s_` 资源 ID。遇到未知 `kid` 时节流刷新 JWKS（例如至多每分钟一次），刷新失败按依赖不可用拒绝，不无限信任旧缓存。参考实现见 [examples/remoteauth](../examples/remoteauth) 的 `Verifier` 与 `ProtectedHandler`（`Critical` 字段区分两级）。

### 签名密钥轮换

1. 新增版本（如 `1:<旧>,2:<新>`，active 仍为 1）并重启全部副本，确认 JWKS 已包含新版本；
2. 等待不短于 JWKS 公共缓存期限（5 分钟）与业务服务自身缓存期限的时间；
3. 把 `ACCOUNTKIT_JWT_ACTIVE_KEY` 切到新版本并重启全部副本；
4. 至少再等待 `ACCESS_TOKEN_TTL` 加验签偏差；
5. 移除旧版本并重启。

同一实例的全部副本须使用一致的密钥集合与 active 版本。过早切换时业务服务会因未知 `kid` 额外刷新一次 JWKS，不会永久拒绝新令牌。

### 内省

向统一地址的 `POST /v1/introspect` 发送表单 `token=<消费者access_token>`，HTTP Basic 使用独立客户端凭据。生产调用链必须提供经过证书校验的 TLS 保护，外部 TLS 终止的边界见[网关接入手册](gateway-integration.md)。成功时返回最小主体上下文：

```json
{"active":true,"sub":"u_...","scope":"user","sid":"s_...","auth_time":1791000000}
```

无效、过期、已吊销、会话不存在、其他实例或 refresh token 返回200 `{"active":false}`。无效调用凭据是401；请求格式错误是400；请求体超限是413；错误内容类型是415。内省响应禁止缓存；接入示例见 [examples/remoteauth](../examples/remoteauth)。

内省在签名、claims 与 ID 格式校验后按 `sid` 查询数据库会话：会话存在、属于 `sub` 且未吊销才返回 `active:true`。它不读取 Redis 吊销集，因此吊销集写入或读取失败都不影响结果；刷新期限已到但未吊销的会话仍为 active。**数据库不可用时返回503 `temporarily_unavailable`（fail-closed）**，不会放行。内省不实时重算账号状态或 scope；冻结、注销、换绑等生命周期操作在同一事务内吊销会话，提交后立即反映。

示例客户端默认要求 HTTPS 并校验证书。只有调用方明确设置 `AllowHTTP=true` 才接受 HTTP URL，适用于本地开发或部署方保障传输的受控代理/网格应用段；客户端不根据服务开关、转发头或重定向自动降级。`TLS_ENABLED=false` 不取消 Basic 认证，也不自动设置调用方的 `AllowHTTP`。

`active:true` 只表示认证通过，不授予全部业务权限。查询超时、网络错误、非200（含503）或畸形响应应拒绝业务请求并报告依赖不可用，不用旧结果或本地验签结果放行，也不要把依赖故障当作用户凭据过期。

## 管理员认证

默认关闭，整个 `/admin/v1` 返回503 `ADMIN_NOT_CONFIGURED`。启用后通过 HTTPS discovery/JWKS 验证外部提供方签发的 RS256 JWT access token。管理员 API audience 应独立于消费者 audience 和管理员登录客户端 ID；不接受登录 ID token 作为 API 凭据。

角色默认读取 `/roles` 字符串数组；例如嵌套角色可配置 `/realm_access/roles`。super-admin 在服务适配层包含 operator，无需依赖提供方的复合角色配置。缺角色返回403并记录拒绝审计；无效凭据返回401；没有可用验证 key 且提供方故障返回503。

JWKS 最多缓存1小时；未知 kid 刷新合并并按60秒节流，无法取得新 key 时不会无限信任旧缓存。启用管理面但首次 discovery/JWKS 失败会阻止启动。首版不支持 opaque token、其他签名算法或管理员浏览器登录流程。

## 邮件、匿名化与停机

EMAIL 启用时，服务显式调用 `smtp.NewWithRenderer(cfg.SMTP, smtp.LegacyChineseRenderer{})`，保留原四用途中文纯文本与有效期分钟数向上取整行为。这里不增加语言协商、模板管理或环境变量；EMAIL 关闭时仍不要求 SMTP，`migrate` 不装配发送器。嵌入式宿主需要本地化时，可参考[双语渲染实现](../examples/codeemail/renderer.go)和[装配示例](../examples/codeemail/example_test.go)，通过自己的 context 约定选择语言；并发、取消、HTML 转义和安全日志责任见 [README 发送器契约](../README.md#发送器)。新旧名称与回退说明见[兼容指南](compatibility.md#验证码发送器命名与渲染接入)。

SMTP 认证使用 AUTH PLAIN，并在生产模式要求证书验证和加密；首版不支持 OAuth SMTP 凭据。提交被接受不代表邮件最终进入收件箱。发送器不自动重发；超时/拒绝返回503且保留已经占用的额度和冷却，避免故障时产生发送风暴。生产日志不包含验证码或完整邮箱。

accountsvc 只匿名化自己拥有的认证数据，不知道使用方的业务表。额外 user_id 关联数据需选择嵌入式同库回调，或独立制定业务清理方案。切换部署前，不得让 accountsvc 与依赖业务匿名化回调的宿主同时运行同一实例的维护任务，否则服务可能先取得锁并跳过业务清理责任。

SIGINT/SIGTERM 触发撤销就绪、统一监听器 HTTP 排空、维护停止/审计刷新、连接关闭。进程管理器的终止宽限应大于 SHUTDOWN_TIMEOUT；超时非零退出，不能据此声称全部审计已落库。回退须成套恢复二进制、监听/证书配置、调用方和网关策略，步骤见[兼容迁移说明](compatibility.md#服务单监听与传输迁移)；不调用数据库 Down 或 UnsafeReset。

## 本人资料属性

serve 可设置 `ACCOUNTSVC_PROFILE_ATTRIBUTES_SCHEMA_FILE=/etc/accountsvc/profile-schema.json`。文件必须是本地普通文件，最大 64 KiB，使用[资料 Schema 规则](../README.md#可选用户资料属性go-与本人-http-接口)。启动时读取、离线校验后再创建运行依赖和监听；无效、空、不可读或超限文件会阻止启动，错误不输出路径或内容。省略或空设置保持关闭；migrate 忽略此项，库 ConfigFromEnv/New 仍不进行文件 I/O。

启用后消费者通过 GET/PATCH `/v1/users/me/profile` 读写本人全部资料；必须持有 `user` scope 的 Bearer，无需近期重新认证。未启用时已认证的合法请求返回 `503 PROFILE_ATTRIBUTES_NOT_CONFIGURED`。GET 与 PATCH 示例及错误见 [HTTP 契约](openapi.md#本人资料属性)。

使用 Compose 时在单独的 override 中显式添加环境变量和只读文件挂载（基础模板不默认启用）：

```yaml
services:
  accountsvc:
    environment:
      ACCOUNTSVC_PROFILE_ATTRIBUTES_SCHEMA_FILE: /etc/accountsvc/profile-schema.json
    volumes:
      - ./profile-schema.json:/etc/accountsvc/profile-schema.json:ro
```

同一账号实例各副本必须使用一致 Schema；文件变更在重启后生效，不热更新、不自动修改旧资料。全部属性均对本人开放，不能依赖 readOnly/writeOnly 隐藏内部数据；收紧或删除声明前按[兼容说明](compatibility.md#可选用户资料属性的-go-与-http-接口)处理历史资料。

## 验证边界

现有库完整门禁仍使用 `scripts/verify.sh`；服务门禁使用 `scripts/verify-accountsvc.sh`，要求一次性 PostgreSQL 和 Redis，内部启动隔离 SMTP/HTTPS OIDC fixtures，缺依赖时失败。首次服务交付的历史证据见[初始验收记录](../openspec/changes/archive/2026-10-03-add-accountsvc/verification.md)，本次单监听器、TLS 模式及本地代理结果见[传输变更验收记录](../openspec/changes/archive/2026-10-06-simplify-accountsvc-transport/verification.md)。外部 SMTP 实际送达、实际 OIDC 提供方、真实网关/网格和产品环境均属于待执行的部署验收，本地 fixture 通过不代替这些联调。
