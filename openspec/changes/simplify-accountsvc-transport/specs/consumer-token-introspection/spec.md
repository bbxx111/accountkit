## MODIFIED Requirements

### Requirement: 独立服务调用认证

单监听器上的 `POST /v1/introspect` SHALL 要求独立 HTTP Basic 客户端凭据；生产调用链 SHALL 具有经过证书校验的 TLS 传输保护，可由服务自身或受控网关/服务网格提供。显式关闭服务端 TLS SHALL 不取消调用认证或使未受保护的跨主机明文连接成为可接受的生产部署。客户端秘密 SHALL 支持重叠轮换且不得与消费者 JWT 或管理员凭据互换。认证 SHALL 先于 token 校验；凭据缺失或错误返回401 OAuth `invalid_client` 与 Basic challenge。

#### Scenario: 消费者令牌不能调用内省
- **WHEN** 请求仅携带消费者 Bearer token 或错误客户端秘密
- **THEN** 返回401，不返回被查询 token 的有效性或主体信息，服务自身 TLS 是否启用不影响该行为

#### Scenario: 调用秘密轮换
- **WHEN** 同一客户端配置新旧秘密并随后移除旧秘密重启
- **THEN** 重叠期两者可用，移除后旧秘密被拒绝，新秘密可用且 token 签名密钥无需变化

#### Scenario: 新路径与旧路径
- **WHEN** 调用方使用新路径，或继续请求 `/internal/v1/introspect`
- **THEN** 前者进入既有内省协议处理；后者返回404，不重定向或自动回退，迁移说明要求更新端点地址

### Requirement: 接入方授权与依赖失败处理

接入文档和示例 SHALL 每次受保护请求执行内省，不缓存成功结果；业务服务 SHALL 自行检查 scope、必要的近期认证和资源归属。远程超时、网络失败、非200或畸形响应 SHALL 拒绝业务访问并作为依赖故障处理，不等同于用户凭据无效。示例客户端 SHALL 默认要求 HTTPS；只有调用方显式允许 HTTP 且部署提供受控外部传输保护（或本地开发）时才使用 HTTP 地址，不因服务端开关或响应重定向自动降级。

#### Scenario: 受限 scope
- **WHEN** 令牌 active:true 但 scope 仅为 user:bind 或 user:undelete
- **THEN** 普通业务访问仍被拒绝，只有符合该 scope 的业务动作可继续

#### Scenario: accountsvc 无响应
- **WHEN** 业务服务查询内省超时
- **THEN** 不使用历史成功结果放行，不引导客户端无意义地刷新或退出登录，按依赖不可用处理

#### Scenario: 外部 TLS 终止的调用方
- **WHEN** 业务服务经 HTTPS 网关调用，或显式允许 HTTP 与本地网格代理配合
- **THEN** 分别使用经过证书校验的 HTTPS 端点，或由部署方保证跨主机链路加密的 HTTP 应用连接；Basic 凭据、用户 token、不缓存和禁止重定向的行为保持
