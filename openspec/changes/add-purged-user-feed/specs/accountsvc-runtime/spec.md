## ADDED Requirements

### Requirement: 已清理用户拉取凭据

serve SHALL 读取可选的 `ACCOUNTSVC_PURGE_FEED_CLIENTS`，格式与 `ACCOUNTSVC_INTROSPECTION_CLIENTS` 相同（JSON：客户端 ID → base64 秘密数组，每个秘密至少 32 个随机字节）。格式非法时 SHALL 启动失败并指出变量名，不回显值。该列表与内省客户端列表 SHALL 相互独立，同一秘密不得同时出现在两个列表中。migrate SHALL 不解析该变量。production SHALL 不强制要求该配置。

#### Scenario: 凭据复用被拒绝
- **WHEN** 同一秘密同时出现在内省客户端与拉取客户端列表中
- **THEN** serve 启动失败并指出 `ACCOUNTSVC_PURGE_FEED_CLIENTS`，不输出秘密

## MODIFIED Requirements

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
- **THEN** 只绑定一次，所有路由使用该地址；内省与已清理用户拉取各自要求独立的 Basic 凭据，消费者和管理员仍分别验证各自凭据及权限，JWKS 无需认证

#### Scenario: 旧部署配置被显式拒绝
- **WHEN** serve 仍提供非空 INTERNAL_ADDR
- **THEN** 启动失败并指出该废弃配置项，不静默将原内部路径暴露到新监听器；删除配置后旧内省路径仍返回404

#### Scenario: 两个新增补全端点
- **WHEN** 服务启用新版消费者协议
- **THEN** 独立提供 users:sendSignInCompletionCode 和 users:completeSignIn，普通发码用途不通过补全票据切换，管理面不暴露未认证的补全操作
