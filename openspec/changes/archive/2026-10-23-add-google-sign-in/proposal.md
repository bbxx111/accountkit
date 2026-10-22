# Proposal

## Why

宿主需要与微信、Apple 并列的 Google 登录。用户首次用 Google 进入时，应能看到已验证邮箱的补全预填，以及来自 Google 的显示名和头像；账号归属仍须再验证手机或邮箱，不能凭 token 里的邮箱自动合并。

## What Changes

- 新增 Google 身份种类与 `POST /users:signInWithIdp` 的 `google` 凭证：校验 ID token，以 `sub` 作为身份主键，支持登录、绑定和既有补全路径。
- 已验证邮箱作为 pending 响应的可选 `hint_email`，供补全页预填；发码和完成仍提交用户最终选择的目标并验证验证码。Apple 已验证邮箱使用同一字段。
- token 中的 `name`、`picture` 写入该 Google 身份的 `provider_meta.profile`。该身份第一次落到账号上时，仅当 `display_name` 或 `avatar_url` 仍为空才填充；之后登录只更新身份上的声明，不覆盖用户资料。
- 显示名上限从 32 放宽到 128 个 Unicode 码位，适用于本人更新和 Google 首次填充。已有较短显示名保持有效。
- `GOOGLE` 追加为枚举值 5；`display_name` 已是不限长的 `TEXT`。数据库只改当前 `0001` 中 `identity.kind` 与 `provider_meta` 的注释，不新增列、索引、约束或 `0002`。

## Capabilities

### New Capabilities

- `google-sign-in`: Google ID token 校验、身份主键、凭证与配置、身份声明存储，以及首次落到账号时对空资料的一次性填充。

### Modified Capabilities

- `user-basic-profile`: 显示名最多 128 个 Unicode 码位；明确一次性写入已校验的空头像地址不改变宿主对头像文件和持续同步的责任。
- `sign-in-completion`: pending 响应可带已验证的 `hint_email`。该值不表示邮箱已绑定，也不因目标是否已注册而变化。
- `accountsvc-runtime`: 显式启用 `GOOGLE` 时要求 client ID；仅 Google 且 Required 为空的服务可以在无 SMTP 时启动。
- `standardized-metrics`: Google 使用缓存 JWKS 继续验证的降级与 Apple 一样可单独观测。
- `migration-safety`: 本变更对当前 `0001` 的修改限于身份种类和 `provider_meta` 注释，并只更新当前初始哈希。
- `versioned-auth-storage`: 同一注释修改并入当前初始定义，新安装仍为 version 1，不新增生产迁移。

## Impact

- 库配置与 `accountsvc` 环境变量增加 Google client ID 与 JWKS 地址；空 client ID 列表表示未启用。校验需要访问 Google JWKS，失败为 `503 IDP_UNAVAILABLE`。
- 消费者登录、绑定请求增加 `google` 成员。pending 与成功 token 响应增加可选 `hint_email` 的使用范围；身份列表和 `:reveal` 仍不返回 `provider_meta`。
- `EnabledIdentityKinds` / `RequiredIdentityKinds` 的现有规则继续适用：`GOOGLE` 不是锚点，不能列入 Required。默认启用仍只包含已配置的第三方。
- 公开资料契约放宽显示名长度。自行按 32 个字符截断或排版的宿主需要接受最长 128 个字符。
- 不保存 Google access/refresh token，不限制 Workspace 域名，不下载或转存头像，不按邮箱自动关联已有账号。已发布 tag、冻结源和 `0001` down 保持不变。
