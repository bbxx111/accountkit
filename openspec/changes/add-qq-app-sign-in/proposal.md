# Proposal

## Why

accountkit 尚未支持 QQ 身份，接入 QQ 互联移动应用 SDK 的 App 无法通过既有第三方登录接口完成 QQ 登录。本变更接入 QQ App SDK 登录，复用账号与会话规则，按微信、支付宝相同的固定标识模式支持跨应用账号识别，并与支付宝一致地在登录时补填空的昵称和头像。

## What Changes

- 新增内置 `QQ` 提供方，第三方登录及普通绑定沿用 `kind` / `credential`，凭证为 `{"kind":"QQ","credential":{"app_id":"...","access_token":"..."}}`。客户端不提交 openid、unionid、标识模式或接口地址；SDK 回调中的用户标识不被采信。
- 仅覆盖移动应用 SDK 登录：客户端/宿主集成 QQ 互联 Android/iOS SDK 取得 access_token 后提交给后端。服务端调用 `graph.qq.com/oauth2.0/me`（`fmt=json`）校验令牌，要求返回的 `client_id` 等于请求的 `app_id` 且在允许列表内，openid/unionid 只取自该响应。
- 与 Facebook Classic 访问令牌一致，QQ access_token 不做一次性限制；令牌在提供方有效期内可重复用于登录，安全边界依赖 `client_id` 校验与令牌保密，文档明确这一风险。
- 配置为允许的 AppID 列表与实例级 `QQ_SUBJECT_MODE`；本流程不需要 AppKey 或其他秘密。新增库配置、宿主可选前缀的环境变量解析及 accountsvc 装配，默认不启用 QQ；`New` 不做 I/O，非法或重复配置启动前拒绝。
- 标识模式默认 `UNIONID`，也可显式配置 `OPENID`，分别使用 `unionid:<unionid>`、`openid:<app_id>:<openid>`。UNIONID 模式请求 unionid，未开通 UnionID 权限（上游 100048）或响应缺少 unionid 时按服务端配置错误拒绝；OPENID 模式不请求 unionid。不自动切换模式、迁移 subject 或合并账号；跨 App 同账号必须使用 UNIONID，并在同一 QQ 互联开发者账号下完成 UnionID 打通。
- 沿用白名单元数据：已验证标识写入 `provider_meta.unionid` 与 `provider_meta.openids[app_id]`，缺失不清除旧值，矛盾时拒绝并回滚。补全配置指纹覆盖允许应用与标识模式。
- 登录时用同一 access_token 调用 `user/get_user_info` 读取 `nickname` 和头像（优先 100px `figureurl_qq_2`，缺失时使用 40px `figureurl_qq_1`），映射为现有 name / picture 声明。资料失败、缺失或 `is_lost=1` 不阻断已通过验证的登录，本次资料不采信并记录脱敏告警。
- QQ 头像地址仅对已知 QQ 头像域名执行 `http://` 到 `https://` 的改写，再走现有头像净化；名单外的 http 地址丢弃。通用头像规则保持只接受 HTTPS。
- 将支付宝的“登录按字段补填空资料”及其原子性要求改为适用于一组提供方（ALIPAY、QQ）：首次及后续成功登录分别补填仍为空的显示名和头像，不覆盖非空值，写入时复核空值与账号生命周期；其他提供方保持仅新建身份时的一次性填充。
- 复用 Enabled/Required、登录补全、绑定解绑、账号生命周期、会话来源与审计契约；扩展有界认证指标以识别 QQ 及资料降级。QQ access_token 与原始响应不持久化、不记日志、不进入审计或对外响应。

## Non-goals

- 不实现 PC 网站、H5、QQ 小程序登录，不托管网页回调或 `redirect_uri`，不实现授权码兑换。
- 不开发客户端 SDK 桥接、Flutter 插件或客户端 UI；提供接入说明并验证后端契约。
- 不为 access_token 增加 nonce 或一次性登记，不增加第三方重新认证/换绑入口，不保存 QQ 令牌做后台资料同步。
- 不强制纯 QQ 注册，不改变 Required 默认值，不把 QQ 昵称、头像作为身份识别依据，不读取性别、地区、黄钻等其他资料字段。
- 不下载、托管头像文件，不放宽其他来源的 HTTPS 头像规则。
- 无数据库结构变更、无新增迁移，不修改 `0001`、历史 SQL、冻结基线或已发布标签。

## Capabilities

### New Capabilities

- `qq-sign-in`: QQ App SDK 登录的接入边界、access_token 校验与应用允许列表、固定身份模式、标识元数据、资料查询与头像地址改写，以及允许登录的降级语义。

### Modified Capabilities

- `identity-provider-registry`: 内置类型和凭证形状增加 QQ（`app_id`、`access_token`），保留 FACEBOOK 与 ALIPAY 的既有增量。
- `user-basic-profile`: 把支付宝专属的登录补填空资料及其原子性要求改为适用于提供方集合（ALIPAY、QQ），保持支付宝已有场景与其他提供方的一次性填充规则。
- `accountsvc-runtime`: 增加 QQ 配置与启动前校验，支持仅启用 QQ 且 Required 为空时不依赖 SMTP；保持 migrate 的运行依赖隔离。
- `standardized-metrics`: 增加有界的 QQ 认证方法值及资料查询降级观测，不使用 AppID 或用户标识作标签。

## Impact

- 代码涉及 `enum`、`user/idp`（新增 QQ 提供方）、`user` 中补填判定（把现有支付宝判断收敛为按提供方集合判定）、`config.go`、`accountkit.go`、`internal/accountsvc` 和 `observability`；公开 HTTP 请求结构保持，新增可选配置与内置身份类型。
- **依赖**：本变更修改 `add-alipay-app-sign-in` 新增的补填要求与代码。该变更已合入 develop 并归档，本分支已合入该 develop；delta 基于归档后的主规格编写。未归档的 `add-facebook-sign-in` 仍需一并核对，注册表、accountsvc、指标及资料的 delta 不得回退 FACEBOOK、ALIPAY 能力。
- 实施需要补充测试：凭证形状与允许列表拒绝发生在外部调用之前、`client_id` 不符、上游错误分类（100016、100048 等）、JSONP 或异常响应拒绝、两种标识模式及跨 App 一致性、标识冲突、资料失败与 `is_lost` 降级、头像域名改写与拒绝、ALIPAY 与 QQ 的后续补填及并发资料更新、pending/绑定/匿名化与配置指纹。真实 PostgreSQL 验证事务行为；QQ 头像域名的 HTTPS 可用性、UnionID 打通及 App 真机登录需要真实应用联调，模拟测试不能替代。
- 文档同步 README、Go 集成及 accountsvc 配置、兼容说明和无秘密配置模板，说明 SDK 接入、UnionID 申请、OPENID 适用场景、令牌可重放风险与模式切换风险。

## References

- [QQ 互联 UnionID 介绍与 /me 接口](https://wiki.connect.qq.com/unionid%E4%BB%8B%E7%BB%8D/)
- [QQ 互联 get_user_info](https://wiki.connect.qq.com/get_user_info/)

以上官方资料已在探索阶段核对；QQ 头像域名的 HTTPS 支持、移动 SDK 返回字段及真实应用联调尚未验证。
