# consumer-token-introspection Specification

## Purpose

让外部业务服务通过受保护的远程接口判断消费者 access token 的有效性并取得最小授权上下文，无需持有消费者签名密钥；明确调用方认证、吊销故障语义和业务授权责任，保持与嵌入式消费者认证一致。

## Requirements

### Requirement: 独立服务调用认证

单监听器上的 `POST /v1/introspect` SHALL 要求独立的服务调用认证：HTTP Basic 客户端凭据，或外部提供方签发、`azp` 在内省允许列表中的客户端凭据 Bearer 访问令牌（规则见服务调用认证规范）。两种方式都未配置时，该路由 SHALL 先于认证与请求解析返回 503 `introspection_not_configured`（OAuth 顶层 `error`，不带 `WWW-Authenticate`），服务其他功能不受影响。生产调用链 SHALL 具有经过证书校验的 TLS 传输保护，可由服务自身或受控网关/服务网格提供。显式关闭服务端 TLS SHALL 不取消调用认证或使未受保护的跨主机明文连接成为可接受的生产部署。客户端秘密 SHALL 支持重叠轮换；调用凭据 SHALL 不得与消费者 JWT 或管理员凭据互换。认证 SHALL 先于 token 校验。错误 SHALL 使用 OAuth 顶层 `error`：凭据缺失、错误或方式未启用返回 401 `invalid_client`；携带的 Bearer 令牌无效返回 401 `invalid_token`；Bearer 客户端未获准调用内省返回 403 `insufficient_scope`；签发方不可用返回 503 `temporarily_unavailable`。

#### Scenario: 消费者令牌不能调用内省
- **WHEN** 请求仅携带消费者 Bearer token 或错误客户端秘密
- **THEN** 返回401，不返回被查询 token 的有效性或主体信息，服务自身 TLS 是否启用不影响该行为

#### Scenario: 调用秘密轮换
- **WHEN** 同一客户端配置新旧秘密并随后移除旧秘密重启
- **THEN** 重叠期两者可用，移除后旧秘密被拒绝，新秘密可用且 token 签名密钥无需变化

#### Scenario: 新路径与旧路径
- **WHEN** 调用方使用新路径，或继续请求 `/internal/v1/introspect`
- **THEN** 前者进入既有内省协议处理；后者返回404，不重定向或自动回退，迁移说明要求更新端点地址

#### Scenario: 内省未配置
- **WHEN** 服务既未设置 `ACCOUNTSVC_INTROSPECTION_BASIC_CLIENTS` 也未设置 `ACCOUNTSVC_INTROSPECTION_BEARER_CLIENTS`，调用方携带任意凭据或不带凭据请求 `/v1/introspect`
- **THEN** 返回 503 `introspection_not_configured`，不校验被查询 token，不返回其有效性或主体信息

#### Scenario: Bearer 调用内省
- **WHEN** 调用方携带 `azp` 在内省允许列表中的有效服务访问令牌提交内省请求
- **THEN** 进入既有内省协议处理，响应格式与 Basic 调用相同

#### Scenario: Bearer 错误分类
- **WHEN** 调用方的服务令牌无效、`azp` 只获准调用拉取接口，或签发方 JWKS 不可用
- **THEN** 分别返回 401 `invalid_token`、403 `insufficient_scope`、503 `temporarily_unavailable`，均不返回被查询 token 的任何信息

### Requirement: 标准内省请求及最小响应

接口 SHALL 使用 RFC 7662 的表单请求及 JSON active 响应，必需 `token`，可选 `token_type_hint`。首版 SHALL 只接受 access token 的实际验证，hint 不改变验证结果。有效响应 SHALL 包含 `active:true`、`sub`、`scope`、`sid`、Unix 秒 `auth_time`；无效或不支持的 token SHALL 仅返回 200 `{"active":false}`。响应 SHALL 设置 `Cache-Control: no-store`，不返回隐私身份或原始凭据。

#### Scenario: 有效和无效令牌
- **WHEN** 已认证调用者提交有效 access，或过期、确认吊销、其他实例、refresh token
- **THEN** 前者返回最小主体上下文，后者返回 active:false 且不泄露无效原因；错误 hint 不否定本来有效的 access

#### Scenario: 请求格式错误
- **WHEN** 表单缺 token、token 为空/重复、请求体超过 64 KiB，或 Content-Type 不是表单
- **THEN** 分别返回 400 invalid_request、413 或 415 的 OAuth 顶层错误，不把格式错误当作 active:false

