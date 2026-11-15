# Spec Delta

## MODIFIED Requirements

### Requirement: 服务鉴权与暴露边界

`/v1/purgedUsers` SHALL 只使用服务认证规格的统一注册表执行独立的服务调用认证：HTTP Basic 客户端凭据，或外部提供方签发的客户端凭据 Bearer 访问令牌，并在认证后要求 `purged_users.read`。同一客户端 SHALL 可以同时拥有内省权限；只持有内省权限的已认证客户端 SHALL 不得拉取，消费者或管理员令牌 SHALL 不作为服务凭据接受。错误 SHALL 使用 AIP-193 格式：凭据缺失、错误或方式未启用返回 401 `INVALID_CLIENT`；携带的 Bearer 令牌无效返回 401 `TOKEN_INVALID`；有效 Bearer 未登记或已认证客户端缺拉取权限返回 403 `CLIENT_NOT_AUTHORIZED`；签发方不可用返回 503 `DEPENDENCY_UNAVAILABLE` 并带 `Retry-After: 1`。无人持有清理权限时，该路由 SHALL 先于认证和参数解析返回 503 `PURGE_FEED_NOT_CONFIGURED`。有效 Basic 缺权限的 403 SHALL 不带挑战，Bearer SHALL 按服务认证规格给出 insufficient_scope 挑战。Basic 凭据 SHALL 支持新旧秘密重叠轮换。该路由与内省一样属于内部入口，网关 SHALL 不对公网开放。SHALL 不保留旧逐接口认证路径，不改变分页、清理事务或传输边界。

#### Scenario: 用内省凭据访问
- **WHEN** 清理能力已配置，调用方使用只持有 tokens.introspect 的注册客户端的有效 Basic 凭据或有效 Bearer 令牌请求 `/v1/purgedUsers`
- **THEN** 两者均返回 403 `CLIENT_NOT_AUTHORIZED`，不查询或返回任何记录

#### Scenario: 未配置
- **WHEN** 统一注册表无人持有 purged_users.read
- **THEN** 路由返回 503 `PURGE_FEED_NOT_CONFIGURED`，serve 启动时记录一条指出相关变量名的 WARN，服务其他功能不受影响

#### Scenario: Bearer 拉取
- **WHEN** 调用方携带有效服务访问令牌，azp 对应注册客户端已启用 bearer 且持有 purged_users.read，请求 `/v1/purgedUsers`
- **THEN** 返回与 Basic 调用相同格式的分页结果

#### Scenario: 令牌无效与签发方不可用
- **WHEN** 清理能力已配置，调用方的服务令牌已过期，或签发方 JWKS 不可用
- **THEN** 前者返回 401 `TOKEN_INVALID`，后者返回 503 `DEPENDENCY_UNAVAILABLE` 与 `Retry-After: 1`，依赖故障不返回 401
#### Scenario: 统一身份拉取
- **WHEN** 统一客户端同时拥有两项权限，以有效 Basic 或 Bearer 请求清理拉取
- **THEN** 正常返回原分页数据，不因客户端也能内省而拒绝

#### Scenario: 注册表权限拒绝
- **WHEN** 清理能力已配置，但有效 Basic 或 Bearer 客户端只持有 tokens.introspect
- **THEN** 返回 403 CLIENT_NOT_AUTHORIZED，不查询记录；Basic 不带挑战，Bearer 带 insufficient_scope 挑战

#### Scenario: 注册表未授权任何调用方
- **WHEN** 统一注册表无人被授予 purged_users.read
- **THEN** 路由先于认证和参数解析返回 503 PURGE_FEED_NOT_CONFIGURED，启动记录变量名级别的 WARN

#### Scenario: 协议和故障不变
- **WHEN** 已获授权的调用方提交非法 page_token，或 Bearer 必需公钥不可用
- **THEN** 分别返回原 400 INVALID_PAGE_TOKEN 或 503 DEPENDENCY_UNAVAILABLE 与 Retry-After: 1，均 no-store
