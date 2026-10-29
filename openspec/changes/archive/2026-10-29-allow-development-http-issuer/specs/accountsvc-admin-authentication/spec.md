## MODIFIED Requirements

### Requirement: 外部 JWT access token 验证

服务 SHALL 从配置的 issuer 发现并获取 JWKS，校验发现 issuer 一致；只接受面向管理员 API 的 RS256 JWT access token，RSA 密钥至少 2048 位。issuer SHALL 为不含凭据、查询、片段和首尾空白的绝对 URL；`ACCOUNTSVC_MODE=production` 时 SHALL 只接受 HTTPS，discovery 返回的 `jwks_uri` 也 SHALL 为 HTTPS。`ACCOUNTSVC_MODE=development` 时 SHALL 另接受 `http://` issuer 与 `jwks_uri`，并在监听前记录一条指出 `ACCOUNTSVC_ADMIN_ISSUER` 的 WARN；两种模式下重定向都 SHALL 拒绝。验证 SHALL 检查签名、issuer、audience、非空 sub、必需 exp 及存在时的 nbf/iat，时间容差 30 秒。服务 SHALL 不信任 token 自带的远程密钥 URL 或转发头中的管理员身份。

#### Scenario: 消费者或错误受众凭据
- **WHEN** 消费者 token、登录客户端 ID token 或错误 issuer/audience/算法/签名的 token 访问管理面
- **THEN** 返回 401 与 Bearer challenge，不执行管理操作

#### Scenario: 管理员凭据有效
- **WHEN** 外部提供方签发正确管理员 API 受众的有效 JWT access token
- **THEN** 以 issuer + sub 识别管理员，展示用户名可缺失，继续执行对应路由角色检查

#### Scenario: 生产模式拒绝 HTTP issuer
- **WHEN** `ACCOUNTSVC_MODE` 为 production（含未设置）且 `ACCOUNTSVC_ADMIN_ISSUER` 为 `http://` 地址
- **THEN** 启动失败并指出 `ACCOUNTSVC_ADMIN_ISSUER`，错误信息与此前相同，不回显值

#### Scenario: 开发模式接受 HTTP issuer
- **WHEN** `ACCOUNTSVC_MODE=development` 且 `ACCOUNTSVC_ADMIN_ISSUER` 为不含凭据、查询和片段的 `http://` 地址
- **THEN** serve 通过配置校验，启动日志在管理员 discovery 之前记录一条指出 `ACCOUNTSVC_ADMIN_ISSUER` 的 WARN，discovery、JWKS 加载与 `iss` 精确匹配按该 HTTP issuer 完成
