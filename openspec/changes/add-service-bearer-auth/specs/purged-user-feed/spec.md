## MODIFIED Requirements

### Requirement: 服务鉴权与暴露边界

`/v1/purgedUsers` SHALL 要求独立的服务调用认证：HTTP Basic 客户端凭据（`ACCOUNTSVC_PURGE_FEED_BASIC_CLIENTS`，格式与内省客户端相同），或外部提供方签发、`azp` 在拉取允许列表（`ACCOUNTSVC_PURGE_FEED_BEARER_CLIENTS`）中的客户端凭据 Bearer 访问令牌（规则见服务调用认证规范）。SHALL 不得接受内省凭据、内省客户端的令牌、消费者或管理员令牌。错误 SHALL 使用 AIP-193 格式：凭据缺失、错误或方式未启用返回 401 `INVALID_CLIENT`；携带的 Bearer 令牌无效返回 401 `TOKEN_INVALID`；Bearer 客户端未获准调用拉取返回 403 `CLIENT_NOT_AUTHORIZED`；签发方不可用返回 503 `DEPENDENCY_UNAVAILABLE` 并带 `Retry-After: 1`。两种方式都未配置时，该路由 SHALL 返回 503 `PURGE_FEED_NOT_CONFIGURED`。Basic 凭据 SHALL 支持新旧秘密重叠轮换。该路由与内省一样属于内部入口，网关 SHALL 不对公网开放。

#### Scenario: 用内省凭据访问
- **WHEN** 调用方使用只在内省客户端列表中的 Basic 凭据，或 `azp` 只在内省允许列表中的令牌请求 `/v1/purgedUsers`
- **THEN** 前者返回 401 `INVALID_CLIENT`，后者返回 403 `CLIENT_NOT_AUTHORIZED`，均不返回任何记录

#### Scenario: 未配置
- **WHEN** 服务既未设置 `ACCOUNTSVC_PURGE_FEED_BASIC_CLIENTS` 也未设置 `ACCOUNTSVC_PURGE_FEED_BEARER_CLIENTS`
- **THEN** 路由返回 503 `PURGE_FEED_NOT_CONFIGURED`，服务其他功能不受影响

#### Scenario: Bearer 拉取
- **WHEN** 调用方携带 `azp` 在拉取允许列表中的有效服务访问令牌请求 `/v1/purgedUsers`
- **THEN** 返回与 Basic 调用相同格式的分页结果

#### Scenario: 令牌无效与签发方不可用
- **WHEN** 调用方的服务令牌已过期，或签发方 JWKS 不可用
- **THEN** 前者返回 401 `TOKEN_INVALID`，后者返回 503 `DEPENDENCY_UNAVAILABLE` 与 `Retry-After: 1`，依赖故障不返回 401
