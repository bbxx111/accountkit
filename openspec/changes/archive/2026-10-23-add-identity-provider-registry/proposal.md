# Proposal

## Why

微信、Apple、Google 三种第三方登录在请求格式、`Deps` 字段、能力校验、配置指纹和资料回填上都有逐个写死的分支，`IdentityKind` 也是只能由本包追加的 SMALLINT 枚举。每接一个标准 OIDC 提供方（企业 SSO、Keycloak、Auth0 等）都要改包代码和 0001。本变更是“宿主自定义登录方式”的第一期：先把身份类型改为字符串，建立统一的提供方注册与验证契约，并提供按配置启用的通用 OIDC 验证器，让多数 OIDC 场景只需配置即可接入。

## What Changes

- **BREAKING** `enum.IdentityKind` 改为字符串底型，取值即 API 名（`PHONE`、`EMAIL`、`WECHAT`、`APPLE`、`GOOGLE` 及配置的 OIDC 类型），零值 `""` 表示未指定。`identity.kind` 改为 `VARCHAR(32) COLLATE "C"`，带格式 CHECK（类型名 2–32 个 ASCII 字符；列定义兼顾将来可能的 MySQL 移植，但多数据库不是本次目标）；相关索引、锚点加锁、管理筛选查询同步改为文本。直接修改当前 0001，不新增 0002，不提供旧数据迁移。
- 新增身份提供方注册表：装配时一次性注册、不可变；类型名须符合 `^[A-Z][A-Z0-9_]{1,31}$`，不得与内置类型重名。数据库中存在但当前未注册的类型仍可列出、解绑、匿名化和审计，只是不能用于登录或新绑定。
- 新增统一提供方接口 `idp.Provider`（`Kind`、`Verify(ctx, json.RawMessage)`、`PolicyFingerprint`）。微信、Apple、Google 改为实现该接口；`user.Deps` 的 `WeChat`/`Apple`/`Google` 三个字段与三个专用验证器接口移除，改为 `Providers []idp.Provider`。`user.IdpCredential` 改为 `{Kind, Credential json.RawMessage}`。
- **BREAKING** C 端所有身份凭证统一为顶层 `{"kind":"...","credential":{...}}`，包括 PHONE、EMAIL；不保留 `phone`、`email`、`wechat`、`apple`、`google` 成员，`CREDENTIAL_ONEOF` 不再使用。各类型 `credential` 内部字段保持原名（`target`/`code_id`/`code`、`app_id`/`code`、`id_token`/`nonce`）。各端点允许的类型范围不变。
- 新增通用 OIDC 验证器：按配置的类型名、issuer、JWKS 地址、client ID 列表与 nonce 有效期校验 RS256 ID token，以 `sub` 为身份主键；昵称与头像的声明名可按提供方配置（默认 `name`、`picture`，显式空列表关闭）；`Config.OIDCProviders` 与 `ConfigFromEnv` / accountsvc 环境变量可启用多个。
- 资料声明与首次填充空资料从“仅 Google”改为“任何返回 name/picture 的提供方”；内置微信、Apple 不返回这两项，行为不变。已验证邮箱提示同样适用于 OIDC。
- 登录补全的配置指纹改为覆盖全部已注册提供方的 `PolicyFingerprint`（当前 Google 未计入）。
- 指标新增有界的 `oidc` 方法值与 OIDC 缓存密钥降级场景；类型名不作为指标标签。

## Capabilities

### New Capabilities

- `identity-provider-registry`: 身份类型命名、提供方注册与不可变快照、统一验证契约与错误分类、C 端统一凭证格式与各端点允许的类型、未注册历史类型的处理。
- `oidc-sign-in`: 通用 OIDC ID token 校验、配置与启用规则、身份主键、资料声明映射与邮箱提示、依赖故障。

### Modified Capabilities

- `google-sign-in`: Google 凭证改为 `kind=GOOGLE` 的统一凭证；资料声明规则由通用提供方规则承担，Google 行为保持。
- `sign-in-completion`: 完成接口的凭证改为统一格式，只接受 PHONE、EMAIL。
- `identity-policy`: Enabled 可包含已注册的任意提供方类型，未知类型以注册表判定；Required 仍只允许 PHONE、EMAIL；能力校验改为查询注册表。
- `accountsvc-runtime`: 服务可通过环境变量启用 OIDC 提供方，显式启用但缺少配置时指出对应变量。
- `standardized-metrics`: OIDC 登录使用固定的 `oidc` 方法值，OIDC 缓存密钥降级单独可见。
- `versioned-auth-storage`: 当前初始定义中 `identity.kind` 改为文本及格式 CHECK，新安装仍为 version 1。
- `migration-safety`: 记录本次按用户授权直接修改 0001 的范围，只更新当前 initialFrozen。

## Impact

- 公共 Go API：`enum.IdentityKind` 底型、`idp.Identity.Kind`、`idp` 三个专用验证器接口、`user.Deps`、`user.IdpCredential`、`accountkit.Config`（新增 `OIDCProviders`）。宿主按 kind 数值做比较或持久化的代码需要调整。
- HTTP：`signInWithCode`、`signInWithIdp`、`completeSignIn`、`reauthenticate`、绑定与换绑的请求体格式全部变化，客户端须同步升级；发码接口的 `channel`/`target` 不变。身份列表、管理筛选等输出与输入的类型字符串不变。
- 数据库：当前 0001 的 `identity.kind` 列类型（`VARCHAR(32)`）、4 个以 `kind` 开头的部分唯一/普通索引的列类型、新增格式 CHECK；同步 sqlc、运行时预检、当前 initialFrozen 与 AGENTS.md 迁移授权记录。同号旧库不重放 0001，不自动转换，不 Force、改版本或清库。
- 进行中的登录补全流程在升级后失效（载荷中的类型编码与配置指纹均变化），客户端需重新开始。
- 第二期另起变更：宿主在 `accountkit.Deps` 注入自写 Provider、能力声明（重新认证、资料回填开关等）、`provider_meta` 扩展区。本期不开放这些能力；`PHONE`、`EMAIL` 的锚点与验证码渠道语义保持内置。
