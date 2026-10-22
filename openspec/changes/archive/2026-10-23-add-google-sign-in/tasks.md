# Tasks

## 1. 显示名上限

- [x] 1.1 将显示名上限改为 128 个 Unicode 码位，保留去空白、控制字符和双向格式控制符拒绝、空字符串清空。用本人更新测试验证 128 个码位成功、129 个码位和禁止字符返回参数错误且不改原值。
- [x] 1.2 更新 README 与基础资料文档中的 32 个字符上限，并核对文档不再要求显示名短于 128。

## 2. Google 校验器

- [x] 2.1 追加 `GOOGLE = 5`，扩展枚举解析、`Valid` 和锚点判断。用枚举测试验证 `GOOGLE` 往返、它不是锚点或验证码渠道，且 Required 不能包含它。
- [x] 2.2 实现 Google ID token 校验：RS256、两个 iss、aud 与 azp 允许列表、30 秒偏差、原始 nonce 的常量时间比较，以及覆盖 `exp` 的 nonce 登记。用校验器测试验证合法 token、摘要形式 nonce 被拒绝、第二个 iss、错误受众、重放和未启用。
- [x] 2.3 实现 JWKS 懒加载、未知 kid 节流刷新和缓存降级。用测试验证无缓存时的不可用、刷新失败不记成凭证无效，以及缓存密钥仍可完成验证。

## 3. 身份声明与资料填充

- [x] 3.1 扩展 `idp.Identity` 和 `provider_meta`，使 name、picture 与微信 openids 能共存。用服务测试验证创建 Google 身份写入 `profile`，后续非空声明覆盖、缺失字段保持，且合并 openids 不删除 `profile`。
- [x] 3.2 在直接注册、补全创建、补全关联和绑定首次插入 Google 身份时，于账号行锁内只填充仍为空的显示名和头像。用服务测试验证截断到 128、非法头像跳过、已有资料保持，以及再次登录不改这两列。
- [x] 3.3 让已验证邮箱成为 hint，且不创建 EMAIL 身份、不按邮箱查找账号。用服务测试验证未验证邮箱没有 hint，Required 为空时相同邮箱仍注册新账号。
- [x] 3.4 按用户授权只修改当前 `0001` 中 `identity.kind` 与 `provider_meta` 的注释，并只更新当前 `initialFrozen`。用迁移检查验证生产目录仍只有 `0001`、新安装为 version=1/dirty=false，已发布 tag 与冻结源字节不变，且没有新增列或 `0002`。

## 4. 补全预填与 HTTP

- [x] 4.1 pending 响应在初始证明含已验证邮箱时返回 `hint_email`，否则省略；发码和完成仍要求显式 target。用补全测试验证已注册和未注册邮箱返回相同 hint，省略 target 返回 400，且日志不包含该邮箱。
- [x] 4.2 为登录和绑定增加与 wechat、apple 互斥的 `google` 凭证。用 HTTP 测试验证成功登录、缺字段、并列凭证，以及身份列表和 reveal 不返回 `provider_meta`。

## 5. 配置、服务与指标

- [x] 5.1 增加 `GoogleClientIDs`、`GoogleNonceTTL`、`GoogleJWKSURL` 及对应环境变量，空列表不装配校验器。用配置测试验证显式启用但缺少 client ID 时启动失败并指出 `ACCOUNTKIT_GOOGLE_CLIENT_IDS`，仅 Google 且 Required 为空时不要求 SMTP。
- [x] 5.2 增加 `verify_google` 和 `google_cached_key_fallback`，标签只使用固定常量。用指标测试验证缓存降级单独计数，且不记成 Apple 降级或凭证无效。
- [x] 5.3 更新 README、迁移说明、兼容说明、Go 集成说明和 `deploy/accountsvc/.env.example`，写明 `0001` 注释授权、Google 凭证、JWKS、pending `hint_email` 与回退限制。核对示例配置没有 client secret，且未配置时 Google 登录保持关闭。

## 6. 集成核对

- [x] 6.1 在 `GOWORK=off` 下运行相关包的 `go test`、迁移历史检查与 `openspec validate --strict`。确认 sqlc 无需因注释变更重新生成。把命令和结果写入本变更的 `verification.md`。

## Workflow follow-up

- 实现和审查完成后再归档本变更。
- 归档时把 delta spec 同步到主规格，并修正验收记录中的相对链接。
