# facebook-sign-in Specification

## Purpose

让宿主在 iOS、Android 与 Web 上使用 Facebook 登录和绑定账号：同时接受 Limited Login 的 ID token 与 Classic Login 的 access token，二者落到同一身份，并沿用通用的资料声明与邮箱提示规则。

## Requirements

### Requirement: Facebook 凭证形状
系统 SHALL 在第三方登录和绑定中接受 kind 为 FACEBOOK 的凭证。credential SHALL 为以下两种之一：仅含非空 `id_token` 与 `nonce`；或仅含非空 `access_token`。两种字段同时出现、都缺失、值为空或含未知字段 SHALL 返回 400 `CREDENTIAL_INCOMPLETE`，且不访问 Facebook、不登记 nonce。第三方重新认证 SHALL 继续沿用重新登录。

#### Scenario: 并列两种凭证
- **WHEN** credential 同时包含 `id_token`、`nonce` 与 `access_token`
- **THEN** 返回 400 `CREDENTIAL_INCOMPLETE`，不拉取 JWKS，不调用 Graph API

#### Scenario: 只有 nonce
- **WHEN** credential 只包含 `nonce`
- **THEN** 返回 400 `CREDENTIAL_INCOMPLETE`

### Requirement: Limited Login ID token 校验
系统 SHALL 只接受 RS256。iss SHALL 等于 `https://www.facebook.com`。aud SHALL 等于配置的 App ID；存在 azp 时 azp 也 SHALL 等于该值。exp 超过 30 秒时钟偏差后 SHALL 拒绝。nonce 声明 SHALL 与请求中的原始 nonce 相等。通过后 SHALL 登记该 nonce，期限 SHALL 覆盖 token 剩余有效期加时钟偏差。

#### Scenario: 受众为其他 App
- **WHEN** ID token 签名有效但 aud 不是配置的 App ID
- **THEN** 返回 400 `IDP_APP_NOT_ALLOWED`，不创建身份

#### Scenario: nonce 重放
- **WHEN** 同一 nonce 在登记期限内再次提交
- **THEN** 返回 400 `IDP_NONCE_REPLAYED`，不创建会话

### Requirement: Classic Login access token 校验
系统 SHALL 由服务端向 Graph API 校验 access token：token 有效、类型为用户 token、所属 App 等于配置的 App ID；随后读取用户资料，其 id SHALL 与校验结果中的用户 id 相同。所有 Graph 调用 SHALL 附带以 App Secret 计算的 appsecret_proof。同一 access token SHALL 可在其有效期内重复用于登录。access token 与 App Secret SHALL 不进入日志、错误响应或审计。

#### Scenario: 其他 App 的 token
- **WHEN** 提交的 access token 有效但属于另一个 Facebook App
- **THEN** 返回 400 `IDP_APP_NOT_ALLOWED`，不创建身份

#### Scenario: 失效 token
- **WHEN** Graph API 报告 token 无效或已过期
- **THEN** 返回 400 `IDP_CREDENTIAL_INVALID`

#### Scenario: 缓存 token 再次登录
- **WHEN** 用户退出后，客户端用同一个仍有效的 access token 再次登录
- **THEN** 登录原账号，不因 token 已使用过而拒绝

### Requirement: 两种凭证落到同一身份
已验证 Facebook 身份的 subject SHALL 为该 App 下的 Facebook 用户 id：Limited 路径取 sub，Classic 路径取用户 id。同一用户通过两种凭证 SHALL 命中同一身份。系统 SHALL 只对接一个 Facebook App；邮箱、姓名和头像 SHALL 不作为主键。

#### Scenario: 跨端同一账号
- **WHEN** 用户先在 iOS 以 Limited Login 注册，再在 Android 以 Classic Login 登录
- **THEN** 两次命中同一 FACEBOOK 身份和同一账号

### Requirement: 启用与配置
App ID 为空 SHALL 表示未启用。App ID 非空时 App Secret SHALL 必填；Graph API 版本 SHALL 符合 `vN.N` 格式；nonce 期限下限 SHALL 为 1 分钟。不满足时 SHALL 在启动前失败并指出配置项，不输出秘密。显式启用 FACEBOOK 但未配置 App ID 或 nonce 存储 SHALL 在启动前拒绝。FACEBOOK SHALL 不能进入 Required。未显式配置 Enabled 时，SHALL 仅在 App ID 非空时默认启用 FACEBOOK。

#### Scenario: 缺少 Secret
- **WHEN** 配置了 App ID 但 App Secret 为空
- **THEN** 启动失败并指出 App Secret 配置项，错误不含 App ID 以外的配置值

#### Scenario: 未启用
- **WHEN** App ID 为空且请求 kind 为 FACEBOOK
- **THEN** 返回 400 `IDP_APP_NOT_ALLOWED`，不访问 Facebook

#### Scenario: 不能作为必需身份
- **WHEN** Required 包含 FACEBOOK
- **THEN** 启动失败

### Requirement: 依赖故障与配置错误
无法取得签名密钥且没有可用缓存、Graph API 网络失败、超时、返回 5xx 或无法解析的响应、nonce 存储不可用时，系统 SHALL 返回 503 `IDP_UNAVAILABLE`。Graph API 拒绝 App 凭据时 SHALL 按服务端配置错误返回固定消息的 500 并记录告警，SHALL 不表示为凭证无效。凭证本身的签名、期限、nonce 或有效性问题 SHALL 返回 400。

#### Scenario: Graph API 不可用
- **WHEN** 校验 access token 时 Graph API 超时
- **THEN** 返回 503，不创建会话

#### Scenario: App Secret 错误
- **WHEN** 部署配置的 App Secret 与 App ID 不匹配
- **THEN** Classic 登录返回 500 固定消息，日志告警不含 Secret

### Requirement: 资料声明与邮箱提示
Facebook 返回的 name 与头像地址 SHALL 按通用的提供方资料声明规则处理。Classic 路径的默认剪影头像 SHALL 视为未提供头像。返回 email 时 SHALL 仅作为 hint_email 返回，SHALL 不创建 EMAIL 身份，也不用于查找或合并已有账号。

#### Scenario: 默认剪影头像
- **WHEN** Classic 登录的资料中 picture 标记为默认剪影
- **THEN** 身份元数据不记录头像，账号头像不被填充

#### Scenario: 邮箱不合并账号
- **WHEN** Required 为空，首次 Facebook 登录返回的 email 与另一个账号的 EMAIL 相同
- **THEN** 创建新账号，不关联那个已有账号，响应可带该 email 作为 hint_email
