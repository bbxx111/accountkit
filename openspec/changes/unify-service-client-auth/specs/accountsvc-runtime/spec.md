# Spec Delta

## MODIFIED Requirements

### Requirement: 生产发送与内省凭据要求

production SHALL 在 EMAIL 启用时要求真实 SMTP 与 SMTP TLS，在 PHONE 启用时要求真实短信服务商。内省调用认证（Basic 客户端凭据或服务令牌客户端列表）在任何模式下 SHALL 为可选：旧模式两种均未配置，或统一模式无人持有 tokens.introspect 时，serve SHALL 照常启动，内省路由返回 503 `introspection_not_configured`；production SHALL 不强制要求内省调用认证配置。production SHALL 不使用日志验证码、固定默认秘密或明文邮件降级；HTTP TLS 开关不得使生产 SMTP 降级。短信 endpoint、管理员 issuer 与服务令牌签发方在 production SHALL 只接受 HTTPS；development SHALL 允许管理员 issuer 与服务令牌签发方使用 `http://`，并在监听前为每个 HTTP issuer 各记一条只写变量名的 WARN。

#### Scenario: 生产发送和内省依赖不可降级
- **WHEN** production 缺少必需的真实 SMTP/TLS 或短信服务商
- **THEN** 拒绝启动，不用日志验证码、默认秘密或明文 SMTP 替代；显式关闭 HTTP TLS 不豁免 SMTP TLS

#### Scenario: 内省调用认证未配置
- **WHEN** production 在旧模式中两项内省配置均未设置，或统一模式无人拥有 tokens.introspect
- **THEN** serve 照常启动并就绪，启动日志记录一条指出相关变量名的 WARN，`POST /v1/introspect` 返回 503 `introspection_not_configured`，其他路由不受影响

#### Scenario: 仅以服务令牌认证内省
- **WHEN** production 采用有效 issuer/audience，旧模式只配置内省 Bearer 名单，或统一模式所有服务客户端仅允许 bearer 且有客户端持有 tokens.introspect
- **THEN** serve 可以启动，内省只接受 Bearer 调用

#### Scenario: 开发模式的 HTTP issuer 警告
- **WHEN** `ACCOUNTSVC_MODE=development` 且 `ACCOUNTSVC_ADMIN_ISSUER` 或 `ACCOUNTSVC_SERVICE_ISSUER` 为 `http://` 地址
- **THEN** serve 启动，并在监听前为每个 HTTP issuer 各记录一条 WARN，分别指出 `ACCOUNTSVC_ADMIN_ISSUER` 或 `ACCOUNTSVC_SERVICE_ISSUER`，不写值；migrate 不记录

#### Scenario: 生产模式不放宽 issuer
- **WHEN** `ACCOUNTSVC_MODE` 为 production 且任一 issuer 为 `http://` 地址
- **THEN** serve 在监听前失败并指出对应变量名，行为与此前相同

### Requirement: migrate 静态校验与依赖边界

migrate SHALL 只执行静态身份策略语法/集合校验及迁移必要检查，不监听、不构造认证处理器，不要求发送器或第三方运行时依赖。migrate SHALL 不要求或解析服务监听地址、HTTP TLS 开关、证书、SMTP、短信服务商、邮件品牌、旧逐接口调用配置或统一服务客户端/issuer/audience 配置；迁移锁与密钥版本检查仍执行。

#### Scenario: 迁移独立于服务运行配置
- **WHEN** migrate 的身份集合合法且迁移必要依赖有效，未提供服务运行配置
- **THEN** 迁移可执行，不读取无关监听/TLS/发送/短信/内省设置，仍核对迁移锁和存储引用密钥

#### Scenario: 服务认证配置错误不阻止迁移
- **WHEN** migrate 的迁移必要配置有效，但存在孤立客户端字段、非法权限或不可达服务 issuer
- **THEN** 不读取这些服务认证配置、不访问 issuer，仍按原迁移契约执行

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
- **THEN** 只绑定一次，所有路由使用该地址；内省与已清理用户拉取要求服务调用认证，旧模式使用各自配置，统一模式共用注册表并分别要求 tokens.introspect 与 purged_users.read，消费者和管理员仍分别验证各自凭据及权限，JWKS 无需认证

#### Scenario: 旧部署配置被显式拒绝
- **WHEN** serve 仍提供非空 INTERNAL_ADDR
- **THEN** 启动失败并指出该废弃配置项，不静默将原内部路径暴露到新监听器；删除配置后旧内省路径仍返回404

