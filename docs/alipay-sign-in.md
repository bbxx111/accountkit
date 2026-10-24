# 支付宝 App 极简登录

本指南覆盖 Android/iOS 等原生 App 的支付宝极简 SDK 授权，以及 accountkit/accountsvc 的既有第三方登录与绑定入口。库的自动验收使用正式 HTTP 入口、实际提供方、独立签名网关及真实 PostgreSQL/Redis，不依赖终端设备。本文末尾的真实 SDK、授权权限和应用分组检查属于宿主上线前接入清单，当前尚未执行。

## 开放平台与普通公钥

在支付宝开放平台创建并配置应用、开通需要的授权能力，按官方控制台要求登记客户端信息与回跳。使用**普通公钥模式**：应用私钥留在服务端，应用公钥上传开放平台；配置项 `AlipayPublicKey` / `PUBLIC_KEY` 使用支付宝提供的验签公钥。两种公钥用途不同。首版不支持证书模式。

每个 AppID 是 16 位 ASCII 数字，必须唯一，并单独配置至少 2048 位 RSA 密钥。应用私钥接受 PEM PKCS#1/PKCS#8，支付宝公钥接受 PEM PKCS#1/SPKI；拒绝证书、弱密钥、非 RSA 和多份 PEM 材料。不把私钥、支付宝 token 或完整授权结果放进 App、日志、审计、错误响应及版本库。

跨 App 共用账号时，把应用配置在同一支付宝应用分组，并实测同一用户返回相同 `union_id`。本地配置校验不能验证控制台分组及权限。

## 客户端授权与回跳

