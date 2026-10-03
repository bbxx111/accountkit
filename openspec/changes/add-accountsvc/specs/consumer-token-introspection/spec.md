## Purpose

让外部业务服务通过受保护的远程接口判断消费者 access token 的有效性并取得最小授权上下文，无需持有消费者签名密钥；明确调用方认证、吊销故障语义和业务授权责任，保持与嵌入式消费者认证一致。

## ADDED Requirements

### Requirement: 独立服务调用认证

内部 `POST /internal/v1/introspect` SHALL 要求独立 HTTP Basic 客户端凭据，生产使用 HTTPS。客户端秘密 SHALL 支持重叠轮换且不得与消费者 JWT 或管理员凭据互换。认证 SHALL 先于 token 校验；凭据缺失或错误返回 401 OAuth `invalid_client` 与 Basic challenge。

#### Scenario: 消费者令牌不能调用内省
- **WHEN** 请求仅携带消费者 Bearer token 或错误客户端秘密
- **THEN** 返回 401，不返回被查询 token 的有效性或主体信息

#### Scenario: 调用秘密轮换
- **WHEN** 同一客户端配置新旧秘密并随后移除旧秘密重启
- **THEN** 重叠期两者可用，移除后旧秘密被拒绝，新秘密可用且 token 签名密钥无需变化

### Requirement: 标准内省请求及最小响应

接口 SHALL 使用 RFC 7662 的表单请求及 JSON active 响应，必需 `token`，可选 `token_type_hint`。首版 SHALL 只接受 access token 的实际验证，hint 不改变验证结果。有效响应 SHALL 包含 `active:true`、`sub`、`scope`、`sid`、Unix 秒 `auth_time`；无效或不支持的 token SHALL 仅返回 200 `{"active":false}`。响应 SHALL 设置 `Cache-Control: no-store`，不返回隐私身份或原始凭据。

#### Scenario: 有效和无效令牌
- **WHEN** 已认证调用者提交有效 access，或过期、确认吊销、其他实例、refresh token
- **THEN** 前者返回最小主体上下文，后者返回 active:false 且不泄露无效原因；错误 hint 不否定本来有效的 access

#### Scenario: 请求格式错误
- **WHEN** 表单缺 token、token 为空/重复、请求体超过 64 KiB，或 Content-Type 不是表单
- **THEN** 分别返回 400 invalid_request、413 或 415 的 OAuth 顶层错误，不把格式错误当作 active:false

### Requirement: 保留当前消费者认证语义

内省 SHALL 复用当前消费者认证规则，包括签名、issuer/audience、时效、ID 格式及 Redis 吊销检查。它 SHALL 不额外宣称查询数据库后得到实时账号状态或 scope。Redis 吊销查询失败 SHALL 沿用 fail-open 与警告日志；就绪状态 SHALL 不改变已经收到的内省请求的此项兼容行为。

#### Scenario: Redis 吊销查询失败
- **WHEN** token 本身有效且吊销查询不可用
- **THEN** 内省仍返回 active:true 并记录警告，同时 readyz 可为 503；文档明确这不保证故障时即时吊销

### Requirement: 接入方授权与依赖失败处理

接入文档和示例 SHALL 每次受保护请求执行内省，不缓存成功结果；业务服务 SHALL 自行检查 scope、必要的近期认证和资源归属。远程超时、网络失败、非 200 或畸形响应 SHALL 拒绝业务访问并作为依赖故障处理，不等同于用户凭据无效。

#### Scenario: 受限 scope
- **WHEN** 令牌 active:true 但 scope 仅为 user:bind 或 user:undelete
- **THEN** 普通业务访问仍被拒绝，只有符合该 scope 的业务动作可继续

#### Scenario: accountsvc 无响应
- **WHEN** 业务服务查询内省超时
- **THEN** 不使用历史成功结果放行，不引导客户端无意义地刷新或退出登录，按依赖不可用处理
