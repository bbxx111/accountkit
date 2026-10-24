# Proposal

## Why

第三方资料补填目前按提供方写死两套规则：ALIPAY、QQ、WEIBO 在首次及后续登录补填空资料，其他提供方只在身份第一次写入账号时填充一次。宿主无法关闭已有账号上的自动补填，也无法让 Google、OIDC、Facebook 等获得同样的补填。本变更把规则统一为与身份类型无关的一套逻辑，并由一个默认开启的配置项控制。

## What Changes

- 新增配置项 `Config.ProviderProfileBackfill *bool`（`nil` 视为 `true`）及环境变量 `<prefix>PROVIDER_PROFILE_BACKFILL`，accountsvc 经 `ConfigFromEnv` 同步支持；`user.Deps` 增加同名字段。
- 新建账号时始终填充：第三方登录直接注册，或登录补全创建新账号时，无论配置如何，都用本次已验证并净化后的 name/picture 填充仍为空的 display_name/avatar_url。
- 已有账号按配置补填：配置开启时，已有身份再次登录、绑定到已有账号（含同账号重复绑定）、登录补全关联到已有账号，都补填账号仍为空的显示名和头像，不覆盖非空值；配置关闭时，这些路径不写账号资料。
- **BREAKING（行为）**：所有身份类型适用同一逻辑，移除 ALIPAY/QQ/WEIBO 专用集合。默认开启时，GOOGLE、OIDC、FACEBOOK 等提供方在后续登录也会补填空资料；用户主动清空的显示名或头像会在下一次第三方登录时被重新填充。
- **BREAKING（行为）**：配置关闭时，绑定第三方身份到已有账号不再填充空资料（原先对所有提供方都会填充）。
- 补填只使用本次验证取得的声明，不使用身份元数据里的历史 profile。`provider_meta.profile` 的记录与覆盖规则不受配置影响；资料查询失败仍不阻断登录。WECHAT、APPLE 不返回资料，行为不变。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `user-basic-profile`：补填规则由“提供方集合”改为“新建账号始终填充 + 已有账号按配置补填”，原子性要求覆盖所有提供方。
- `identity-provider-registry`：提供方资料声明规则不再区分补填集合，账号填充引用统一规则。
- `google-sign-in`：“首次填充空资料”改为遵循通用规则，后续登录在配置开启时可补填空字段。
- `oidc-sign-in`：OIDC 资料声明映射改为参与统一补填规则（新建账号填充，已有账号按配置补填）。
- `accountsvc-runtime`：服务接受 `PROVIDER_PROFILE_BACKFILL` 并拒绝非法布尔值。

## Impact

- 代码：`config.go`（字段、默认值、环境变量）、`accountkit.go`（传入 `user.Deps`）、`user/service.go`、`user/service_idp.go`（删除 `refillsProfileOnSignIn`，统一 `verifyIdp` 声明净化）、`user/service_signin_completion.go`、`user/service_identity.go`（区分新账号与已有账号的调用）。
- HTTP 请求与响应、数据库结构、迁移、指标与审计均不变。
- 文档：README 配置表与资料补填说明、`docs/go-integration.md`、`docs/accountsvc.md`、`docs/compatibility.md`、`deploy/accountsvc/.env.example`，以及描述 ALIPAY/QQ/WEIBO 专用补填的提供方接入指南。
- 测试：现有“集合外提供方不补填”的断言需要改为按配置判定；新增开关关闭、新建账号不受开关影响、Google/OIDC/Facebook 后续补填，以及真实 PostgreSQL 的并发与回滚用例。