#### Scenario: 两个新增补全端点
- **WHEN** 服务启用新版消费者协议
- **THEN** 独立提供 users:sendSignInCompletionCode 和 users:completeSignIn，普通发码用途不通过补全票据切换，管理面不暴露未认证的补全操作
### Requirement: 已清理用户拉取凭据

下列逐接口配置约束 SHALL 仅适用于旧配置模式；统一模式 SHALL 按服务认证规格的客户端注册表与 purged_users.read 权限执行，允许同一客户端同时访问内省和清理拉取。

serve SHALL 读取可选的 `ACCOUNTSVC_PURGE_FEED_BASIC_CLIENTS`，格式与 `ACCOUNTSVC_INTROSPECTION_BASIC_CLIENTS` 相同（JSON：客户端 ID → base64 秘密数组，每个秘密至少 32 个随机字节），以及可选的 `ACCOUNTSVC_PURGE_FEED_BEARER_CLIENTS`（允许的 `azp` 字符串数组）。格式非法时 SHALL 启动失败并指出变量名，不回显值。拉取的 Basic 列表与内省 Basic 列表 SHALL 相互独立，同一秘密不得同时出现在两个列表中；拉取与内省的 `azp` 列表 SHALL 不相交。migrate SHALL 不解析这些变量。production SHALL 不强制要求拉取认证配置。

#### Scenario: 凭据复用被拒绝
- **WHEN** 使用旧配置模式，同一秘密同时出现在内省客户端与拉取客户端列表中
- **THEN** serve 启动失败并指出 `ACCOUNTSVC_PURGE_FEED_BASIC_CLIENTS`，不输出秘密

#### Scenario: 令牌客户端复用被拒绝
- **WHEN** 使用旧配置模式，同一 `azp` 同时出现在内省与拉取的令牌客户端列表中
- **THEN** serve 启动失败并指出 `ACCOUNTSVC_PURGE_FEED_BEARER_CLIENTS`

#### Scenario: 统一客户端复用身份
- **WHEN** 使用统一模式，同一客户端被显式授予 tokens.introspect 与 purged_users.read
- **THEN** 两个接口共用该客户端的已启用认证方式和凭据，不执行旧逐接口名单不相交约束

## ADDED Requirements

### Requirement: 统一服务配置的启动快照

serve SHALL 在监听前解析并冻结统一服务客户端配置，遵守服务认证规格的模式选择与严格校验。改变文件或环境 SHALL 不热更新；重启后新权限、方法和秘密生效。日志及错误 SHALL 不输出配置值或凭据。部署者 SHALL 在全部副本完成重启或排空旧副本后才宣称策略已生效；令牌仍有效也不能绕过新进程中撤销的权限。

#### Scenario: 撤销权限后重启
- **WHEN** 从某客户端移除清理权限并重启实例，其他客户端仍持有该权限
- **THEN** 该客户端原有效令牌在此实例请求清理接口返回 403，保留的其他权限继续可用

#### Scenario: 配置非法
- **WHEN** 统一配置中存在不合法别名、秘密、权限或新旧模式冲突
- **THEN** serve 在监听前非零退出，仅报告相关变量名及固定错误类别

### Requirement: 未配置能力的观测与部署示例

统一模式某接口无人获授权时 SHALL 返回原 NOT_CONFIGURED 503，并在 serve 启动时为该能力记录一次指出 SERVICE_CLIENTS 和 PERMISSIONS 配置类别的 WARN，不记录值。模板及接入文档 SHALL 提供分项变量的 Basic-only、Bearer-only、双方式、最小权限与重叠秘密轮换示例，区分 Basic 密码和 IdP client secret，说明 TLS、客户端凭据换令牌及新旧配置互斥迁移。SHALL 不在部署示例中授予尚未实现的权限。

#### Scenario: 只启用清理能力
- **WHEN** 统一客户端仅被授予 purged_users.read
- **THEN** 服务正常启动，清理接口可用，内省返回 503 并有一次配置警告，不强制给客户端增加内省权限

#### Scenario: 示例可核验
- **WHEN** 使用模板的合成秘密分别配置三种认证方式组合
- **THEN** 配置可解析，权限与示例说明一致，Basic-only 不要求 OIDC 配置，未注册的身份查询权限不出现在可运行示例中
