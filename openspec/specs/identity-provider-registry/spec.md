# identity-provider-registry Specification

## Purpose
定义身份类型的字符串命名、第三方身份提供方的注册与统一验证契约，以及 C 端提交第三方凭证的统一格式，使新增提供方无需修改请求结构、分派逻辑或数据库定义。

## Requirements

### Requirement: 身份类型以字符串表示
身份类型 SHALL 以 UPPER_SNAKE_CASE 字符串在 Go、存储、HTTP 与审计中表示，格式为 `^[A-Z][A-Z0-9_]{1,31}$`，即 2 至 32 个 ASCII 字符。内置类型为 PHONE、EMAIL、WECHAT、APPLE、GOOGLE、FACEBOOK、ALIPAY、QQ、WEIBO。空字符串 SHALL 表示未指定，不得存储或输出。存储层 SHALL 按区分大小写的二进制比较存储类型，拒绝不符合格式的值，但 SHALL 不枚举合法值。

#### Scenario: 存储格式
- **WHEN** 新空库创建一个微信身份
- **THEN** 该行类型列的值为 `WECHAT`，不是数字

#### Scenario: 非法类型值
- **WHEN** 直接向存储写入类型为 `wechat` 或 `3` 的身份行
- **THEN** 写入被约束拒绝

#### Scenario: 配置 OIDC 不能占用 FACEBOOK
- **WHEN** 配置一个类型名为 FACEBOOK 的 OIDC 提供方
- **THEN** 启动失败并指出该配置项

#### Scenario: 配置 OIDC 不能占用 ALIPAY
- **WHEN** 配置一个类型名为 ALIPAY 的 OIDC 提供方
- **THEN** 启动失败并指出该配置项，不把支付宝当作标准 OIDC 验证器

#### Scenario: 配置 OIDC 不能占用 QQ
- **WHEN** 配置一个类型名为 QQ 的 OIDC 提供方
- **THEN** 启动失败并指出该配置项，不把 QQ 互联当作标准 OIDC 验证器

#### Scenario: 配置 OIDC 不能占用 WEIBO
- **WHEN** 配置一个类型名为 WEIBO 的 OIDC 提供方
- **THEN** 启动失败并指出该配置项，不把微博当作标准 OIDC 验证器

### Requirement: 提供方注册
系统 SHALL 在装配时一次性登记全部第三方提供方，之后不可变。登记的类型 SHALL 格式合法、互不重复，且不得为 PHONE 或 EMAIL；配置驱动的提供方 SHALL 不得使用任何内置类型名。不满足时 SHALL 在启动前失败并指出配置项，不输出秘密。

#### Scenario: 重复类型
- **WHEN** 两个提供方登记为同一类型
- **THEN** 构造失败，不启动服务

#### Scenario: 占用内置名
- **WHEN** 配置一个类型名为 GOOGLE 或 PHONE 的 OIDC 提供方
- **THEN** 启动失败并指出该配置项

### Requirement: 统一凭证格式
所有接收身份凭证的 C 端请求 SHALL 使用顶层 `kind` 与 `credential` 两个字段：`kind` 为类型名，`credential` 为 JSON 对象。系统 SHALL 不再接受 `phone`、`email`、`wechat`、`apple`、`google` 成员。PHONE、EMAIL 的 `credential` SHALL 为 `target`、`code_id`、`code`；WECHAT、ALIPAY 为 `app_id`、`code`；QQ、WEIBO 为 `app_id`、`access_token`；APPLE、GOOGLE 与 OIDC 类型为 `id_token`、`nonce`；FACEBOOK 为 `id_token`、`nonce` 或单独的 `access_token`，二者择一。第三方类型的 `credential` SHALL 原样交给对应提供方解析。

#### Scenario: 微信登录
- **WHEN** 客户端向第三方登录提交 `{"kind":"WECHAT","credential":{"app_id":"wx1","code":"c"}}` 且凭证有效
- **THEN** 与原微信登录结果相同

