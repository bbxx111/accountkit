## MODIFIED Requirements

### Requirement: 生产发送与内省凭据要求

production SHALL 在 EMAIL 启用时要求真实 SMTP 与 SMTP TLS，在 PHONE 启用时要求真实短信服务商。内省调用认证（Basic 客户端凭据或服务令牌客户端列表）在任何模式下 SHALL 为可选：两种都未配置时 serve 照常启动，内省路由返回 503 `introspection_not_configured`；production SHALL 不强制要求内省调用认证配置。production SHALL 不使用日志验证码、固定默认秘密或明文邮件降级；HTTP TLS 开关不得使生产 SMTP 降级。短信 endpoint 在 production SHALL 只接受 HTTPS。

#### Scenario: 生产发送和内省依赖不可降级
- **WHEN** production 缺少必需的真实 SMTP/TLS 或短信服务商
- **THEN** 拒绝启动，不用日志验证码、默认秘密或明文 SMTP 替代；显式关闭 HTTP TLS 不豁免 SMTP TLS

#### Scenario: 内省调用认证未配置
- **WHEN** production 既未设置 `ACCOUNTSVC_INTROSPECTION_BASIC_CLIENTS` 也未设置 `ACCOUNTSVC_INTROSPECTION_BEARER_CLIENTS`
- **THEN** serve 照常启动并就绪，`POST /v1/introspect` 返回 503 `introspection_not_configured`，其他路由不受影响

#### Scenario: 仅以服务令牌认证内省
- **WHEN** production 未设置 `ACCOUNTSVC_INTROSPECTION_BASIC_CLIENTS`，但服务令牌签发方、audience 与 `ACCOUNTSVC_INTROSPECTION_BEARER_CLIENTS` 有效
- **THEN** serve 可以启动，内省只接受 Bearer 调用

### Requirement: 已清理用户拉取凭据

serve SHALL 读取可选的 `ACCOUNTSVC_PURGE_FEED_BASIC_CLIENTS`，格式与 `ACCOUNTSVC_INTROSPECTION_BASIC_CLIENTS` 相同（JSON：客户端 ID → base64 秘密数组，每个秘密至少 32 个随机字节），以及可选的 `ACCOUNTSVC_PURGE_FEED_BEARER_CLIENTS`（允许的 `azp` 字符串数组）。格式非法时 SHALL 启动失败并指出变量名，不回显值。拉取的 Basic 列表与内省 Basic 列表 SHALL 相互独立，同一秘密不得同时出现在两个列表中；拉取与内省的 `azp` 列表 SHALL 不相交。migrate SHALL 不解析这些变量。production SHALL 不强制要求拉取认证配置。

#### Scenario: 凭据复用被拒绝
- **WHEN** 同一秘密同时出现在内省客户端与拉取客户端列表中
- **THEN** serve 启动失败并指出 `ACCOUNTSVC_PURGE_FEED_BASIC_CLIENTS`，不输出秘密

#### Scenario: 令牌客户端复用被拒绝
- **WHEN** 同一 `azp` 同时出现在内省与拉取的令牌客户端列表中
- **THEN** serve 启动失败并指出 `ACCOUNTSVC_PURGE_FEED_BEARER_CLIENTS`

### Requirement: HTTP 边界与可信请求元信息

serve SHALL 仅使用 ACCOUNTSVC_HTTP_ADDR 创建一个监听器，默认绑定 127.0.0.1:8080，挂载消费者 /v1、管理员 /admin/v1、内省 /v1/introspect、已清理用户拉取 /v1/purgedUsers、公钥 /.well-known/jwks.json 与 /healthz、/readyz。不同身份体系和处理器 SHALL 独立。网关与网络策略 SHALL 控制公网暴露范围，不把路径名称视为网络隔离；单监听器不自动区分公网和内网来源。

#### Scenario: 公网请求内部路径
- **WHEN** 公网调用者通过按接入契约配置的网关请求 `/v1/introspect`、`/v1/purgedUsers`、`/healthz` 或 `/readyz`
- **THEN** 网关拒绝且请求不进入 accountsvc；单监听器本身不承诺识别公网/内网来源并自动隔离这些路由

#### Scenario: 伪造代理头
- **WHEN** 不可信直接对端发送 X-Forwarded-For
- **THEN** 验证码限流与审计使用直接对端 IP；可信链畸形时同样回退直接对端

#### Scenario: 单地址提供不同身份面
- **WHEN** serve 使用有效配置启动
- **THEN** 只绑定一次，所有路由使用该地址；内省与已清理用户拉取各自要求独立的服务调用认证（Basic 凭据或获准的服务令牌），消费者和管理员仍分别验证各自凭据及权限，JWKS 无需认证

#### Scenario: 旧部署配置被显式拒绝
- **WHEN** serve 仍提供非空 INTERNAL_ADDR
- **THEN** 启动失败并指出该废弃配置项，不静默将原内部路径暴露到新监听器；删除配置后旧内省路径仍返回404

#### Scenario: 两个新增补全端点
- **WHEN** 服务启用新版消费者协议
- **THEN** 独立提供 users:sendSignInCompletionCode 和 users:completeSignIn，普通发码用途不通过补全票据切换，管理面不暴露未认证的补全操作