官方入口：[极简版 SDK 调用方法](https://opendocs.alipay.com/open/218/sxc60m)、[极简版授权请求参数和返回](https://opendocs.alipay.com/open/218/wy75xo)。SDK 的平台配置与版本要求以这两份文档为准。

1. 用户点击登录后，使用密码学安全随机源生成不可预测的 `state`，按官方格式使用不超过 100 字符的 Base64 随机字符串。把它与本次授权、AppID、预期回跳和短有效期关联；同一时刻的不同尝试分别管理。若宿主服务参与回跳，则由宿主保存并核对关联。
2. 检测支付宝是否安装；极简版需要唤起已安装的支付宝客户端。未安装时说明原因或提供宿主的其他登录方式，不把浏览器兜底当作已完成极简授权。
3. 按官方 SDK 发起极简授权，参数为 `auth_type=PURE_OAUTH_SDK`、`app_id`、`scope=auth_user` 和本次 `state`。`auth_user` 请求资料授权；`auth_base` 不能用来承诺昵称、头像可用。SDK 使用的授权 URL 入口是 `https://authweb.alipay.com/auth`；参数按官方要求编码，不添加完整版的签名 `authInfo`。
4. 按所选平台注册并处理 SDK 回跳。Android 使用官方 `OpenAuthTask.execute` / `Callback.onResult`；iOS 按官方方式将回跳交给 `AFServiceCenter` 的 `handleResponseURL:withCompletion:`。这里只说明接入位置，具体签名和安装检测方式以所固定 SDK 版本为准。
5. 回跳时核对预期来源、当前授权上下文、AppID 与完全相同的 `state`；一次性消费关联，拒绝重复、过期、缺失或不匹配的回跳。用户取消、拒绝授权、SDK 失败均结束本次流程，不提交为成功登录。客户端回调中的用户标识不能当作账号认证结果。
6. 仅在授权成功且关联核对通过后，把回传的 `auth_code` 映射为后端 credential 的 `code`。`state` 在客户端/宿主回跳层处理，不加入后端 credential；accountkit 换码只能验证凭证，不能替你完成回跳关联。

Flutter 接入需要支持**极简版**的原生桥接：将以上授权、生命周期回跳及结果交给 Dart，并保留 state 核对和一次性消费。可以由宿主维护 MethodChannel 桥接。依赖仅提供 `auth` / `authV2` 并要求签名 authInfo 时，应先核实它是否支持极简版；不要直接把完整版方法或其回跳协议套用过来。本项目不提供 Flutter 插件或客户端 SDK。

## 提交统一凭证

对宿主挂载的终端用户 API 发起 `POST /users:signInWithIdp`（accountsvc 默认完整路径为 `/v1/users:signInWithIdp`），请求体示例全部为合成值：

```json
{"kind":"ALIPAY","credential":{"app_id":"2026000000000001","code":"synthetic-one-time-code"}}
```

设备沿用 `X-Device-Id` / `X-Device-Name`。credential 严格只接受非空 `app_id` 和 `code`；不接受 state、用户标识、token、网关或模式。普通绑定使用既有 `POST /users/me/identities`，遵守登录态、scope 和身份冲突约束；解绑/换绑继续要求近期认证。

授权码是一次性的。兑换超时也可能已经消费 code，库不会自动重复换码；失败后重新发起授权获取新 code，不盲目重发旧请求。数据库事务内部的唯一冲突重试复用已验证证明，不再兑换 code。进入 pending 后按现有手机/邮箱补全协议继续，完成与结果重放不会再次请求支付宝。

`CREDENTIAL_INCOMPLETE`、`IDP_APP_NOT_ALLOWED`、`IDP_CREDENTIAL_INVALID` 为 400；网络、429/5xx 或兑换超时为 503 `IDP_UNAVAILABLE`；服务端配置、缺少模式要求的标识或不可信协议响应为固定消息 500。前端不要把所有失败都当作用户授权取消，也不要因错误响应中 `Retry-After` 的通用提示而重放支付宝 code。

## Go 宿主配置

根包公开 `AlipayApp`、`AlipaySubjectMode` 及 `AlipaySubjectModeUnionID` / `AlipaySubjectModeOpenID`。下面是装配片段，`privatePEM`、`alipayPublicPEM` 由宿主安全加载内容：

```go
cfg.AlipayApps = []accountkit.AlipayApp{{
    AppID: "2026000000000001",
    PrivateKey: privatePEM,
    AlipayPublicKey: alipayPublicPEM,
}}
cfg.AlipaySubjectMode = accountkit.AlipaySubjectModeUnionID
cfg.EnabledIdentityKinds = []enum.IdentityKind{enum.IdentityAlipay}
cfg.RequiredIdentityKinds = []enum.IdentityKind{}
```

`New` 只校验和装配，不读取密钥文件、不访问支付宝。`SignInWithIdp` 的 Go 凭证为 `user.IdpCredential{Kind: enum.IdentityAlipay, Credential: json.RawMessage(...)}`。低层验证器入口是 `idp.NewAlipay(idp.AlipayOptions{Config: idp.AlipayConfig{Apps: apps}})`；一般宿主通过根门面配置即可。

启用 ALIPAY 不改变 Required：省略时仍为 PHONE/EMAIL 二选一，资料授权不能当作邮箱/手机证明。纯支付宝账号须显式 Required 为空并显式只启用 ALIPAY，无需短信或邮件发送器。需要邮箱补全时启用 EMAIL、ALIPAY，Required 设 EMAIL，并装配邮件发送器。普通绑定/解绑、会话来源、账号状态及重新认证规则保持，见 [Go 集成指南](go-integration.md)。

## accountsvc 与环境变量

库使用 `ConfigFromEnv(prefix)` 的宿主前缀；accountsvc 固定为 `ACCOUNTKIT_`。以下展示变量名与合成 AppID，省略密钥值，不能直接作为完整运行配置：

```text
ACCOUNTKIT_ALIPAY_APP_IDS=2026000000000001,2026000000000002
ACCOUNTKIT_ALIPAY_2026000000000001_PRIVATE_KEY=<安全注入的多行 PEM 应用私钥>
ACCOUNTKIT_ALIPAY_2026000000000001_PUBLIC_KEY=<安全注入的多行 PEM 支付宝公钥>
ACCOUNTKIT_ALIPAY_2026000000000002_PRIVATE_KEY=<另一应用的多行 PEM 私钥>
ACCOUNTKIT_ALIPAY_2026000000000002_PUBLIC_KEY=<对应的多行 PEM 支付宝公钥>
ACCOUNTKIT_ALIPAY_SUBJECT_MODE=UNIONID
ACCOUNTKIT_ENABLED_IDENTITY_KINDS=ALIPAY
ACCOUNTKIT_REQUIRED_IDENTITY_KINDS=[]
```

PEM 必须包含真实换行，库不展开字面 `\n`，不读取文件路径。由密钥注入工具或宿主预先加载内容。应用列表缺省/空表示不配置，已配置列表不允许重复或空项。模式未设置时 UNIONID；显式空白、小写和其他值拒绝。网关缺省 `https://openapi.alipay.com/gateway.do`，覆盖项 `ALIPAY_GATEWAY_URL` 必须为无 userinfo、查询或 fragment 的 HTTPS 绝对地址；HTTP 仅允许数值回环测试地址。

serve 显式 Enabled=ALIPAY 却无应用时指出 `ACCOUNTKIT_ALIPAY_APP_IDS`；缺密钥指出对应 AppID 的变量名，错误不输出配置值。migrate 无需配置支付宝、不调用支付宝，但显式提供的非法配置仍会被静态校验拒绝。

[本地 Compose](../deploy/accountsvc/compose.yaml) 默认 EMAIL；仅编辑 `.env` 不会自动把任意 IdP 变量传进容器。切换时用 Compose override 或实际部署的环境注入，显式覆盖 Enabled/Required 并传入每个 ALIPAY 配置；不要把密钥写入 Compose 或镜像。

## 身份模式、资料与回退

默认 UNIONID 使用 `unionid:<union_id>`，跨 App 同值命中同一 ALIPAY 身份；OPENID 使用 `openid:<app_id>:<open_id>`，按 App 隔离。缺少必需标识拒绝，不回退 `user_id`，也不按昵称、头像或历史元数据合并账号。模式、应用分组或验签信任变更须独立评估；不会自动迁移或合并旧账号。所有副本保持一致配置。

pending 指纹覆盖排序后的 AppID、模式、网关和规范化支付宝公钥，不含应用私钥。列表顺序或等价 PEM 表达不影响指纹；配置指纹改变使旧 pending 必须重新开始。控制台分组变化也须重新完成真实多应用验收，不能仅依赖本地指纹检测。

每次验证先以最多 10 秒兑换 code，再以独立最多 3 秒尝试 `alipay.user.info.share`，均受调用方更早 deadline 限制。资料只在验签成功且 OpenID 与本次兑换完全一致时采信，只映射 `nick_name`、`avatar`，不使用真实姓名、登录名、手机号或邮箱兜底。资料超时、权限不足、坏签名或无法关联等只记 `alipay_profile_unavailable`，允许已验证身份继续；正常缺昵称/头像不算故障。外层取消/超时会终止整体操作，不会作为资料降级继续建会话。

资料填充遵循统一规则：新建账号始终填充；已有账号的后续登录在 `PROVIDER_PROFILE_BACKFILL` 开启（默认）时补填空字段。保留任何非空值；用户主动清空也会在下一次取得可信声明时补填。两个字段均非空时不更新资料时间。资料失败不从历史 `provider_meta.profile` 回填，不清空旧字段。QQ、微博及其他提供方适用同一规则。资料、身份和会话随同一领域事务提交/回滚，并发先提交的非空 PATCH 优先。库不下载头像，不保存第三方 token，不负责头像持续同步。

本变更无 SQL/schema 或迁移变化。禁用 ALIPAY 只停止新验证；历史身份仍可按现有规则列出、解绑和匿名化。回退前验证目标版本对未登记历史类型的兼容性，不改 subject、数据库版本/dirty 或清理 Redis 来替代身份迁移。观测口径见 [指标指南](metrics.md)。

## 库侧自动验收

`TestAlipayHTTPLoginContract` 通过正式门面与终端用户 handler，连接实际 Alipay Provider、本地独立签名网关和真实 PostgreSQL/Redis，验证登录、本人资料、刷新、错误凭证及资料降级后补填。`TestAlipayHTTPMultiAppIdentityContract` 验证两个 AppID 的独立密钥、UNIONID 共用账号、OpenID 合并、OPENID 应用隔离和不自动升级，以及跨 App 补填与非空资料保护。普通测试缺少真实依赖时明确 skip，CI 必需清单要求两项实际 PASS。

这些证据证明库对协议声明的处理，不证明真实 SDK 回跳、支付宝权限或控制台分组配置；不会把模拟平台数据描述为实际平台返回。

## 宿主上线前接入检查清单

由宿主在接入或上线前完成以下检查，记录实际平台/SDK 版本和脱敏结论：

- [ ] 实际 SDK 的安装/未安装、取消/拒绝、生命周期与回跳正确；随机 state 能一次性核对，错误/过期/重复回跳被拒绝。
- [ ] 实际应用权限允许兑换授权码及获取期望昵称/头像；实际授权结果可以建立本系统会话或按业务策略进入 pending。
- [ ] 同一控制台分组下两个 AppID 对同一测试用户返回相同 UnionID，并进入同一个 accountkit 账号。
- [ ] 实际资料缺失/权限失败时的交互符合宿主产品要求；后续取得资料可补填空字段，非空资料不覆盖。

只保留脱敏结论，不保存授权码、token、签名或用户资料。此清单是宿主责任记录，勾选状态不计入库的 OpenSpec 实施进度。

当前宿主真机与应用分组检查尚未执行，保留在接入清单，不作为库 CI 或实施完成的阻塞项，也不记为已通过。原真机任务已按职责边界调整为库侧自动契约验收；模拟网关、真实数据库及 race 不替代宿主真实平台联调。换码协议参见官方 [alipay.system.oauth.token](https://opendocs.alipay.com/open/02ailc)，资料接口参见支付宝官方 SDK 的 [AlipayUserInfoShareRequest](https://github.com/alipay/alipay-sdk-java-all/blob/master/v2/src/main/java/com/alipay/api/request/AlipayUserInfoShareRequest.java)。
