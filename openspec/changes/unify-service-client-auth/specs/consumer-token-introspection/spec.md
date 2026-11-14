# Spec Delta

## MODIFIED Requirements

### Requirement: 独立服务调用认证

旧配置模式 SHALL 沿用下列逐接口凭据规则；统一模式 SHALL 使用服务认证规格的注册表，并在认证后要求 tokens.introspect。统一模式无人持有该权限时 SHALL 先返回原 NOT_CONFIGURED 503；有效 Basic 缺权限 SHALL 与 Bearer 一样返回 403 insufficient_scope，挑战按统一模式规则处理。生产传输、轮换、消费者令牌隔离及错误格式的通用要求 SHALL 在两种模式下均保持。

单监听器上的 `POST /v1/introspect` SHALL 要求独立的服务调用认证：HTTP Basic 客户端凭据，或外部提供方签发、`azp` 在内省允许列表中的客户端凭据 Bearer 访问令牌（规则见服务调用认证规范）。两种方式都未配置时，该路由 SHALL 先于认证与请求解析返回 503 `introspection_not_configured`（OAuth 顶层 `error`，不带 `WWW-Authenticate`），服务其他功能不受影响。生产调用链 SHALL 具有经过证书校验的 TLS 传输保护，可由服务自身或受控网关/服务网格提供。显式关闭服务端 TLS SHALL 不取消调用认证或使未受保护的跨主机明文连接成为可接受的生产部署。客户端秘密 SHALL 支持重叠轮换；调用凭据 SHALL 不得与消费者 JWT 或管理员凭据互换。认证 SHALL 先于 token 校验。错误 SHALL 使用 OAuth 顶层 `error`：凭据缺失、错误或方式未启用返回 401 `invalid_client`；携带的 Bearer 令牌无效返回 401 `invalid_token`；Bearer 客户端未获准调用内省返回 403 `insufficient_scope`；签发方不可用返回 503 `temporarily_unavailable`。

#### Scenario: 消费者令牌不能调用内省
- **WHEN** 使用旧配置模式，请求仅携带消费者 Bearer token 或错误客户端秘密
- **THEN** 返回401，不返回被查询 token 的有效性或主体信息，服务自身 TLS 是否启用不影响该行为

#### Scenario: 调用秘密轮换
- **WHEN** 使用旧配置模式，同一客户端配置新旧秘密并随后移除旧秘密重启
- **THEN** 重叠期两者可用，移除后旧秘密被拒绝，新秘密可用且 token 签名密钥无需变化

#### Scenario: 新路径与旧路径
- **WHEN** 使用旧配置模式，调用方使用新路径，或继续请求 `/internal/v1/introspect`
- **THEN** 前者进入既有内省协议处理；后者返回404，不重定向或自动回退，迁移说明要求更新端点地址

#### Scenario: 内省未配置
- **WHEN** 使用旧配置模式，服务既未设置 `ACCOUNTSVC_INTROSPECTION_BASIC_CLIENTS` 也未设置 `ACCOUNTSVC_INTROSPECTION_BEARER_CLIENTS`，调用方携带任意凭据或不带凭据请求 `/v1/introspect`
- **THEN** 返回 503 `introspection_not_configured`，不校验被查询 token，不返回其有效性或主体信息

#### Scenario: Bearer 调用内省
- **WHEN** 使用旧配置模式，调用方携带 `azp` 在内省允许列表中的有效服务访问令牌提交内省请求
- **THEN** 进入既有内省协议处理，响应格式与 Basic 调用相同

#### Scenario: Bearer 错误分类
- **WHEN** 使用旧配置模式，调用方的服务令牌无效、`azp` 只获准调用拉取接口，或签发方 JWKS 不可用
- **THEN** 分别返回 401 `invalid_token`、403 `insufficient_scope`、503 `temporarily_unavailable`，均不返回被查询 token 的任何信息
#### Scenario: 统一身份跨接口授权
- **WHEN** 统一客户端被授予 tokens.introspect 和 purged_users.read，并以有效 Basic 或 Bearer 提交内省请求
- **THEN** 正常执行原内省协议，不因同一身份还拥有清理权限而拒绝

#### Scenario: 统一模式的权限拒绝
- **WHEN** 内省能力已配置，持有效 Basic 或 Bearer 的统一客户端只有 purged_users.read
- **THEN** 返回 403 insufficient_scope，不读取或内省请求体 token；Basic 无认证挑战，Bearer 带 insufficient_scope 挑战

#### Scenario: 统一模式的未配置与故障
- **WHEN** 统一注册表无人持有 tokens.introspect，或已配置该能力但 Bearer 验签依赖不可用
- **THEN** 分别返回 503 introspection_not_configured 或 temporarily_unavailable，均不返回消费者主体数据

#### Scenario: 消费者判定语义保持
- **WHEN** 调用方通过统一认证及授权，而被检查会话已在数据库中吊销或数据库查询失败
- **THEN** 分别保持 active:false 或 503 temporarily_unavailable，不因统一服务认证回退到 Redis 吊销判定
