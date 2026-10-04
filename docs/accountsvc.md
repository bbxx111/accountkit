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

默认服务地址为 `http://127.0.0.1:18080`，内部面为 `http://127.0.0.1:18081`，邮件捕获界面为 `http://127.0.0.1:18025`。数据库和 Redis 不映射宿主端口。Compose 明确使用 development、隔离邮件捕获及回环端口，只用于本地开发；`down` 保留开发数据库 volume，清理数据需由使用者明确选择。

## 配置

库配置按 [README](../README.md) 的变量表使用 `ACCOUNTKIT_` 前缀。例如 `ACCOUNTKIT_AUTH_SCHEMA`、`ACCOUNTKIT_AUTH_KEY_PREFIX`、`ACCOUNTKIT_JWT_KEYS`、`ACCOUNTKIT_JWT_ISSUER`。已有宿主的环境变量契约不变。

以下名称均加 `ACCOUNTSVC_` 前缀：

| 变量 | 默认/要求 |
|---|---|
| `MODE` | production；开发显式 development |
| `DATABASE_URL` | 必填 PostgreSQL DSN |
| `REDIS_URL` | 必填 Redis URI，支持认证、数据库编号及 rediss |
| `HTTP_ADDR` / `INTERNAL_ADDR` | 127.0.0.1:8080 / 127.0.0.1:8081 |
| `TLS_CERT_FILE` / `TLS_KEY_FILE` | 生产必填；两个监听器共用证书 |
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

生产要求服务端 HTTPS 和经过证书验证的 SMTP TLS；反向代理到服务也使用 TLS。不要以 development 绕过生产加密要求。启用代理信任前核对实际网络路径，服务默认忽略不可信直接对端的转发头。

## 身份密钥与验证码轮换

服务使用库的 [密钥轮换规则](../README.md#密钥轮换)：保持旧 active 时先升级全部进程，再向所有进程分发相同完整的 `ACCOUNTKIT_SUBJECT_HMAC_KEYS`，确认验证码策略一致后，滚动修改 `ACCOUNTKIT_SUBJECT_HMAC_ACTIVE_KEY` 并重启。配置在进程启动时读取，不新增轮换命令、端点或热加载；实例 schema、Redis 前缀和既有策略保持一致。

满足此前提时，不同 active 进程和重启后的服务可消费旧码，共用目标冷却及累计 UTC 日额度；IP 额度原本独立于 HMAC。旧码不会延长寿命或重置错误次数，历史多份冲突码返回原 `CODE_EXPIRED` 并作废，重新发码仍受原冷却和额度约束。

服务继续周期性回填数据库，默认每5分钟运行；混合 active 期间回填方向仍可能变化。最后一个旧 active 进程及其在途请求、维护结束后记录 T0，同时满足 README 中未删除身份旧摘要引用归零及 Redis 业务窗口条件，才移除旧 HMAC 并重启。AES 单独按旧密文引用退役，不要求与 HMAC 版本编号一致；启动期密钥检查不代替 Redis 退役检查，旧备份恢复所需历史密钥须受控保留。本地实际服务进程与 SMTP fixture 验证见[轮换验收记录](../openspec/changes/archive/2026-10-04-harden-code-key-rotation/verification.md)。

## HTTP 面

| 监听器 | 路径 | 身份 |
|---|---|---|
| 公开 | /v1 | 原消费者登录、身份、会话与生命周期接口 |
| 公开 | /admin/v1 | 独立外部管理员 JWT，关闭时503 |
| 内部 | POST /internal/v1/introspect | 独立服务调用 Basic 凭据 |
| 内部 | GET /healthz、GET /readyz | 探针；通过网络策略限制内部端口 |

公开监听器不提供内部路径。生产只向所需网络开放端口，不将内部监听器直接映射为公共入口。服务无管理前端，不提供 Cookie 登录或默认宽松 CORS。

healthz 只表示进程存活。readyz 要求启动完成、未停机且 PostgreSQL/Redis 在两秒内可用；SMTP 或运行期 OIDC 故障不影响整个服务就绪，但对应功能会失败。请求/响应携带 X-Request-Id 用于关联；不要记录 Authorization 或请求体中的凭据。

## 更换邮箱或手机号

服务复用 [库的换绑端点](../README.md#手机号与邮箱换绑)：先完成必要的重新认证，向新地址请求 BIND 码，再调用 `POST /v1/users/me/identities/{identity}:replace`。保留当前会话，撤销其他会话；旧身份 ID 失效，客户端应保存响应中的新资源名。服务默认只启用邮件投递，短信禁用策略不变。

数据库明确回滚时旧绑定保持，但已经消费的验证码不能恢复；结果未知时先查询身份列表确认。旧邮箱之后的新登录可能建立另一个账号，不会获得原账号的数据。业务服务继续执行自身 scope/资源归属校验，access 吊销的 Redis fail-open 限制不变。实现的本地检查见[换绑验收记录](../openspec/changes/archive/2026-10-03-add-identity-replacement/verification.md)。

## 会话到期与重新登录

会话列表和管理员活跃计数不展示已到刷新期限的会话。到期refresh返回400 `invalid_grant`；到期重新认证返回401 `TOKEN_INVALID`并带Bearer challenge，应重新登录。有效重新认证不会延长refresh期限，显式关闭新鲜度检查也不会让到期会话恢复可用。

原access可能仍在自身有效期内，内省继续按既有JWT/Redis规则判断；自然到期不会自动写入吊销集。需要使这些access失效时，仍可按原接口明确撤销未清理的会话，Redis fail-open边界不变。期限判断与码消费细节见 [README](../README.md#会话过期语义)。

## 业务服务远程鉴权

以 HTTPS 向内省端点发送表单 `token=<消费者access_token>`，HTTP Basic 使用独立客户端凭据。成功时返回最小主体上下文：

```json
{"active":true,"sub":"u_...","scope":"user","sid":"s_...","auth_time":1791000000}
```

无效、过期、确认吊销、其他实例或 refresh token 返回200 `{"active":false}`。无效调用凭据是401；请求格式错误是400；请求体超限是413；错误内容类型是415。内省响应禁止缓存；接入示例见 [examples/remoteauth](../examples/remoteauth)。

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

SIGINT/SIGTERM 触发撤销就绪、HTTP排空、维护停止/审计刷新、连接关闭。进程管理器的终止宽限应大于 SHUTDOWN_TIMEOUT；超时非零退出，不能据此声称全部审计已落库。回退只切换二进制/流量，不调用数据库 Down 或 UnsafeReset。

## 验证边界

现有库完整门禁仍使用 `scripts/verify.sh`；服务门禁使用 `scripts/verify-accountsvc.sh`，要求一次性 PostgreSQL 和 Redis，内部启动隔离 SMTP/HTTPS OIDC fixtures，缺依赖时失败。本地执行记录见 [验收记录](../openspec/changes/archive/2026-10-03-add-accountsvc/verification.md)。外部 SMTP 实际送达、实际 OIDC 提供方和生产网络配置属于部署验收，测试 fixture 通过不代替这些联调。
