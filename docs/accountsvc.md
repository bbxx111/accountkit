# accountsvc 服务

accountsvc 是 accountkit 的可选官方宿主。同一个 Go module 同时支持直接嵌入库和运行服务；使用库的项目不需要部署或调用 accountsvc。

## 两种集成方式

| 方式 | 使用方负责 | 适用边界 |
|---|---|---|
| 嵌入 accountkit | 连接、发送器、HTTP 挂载、管理员验证和业务匿名化回调 | 需要同库业务事务或定制装配 |
| 运行 accountsvc | 运行配置、TLS、SMTP、外部管理员提供方、业务端授权 | 通过消费者 API 和内部令牌内省接入 |

服务首版提供 SMTP 邮件验证码，短信显式禁用；禁用渠道请求返回 400 `CHANNEL_NOT_ENABLED`，不生成验证码、不扣发送额度。库宿主原有短信/邮件发送器继续可用。

## 构建与运行

在仓库根目录运行：

```bash
GOWORK=off go build -o bin/accountsvc ./cmd/accountsvc
./bin/accountsvc migrate
./bin/accountsvc serve
```

PowerShell 使用 `$env:GOWORK = 'off'`，构建目标可使用 `bin/accountsvc.exe`。无参数等同 `serve`。命令不自动读取 `.env` 文件，应由 shell、容器或进程管理器注入环境变量。

`migrate` 仅要求库配置、数据库/Redis URL 和启动超时，不要求 SMTP、TLS 监听配置、内省客户端或管理员参数；它不监听、不运行维护任务。`serve` 仍执行幂等迁移、依赖连通性和身份密钥版本检查。迁移 dirty 或密钥缺失会阻止启动，不自动 Force 或清库。恢复流程见 [迁移手册](migrations.md)。

开发环境可使用 [Compose](../deploy/accountsvc/compose.yaml)：

```bash
cp deploy/accountsvc/.env.example deploy/accountsvc/.env
# 填入各个独立随机测试密钥后运行
docker compose --env-file deploy/accountsvc/.env -f deploy/accountsvc/compose.yaml up --build -d
docker compose --env-file deploy/accountsvc/.env -f deploy/accountsvc/compose.yaml down
```

默认统一服务地址为 `http://127.0.0.1:18080`，邮件捕获界面为 `http://127.0.0.1:18025`。`ACCOUNTSVC_PUBLIC_PORT` 保留为统一端口映射变量；数据库和 Redis 不映射宿主端口。Compose 明确使用 development、`TLS_ENABLED=false`、隔离邮件捕获及回环端口，只用于本地开发。这个示例显式选择 HTTP，服务未设置开关时的默认行为见下表；模板不提供证书挂载，使用 true 时需另行装配证书。`down` 保留开发数据库 volume，清理数据需由使用者明确选择。

同一地址检查探针：`curl -f http://127.0.0.1:18080/healthz` 和 `curl -f http://127.0.0.1:18080/readyz`。启用服务 TLS 时，两者改用同址 HTTPS 并校验证书。探针访问范围见[网关接入手册](gateway-integration.md)。

## 配置

库配置按 [README](../README.md) 的变量表使用 `ACCOUNTKIT_` 前缀。例如 `ACCOUNTKIT_AUTH_SCHEMA`、`ACCOUNTKIT_AUTH_KEY_PREFIX`、`ACCOUNTKIT_JWT_KEYS`、`ACCOUNTKIT_JWT_ISSUER`。已有宿主的环境变量契约不变。

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
| `TRUSTED_PROXY_CIDRS` | 空；逗号分隔可信代理网段 |
| `STARTUP_TIMEOUT` / `SHUTDOWN_TIMEOUT` | 60s / 30s；关闭预算需给库收尾预留15秒 |
| `SMTP_HOST` / `SMTP_PORT` / `SMTP_FROM` | serve 必填；一个发件地址 |
| `SMTP_TLS_MODE` | implicit；可选 starttls；none 仅限 development |
| `SMTP_USERNAME` / `SMTP_PASSWORD` | 成对配置，生产必填 |
| `SMTP_TIMEOUT` | 10s，包含连接、TLS、认证和提交 |
| `INTROSPECTION_CLIENTS` | serve 必填；JSON：客户端 ID → base64 秘密数组 |
| `ADMIN_ENABLED` | false |
| `ADMIN_ISSUER` / `ADMIN_AUDIENCE` | 启用时必填；issuer 是 HTTPS URL，audience 为管理员 API 专用标识 |
| `ADMIN_ROLES_CLAIM` / `ADMIN_USERNAME_CLAIM` | /roles / /preferred_username，JSON Pointer |

