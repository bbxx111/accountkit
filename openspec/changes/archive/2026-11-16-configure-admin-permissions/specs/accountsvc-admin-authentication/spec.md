# Spec Delta

## MODIFIED Requirements

### Requirement: 默认关闭的独立管理面

管理员认证 SHALL 默认关闭；关闭时管理面所有请求返回 503 `ADMIN_NOT_CONFIGURED`，消费者仍可使用。启用 SHALL 要求完整 issuer、管理员 API audience 和有效角色读取配置，不创建本地管理员账号。消费端 audience、登录客户端 ID 与管理员 API audience SHALL 分离。显式角色权限绑定允许为空；空绑定不关闭管理认证，也不产生内置角色授权。

#### Scenario: 无管理认证部署
- **WHEN** 服务未启用管理员认证
- **THEN** `/admin/v1` 返回既有未配置响应，邮箱登录和消费者内省不受影响

#### Scenario: 配置残缺
- **WHEN** 启用管理员认证但缺少必需参数，或禁用状态混入非空管理员认证配置或残留角色分项
- **THEN** 启动失败并指出配置冲突，不静默开放或忽略管理认证

#### Scenario: 已启用但无角色绑定
- **WHEN** 管理认证配置有效，绑定列表未设置、空或全空白且无孤立分项
- **THEN** 服务正常启动；有效管理员访问管理操作返回 403 `ADMIN_FORBIDDEN`，无效或缺失凭据仍返回 401，认证依赖故障仍按原规则返回 503

### Requirement: 外部 JWT access token 验证

服务 SHALL 从配置的 issuer 发现并获取 JWKS，校验发现 issuer 一致；只接受面向管理员 API 的 RS256 JWT access token，RSA 密钥至少 2048 位。issuer SHALL 为不含凭据、查询、片段和首尾空白的绝对 URL；`ACCOUNTSVC_MODE=production` 时 SHALL 只接受 HTTPS，discovery 返回的 `jwks_uri` 也 SHALL 为 HTTPS。`ACCOUNTSVC_MODE=development` 时 SHALL 另接受 `http://` issuer 与 `jwks_uri`，并在监听前记录一条指出 `ACCOUNTSVC_ADMIN_ISSUER` 的 WARN；两种模式下重定向都 SHALL 拒绝。验证 SHALL 检查签名、issuer、audience、非空 sub、必需 exp 及存在时的 nbf/iat，时间容差 30 秒。服务 SHALL 不信任 token 自带的远程密钥 URL 或转发头中的管理员身份。

#### Scenario: 消费者或错误受众凭据
- **WHEN** 消费者 token、登录客户端 ID token 或错误 issuer/audience/算法/签名的 token 访问管理面
- **THEN** 返回 401 与 Bearer challenge，不执行管理操作

#### Scenario: 管理员凭据有效
- **WHEN** 外部提供方签发正确管理员 API 受众的有效 JWT access token
- **THEN** 以 issuer + sub 识别管理员，展示用户名可缺失，继续执行对应路由的管理操作权限检查

#### Scenario: 生产模式拒绝 HTTP issuer
- **WHEN** `ACCOUNTSVC_MODE` 为 production（含未设置）且 `ACCOUNTSVC_ADMIN_ISSUER` 为 `http://` 地址
- **THEN** 启动失败并指出 `ACCOUNTSVC_ADMIN_ISSUER`，错误信息与此前相同，不回显值

#### Scenario: 开发模式接受 HTTP issuer
- **WHEN** `ACCOUNTSVC_MODE=development` 且 `ACCOUNTSVC_ADMIN_ISSUER` 为不含凭据、查询和片段的 `http://` 地址
- **THEN** serve 通过配置校验，启动日志在管理员 discovery 之前记录一条指出 `ACCOUNTSVC_ADMIN_ISSUER` 的 WARN，discovery、JWKS 加载与 `iss` 精确匹配按该 HTTP issuer 完成

### Requirement: 可配置角色与拒绝审计

角色 SHALL 从已验证 JWT 的可配置 JSON Pointer 提取字符串数组，默认 `/roles`；缺失视为空，类型错误拒绝凭据。角色名 SHALL 按原字符串精确匹配部署绑定，多个匹配角色的权限取并集；未知角色不授予权限。`operator`、`super-admin` SHALL 无内置权限、继承或绕过。权限不足 SHALL 返回 403 `ADMIN_FORBIDDEN` 且通过原契约记录一次拒绝审计；认证/依赖失败不得误记为已认证管理员的权限拒绝。

#### Scenario: 普通管理员与高权限管理员
- **WHEN** 部署采用文档示例，operator 访问 reveal/代办删除，或 super-admin 访问示例中的普通管理操作
- **THEN** 前者 403 且产生一次拒绝审计，后者按显式配置通过权限检查，最终操作仍受领域规则约束

#### Scenario: 内置名称没有默认权限
- **WHEN** JWT 角色包含 operator 或 super-admin，但没有对应显式角色绑定
- **THEN** 不因此获得任何权限，不自动补入其他角色

#### Scenario: 多角色合并与精确匹配
- **WHEN** 角色分别授予 users.read 与 audit_events.read，令牌同时命中两者并携带其他未知角色
- **THEN** 只获得两个已配置权限；大小写或首尾空白不同的角色不视为同名

