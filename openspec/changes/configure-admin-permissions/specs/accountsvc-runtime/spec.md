# Spec Delta

## MODIFIED Requirements

### Requirement: 可选服务与库独立消费

项目 SHALL 提供可独立构建的 accountsvc 可执行程序；直接集成 accountkit 的宿主 SHALL 无需运行该服务。两种方式 SHALL 使用同一新版消费者协议；保留 users 资源、身份数据、成功令牌格式和库生命周期契约，登录待完成结果及相关 Go/HTTP 升级 SHALL 有明确迁移说明。accountsvc 管理面 SHALL 使用显式角色权限绑定，既有嵌入宿主 SHALL 保留旧角色验证接口兼容。

#### Scenario: 独立使用库
- **WHEN** 外部 Go 宿主只依赖 accountkit 并自行注入依赖
- **THEN** 在 GOWORK=off 下能够构建和运行，不要求 accountsvc 配置或网络连接

#### Scenario: 服务消费既有认证能力
- **WHEN** accountsvc 挂载消费者和管理面
- **THEN** 分别使用 `/v1` 和 `/admin/v1`，请求响应、刷新宽限及领域规则沿用库契约，管理操作通过新权限接口按显式配置授权，不回退旧角色检查

### Requirement: migrate 静态校验与依赖边界

migrate SHALL 只执行静态身份策略语法/集合校验及迁移必要检查，不监听、不构造认证处理器，不要求发送器或第三方运行时依赖。migrate SHALL 不要求或解析服务监听地址、HTTP TLS 开关、证书、SMTP、短信服务商、邮件品牌、旧逐接口调用配置、统一服务客户端/issuer/audience 配置或管理员认证与角色权限绑定；迁移锁与密钥版本检查仍执行。

#### Scenario: 迁移独立于服务运行配置
- **WHEN** migrate 的身份集合合法且迁移必要依赖有效，未提供服务运行配置
- **THEN** 迁移可执行，不读取无关监听/TLS/发送/短信/内省设置，仍核对迁移锁和存储引用密钥

#### Scenario: 服务认证配置错误不阻止迁移
- **WHEN** migrate 的迁移必要配置有效，但存在非空白旧逐接口变量、孤立客户端字段、非法新列表/权限或不可达服务 issuer
- **THEN** 不读取这些服务认证配置、不访问 issuer，仍按原迁移契约执行

#### Scenario: 管理权限配置错误不阻止迁移
- **WHEN** migrate 的必要配置有效，但管理员配置含非法绑定、孤立字段或不支持的授权模式项
- **THEN** 不解析这些管理员运行配置，不访问管理员 issuer，仍按原迁移契约执行

### Requirement: 路由访问类别清单

服务 SHALL 保持消费者、管理员、服务调用、公共元数据和运维五类路由的可核对清单；三个服务端点分别标明 tokens.introspect、purged_users.read、identities.resolve；11 个管理操作 SHALL 登记各自的管理权限。清单 SHALL 与实际挂载一致，不改变 URL、方法或既有错误顺序，不把类别标记当作认证或网络隔离。OpenAPI SHALL 以所需权限描述 accountsvc 授权，并单独标识旧嵌入角色兼容含义，不将固定角色当作 accountsvc 的授权前提。

#### Scenario: 清单与实际路由一致
- **WHEN** 检查服务路由挂载和接入文档
- **THEN** 三个服务端点及其权限一致，11 个管理操作的权限与路由一致，消费者与管理员分别验证，JWKS 与运维边界保持

## ADDED Requirements

### Requirement: 管理角色分项环境变量

serve SHALL 用 `ACCOUNTSVC_ADMIN_ROLE_BINDINGS` 声明逗号分隔的别名，按 `ACCOUNTSVC_ADMIN_ROLE_<ALIAS>_NAME` 和 `_PERMISSIONS` 读取实际角色名和权限。别名 SHALL 匹配 `[A-Z][A-Z0-9_]{0,31}` 且唯一；实际角色名 SHALL 唯一、为1–256字节的有效 UTF-8、无首尾空白及控制字符，不裁剪或转换大小写，内部空格和逗号允许。主列表项裁剪首尾空白后 SHALL 拒绝空项与重复项。每个声明角色 SHALL 提供非空有效权限列表。

#### Scenario: 别名与真实角色名解耦
- **WHEN** 别名为 CUSTOMER_SUPPORT，NAME 为 customer-support，令牌包含同名角色
- **THEN** 按该绑定授权，无需把真实角色名编码进环境变量名；别名中的下划线不造成字段解析歧义

#### Scenario: 实际角色精确保存
- **WHEN** NAME 为合法的含内部空格或逗号的角色名
- **THEN** 按单个标量保存并精确匹配令牌角色，不将 NAME 按逗号拆分

#### Scenario: 重复或缺失配置
- **WHEN** 别名重复、实际角色名重复、别名非法，或声明的 NAME / PERMISSIONS 缺失或为空
- **THEN** serve 在监听前失败，指出具体配置变量名，不输出变量值