生成独立随机秘密，例如 `openssl rand -base64 32`。JWT、身份摘要、身份加密和服务调用秘密分别生成；不复用。内省 JSON 示例中的值是占位符，须替换为至少32随机字节的 base64 编码：

```json
{"business-service":["<base64-secret>"]}
```

调用内省时 HTTP Basic 的 password 使用同一 base64 文本。轮换时先同时配置旧、新秘密并重启，再更新调用方，最后移除旧秘密重启。服务不发放消费者签名密钥给业务服务。

`TLS_ENABLED` 的输入与有效协议如下，非法布尔值拒绝启动，不自动降级：

| 输入 | mode | HTTP 证书项 | 有效行为 |
|---|---|---|---|
| 未设置或空 | production | 任意 | 要求完整有效证书对，使用 HTTPS |
| 未设置或空 | development | 均为空 | HTTP |
| 未设置或空 | development | 任一非空 | 要求完整有效证书对，使用 HTTPS |
| true | 任一合法模式 | 任意 | 要求完整有效证书对，使用 HTTPS |
| false | 任一合法模式 | 均为空 | HTTP；日志明确本进程未启用 HTTP TLS |
| false | 任一合法模式 | 任一非空 | 配置冲突，拒绝启动 |

启用服务 TLS 时最低 TLS1.2。显式 false 可供受控外部 TLS 终止使用，但开关本身不证明网关或网格提供了加密。生产 SMTP 仍要求认证与经过证书验证的 TLS，外部 OIDC 仍使用 HTTPS；PostgreSQL/Redis 的 TLS 分别由 DSN/URI 控制。`migrate` 不读取监听地址、TLS 开关/证书、SMTP、内省或管理员设置，因此这些服务设置即使无效也不影响它。传输部署方式、可信代理与验收职责统一见[网关接入手册](gateway-integration.md)。

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
| HTTP_ADDR | GET /healthz、GET /readyz | 无认证探针；访问范围由部署策略限制 |

