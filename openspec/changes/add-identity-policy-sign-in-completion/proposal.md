## Why

当前身份启停分散在发送器和第三方应用配置中，PHONE/EMAIL 被硬编码为账号锚点。第三方首次登录先创建用户，再绑定已有账号的联系方式会发生冲突；部署者也无法统一选择可用身份和账号完整性条件。

本次用两个配置定义身份策略，在需要补全时延后建号，并按已经验证的手机或邮箱归属自动关联已有账号或注册新账号。账号 HTTP 资源统一采用 accounts，登录补全保持两个独立、用途明确的操作。

## What Changes

- 增加 `Config.EnabledIdentityKinds` 与 `Config.RequiredIdentityKinds`，环境变量沿宿主前缀读取 `ENABLED_IDENTITY_KINDS` / `REQUIRED_IDENTITY_KINDS`。Enabled 可以包含 PHONE、EMAIL、WECHAT、APPLE；Required 非空时只允许 PHONE、EMAIL，表示至少具备其中一种。显式空 Required 允许任意已启用的有效身份。
- **BREAKING**：C 端和管理端的账号资源由 `/users` 统一改为 `/accounts`，响应中对应资源 name 及下级 identities/sessions 路径同步改变；两套 HTTP 面继续独立。原 user_id、u_…、JWT claims、scope 和数据库表名不随 HTTP 改名。
- **BREAKING**：登录响应支持 token 与 pending。未知身份不满足 Required 时只返回 `sign_in_completion_token` 等待补全，不提前创建用户、identity 或 session；已经满足要求时直接注册，不再提供可选延后注册参数。
- 仅新增 `POST /accounts:sendSignInCompletionCode` 和 `POST /accounts:completeSignIn`。补全发码保持独立，不复用普通 `sendSignInCode`；完成接口只接受本流程的手机或邮箱验证码，不接受第三方补充证明。
- 补充验证通过后自动分流：已有活动账号 B 则关联并登录 B；身份无归属且满足要求时才原子注册。用户无需选择“创建 / 关联”，接入界面需说明验证并继续的效果。
- 普通绑定允许 Enabled 中的类型；Required 只决定完整性与解绑保护，不限制每次登录方式。新登录/刷新按配置决定 user 或 user:bind，刷新宽限仍返回原 pair。
- 现有手机/邮箱重新认证及消费者、管理员注销恢复保留；只有第三方身份时通过重新登录满足既有近期认证规则，本次不新增第三方重新认证或恢复接口。
- accountsvc 按启用渠道装配 SMTP；EMAIL 显式禁用时不要求 SMTP。服务仍不提供短信服务商，显式启用 PHONE 必须拒绝启动。
- 保留短期加密完成记录、用途与流程绑定、并发唯一性、幂等重试、脱敏审计及升级说明。

## Capabilities

### New Capabilities

- `identity-policy`: 启用集合、仅 PHONE/EMAIL 的 any-of 要求、默认与空值语义、scope 和身份管理约束。
- `sign-in-completion`: 无账号补全票据、独立验证码端点、验证后自动关联或注册，以及一次性事务完成。

### Modified Capabilities

- `embedded-auth-package`: accounts 资源契约、两类登录结果、配置驱动权限及已有认证/恢复的兼容边界。
- `verification-challenges`: 登录补全验证码用途、流程绑定和共享失败预算。
- `verification-delivery`: 按策略拒绝渠道、独立补全发码、新用途文案及按需 SMTP。
- `accountsvc-runtime`: accounts 路由、身份配置、条件依赖校验与 migrate 边界。
- `identity-replacement`: accounts 下的换绑路由、启用策略与配置完整性保护。
- `gateway-integration`: accounts 路由迁移、两个补全端点的显式允许清单与流量预算。

## Impact

- 涉及根配置/装配、user.Service、验证码服务、登录结果 DTO、C 端和管理端 HTTP 路由/资源 name、accountsvc、示例及部署清单。Go 登录返回类型和 HTTP 契约按明确的破坏性升级迁移，普通成功 token 字段保留。
- 新增仅用于登录补全的短期 identity_flow 表及递增迁移，经 user/query.sql / sqlc 管理；流程使用 f_ 类型 TSID，认证票据独立使用高熵随机值。既有 u_/i_/s_/e_、身份密文、JWT、refresh token 格式和冻结迁移保持。
- 原有验证码限流/轮换、刷新宽限、access 吊销 fail-open、异步审计、匿名化回调及宿主连接责任保持。
- 更新 README、兼容指南、服务与网关说明，补充真实数据库并发、隔离、密钥轮换及备份恢复验收。
- 排除已有账号合并、业务数据迁移、MERGED 状态、新身份提供方、新短信服务商、前端、配置热加载、第三方补全挑战及新的注销恢复能力。