### Requirement: 管理权限列表解析

`ADMIN_ROLE_<ALIAS>_PERMISSIONS` SHALL 使用逗号分隔，项首尾空白裁剪后按管理权限目录及通配符规则校验；空项、未知权限和非法模式 SHALL 拒绝，重复与重叠权限 SHALL 去重。环境变量的列表语法 SHALL 不改变 OAuth scope 的空格分隔协议，也不改变服务权限列表的既有校验和无通配符规则。

#### Scenario: 合法列表规范化
- **WHEN** PERMISSIONS 为 `users.read, sessions.*,users.read`
- **THEN** 获得 users.read 与该版本全部会话权限的去重集合

#### Scenario: 非法分隔或空项
- **WHEN** PERMISSIONS 为 `users.read users.freeze`、`users.read,` 或 `users.read,,sessions.read`
- **THEN** 监听前拒绝并指出对应 PERMISSIONS 变量

### Requirement: 无隐式管理授权与严格残留检查

主列表未设置、空或全空白 SHALL 产生空绑定；SHALL 不回退内置角色或引入 `ADMIN_AUTHORIZATION_MODE`。serve SHALL 拒绝非空白的 `ACCOUNTSVC_ADMIN_AUTHORIZATION_MODE`，以及 `ACCOUNTSVC_ADMIN_ROLE_` 前缀下除主列表和已声明 NAME/PERMISSIONS 外的变量，即使其值为空。管理认证禁用时，非空主列表或任何角色分项 SHALL 拒绝；空主列表占位允许，既有认证项校验保持。

#### Scenario: 空绑定无授权
- **WHEN** 管理认证已启用且主列表为空，无角色分项
- **THEN** 服务可启动，任何有效角色令牌均不能获得管理操作权限，不恢复 operator/super-admin 默认授权

#### Scenario: 孤立或未知字段
- **WHEN** 主列表未声明 SUPPORT 却存在 ADMIN_ROLE_SUPPORT_NAME，或已声明 SUPPORT 但存在 ADMIN_ROLE_SUPPORT_PERMISSION 拼写错误项
- **THEN** 无论该分项值是否为空，serve 均拒绝并指出变量名

#### Scenario: 管理面关闭时残留配置
- **WHEN** ADMIN_ENABLED=false，存在非空绑定列表或角色分项
- **THEN** 启动失败；仅空主列表占位且无其他管理配置时保持关闭

#### Scenario: 不支持模式开关
- **WHEN** serve 设置非空白 ADMIN_AUTHORIZATION_MODE=builtin 或 custom
- **THEN** 拒绝并指出该项不受支持，不解释为任何授权模式；此变量从未作为正式配置发布

### Requirement: 管理策略启动快照

serve SHALL 在监听前校验全部管理绑定并冻结角色到具体权限的映射；配置失败仅返回变量名和固定安全分类，不输出值。运行中的配置或原始 Go 输入变更 SHALL 不影响已装配策略；更新须重启全部副本或排空旧副本。策略 SHALL 不添加远程授权依赖或热更新；权限撤销与 JWT 中角色撤销的生效边界 SHALL 分开说明。

#### Scenario: 映射撤权后重启
- **WHEN** 从角色映射移除 users.freeze 并重启实例，管理员仍使用原有效令牌
- **THEN** 新实例拒绝冻结；旧进程在排空前仍可能按旧策略授权

#### Scenario: 提供方撤销角色
- **WHEN** 提供方撤销用户角色但旧 JWT 仍有效且本地映射未改变
- **THEN** 不宣称角色立即失效；文档说明其依赖新令牌、令牌到期或另行设计的撤权机制

### Requirement: 管理部署示例与迁移

模板 SHALL 展示显式 operator/super-admin 角色绑定，super-admin 示例使用 users.*、sessions.*、identities.*、audit_events.*；operator 示例限制用户操作，且可使用 sessions.*。文档 SHALL 列出全部现有与新增管理变量、空配置行为、通配符升级扩权及具体枚举替代。单文件 Compose SHALL 通过既有 env_file 透传任意声明别名，不枚举固定角色或注入默认绑定。

#### Scenario: 示例恢复当前能力
- **WHEN** 使用模板的显式角色绑定和满足既有条件的有效管理员 token
- **THEN** 当前 operator/super-admin 操作范围与原部署一致；示例明确通配符会包含未来同组操作，精确固定范围须枚举

#### Scenario: Compose 动态别名注入
- **WHEN** .env 使用含下划线的自定义别名，通过既有 compose.yaml 启动
- **THEN** 容器接收对应主列表和分项，无需增加 override 文件或在 compose.yaml 写死别名

#### Scenario: 旧部署升级及回退
- **WHEN** 旧部署升级新二进制，或计划恢复旧二进制
- **THEN** 升级须补充显式绑定并核验全部管理操作；回退须恢复匹配的旧角色签发与配置，明确旧内置授权可能扩大权限，不依赖清库或清 Redis
