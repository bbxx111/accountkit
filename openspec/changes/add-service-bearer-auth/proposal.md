## Why

内省 `POST /v1/introspect` 与已清理用户拉取 `GET /v1/purgedUsers` 目前只接受 HTTP Basic，accountsvc 须长期保存每个调用方的共享秘密，轮换靠重叠配置与重启。接入方（ai-food）已用 Keycloak 管理服务身份，希望改用 RFC 6749 §4.4 客户端凭据模式换取短期访问令牌调用这两个接口，使 accountsvc 不再保存调用方秘密，并能由 IdP 停用客户端。当前只有一个调用方，不紧急，但先定设计以免后续各自实现。

## What Changes

- 两个服务接口在 Basic 之外 SHALL 可选接受外部 OIDC 提供方签发的 RS256 Bearer 访问令牌（客户端凭据模式）；两种方式可同时启用，便于逐步切换与回退。
- 新增服务调用认证层：抽取管理员认证中的 OIDC discovery、JWKS 缓存和 JWT 解析为内部公共层，管理员认证行为保持不变；新的服务认证只给出「调用方 ID + 成功/未认证/无权限/依赖不可用」判定，由两个处理器各自映射为原有错误格式。
- 授权按令牌的 `azp`（授权方客户端 ID）匹配每个接口独立的允许列表；两个列表 SHALL 不相交，延续「一个凭据只能访问一个接口」。不使用管理员角色规则。
- 新增配置 `ACCOUNTSVC_SERVICE_TOKEN_ISSUER`、`ACCOUNTSVC_SERVICE_TOKEN_AUDIENCE`、`ACCOUNTSVC_INTROSPECTION_TOKEN_CLIENTS`、`ACCOUNTSVC_PURGE_FEED_TOKEN_CLIENTS`，在 serve 启动阶段校验；服务 audience 不得与消费者、管理员 audience 或任一第三方应用/客户端 ID 重合。
- 内省的「必须配置 Basic」放宽为「至少配置一种调用认证」；拉取在两种都未配置时才返回 503 `PURGE_FEED_NOT_CONFIGURED`。
- 401 同时给出 Basic 与 Bearer challenge（各带 realm），仅当请求确实携带 Bearer 令牌时附 `error="invalid_token"`。
- IdP 不可用 SHALL 不阻止 accountsvc 启动；Bearer 请求在取不到签名密钥时返回 503，不当作 401。
- 不涉及数据库迁移、游标格式或内省响应体。无破坏性变化：仅配置 Basic 的部署行为不变。

## Capabilities

### New Capabilities
- `accountsvc-service-authentication`：服务接口调用方认证——Basic 与客户端凭据 Bearer 两种方式、`azp` 授权、challenge 规则、IdP 故障分类、启动校验与可观测性约束。

### Modified Capabilities
- `consumer-token-introspection`：「独立服务调用认证」由只接受 Basic 改为接受 Basic 或服务 Bearer 令牌，补充 Bearer 的 401/403/503 OAuth 错误。
- `purged-user-feed`：「服务鉴权与暴露边界」同上，补充 AIP-193 错误码与未配置判定。
- `accountsvc-runtime`：「生产发送与内省凭据要求」改为至少一种内省调用认证；「已清理用户拉取凭据」补充 token 客户端列表及跨列表不相交校验。

## Impact

- 代码：`internal/accountsvc/adminauth`（抽出公共层，行为不变）、新增 `internal/accountsvc/oidcjwt`（暂名）与 `internal/accountsvc/serviceauth`、`introspection`、`purgefeed`、`basicclients`（返回匹配的客户端 ID）、`config.go`、`run.go`。
- 文档：`docs/accountsvc.md`「业务服务错误与配置契约」及配置表、`docs/gateway-integration.md`（内部入口透传 Bearer）、`deploy/accountsvc/.env.example`、`examples/remoteauth` 说明。
- 依赖：沿用现有 `golang-jwt/jwt/v5`，不新增第三方库。
- 运维：接入方自行在 IdP 配置仅开服务账号的机密客户端和 audience mapper；accountsvc 不提供客户端库。
