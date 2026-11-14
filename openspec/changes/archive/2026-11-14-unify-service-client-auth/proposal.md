# Proposal

## Why

accountsvc 的内部接口需要让同一业务服务按最小权限访问多项能力；按接口配置独立凭据和客户端名单，会增加接入、秘密轮换及新增接口的维护成本。统一服务身份认证与能力授权，并采用客户端别名加分项环境变量，使 Basic 与 OIDC/JWKS Bearer 可以并存，部署方无需在环境变量里维护嵌套 JSON。

## What Changes

- 引入统一服务客户端注册表：客户端拥有明确的 ID、允许的认证方式和权限集合。同一客户端可同时启用 `basic,bearer`，两种方式共享权限；每个请求只接受一个 Authorization 头和一种认证方式，不在失败后自动切换。
- Basic 通过用户名和客户端独立秘密认证；Bearer 通过统一服务 issuer 的 OIDC Discovery/JWKS 校验 RS256 签名、issuer、audience、有效期及必要 claims，再用已验证的 `azp` 识别客户端。服务令牌与消费者、管理员令牌保持用途隔离；JWKS 公钥由签发方发布，accountsvc 不持有调用方的 OIDC 客户端秘密。
- 接口声明所需权限，注册表决定客户端可访问哪些能力。内省使用 `tokens.introspect`；清理拉取接口使用 `purged_users.read`。同一客户端允许持有多个权限，取消“一个客户端只能访问一个接口”的限制；未授予的权限默认拒绝，不从令牌角色或 scope 自动扩大权限。
- 使用 `ACCOUNTSVC_SERVICE_CLIENTS` 列出配置别名；每个别名对应 `ACCOUNTSVC_SERVICE_CLIENT_<ALIAS>_ID`、`_AUTH_METHODS`、`_PERMISSIONS`、`_BASIC_SECRETS`。列表使用逗号分隔；别名与真正客户端 ID 分离，避免连字符转换碰撞。
- Bearer 共用 `ACCOUNTSVC_SERVICE_ISSUER`、`ACCOUNTSVC_SERVICE_AUDIENCE`；仅启用 Basic 时无需 OIDC 配置。Basic 秘密支持新旧重叠轮换，与 OIDC 客户端秘密独立管理；配置在启动时加载，修改后重启生效。
- 启动时拒绝重复别名或 ID、空列表项、未知认证方式或权限、缺少必需秘密或 OIDC 配置、孤立的客户端配置。错误只指出变量名和安全分类，不输出秘密或令牌。serve 的服务调用配置不增加 migrate 对 OIDC 或调用凭据的依赖。
- 保持各接口原有请求、成功响应及业务语义；统一认证结果由各处理器映射为自身协议中的 401（认证无效）、403（权限不足）、503（依赖不可用或接口未配置）。Basic 验证通过但权限不足也返回 403。
- **BREAKING**：按用户在初次实现提交 `b25ec98` 后的明确要求，移除旧逐接口配置模式、模式选择、旧解析/装配/认证分支与弃用警告，只保留统一注册表。四项旧逐接口变量只要非空白即在监听前拒绝，含 `{}`、`[]`，无论是否设置新变量；空白旧占位忽略。迁移必须显式转换客户端与权限，不自动合并旧身份。回退须回退到支持旧格式的二进制及其匹配配置。
- `ACCOUNTSVC_SERVICE_CLIENTS` 未设置、空或全空白均产生空注册表；未声明别名字段仍拒绝。无客户端时 serve 正常启动，两项能力分别返回原 NOT_CONFIGURED 503；没有 Bearer 客户端时多余 issuer/audience 仍拒绝。migrate 不解析新旧服务运行变量。
- 合并为一个 `deploy/accountsvc/compose.yaml`，accountsvc 直接通过 `env_file: ./.env` 注入动态别名字段，删除额外 service-clients override。文档的 Compose CLI `--env-file deploy/accountsvc/.env` 与服务 env_file 指向同一文件，基础 environment 不再默认注入旧变量。

