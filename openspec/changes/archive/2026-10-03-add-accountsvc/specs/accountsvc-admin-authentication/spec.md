## Purpose

为可选 accountsvc 装配外部 OIDC 提供方的管理员 JWT 验证能力，保持消费者与管理员身份分离、逐路由角色授权和审计约束，并对默认关闭、密钥轮换和提供方故障给出可验证的运行行为。

## ADDED Requirements

### Requirement: 默认关闭的独立管理面

管理员认证 SHALL 默认关闭；关闭时管理面所有请求返回 503 `ADMIN_NOT_CONFIGURED`，消费者仍可使用。启用 SHALL 要求完整 issuer、管理员 API audience 和角色映射配置，不创建本地管理员账号。消费端 audience、登录客户端 ID 与管理员 API audience SHALL 分离。

#### Scenario: 无管理认证部署
- **WHEN** 服务未启用管理员认证
- **THEN** `/admin/v1` 返回既有未配置响应，邮箱登录和消费者内省不受影响

#### Scenario: 配置残缺
- **WHEN** 启用管理员认证但缺少必需参数，或禁用状态混入管理员认证参数
- **THEN** 启动失败并指出配置冲突，不静默开放或忽略管理认证

### Requirement: 外部 JWT access token 验证

服务 SHALL 从配置的 HTTPS issuer 发现并获取 JWKS，校验发现 issuer 一致；只接受面向管理员 API 的 RS256 JWT access token，RSA 密钥至少 2048 位。验证 SHALL 检查签名、issuer、audience、非空 sub、必需 exp 及存在时的 nbf/iat，时间容差 30 秒。服务 SHALL 不信任 token 自带的远程密钥 URL 或转发头中的管理员身份。

#### Scenario: 消费者或错误受众凭据
- **WHEN** 消费者 token、登录客户端 ID token 或错误 issuer/audience/算法/签名的 token 访问管理面
- **THEN** 返回 401 与 Bearer challenge，不执行管理操作

#### Scenario: 管理员凭据有效
- **WHEN** 外部提供方签发正确管理员 API 受众的有效 JWT access token
- **THEN** 以 issuer + sub 识别管理员，展示用户名可缺失，继续执行对应路由角色检查

### Requirement: 可配置角色与拒绝审计

角色 SHALL 从已验证 JWT 的可配置 JSON Pointer 提取字符串数组，默认 `/roles`；缺失视为空，类型错误拒绝凭据。super-admin SHALL 包含 operator 权限。角色不足 SHALL 返回 403 且通过原契约记录一次 ADMIN_FORBIDDEN；认证/依赖失败不得误记为已认证管理员的角色拒绝。

#### Scenario: 普通管理员与高权限管理员
- **WHEN** operator 访问 reveal/代办删除，或 super-admin 访问 operator 路由
- **THEN** 前者 403 且产生一次拒绝审计，后者通过角色检查，最终操作仍受领域规则约束

### Requirement: JWKS 轮换与故障分类

密钥缓存 SHALL 最多使用 1 小时，未知 kid 刷新 SHALL 合并并发请求且间隔至少 60 秒。可用缓存中的已知 key SHALL 允许提供方短时故障期间继续验证；没有可用 key 且依赖拉取失败时 SHALL 返回 503，成功刷新仍无对应 key 时 SHALL 返回 401。启用管理面的首次发现/密钥加载失败 SHALL 阻止启动。

#### Scenario: 正常换钥
- **WHEN** 提供方发布新 key 并签发未知 kid 的 token
- **THEN** 有界刷新后接受有效新 token，并发未知 kid 请求不形成无界拉取

#### Scenario: 提供方不可用
- **WHEN** 拉取失败且旧缓存已过期或不包含请求所需 key
- **THEN** 返回 503，不无限延长旧缓存、不把依赖故障误报为错误凭据；消费者接口仍按自己的规则工作
