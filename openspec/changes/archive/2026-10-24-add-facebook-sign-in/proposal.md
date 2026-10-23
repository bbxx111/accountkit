# Proposal

## Why

宿主需要与微信、Apple、Google 并列的 Facebook 登录，覆盖 iOS、Android 与 Web 三端。Facebook 在 iOS 拒绝跟踪时下发 OIDC id_token（Limited Login），其他场景下发不透明的 user access token（Classic Login），两种凭证对应同一个 App-scoped User ID，现有的配置 OIDC 提供方无法同时承接。Meta 平台政策还要求使用 Facebook Login 的应用响应数据删除请求，而当前库没有按第三方 subject 发起、由外部系统触发的领域入口，宿主无法自行实现。

## What Changes

- 新增内置身份类型 `FACEBOOK`。第三方登录与绑定接受两种互斥的 credential：`{id_token, nonce}`（Limited Login）与 `{access_token}`（Classic Login），两者解析出同一 subject。
- Limited Login 复用现有 ID token 校验核心：RS256、issuer `https://www.facebook.com`、aud 为 App ID、nonce 校验与登记。Classic Login 由服务端调用 Graph API 的 `debug_token` 与 `/me` 校验，所有调用带 `appsecret_proof`；Classic token 不做一次性限制。
- 一个 accountkit 实例只对接一个 Facebook App，subject 为 App-scoped User ID。name、picture 按通用的提供方资料声明规则处理，email 只作为 `hint_email`。
- 新增库配置与 `ACCOUNTKIT_FACEBOOK_*` 环境变量：App ID、App Secret、Graph API 版本、Graph 与 JWKS 地址覆盖、NonceTTL。App ID 为空表示未启用。
- 新增 Facebook `signed_request` 解析校验，以及按第三方身份处理数据删除请求的领域方法：
  - 找不到活动身份时幂等成功；
  - 去掉该身份后仍有有效的 Enabled 身份时，强制解绑、清空该身份的提供方元数据并吊销以它为认证来源的会话，不检查 Required；
  - 否则走账号注销，宿主 `BeforeDelete` 拒绝时返回“被阻止”且不改动数据。
  - 返回确认码与处理状态。HTTP 回调端点与状态查询页由宿主提供。
- 追加审计事件类型，记录数据删除请求的处理结果；追加 Facebook 认证方法指标值与缓存密钥降级场景。
- 不实现 Deauthorize 回调，不保存 Facebook access token，不按邮箱合并账号，不支持多个 Facebook App 共享账号。

## Capabilities

### New Capabilities

- `facebook-sign-in`: Facebook 两种凭证的校验、身份主键、配置与启用、依赖故障分类、身份声明与邮箱提示。
- `provider-data-deletion`: 由第三方平台按 subject 发起的数据删除：Facebook `signed_request` 校验、强制解绑与注销兜底的分支规则、宿主阻止时的语义、幂等、确认码与审计。

### Modified Capabilities

- `identity-provider-registry`: 内置类型增加 FACEBOOK；统一凭证格式增加 FACEBOOK 的两种互斥 credential 形状。
- `accountsvc-runtime`: 显式启用 FACEBOOK 时要求 App ID 与 App Secret；仅 Facebook 且 Required 为空的服务可在无 SMTP 时启动。
- `standardized-metrics`: Facebook 使用独立的认证方法值，缓存密钥继续验证的降级场景独立于 Apple、Google 与 OIDC 可见。

## Impact

- 代码：`enum`（类型、审计事件）、`user/idp`（Facebook 提供方与 signed_request）、`user`（数据删除领域方法与强制解绑路径）、`config.go` 与 `ConfigFromEnv`、`internal/accountsvc` 配置、`observability` 枚举值。
- 外部依赖：校验需访问 Facebook JWKS 与 Graph API，失败为 `503 IDP_UNAVAILABLE`。
- 公开 API：第三方登录与绑定新增可用的 kind，请求结构不变；新增宿主可调用的 Go 方法，不新增内置 HTTP 路由。
- 数据库：无结构变更、无新迁移，不修改 `0001`。`identity.kind` 已按格式 CHECK 存储字符串，`FACEBOOK` 无需迁移；`0001` 中列举内置类型的注释不随本变更更新。
- 文档：README 配置、`docs/go-integration.md`、`docs/accountsvc.md`、`docs/compatibility.md`、`deploy/accountsvc/.env.example`，以及宿主实现数据删除回调与状态页的说明。