#### Scenario: 邮箱验证码登录
- **WHEN** 客户端向验证码登录提交 `{"kind":"EMAIL","credential":{"target":"a@example.com","code_id":"<32位十六进制>","code":"123456"}}` 且验证码有效
- **THEN** 与原邮箱验证码登录结果相同

#### Scenario: 旧成员
- **WHEN** 请求体使用 `{"wechat":{"app_id":"wx1","code":"c"}}` 或 `{"email":{...}}`
- **THEN** 返回 400，不访问提供方，不消费验证码

#### Scenario: Facebook Classic 凭证
- **WHEN** 客户端向第三方登录提交 `{"kind":"FACEBOOK","credential":{"access_token":"t"}}` 且 token 有效
- **THEN** 按 Facebook 登录规则处理，请求结构与其他第三方类型一致

#### Scenario: 支付宝登录与普通绑定
- **WHEN** 客户端向第三方登录或普通绑定接口提交有效 ALIPAY app_id/code
- **THEN** 分派支付宝提供方并使用原端点响应结构；不新增支付宝专用登录接口

#### Scenario: 支付宝不能用于锚点端点
- **WHEN** 验证码登录、重新认证、换绑或登录补全的补充证明提交 kind=ALIPAY
- **THEN** 按既有端点类型限制返回 400 CREDENTIAL_KIND_NOT_ALLOWED，不访问支付宝

#### Scenario: QQ 登录与普通绑定
- **WHEN** 客户端向第三方登录或普通绑定接口提交有效 QQ app_id/access_token
- **THEN** 分派 QQ 提供方并使用原端点响应结构；不新增 QQ 专用登录接口

#### Scenario: QQ 不能用于锚点端点
- **WHEN** 验证码登录、重新认证、换绑或登录补全的补充证明提交 kind=QQ
- **THEN** 按既有端点类型限制返回 400 CREDENTIAL_KIND_NOT_ALLOWED，不访问 QQ

#### Scenario: 微博登录与普通绑定
- **WHEN** 客户端向第三方登录或普通绑定接口提交有效 WEIBO app_id/access_token
- **THEN** 分派微博提供方并使用原端点响应结构；不新增微博专用登录接口

#### Scenario: 微博不能用于锚点端点
- **WHEN** 验证码登录、重新认证、换绑或登录补全的补充证明提交 kind=WEIBO
- **THEN** 按既有端点类型限制返回 400 CREDENTIAL_KIND_NOT_ALLOWED，不访问微博

### Requirement: 各端点接受的类型
验证码登录、重新认证、换绑与登录补全 SHALL 只接受 PHONE、EMAIL；第三方登录 SHALL 只接受第三方类型；绑定 SHALL 两类都接受。类型不在端点允许范围内 SHALL 返回 400 `CREDENTIAL_KIND_NOT_ALLOWED`，且不消费验证码、不访问提供方。

#### Scenario: 验证码登录提交第三方类型
- **WHEN** 向验证码登录提交 kind 为 GOOGLE 的凭证
- **THEN** 返回 400 `CREDENTIAL_KIND_NOT_ALLOWED`

#### Scenario: 第三方登录提交锚点类型
- **WHEN** 向第三方登录提交 kind 为 EMAIL 的凭证
- **THEN** 返回 400 `CREDENTIAL_KIND_NOT_ALLOWED`

#### Scenario: 绑定任一类型
- **WHEN** 向绑定提交 kind 为 EMAIL 或 WECHAT 的有效凭证
- **THEN** 两者都按原绑定规则处理

### Requirement: 凭证形状校验
`kind` 或 `credential` 缺失、`credential` 不是对象或超过 16 KiB，或字段缺失、未知、格式错误时 SHALL 返回 400 `CREDENTIAL_INCOMPLETE`；PHONE、EMAIL 的 `code_id` 格式错误 SHALL 保持原错误分类。`kind` 格式非法 SHALL 返回 400。这些拒绝 SHALL 发生在验证码消费、外部调用和 nonce 登记之前，且不创建身份或会话。

#### Scenario: 缺少 nonce
- **WHEN** 提交 kind 为 GOOGLE 的凭证但缺少 `nonce`
- **THEN** 返回 400 `CREDENTIAL_INCOMPLETE`，不拉取 JWKS，不登记 nonce

