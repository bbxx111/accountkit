## Why

accountsvc 作为独立微服务部署时，业务服务只能对每个受保护请求调用 `POST /v1/introspect` 判断消费者 access token。原因是消费者 access 用 HS256 签发，签名密钥一旦交给业务服务，业务服务就能伪造令牌，所以所有业务请求都要依赖一次远程调用。

另外，现有内省对吊销的判断只是尽力即时：吊销写入 Redis 失败时只记告警，内省读取 Redis 失败时 fail-open 返回 `active:true`。因此它也不能满足关键接口“撤销后立即失效”的要求。

本变更把校验分为两级：

- 普通接口：业务服务用公钥在本地验签，接受最多一个 access 有效期加验签偏差的吊销延迟。
- 关键接口：调用内省，以数据库会话状态为准，撤销立即生效；依赖故障时 fail-closed。

目前没有正式部署，因此直接把签名算法换成 ES256，不保留 HS256，也不提供旧配置或旧令牌的迁移。

## What Changes

- **BREAKING**：消费者 access token 的签名算法从 HS256 改为 ES256（P-256），header 设置 `typ: at+jwt`（RFC 9068），`kid` 继续使用十进制版本号。不再支持 HS256：
  - `Config.JWTKeys` 改为版本化的 ES256 私钥；
  - 环境变量 `JWT_KEYS` 的值改为 PKCS#8 DER 的 base64；
  - 旧的 HMAC 密钥会在配置校验时被拒绝；
  - 升级前签发的 HS256 access 全部失效。
- 库新增 JWKS 发布能力，输出所有已配置版本的公钥。accountsvc 在单监听器上挂载 `GET /.well-known/jwks.json`。
- **BREAKING**（内省语义）：`POST /v1/introspect` 在 JWT 校验通过后改为查询数据库会话，会话存在、属于 `sub` 且未吊销时才返回 `active:true`；不再查询 Redis 吊销集。数据库不可用时返回 503 `temporarily_unavailable`（fail-closed）。刷新期限已到但未吊销的会话仍按既有规则有效。
- `user.Service` 新增严格认证方法，内省使用该方法。门面新增严格认证中间件（`RequireScopeStrict`），嵌入式宿主可以直接用于关键路由，401、403 行为与 `RequireScope` 一致，依赖失败时返回 503。`Authenticate` 和普通消费者中间件保持 JWT 加 Redis 吊销查询、fail-open 的语义。
- 接入规范从“每个受保护请求都内省”改为分级：普通接口用 JWKS 本地验签，关键接口每次调用内省、不缓存结果。`examples/remoteauth` 增加本地 JWKS 验签示例，并保留内省客户端。
- 不改数据库结构：严格认证按 `session` 主键查询，不新增迁移。

## Capabilities

### New Capabilities

- `consumer-token-local-verification`：消费者 access 的 ES256 签发与 `typ` 头、JWKS 发布、密钥轮换顺序，以及业务服务本地验签的校验要求与吊销延迟边界。

### Modified Capabilities

- `consumer-token-introspection`：内省以数据库会话状态判定吊销，依赖故障时 fail-closed；接入方由“每次请求都内省”改为普通接口本地验签、关键接口内省。
- `session-expiry`：原规范规定 Authenticate 和内省都只用 JWT 加 Redis、fail-open；现拆分为 Authenticate 保持不变，内省按数据库吊销状态判定，但仍不按刷新期限判定。
- `session-authentication-context`：Redis 故障时的 fail-open 边界只适用于 Authenticate；内省以数据库为准。相关场景同步调整。
- `session-client-information`：内省只读会话的吊销状态，不读写客户端描述。
- `embedded-auth-package`：新增严格认证中间件。
- `accountsvc-runtime`：单监听器路由增加 `/.well-known/jwks.json`。
- `gateway-integration`：入口策略增加 JWKS 入口；它是公钥，可对公网开放，也可以只开放给内网，内省和探针的边界不变。

## Impact

- 代码：
  - `tokens/`：ES256 Signer、JWKS 输出；
  - `config.go`：密钥类型及环境变量解析；
  - `accountkit.go`：JWKS 入口；
  - `user/`：严格认证方法及 sqlc 查询；
  - `internal/accountsvc/`：路由挂载、内省装配；
  - `examples/`、`deploy/accountsvc/`：密钥模板与示例。
  - 所有使用 HS256 测试密钥的测试改用 ES256 测试密钥。
- 依赖：继续使用 `golang-jwt/jwt/v5`（已支持 ES256），不新增第三方依赖。
- 宿主与部署方：需要生成 ES256 私钥（如 `openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256`）替换原 JWT 密钥。升级后客户端持有的旧 access 收到 401，用 refresh token 刷新即可，refresh token 不受影响。不提供旧配置兼容或迁移工具。
- 接入方：普通接口改为本地验签后，内省调用量下降，但存在吊销延迟窗口。关键接口使用内省后，数据库故障时返回 503。
- 文档：README、`docs/accountsvc.md`、`docs/gateway-integration.md`、`docs/compatibility.md`。
- 数据库：无迁移。内省对数据库的访问从没有变为每次一次主键查询。