#### Scenario: 缺失或畸形角色
- **WHEN** 合法令牌缺失角色字段，或角色字段为字符串、null、包含非字符串的数组
- **THEN** 前者认证通过但无角色权限，管理操作403；后者凭据401，不记录 ADMIN_FORBIDDEN

### Requirement: 显式外部令牌格式

管理员 SHALL 按 ADMIN_TOKEN_PROFILE 选择 default 或 rfc9068 格式，缺省为 default；未知格式 SHALL 在启动前拒绝，管理员禁用时非空显式profile SHALL 作为半配置拒绝。rfc9068 SHALL 校验 at+jwt/application/at+jwt header 及必需 iss/aud/exp/sub/client_id/iat/jti。两种格式 SHALL 保持 issuer+sub 管理身份，并通过显式角色绑定决定操作权限；不应用服务机器主体、服务权限或 token scope 规则。

#### Scenario: 禁止自动格式回退
- **WHEN** 配置 rfc9068 但令牌缺少必需声明或访问令牌类型
- **THEN** 管理认证401，不回退到 default 格式

#### Scenario: 默认模式改名
- **WHEN** 管理认证启用且 ADMIN_TOKEN_PROFILE 省略、为空或为 default，或者显式使用旧模式值 azp
- **THEN** 前三者采用相同默认校验且不要求 azp 声明；旧模式值在启动前拒绝，不作为别名

#### Scenario: scope 不授予或收窄管理权限
- **WHEN** 已认证令牌的角色绑定相同，而 scope 缺失或含不同字符串，包括 users.*、identities.resolve
- **THEN** 管理操作授权结果相同；scope 不扩大或减少角色映射权限

## ADDED Requirements

### Requirement: 管理操作权限目录

管理权限 SHALL 使用大小写敏感的 `资源.操作`，不含 account 命名空间。目录 SHALL 包含 users.read、users.freeze、users.unfreeze、users.delete、users.undelete、identities.reveal、sessions.read、sessions.revoke、sessions.revoke_all、audit_events.read。权限 SHALL 相互独立；管理目录与服务调用目录独立，未知权限不得授权。

#### Scenario: 查询与变更独立
- **WHEN** 角色只获 users.read，或只获 users.freeze
- **THEN** 前者可列表/查看用户但不能冻结；后者仅可冻结，不隐含用户查询或解冻权限

#### Scenario: 服务权限不能混入管理目录
- **WHEN** 管理角色配置含 tokens.introspect、purged_users.read 或 identities.resolve
- **THEN** 启动校验拒绝，不借用服务权限目录扩展管理权限

### Requirement: 管理路由逐操作授权

11 条管理路由 SHALL 分别检查对应权限：用户列表/详情为 users.read；冻结/解冻/删除/恢复分别为 users.freeze/users.unfreeze/users.delete/users.undelete；身份明文为 identities.reveal；会话列表/单个撤销/全部撤销分别为 sessions.read/sessions.revoke/sessions.revoke_all；认证审计为 audit_events.read。权限检查 SHALL 先于业务解析与执行，保留路由、DTO、领域校验、错误协议与敏感响应 no-store。

#### Scenario: 全部操作可独立验证
- **WHEN** 对每条已挂载管理路由分别提供仅有所需权限和没有所需权限的有效管理员
- **THEN** 前者进入既有业务处理，后者403且业务方法未调用；users.read 同时覆盖两个查询路由

#### Scenario: 无权限的畸形业务请求
- **WHEN** 已认证但无所需权限的管理员发送非法资源 ID、畸形请求体或错误查询参数
- **THEN** 先返回403并记录一次拒绝审计，不解析业务输入或查询目标存在性

### Requirement: 资源级权限通配符

角色权限配置 SHALL 仅接受精确权限及 `<资源组>.*`；资源组限管理目录中的 users、sessions、identities、audit_events。通配符 SHALL 在启动时按该版本目录展开为不可变的具体权限集合；重复及重叠权限去重。SHALL 拒绝全局星号、跨组匹配、任意 glob、正则、排除式和未知组；令牌角色及 scope 不进行通配匹配。

#### Scenario: 用户资源通配符边界
- **WHEN** 仅授予 users.*
- **THEN** 可执行用户查询、冻结、解冻、删除、恢复，但不能查看身份明文、查询/撤销会话或查询审计，不按 URL 子路径推断权限

#### Scenario: 同名资源组的身份面隔离
- **WHEN** 管理角色授予 identities.*
- **THEN** 仅展开到管理目录的 identities.reveal，不授予服务 identities.resolve，也不使管理员 token 可代替服务 token

#### Scenario: 非法通配与去重
- **WHEN** 配置 *、*.read、users.fr*、users.**、unknown.* 或排除表达式；或者配置 users.read,users.*,users.read
- **THEN** 前一组启动失败；后一组合法且每个具体权限只保留一次

### Requirement: 通配符跨版本扩展

`<资源组>.*` SHALL 表示该组当前及未来新增操作。新二进制启动时 SHALL 按新目录重新展开，不固定为首次部署的旧权限集合；具体权限枚举 SHALL 不因新增目录项而扩大。SHALL 不引入自动扩展开关或策略目录版本锁定；发布说明须列明新增权限及受影响通配符组。

#### Scenario: 新版本增加同组操作
- **WHEN** 新版本目录新增一个 users 组操作且部署原配置为 users.*
- **THEN** 新实例获得该新增权限；同条件下仅列出旧具体权限的实例没有新增权限；旧进程不热更新
