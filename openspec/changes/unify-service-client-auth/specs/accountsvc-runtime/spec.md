# Spec Delta

## MODIFIED Requirements

### Requirement: 生产发送与内省凭据要求

production SHALL 在 EMAIL 启用时要求真实 SMTP 与 SMTP TLS，在 PHONE 启用时要求真实短信服务商。统一注册表中的内省调用认证在任何模式下 SHALL 为可选：无人持有 tokens.introspect 时，serve SHALL 照常启动，内省路由返回 503 `introspection_not_configured`；production SHALL 不强制要求内省调用认证配置。production SHALL 不使用日志验证码、固定默认秘密或明文邮件降级；HTTP TLS 开关不得使生产 SMTP 降级。短信 endpoint、管理员 issuer 与服务令牌签发方在 production SHALL 只接受 HTTPS；development SHALL 允许管理员 issuer 与服务令牌签发方使用 `http://`，并在监听前为每个 HTTP issuer 各记一条只写变量名的 WARN。

#### Scenario: 生产发送和内省依赖不可降级
- **WHEN** production 缺少必需的真实 SMTP/TLS 或短信服务商
- **THEN** 拒绝启动，不用日志验证码、默认秘密或明文 SMTP 替代；显式关闭 HTTP TLS 不豁免 SMTP TLS

#### Scenario: 内省调用认证未配置
- **WHEN** production 的统一注册表无人拥有 tokens.introspect
- **THEN** serve 照常启动并就绪，启动日志记录一条指出相关变量名的 WARN，`POST /v1/introspect` 返回 503 `introspection_not_configured`，其他路由不受影响

#### Scenario: 仅以服务令牌认证内省
- **WHEN** production 采用有效 issuer/audience，所有服务客户端仅允许 bearer 且有客户端持有 tokens.introspect
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
- **WHEN** migrate 的迁移必要配置有效，但存在非空白旧逐接口变量、孤立客户端字段、非法新列表/权限或不可达服务 issuer
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
- **THEN** 只绑定一次，所有路由使用该地址；内省与已清理用户拉取要求服务调用认证，共用注册表并分别要求 tokens.introspect 与 purged_users.read，消费者和管理员仍分别验证各自凭据及权限，JWKS 无需认证

#### Scenario: 旧部署配置被显式拒绝
- **WHEN** serve 仍提供非空 INTERNAL_ADDR
- **THEN** 启动失败并指出该废弃配置项，不静默将原内部路径暴露到新监听器；删除配置后旧内省路径仍返回404

#### Scenario: 两个新增补全端点
- **WHEN** 服务启用新版消费者协议
- **THEN** 独立提供 users:sendSignInCompletionCode 和 users:completeSignIn，普通发码用途不通过补全票据切换，管理面不暴露未认证的补全操作
### Requirement: 已清理用户拉取凭据

serve SHALL 只使用服务认证规格的统一客户端注册表配置拉取凭据，并要求 purged_users.read。客户端 SHALL 可以同时持有 tokens.introspect，两个接口共用该身份启用的认证方式与凭据。跨客户端 Basic 秘密复用 SHALL 仍被拒绝，同一客户端多权限不视为秘密复用；SHALL 不保留旧逐接口 Basic 映射或 Bearer 名单。非空白旧逐接口变量 SHALL 按服务认证规格在监听前拒绝，错误只指出变量名与移除原因，不回显值。migrate SHALL 不解析新旧服务调用变量。production SHALL 不强制要求拉取认证配置。

#### Scenario: 凭据复用被拒绝
- **WHEN** 统一注册表中两个不同客户端 ID 使用相同 Basic 秘密
- **THEN** serve 启动失败并指出相关 BASIC_SECRETS 变量名，不输出秘密

#### Scenario: 旧拉取变量被拒绝
- **WHEN** 旧 PURGE_FEED_BASIC_CLIENTS 或 PURGE_FEED_BEARER_CLIENTS 非空白，即使值为 {} 或 []
- **THEN** serve 监听前拒绝并指出已移除变量，不恢复旧逐接口认证

