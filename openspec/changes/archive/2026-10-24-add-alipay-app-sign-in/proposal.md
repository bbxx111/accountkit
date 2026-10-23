# Proposal

## Why

accountkit 尚未提供支付宝身份验证能力，App 无法通过既有第三方登录接口完成支付宝登录。本变更接入支付宝极简版 App 授权，复用账号与会话规则，并按微信相同的固定标识模式支持跨应用账号识别，以及昵称、头像首次填充和后续空字段补填。

## What Changes

- 新增内置 `ALIPAY` 提供方，第三方登录及普通绑定沿用 `kind` / `credential`，凭证为 `{"kind":"ALIPAY","credential":{"app_id":"...","code":"..."}}`。客户端不提交身份标识、令牌、密钥、网关地址或标识模式。
- 仅覆盖 App 极简授权：客户端/宿主集成极简版 SDK，以 `auth_user` 发起授权，关联并校验一次性随机 `state`，将 `auth_code` 提交给后端。库不提供签名 `authInfo` 生成接口；Flutter 接入方需使用支持极简版的桥接。
- 服务端使用 `alipay.system.oauth.token` 兑换授权码，对请求使用 RSA2 签名并验证响应。应用允许列表、凭证形状、上游错误与响应标识均需校验；身份验证失败或身份响应验签失败拒绝认证，不信任 SDK 返回的用户标识。
- 提供多个 AppID 的独立配置，每项包含 AppID、应用 RSA 私钥、支付宝公钥；首版采用普通公钥模式。新增库配置、宿主可选前缀的环境变量解析及 accountsvc 装配，默认不启用支付宝；`New` 不做 I/O，非法或重复应用配置启动前拒绝，错误不泄露秘密。
- 新增实例级 `ALIPAY_SUBJECT_MODE`：默认 `UNIONID`，也可显式配置 `OPENID`，分别使用 `unionid:<union_id>`、`openid:<app_id>:<open_id>`。所需标识缺失时拒绝认证，不自动切换模式、回退旧 `user_id`、迁移 subject 或合并账号。跨 App 同账号部署必须使用 UNIONID，并将相关应用配置在同一支付宝应用分组；OPENID 不承诺不同 AppID 自动关联。
- 沿用白名单元数据：已验证标识写入 `provider_meta.unionid` 与 `provider_meta.openids[app_id]`，缺失不清除旧值，已有标识矛盾时拒绝并回滚。补全配置指纹覆盖允许应用、标识模式及影响验证的公开配置，不包含秘密。
- 首次支付宝登录使用兑换得到的访问令牌调用 `alipay.user.info.share`，读取 `nick_name` / `avatar`，映射为现有 name / picture 声明；不使用真实姓名、登录名等字段代替昵称。净化后保存身份资料声明，只填充账号仍为空的显示名与头像，兼容直接注册、已有账号关联与 pending 完成。
- 昵称、头像缺失或资料查询失败不阻断已通过身份验证的登录；无效签名或不属于本次已验证身份的资料不采信，记录脱敏告警。后续支付宝登录仍可尝试补填空字段，不覆盖非空值；写入时原子复核空值及账号生命周期，不能覆盖并发用户更新，不在数据库事务内等待上游网络。该补填规则仅针对支付宝，不改变其他提供方既有规则。
- 复用 Enabled/Required、登录补全、绑定解绑、账号生命周期、会话来源与审计契约；扩展有界认证指标以识别支付宝及资料降级。第三方 code、访问/刷新令牌、私钥和原始响应不持久化、不记日志、不进入审计或对外响应。

## Non-goals

- 不实现 PC Web、H5、小程序授权入口、Web 回调托管或完整版 App `authInfo` 签名接口。
- 不开发 Flutter 插件、客户端 UI 或其他独立客户端项目；提供接入说明并验证后端契约。
- 不增加证书模式、支付能力、旧 `user_id` 模式、旧身份迁移、自动账号合并或第三方重新认证/换绑入口。
- 不强制纯支付宝注册、不改变 Required 默认值，不把支付宝昵称、头像作为身份识别依据。
- 不下载、托管头像文件，不持续覆盖已有资料；不后台保存支付宝令牌重试资料查询。
- 无数据库结构变更、无新增迁移，不修改 `0001`、历史 SQL、冻结基线或已发布标签。

## Capabilities

### New Capabilities

- `alipay-sign-in`: 极简版 App 授权接入边界、授权码验证、应用与密钥配置、固定身份模式、标识元数据、资料查询及允许登录的降级语义。

### Modified Capabilities

- `identity-provider-registry`: 内置类型和凭证形状增加 ALIPAY；在通用资料规则中明确支付宝后续登录仅补填空字段的例外，保持其他提供方契约。
- `user-basic-profile`: 增加支付宝后续登录补填空昵称/头像的边界、并发空值复核及生命周期约束，保留宿主对头像文件的责任。
- `accountsvc-runtime`: 增加支付宝配置与启动前校验，支持仅启用 ALIPAY 且 Required 为空时不依赖 SMTP；保持 migrate 的运行依赖隔离。
- `standardized-metrics`: 增加有界的支付宝认证方法值及资料查询降级观测，不将资料降级混记为身份验证失败，不使用 AppID/用户标识作标签。

## Impact

- 代码涉及 `enum`、`user/idp`、`user` 的资料查询与条件补填路径、`config.go`、`accountkit.go`、`internal/accountsvc` 和 `observability`；公开 HTTP 请求结构保持，新增可选配置与内置身份类型。
- 实施需要补充测试：格式及允许列表拒绝发生在外部调用之前、RSA2 请求与原始响应验签、上游错误分类、两种标识模式及跨 App 一致性、标识冲突、资料失败仍可登录、后续补填、并发资料更新、pending/绑定/匿名化与配置指纹。库以正式 HTTP 入口、实际提供方、独立签名网关及真实 PostgreSQL/Redis 做自动契约验收。真实 SDK、权限与控制台应用分组由宿主另行联调；保留未执行事实，不以模拟测试证明真实平台行为，也不把设备条件作为库验收阻塞项。
- 资料查询与账号判定的组织方式在 design 中明确：沿用提供方无数据库职责，并确保网络请求有界、不会在重试或失败路径提前提交资料；不为补填而保存第三方令牌。
- 文档同步 README、Go 集成及 accountsvc 配置、兼容说明和无秘密配置模板，说明 `auth_user` 权限、极简版需要安装支付宝客户端、state 责任、普通公钥配置和模式切换风险。
- 兼容性需同时读取未归档的 [Facebook 变更](../../add-facebook-sign-in/proposal.md) 及其 delta specs：本提案针对注册表、accountsvc 和指标的后续 delta 不得回退 FACEBOOK 能力；保留其与其他提供方的资料策略。

## References

- [支付宝极简版 SDK 调用方法](https://opendocs.alipay.com/open/218/sxc60m)
- [极简版授权请求参数和返回](https://opendocs.alipay.com/open/218/wy75xo)
- [换取授权访问令牌](https://opendocs.alipay.com/open/02ailc)
- [官方授权响应定义：应用分组与 union_id](https://github.com/alipay/alipay-sdk-java-all/blob/master/v2/src/main/java/com/alipay/api/response/AlipaySystemOauthTokenResponse.java)
- [官方资料响应定义：nick_name / avatar 可缺失](https://github.com/alipay/alipay-sdk-java-all/blob/master/v2/src/main/java/com/alipay/api/response/AlipayUserInfoShareResponse.java)

以上官方资料已在探索阶段核对；尚未进行真实应用权限、应用分组及 App 真机联调。
