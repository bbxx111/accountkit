# Proposal

## Why

现有微信验证器只调用 OAuth 接口并强制使用 UnionID，不能处理小程序 code 或仅使用应用内 OpenID 的部署。用户已选择两组应用凭据和实例级固定 subject 模式，避免自动升级或账号合并。

## What Changes

- 保留 `WECHAT_APPS`，增加 `WECHAT_MINI_PROGRAM_APPS`，两者均为 `appid:secret,...`。
- 增加全局 `WECHAT_SUBJECT_MODE=UNIONID|OPENID`，默认 UNIONID；两组应用共享，启动时拒绝未知模式和重复 AppID。
- 小程序通过 jscode2session 验证；OAuth 保留原接口；根据服务端 AppID 配置选择流程。
- **BREAKING**：微信 subject 使用 `unionid:<unionid>` 或 `openid:<appid>:<openid>`，不转换已有身份，不自动升级、降级、合并或依元数据登录。
- provider_meta 保存已验证 unionid、按 AppID 分组的 openids；保留现有 profile 规则，不保存 code、token、session_key、secret 或任意原始响应。
- 库、accountsvc、策略指纹、测试及文档同步支持新配置。

## Capabilities

### New Capabilities
- `wechat-sign-in`: 微信两类验证流程、全局标识模式与身份元数据。

### Modified Capabilities
无。沿用身份提供方注册、统一凭证、账号完整性和登录补全契约。

## Impact

涉及 config.go、accountkit.go、user/idp、身份元数据读写、accountsvc 配置及相关文档测试。HTTP 继续使用 kind/credential，无新路由；无数据库 DDL、迁移修改或新增依赖。已存裸 UnionID 与新 subject 不自动匹配，需要在兼容指南明确仅适用于新身份数据及配置切换后果。