### Requirement: 接入方授权与依赖失败处理

接入文档和示例 SHALL 采用分级校验。可以接受吊销延迟的普通接口 SHALL 按本地验签规则使用 JWKS；要求撤销后立即失效的关键接口 SHALL 在每次请求时执行内省，不缓存成功结果，并且 SHALL 在本地验签通过后再调用内省，以免无效令牌消耗内省调用。哪些接口属于关键接口由业务服务决定，并 SHALL 写入其接入说明。业务服务 SHALL 自行检查 scope、必要的近期认证和资源归属。远程超时、网络失败、非 200 或畸形响应 SHALL 拒绝业务访问并作为依赖故障处理，不等同于用户凭据无效。示例客户端 SHALL 默认要求 HTTPS；只有调用方显式允许 HTTP 且部署提供受控外部传输保护（或本地开发）时才使用 HTTP 地址，不因服务端开关或响应重定向自动降级。

#### Scenario: 受限 scope
- **WHEN** 令牌 active:true 或本地验签通过，但 scope 仅为 user:bind 或 user:undelete
- **THEN** 普通业务访问仍被拒绝，只有符合该 scope 的业务动作可继续

#### Scenario: accountsvc 无响应
- **WHEN** 业务服务在关键接口上查询内省超时，或内省返回 503
- **THEN** 不使用历史成功结果或本地验签结果放行，不引导客户端无意义地刷新或退出登录，按依赖不可用处理

#### Scenario: 普通接口与关键接口
- **WHEN** 同一会话被撤销后，客户端继续用尚未过期的 access 访问普通接口和关键接口
- **THEN** 普通接口在 access 剩余有效期内可能仍被放行；关键接口经内省立即拒绝

#### Scenario: 外部 TLS 终止的调用方
- **WHEN** 业务服务经 HTTPS 网关调用，或显式允许 HTTP 与本地网格代理配合
- **THEN** 分别使用经过证书校验的 HTTPS 端点，或由部署方保证跨主机链路加密的 HTTP 应用连接；Basic 凭据、用户 token、不缓存和禁止重定向的行为保持

### Requirement: 以数据库会话状态判定吊销

内省 SHALL 先执行签名、issuer/audience、时效和 ID 格式校验，再按 `sid` 查询数据库会话。只有会话存在、所属账号与 `sub` 一致且未吊销时，才 SHALL 返回 `active:true`。吊销事务一旦提交，后续内省 SHALL 立即返回 `active:false`，不论 Redis 吊销集是否写入成功。内省 SHALL 不查询 Redis 吊销集，也不按刷新期限判定 access：刷新期限已到但未吊销的会话，其 access 在自身有效期内仍为 active。查询数据库失败或超时 SHALL 返回 503 OAuth `temporarily_unavailable`，不得返回 `active:true`，也不得用 `active:false` 掩盖依赖故障。内省 SHALL 不读写会话来源、客户端描述或账号资料，不更新会话的任何字段。同一判定能力 SHALL 以 Go 方法的形式提供给嵌入式宿主，结果与内省一致；普通 Authenticate 和消费者中间件 SHALL 保持 JWT + Redis 吊销校验及 fail-open 行为。

#### Scenario: Redis 写入失败后的撤销
- **WHEN** 撤销事务已提交，但 sid 写入 Redis 吊销集失败
- **THEN** 后续内省返回 `active:false`；普通 Authenticate 仍按既有 fail-open 边界处理

#### Scenario: 数据库不可用
- **WHEN** 令牌签名和时效有效，但会话查询失败或超时
- **THEN** 内省返回 503 `temporarily_unavailable` 和 `Cache-Control: no-store`，不返回主体信息

#### Scenario: 会话不存在或不属于该主体
- **WHEN** `sid` 对应的会话已被清理，或会话的所属账号与 `sub` 不一致
- **THEN** 返回 200 `{"active":false}`，不泄露原因

#### Scenario: 刷新期限已到但未吊销
- **WHEN** 会话的 refresh 期限已到但未被明确吊销，access 仍在自身有效期内
- **THEN** 内省返回 `active:true`，与会话期限规范中的 access 边界一致

#### Scenario: 冻结或注销
- **WHEN** 管理员冻结账号或用户发起注销，事务内吊销了该账号全部会话
- **THEN** 提交后这些会话的 access 立即在内省中返回 `active:false`