服务只绑定一个监听器，路径不构成网络隔离。公网明确允许消费者路由、分别设置管理员策略并阻断内省和探针；不得直接公开全部 `/v1/*`，后端端口也须限制直连来源。具体接入规则见[网关接入手册](gateway-integration.md)。旧 `/internal/v1/introspect` 返回404，无别名或重定向；旧配置、端口、调用方与整体回退见[兼容迁移说明](compatibility.md#服务单监听与传输迁移)。服务无管理前端，不提供 Cookie 登录或默认宽松 CORS。

healthz 只表示进程存活。readyz 要求启动完成、未停机且 PostgreSQL/Redis 在两秒内可用；SMTP 或运行期 OIDC 故障不影响整个服务就绪，但对应功能会失败。请求/响应携带 X-Request-Id 用于关联；不要记录 Authorization 或请求体中的凭据。

## 更换邮箱或手机号

服务复用 [库的换绑端点](../README.md#手机号与邮箱换绑)：先完成必要的重新认证，向新地址请求 BIND 码，再调用 `POST /v1/users/me/identities/{identity}:replace`。保留当前会话，撤销其他会话；旧身份 ID 失效，客户端应保存响应中的新资源名。服务默认只启用邮件投递，短信禁用策略不变。

数据库明确回滚时旧绑定保持，但已经消费的验证码不能恢复；结果未知时先查询身份列表确认。旧邮箱之后的新登录可能建立另一个账号，不会获得原账号的数据。业务服务继续执行自身 scope/资源归属校验，access 吊销的 Redis fail-open 限制不变。实现的本地检查见[换绑验收记录](../openspec/changes/archive/2026-10-03-add-identity-replacement/verification.md)。

## 会话到期与重新登录

会话列表和管理员活跃计数不展示已到刷新期限的会话。到期refresh返回400 `invalid_grant`；到期重新认证返回401 `TOKEN_INVALID`并带Bearer challenge，应重新登录。有效重新认证不会延长refresh期限，显式关闭新鲜度检查也不会让到期会话恢复可用。

原access可能仍在自身有效期内，内省继续按既有JWT/Redis规则判断；自然到期不会自动写入吊销集。需要使这些access失效时，仍可按原接口明确撤销未清理的会话，Redis fail-open边界不变。期限判断与码消费细节见 [README](../README.md#会话过期语义)。

## 业务服务远程鉴权

向统一地址的 `POST /v1/introspect` 发送表单 `token=<消费者access_token>`，HTTP Basic 使用独立客户端凭据。生产调用链必须提供经过证书校验的 TLS 保护，外部 TLS 终止的边界见[网关接入手册](gateway-integration.md)。成功时返回最小主体上下文：

```json
{"active":true,"sub":"u_...","scope":"user","sid":"s_...","auth_time":1791000000}
```

无效、过期、确认吊销、其他实例或 refresh token 返回200 `{"active":false}`。无效调用凭据是401；请求格式错误是400；请求体超限是413；错误内容类型是415。内省响应禁止缓存；接入示例见 [examples/remoteauth](../examples/remoteauth)。

示例客户端默认要求 HTTPS 并校验证书。只有调用方明确设置 `AllowHTTP=true` 才接受 HTTP URL，适用于本地开发或部署方保障传输的受控代理/网格应用段；客户端不根据服务开关、转发头或重定向自动降级。`TLS_ENABLED=false` 不取消 Basic 认证，也不自动设置调用方的 `AllowHTTP`。

每次受保护业务请求查询内省，然后由业务服务校验 scope、必要的近期认证及资源所属。`active:true` 只表示认证通过，不授予全部业务权限；`user:bind`、`user:undelete` 不能访问普通业务。

查询超时、网络错误、非200或畸形响应应拒绝业务请求并报告依赖不可用，不用旧结果放行，也不要把依赖故障当作用户凭据过期。

内省复用当前库认证语义：签名/claims、ID格式及 Redis 吊销检查，不实时查询数据库重新计算账号状态或 scope。**Redis 吊销查询故障仍 fail-open 并记录警告**；因此即使 readyz 为503，已经到达的有效令牌内省仍可能返回 active:true。需要更强吊销一致性的接入方不能把本服务当作故障时立即拒绝所有旧凭据的保证。

## 管理员认证

默认关闭，整个 `/admin/v1` 返回503 `ADMIN_NOT_CONFIGURED`。启用后通过 HTTPS discovery/JWKS 验证外部提供方签发的 RS256 JWT access token。管理员 API audience 应独立于消费者 audience 和管理员登录客户端 ID；不接受登录 ID token 作为 API 凭据。

角色默认读取 `/roles` 字符串数组；例如嵌套角色可配置 `/realm_access/roles`。super-admin 在服务适配层包含 operator，无需依赖提供方的复合角色配置。缺角色返回403并记录拒绝审计；无效凭据返回401；没有可用验证 key 且提供方故障返回503。

JWKS 最多缓存1小时；未知 kid 刷新合并并按60秒节流，无法取得新 key 时不会无限信任旧缓存。启用管理面但首次 discovery/JWKS 失败会阻止启动。首版不支持 opaque token、其他签名算法或管理员浏览器登录流程。

## 邮件、匿名化与停机

SMTP 认证使用 AUTH PLAIN，并在生产模式要求证书验证和加密；首版不支持 OAuth SMTP 凭据。提交被接受不代表邮件最终进入收件箱。发送器不自动重发；超时/拒绝返回503且保留已经占用的额度和冷却，避免故障时产生发送风暴。生产日志不包含验证码或完整邮箱。

accountsvc 只匿名化自己拥有的认证数据，不知道使用方的业务表。额外 user_id 关联数据需选择嵌入式同库回调，或独立制定业务清理方案。切换部署前，不得让 accountsvc 与依赖业务匿名化回调的宿主同时运行同一实例的维护任务，否则服务可能先取得锁并跳过业务清理责任。

SIGINT/SIGTERM 触发撤销就绪、统一监听器 HTTP 排空、维护停止/审计刷新、连接关闭。进程管理器的终止宽限应大于 SHUTDOWN_TIMEOUT；超时非零退出，不能据此声称全部审计已落库。回退须成套恢复二进制、监听/证书配置、调用方和网关策略，步骤见[兼容迁移说明](compatibility.md#服务单监听与传输迁移)；不调用数据库 Down 或 UnsafeReset。

## 验证边界

现有库完整门禁仍使用 `scripts/verify.sh`；服务门禁使用 `scripts/verify-accountsvc.sh`，要求一次性 PostgreSQL 和 Redis，内部启动隔离 SMTP/HTTPS OIDC fixtures，缺依赖时失败。首次服务交付的历史证据见[初始验收记录](../openspec/changes/archive/2026-10-03-add-accountsvc/verification.md)，本次单监听器、TLS 模式及本地代理结果见[传输变更验收记录](../openspec/changes/archive/2026-10-06-simplify-accountsvc-transport/verification.md)。外部 SMTP 实际送达、实际 OIDC 提供方、真实网关/网格和产品环境均属于待执行的部署验收，本地 fixture 通过不代替这些联调。