#### Scenario: 令牌客户端复用被拒绝
- **WHEN** 两个不同配置别名使用相同的 Bearer 客户端 ID
- **THEN** serve 启动失败并指出相关 ID 变量名；同一注册客户端显式持有两项权限不属于重复声明

#### Scenario: 统一客户端复用身份
- **WHEN** 同一客户端被显式授予 tokens.introspect 与 purged_users.read
- **THEN** 两个接口共用该客户端的已启用认证方式和凭据，不执行旧逐接口名单不相交约束

## ADDED Requirements

### Requirement: 统一服务配置的启动快照

serve SHALL 在监听前解析并冻结唯一的统一服务客户端注册表，遵守服务认证规格的严格校验；SERVICE_CLIENTS 缺省、空或全空白均产生非 nil 空注册表，不选择旧路径。改变文件或环境 SHALL 不热更新；重启后新权限、方法和秘密生效。日志及错误 SHALL 不输出配置值或凭据。部署者 SHALL 在全部副本完成重启或排空旧副本后才宣称策略已生效；令牌仍有效也不能绕过新进程中撤销的权限。

#### Scenario: 撤销权限后重启
- **WHEN** 从某客户端移除清理权限并重启实例，其他客户端仍持有该权限
- **THEN** 该客户端原有效令牌在此实例请求清理接口返回 403，保留的其他权限继续可用

#### Scenario: 配置非法
- **WHEN** 配置中存在不合法别名、秘密、权限、孤立字段或任一非空白旧逐接口变量
- **THEN** serve 在监听前非零退出，仅报告相关变量名及固定错误类别

### Requirement: 未配置能力的观测与部署示例

某接口无人获授权时 SHALL 返回原 NOT_CONFIGURED 503，并在 serve 启动时为该能力记录一次指出 SERVICE_CLIENTS 和 PERMISSIONS 配置类别的 WARN，不记录值。模板及接入文档 SHALL 提供分项变量的 Basic-only、Bearer-only、双方式、最小权限与重叠秘密轮换示例，区分 Basic 密码和 IdP client secret，说明 TLS、客户端凭据换令牌及旧配置的显式迁移。SHALL 不在部署示例中授予尚未实现的权限，不提供当前二进制恢复旧模式的步骤。

#### Scenario: 只启用清理能力
- **WHEN** 统一客户端仅被授予 purged_users.read
- **THEN** 服务正常启动，清理接口可用，内省返回 503 并有一次配置警告，不强制给客户端增加内省权限

#### Scenario: 示例可核验
- **WHEN** 使用模板的合成秘密分别配置三种认证方式组合
- **THEN** 配置可解析，权限与示例说明一致，Basic-only 不要求 OIDC 配置，未注册的身份查询权限不出现在可运行示例中

### Requirement: 单一 Compose 配置文件

部署 SHALL 只使用 deploy/accountsvc/compose.yaml；accountsvc 服务 SHALL 直接通过 env_file: ./.env 注入 SERVICE_CLIENTS 及任意已声明别名字段，删除 compose.service-clients.yaml。基础 environment SHALL 不保留四项旧逐接口变量的默认注入。文档标准 Compose 命令 SHALL 使用 --env-file deploy/accountsvc/.env，与服务 env_file 指向同一文件，并区分 CLI 插值与容器注入职责。模板 SHALL 默认空注册表，不含可运行旧格式；迁移说明 SHALL 要求显式转换客户端与权限，如需旧模式回退须同时恢复支持该模式的旧二进制及匹配配置。

#### Scenario: 单文件注入动态别名
- **WHEN** 部署者在 deploy/accountsvc/.env 声明含下划线的客户端别名及其全部必需字段，并使用文档的单文件 Compose 命令
- **THEN** Compose config 与容器环境包含该别名字段，无需额外 override，服务按显式权限认证

#### Scenario: 默认无服务客户端
- **WHEN** 使用默认模板且未配置服务客户端或信任参数
- **THEN** 单文件 Compose 可解析，serve 正常启动并为两个未配置能力返回各自 NOT_CONFIGURED 503
