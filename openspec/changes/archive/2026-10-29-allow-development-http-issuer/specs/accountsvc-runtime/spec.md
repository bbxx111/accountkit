## MODIFIED Requirements

### Requirement: 生产发送与内省凭据要求

production SHALL 在 EMAIL 启用时要求真实 SMTP 与 SMTP TLS，在 PHONE 启用时要求真实短信服务商。内省调用认证（Basic 客户端凭据或服务令牌客户端列表）在任何模式下 SHALL 为可选：两种都未配置时 serve 照常启动，内省路由返回 503 `introspection_not_configured`；production SHALL 不强制要求内省调用认证配置。production SHALL 不使用日志验证码、固定默认秘密或明文邮件降级；HTTP TLS 开关不得使生产 SMTP 降级。短信 endpoint、管理员 issuer 与服务令牌签发方在 production SHALL 只接受 HTTPS；development SHALL 允许管理员 issuer 与服务令牌签发方使用 `http://`，并在监听前为每个 HTTP issuer 各记一条只写变量名的 WARN。

#### Scenario: 生产发送和内省依赖不可降级
- **WHEN** production 缺少必需的真实 SMTP/TLS 或短信服务商
- **THEN** 拒绝启动，不用日志验证码、默认秘密或明文 SMTP 替代；显式关闭 HTTP TLS 不豁免 SMTP TLS

#### Scenario: 内省调用认证未配置
- **WHEN** production 既未设置 `ACCOUNTSVC_INTROSPECTION_BASIC_CLIENTS` 也未设置 `ACCOUNTSVC_INTROSPECTION_BEARER_CLIENTS`
- **THEN** serve 照常启动并就绪，启动日志记录一条指出相关变量名的 WARN，`POST /v1/introspect` 返回 503 `introspection_not_configured`，其他路由不受影响

#### Scenario: 仅以服务令牌认证内省
- **WHEN** production 未设置 `ACCOUNTSVC_INTROSPECTION_BASIC_CLIENTS`，但服务令牌签发方、audience 与 `ACCOUNTSVC_INTROSPECTION_BEARER_CLIENTS` 有效
- **THEN** serve 可以启动，内省只接受 Bearer 调用

#### Scenario: 开发模式的 HTTP issuer 警告
- **WHEN** `ACCOUNTSVC_MODE=development` 且 `ACCOUNTSVC_ADMIN_ISSUER` 或 `ACCOUNTSVC_SERVICE_ISSUER` 为 `http://` 地址
- **THEN** serve 启动，并在监听前为每个 HTTP issuer 各记录一条 WARN，分别指出 `ACCOUNTSVC_ADMIN_ISSUER` 或 `ACCOUNTSVC_SERVICE_ISSUER`，不写值；migrate 不记录

#### Scenario: 生产模式不放宽 issuer
- **WHEN** `ACCOUNTSVC_MODE` 为 production 且任一 issuer 为 `http://` 地址
- **THEN** serve 在监听前失败并指出对应变量名，行为与此前相同
