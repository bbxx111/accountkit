# Spec Delta

## MODIFIED Requirements

### Requirement: 服务鉴权与暴露边界

旧配置模式 SHALL 沿用下列逐接口规则；统一模式 SHALL 使用服务认证规格的注册表，并在认证后要求 purged_users.read。同一客户端 SHALL 可以同时拥有内省权限。统一模式无人持有清理权限时 SHALL 返回原 NOT_CONFIGURED 503；有效 Basic 缺权限 SHALL 与 Bearer 一样返回 403 CLIENT_NOT_AUTHORIZED。AIP 格式、秘密轮换、传输及内部暴露边界 SHALL 在两种模式下均保持。

`/v1/purgedUsers` SHALL 要求独立的服务调用认证：HTTP Basic 客户端凭据（`ACCOUNTSVC_PURGE_FEED_BASIC_CLIENTS`，格式与内省客户端相同），或外部提供方签发、`azp` 在拉取允许列表（`ACCOUNTSVC_PURGE_FEED_BEARER_CLIENTS`）中的客户端凭据 Bearer 访问令牌（规则见服务调用认证规范）。SHALL 不得接受内省凭据、内省客户端的令牌、消费者或管理员令牌。错误 SHALL 使用 AIP-193 格式：凭据缺失、错误或方式未启用返回 401 `INVALID_CLIENT`；携带的 Bearer 令牌无效返回 401 `TOKEN_INVALID`；Bearer 客户端未获准调用拉取返回 403 `CLIENT_NOT_AUTHORIZED`；签发方不可用返回 503 `DEPENDENCY_UNAVAILABLE` 并带 `Retry-After: 1`。两种方式都未配置时，该路由 SHALL 返回 503 `PURGE_FEED_NOT_CONFIGURED`。Basic 凭据 SHALL 支持新旧秘密重叠轮换。该路由与内省一样属于内部入口，网关 SHALL 不对公网开放。

#### Scenario: 用内省凭据访问
- **WHEN** 使用旧配置模式，调用方使用只在内省客户端列表中的 Basic 凭据，或 `azp` 只在内省允许列表中的令牌请求 `/v1/purgedUsers`
- **THEN** 前者返回 401 `INVALID_CLIENT`，后者返回 403 `CLIENT_NOT_AUTHORIZED`，均不返回任何记录

#### Scenario: 未配置
- **WHEN** 使用旧配置模式，服务既未设置 `ACCOUNTSVC_PURGE_FEED_BASIC_CLIENTS` 也未设置 `ACCOUNTSVC_PURGE_FEED_BEARER_CLIENTS`
- **THEN** 路由返回 503 `PURGE_FEED_NOT_CONFIGURED`，serve 启动时记录一条指出相关变量名的 WARN，服务其他功能不受影响

#### Scenario: Bearer 拉取
- **WHEN** 使用旧配置模式，调用方携带 `azp` 在拉取允许列表中的有效服务访问令牌请求 `/v1/purgedUsers`
- **THEN** 返回与 Basic 调用相同格式的分页结果

#### Scenario: 令牌无效与签发方不可用
- **WHEN** 使用旧配置模式，调用方的服务令牌已过期，或签发方 JWKS 不可用
- **THEN** 前者返回 401 `TOKEN_INVALID`，后者返回 503 `DEPENDENCY_UNAVAILABLE` 与 `Retry-After: 1`，依赖故障不返回 401
#### Scenario: 统一身份拉取
- **WHEN** 统一客户端同时拥有两项权限，以有效 Basic 或 Bearer 请求清理拉取
- **THEN** 正常返回原分页数据，不因客户端也能内省而拒绝

#### Scenario: 统一模式的权限拒绝
- **WHEN** 清理能力已配置，但有效 Basic 或 Bearer 客户端只持有 tokens.introspect
- **THEN** 返回 403 CLIENT_NOT_AUTHORIZED，不查询记录；Basic 不带挑战，Bearer 带 insufficient_scope 挑战

#### Scenario: 统一模式未授权任何调用方
- **WHEN** 统一注册表无人被授予 purged_users.read
- **THEN** 路由先于认证和参数解析返回 503 PURGE_FEED_NOT_CONFIGURED，启动记录变量名级别的 WARN

#### Scenario: 统一模式协议和故障不变
- **WHEN** 已获授权的调用方提交非法 page_token，或 Bearer 必需公钥不可用
- **THEN** 分别返回原 400 INVALID_PAGE_TOKEN 或 503 DEPENDENCY_UNAVAILABLE 与 Retry-After: 1，均 no-store