配置示例（拟议格式，秘密为占位符）：

```dotenv
ACCOUNTSVC_SERVICE_ISSUER=https://sso.example.com/realms/services
ACCOUNTSVC_SERVICE_AUDIENCE=accountsvc-service
ACCOUNTSVC_SERVICE_CLIENTS=AI_FOOD,REPORTING
ACCOUNTSVC_SERVICE_CLIENT_AI_FOOD_ID=ai-food-server
ACCOUNTSVC_SERVICE_CLIENT_AI_FOOD_AUTH_METHODS=basic,bearer
ACCOUNTSVC_SERVICE_CLIENT_AI_FOOD_PERMISSIONS=tokens.introspect,purged_users.read
ACCOUNTSVC_SERVICE_CLIENT_AI_FOOD_BASIC_SECRETS=<new-base64-secret>,<old-base64-secret>
ACCOUNTSVC_SERVICE_CLIENT_REPORTING_ID=reporting-service
ACCOUNTSVC_SERVICE_CLIENT_REPORTING_AUTH_METHODS=bearer
ACCOUNTSVC_SERVICE_CLIENT_REPORTING_PERMISSIONS=tokens.introspect
```

示例用于说明配置结构，不代表建议 ai-food 开启远程内省；实际部署仅授予所需权限。

## Capabilities

### New Capabilities

无；复用当前已有能力，避免重复定义服务认证。

### Modified Capabilities

- `accountsvc-service-authentication`: 统一客户端注册表、Basic/Bearer 共用能力授权、分项环境变量、配置迁移及认证挑战。
- `purged-user-feed`: 清理拉取只使用统一授权并要求 `purged_users.read`，保留分页与清理事务。

- `consumer-token-introspection`: 内省调用方接入统一认证并要求 `tokens.introspect`，保留内省协议与被查询消费者令牌的既有验证语义。
- `accountsvc-runtime`: 服务调用配置的加载、启动校验、可选能力配置和生命周期；迁移命令不依赖服务认证配置，部署示例改用分项环境变量。

## Impact

- 代码范围：`internal/accountsvc` 的配置、认证装配、内省处理器及相关测试；将清理接口处理器接入同一权限机制。配置不需要数据库迁移，不修改消费者 SDK、家庭业务或产品端角色。
- 文档范围：`docs/accountsvc.md`、兼容迁移说明、无秘密环境变量模板及远程调用示例；同步说明 TLS、OIDC client credentials、客户端秘密与 Basic 秘密的区别。
- 验收重点：同一客户端通过两种方式访问获准接口；未授予能力返回 403；未启用的认证方式、消费者/管理员凭据不能替代服务凭据；错误 issuer/audience/azp、过期令牌、JWKS 故障、秘密轮换、仅旧/混合旧配置拒绝、缺省空注册表、单一 Compose 动态字段注入及日志脱敏有明确覆盖。

### Baseline and Dependencies

已按用户要求获取最新远端 develop，并将本地 develop 与任务分支快进到 `ac3eff1f076f52d4b58b8e43269939342c4a0416`。本变更以该提交为基线，已有 `accountsvc-service-authentication`、`purged-user-feed`、Basic/Bearer 及数据库会话内省能力；不再依赖旧 v0.6.0 基线，也无需补建这些接口。消费者内省保持该基线的数据库吊销判定，统一的仅是服务调用方认证和授权。

### Non-goals

- 不在本次新增手机号/邮箱查询用户接口、邀请匹配、通知或家庭成员管理；`identities.resolve` 仅为讨论过的未来权限名称，待对应接口单独设计实现后再注册，当前配置使用未知权限应失败。
- 不新增清理 feed 的业务实现，不改变内省对消费者令牌的吊销判定，不修改消费者或管理员认证体系。
- 不引入权限管理后台、动态授权、配置热加载、通配符权限或多 issuer 联邦；不将服务端权限配置同步为 Keycloak 角色。
