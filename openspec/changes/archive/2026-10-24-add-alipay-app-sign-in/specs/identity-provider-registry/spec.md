# Spec Delta

## MODIFIED Requirements

### Requirement: 身份类型以字符串表示
身份类型 SHALL 以 UPPER_SNAKE_CASE 字符串在 Go、存储、HTTP 与审计中表示，格式为 `^[A-Z][A-Z0-9_]{1,31}$`，即 2 至 32 个 ASCII 字符。内置类型为 PHONE、EMAIL、WECHAT、APPLE、GOOGLE、FACEBOOK、ALIPAY。空字符串 SHALL 表示未指定，不得存储或输出。存储层 SHALL 按区分大小写的二进制比较存储类型，拒绝不符合格式的值，但 SHALL 不枚举合法值。

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

### Requirement: 统一凭证格式
所有接收身份凭证的 C 端请求 SHALL 使用顶层 `kind` 与 `credential` 两个字段：`kind` 为类型名，`credential` 为 JSON 对象。系统 SHALL 不再接受 `phone`、`email`、`wechat`、`apple`、`google` 成员。PHONE、EMAIL 的 `credential` SHALL 为 `target`、`code_id`、`code`；WECHAT、ALIPAY 为 `app_id`、`code`；APPLE、GOOGLE 与 OIDC 类型为 `id_token`、`nonce`；FACEBOOK 为 `id_token`、`nonce` 或单独的 `access_token`，二者择一。第三方类型的 `credential` SHALL 原样交给对应提供方解析。

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

### Requirement: 提供方资料声明不按类型区分
任何提供方返回的 name 与 picture SHALL 采用同一净化及元数据规则：记入该身份的提供方元数据、后续非空值覆盖、身份第一次写入账号时填充仍为空的显示名与头像。规则内容与 Google 身份声明和首次填充要求一致。未返回这两项的提供方 SHALL 不写入资料声明。ALIPAY SHALL 额外允许后续成功登录补填账号仍为空的字段，遵循 user-basic-profile；其他提供方 SHALL 保持仅首次写入身份时填充的契约。

#### Scenario: 微信不写资料
- **WHEN** 首次微信登录创建账号
- **THEN** 显示名与头像保持为空，身份元数据不含 profile

#### Scenario: OIDC 返回姓名
- **WHEN** 首次 OIDC 登录的 token 含 name
- **THEN** 账号显示名按首次填充规则写入

#### Scenario: 支付宝后续补填
- **WHEN** 支付宝首次登录未取得头像，后续成功登录取得合法头像且账号头像仍为空
- **THEN** 可以补填头像，已有显示名不覆盖

#### Scenario: 其他提供方保持兼容
- **WHEN** 已有 GOOGLE、APPLE、FACEBOOK 或 OIDC 身份再次登录并返回资料
- **THEN** 按既有规则更新非空提供方元数据，不因支付宝的例外自动补填账号资料