#### Scenario: 缺少 credential
- **WHEN** 请求体只有 `{"kind":"PHONE"}`
- **THEN** 返回 400 `CREDENTIAL_INCOMPLETE`

### Requirement: 提供方分派与错误分类
系统 SHALL 在 Enabled 检查通过后按类型调用已登记提供方。类型未登记时：Enabled 未显式配置 SHALL 返回 400 `IDP_APP_NOT_ALLOWED`，显式配置 SHALL 返回 `IDENTITY_KIND_NOT_ENABLED`。验证结果的类型 SHALL 与请求一致且 subject 非空，否则视为凭证无效。提供方错误 SHALL 按凭证无效、应用不允许、nonce 重放、依赖不可用分类映射；其他错误 SHALL 返回固定消息的 500，原始错误不进入响应。

#### Scenario: 未配置的类型
- **WHEN** 未显式配置 Enabled，请求类型 ACME_SSO 未登记
- **THEN** 返回 400 `IDP_APP_NOT_ALLOWED`

#### Scenario: 提供方返回未分类错误
- **WHEN** 提供方返回包含内部地址的普通错误
- **THEN** 返回 500 固定消息，响应不含该错误文本

### Requirement: 未登记的历史类型
数据库中存在但当前未登记的类型的身份 SHALL 仍可被列出、解绑、匿名化、审计和管理查询，输出其原类型名。这类身份 SHALL 不能用于登录或新绑定，且不计入完整性。

#### Scenario: 下线提供方后查看身份
- **WHEN** 账号有一条 ACME_SSO 身份，而该类型已从配置移除
- **THEN** 身份列表返回该身份且类型为 ACME_SSO；使用该类型登录被拒绝

### Requirement: 提供方资料声明不按类型区分
任何提供方返回的 name 与 picture SHALL 采用同一净化及元数据规则：记入该身份的提供方元数据、后续非空值覆盖、身份第一次写入账号时填充仍为空的显示名与头像。规则内容与 Google 身份声明和首次填充要求一致。未返回这两项的提供方 SHALL 不写入资料声明。登录补填提供方集合（ALIPAY、QQ、WEIBO）SHALL 额外允许后续成功登录补填账号仍为空的字段，遵循 user-basic-profile；其他提供方 SHALL 保持仅首次写入身份时填充的契约。

#### Scenario: 微信不写资料
- **WHEN** 首次微信登录创建账号
- **THEN** 显示名与头像保持为空，身份元数据不含 profile

#### Scenario: OIDC 返回姓名
- **WHEN** 首次 OIDC 登录的 token 含 name
- **THEN** 账号显示名按首次填充规则写入

#### Scenario: 支付宝后续补填
- **WHEN** 支付宝首次登录未取得头像，后续成功登录取得合法头像且账号头像仍为空
- **THEN** 可以补填头像，已有显示名不覆盖

#### Scenario: QQ 后续补填
- **WHEN** QQ 首次登录资料查询失败，后续成功登录取得合法昵称且账号显示名仍为空
- **THEN** 可以补填显示名，已有头像不覆盖

#### Scenario: 微博后续补填
- **WHEN** 微博首次登录资料查询被限流，后续成功登录取得合法昵称且账号显示名仍为空
- **THEN** 可以补填显示名，已有头像不覆盖

#### Scenario: 其他提供方保持兼容
- **WHEN** 已有 GOOGLE、APPLE、FACEBOOK 或 OIDC 身份再次登录并返回资料
- **THEN** 按既有规则更新非空提供方元数据，不因登录补填提供方的例外自动补填账号资料

### Requirement: 补全配置指纹覆盖全部提供方
登录补全流程绑定的配置指纹 SHALL 包含身份策略与全部已登记提供方的配置标识。任一提供方的允许应用、签发者或密钥地址变化后，未完成的流程 SHALL 被拒绝并要求重新开始。

#### Scenario: Google 允许列表变化
- **WHEN** pending 流程创建后，部署更换了 Google client ID 列表并重启
- **THEN** 继续该流程被拒绝，要求重新开始
