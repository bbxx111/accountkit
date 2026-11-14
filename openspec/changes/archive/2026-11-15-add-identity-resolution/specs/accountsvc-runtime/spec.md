# Spec Delta

## MODIFIED Requirements

### Requirement: 未配置能力的观测与部署示例

某接口无人获授权时 SHALL 返回原 NOT_CONFIGURED 503，并在 serve 启动时为该能力记录一次指出 SERVICE_CLIENTS 和 PERMISSIONS 配置类别的 WARN，不记录值。模板及接入文档 SHALL 提供分项变量的 Basic-only、Bearer-only、双方式、最小权限与重叠秘密轮换示例，区分 Basic 密码和 IdP client secret，说明 TLS、客户端凭据换令牌及旧配置的显式迁移。SHALL 不在部署示例中授予尚未实现的权限，不提供当前二进制恢复旧模式的步骤。

#### Scenario: 只启用清理能力
- **WHEN** 统一客户端仅被授予 purged_users.read
- **THEN** 服务正常启动，清理接口可用，内省返回 503 并有一次配置警告，不强制给客户端增加内省权限

#### Scenario: 示例可核验
- **WHEN** 使用模板的合成秘密分别配置三种认证方式组合
- **THEN** 配置可解析，权限与示例说明一致，Basic-only 不要求 OIDC 配置，身份解析示例仅授予已实现的 identities.resolve，不隐含内省或清理权限

### Requirement: 单一 Compose 配置文件

部署 SHALL 只使用 deploy/accountsvc/compose.yaml；accountsvc 服务 SHALL 直接通过 env_file: ./.env 注入 SERVICE_CLIENTS 及任意已声明别名字段，删除 compose.service-clients.yaml。基础 environment SHALL 不保留四项旧逐接口变量的默认注入。文档标准 Compose 命令 SHALL 使用 --env-file deploy/accountsvc/.env，与服务 env_file 指向同一文件，并区分 CLI 插值与容器注入职责。模板 SHALL 默认空注册表，不含可运行旧格式；迁移说明 SHALL 要求显式转换客户端与权限，如需旧模式回退须同时恢复支持该模式的旧二进制及匹配配置。

#### Scenario: 单文件注入动态别名
- **WHEN** 部署者在 deploy/accountsvc/.env 声明含下划线的客户端别名及其全部必需字段，并使用文档的单文件 Compose 命令
- **THEN** Compose config 与容器环境包含该别名字段，无需额外 override，服务按显式权限认证

#### Scenario: 默认无服务客户端
- **WHEN** 使用默认模板且未配置服务客户端或信任参数
- **THEN** 单文件 Compose 可解析，serve 正常启动并为三个未配置能力返回各自 NOT_CONFIGURED 503

## ADDED Requirements

### Requirement: 身份解析服务装配

serve SHALL 在 /v1/identities:resolve 装配独立服务认证的身份解析处理器，不挂入消费者或管理员 handler。SHALL 复用统一注册表并要求 identities.resolve，至少一个客户端持有该权限才启用该能力。无人持有时 SHALL 返回 503 IDENTITY_RESOLUTION_NOT_CONFIGURED，并按未配置能力约定记录一次警告。migrate SHALL 不解析服务权限变量或访问身份解析依赖。SHALL 不要求额外发送器、Redis 查询或 IdP 资料接口。

#### Scenario: 只启用身份解析
- **WHEN** Basic-only 客户端仅被授予 identities.resolve，其他必要服务配置有效
- **THEN** serve 启动，身份解析可用；内省与清理接口分别返回原 NOT_CONFIGURED 503，不需要 OIDC 配置

#### Scenario: 无解析授权
- **WHEN** 无客户端拥有 identities.resolve，无论请求是否携带有效凭据
- **THEN** 新路由返回 503 IDENTITY_RESOLUTION_NOT_CONFIGURED，不查询账号；原有获授权能力继续可用

#### Scenario: 权限与二进制回退
- **WHEN** 部署者启用新版身份解析或回退至不识别 identities.resolve 的旧版本
- **THEN** 文档要求新版显式授权并重启生效，旧版回退前移除该权限；不静默忽略未知权限
