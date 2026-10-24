# google-sign-in Specification

## Purpose

让宿主使用 Google ID token 登录和绑定账号，保存该身份的姓名与头像声明，并在身份第一次落到账号时填充仍为空的显示名和头像，而不按邮箱自动合并账号。

## Requirements

### Requirement: Google 凭证
系统 SHALL 在登录和绑定中接受 kind 为 GOOGLE 的凭证，其 credential 同时包含 id_token 与 nonce。登录、绑定和补全 SHALL 使用同一校验结果。第三方重新认证 SHALL 继续沿用重新登录。

#### Scenario: 已有身份登录
- **WHEN** 客户端提交签名有效、受众允许且 nonce 未使用的 Google ID token，其 sub 已属于活动账号
- **THEN** 登录该账号，不创建第二个用户

#### Scenario: 凭证不完整或并列
- **WHEN** GOOGLE 凭证缺少 id_token 或 nonce，或请求仍使用旧的 `google` 成员
- **THEN** 返回 400，不访问 Google，不创建身份

### Requirement: ID token 校验
系统 SHALL 只接受 RS256。iss SHALL 为 `https://accounts.google.com` 或 `accounts.google.com`。aud SHALL 命中已配置的 client ID；存在 azp 时 azp 也 SHALL 命中该列表。exp 超过 30 秒时钟偏差后 SHALL 拒绝。nonce 声明 SHALL 与请求中的原始 nonce 相等。通过后 SHALL 登记该 nonce，登记期限 SHALL 覆盖 ID token 剩余有效期加时钟偏差。

#### Scenario: 签发者或受众不符
- **WHEN** iss 不是两个合法值之一，或 aud 或 azp 不在允许的 client ID 中
- **THEN** 返回 400 `IDP_CREDENTIAL_INVALID` 或 400 `IDP_APP_NOT_ALLOWED`，不创建身份

#### Scenario: nonce 重放
- **WHEN** 同一 nonce 在登记期限内再次提交
- **THEN** 返回 400 `IDP_NONCE_REPLAYED`，不创建会话

### Requirement: 身份主键与启用
已验证 Google 身份的 subject SHALL 为 sub。邮箱、姓名和头像 SHALL 不作为主键。空 client ID 列表 SHALL 表示未启用。显式启用 GOOGLE 但没有 client ID 或 nonce 存储 SHALL 在启动前拒绝。GOOGLE SHALL 不能进入 Required。未显式配置 Enabled 时，SHALL 仅在 client ID 非空时默认启用 GOOGLE。

#### Scenario: 同一项目的另一个客户端
- **WHEN** 同一 sub 使用同一 Google Cloud 项目中的另一个已允许 client ID 登录
- **THEN** 命中原来的 Google 身份

#### Scenario: 未启用
- **WHEN** client ID 列表为空且请求携带 google 凭证
- **THEN** 返回 400 `IDP_APP_NOT_ALLOWED`，不访问 Google

#### Scenario: 不能作为必需身份
- **WHEN** Required 包含 GOOGLE
- **THEN** 启动失败

### Requirement: 依赖故障
无法取得签名密钥且没有可用缓存，或 nonce 存储不可用时，系统 SHALL 返回 503 `IDP_UNAVAILABLE`。签名、期限或 nonce 不匹配 SHALL 返回 400，不得把依赖故障表示成凭证无效。

#### Scenario: JWKS 不可用
- **WHEN** 验证时没有可用的签名密钥缓存且刷新失败
- **THEN** 返回 503，不创建会话

### Requirement: 身份声明
系统 SHALL 将 token 中的 name、picture 记入该 Google 身份的提供者元数据。name SHALL 先去掉显示名禁止的控制字符，再保留最多 256 个 Unicode 码位。picture SHALL 仅在通过既有头像地址校验时保存。随后的成功验证 SHALL 用非空新值覆盖对应字段，缺失字段保持旧值。身份列表和 reveal SHALL 不返回该元数据。匿名化 SHALL 与提供者元数据一并清除这些声明。

#### Scenario: 声明变化
- **WHEN** 同一 Google 身份再次登录且 token 带有新的非空 name
- **THEN** 身份元数据中的 name 更新，picture 在本次缺失时保持上次的值

#### Scenario: 声明不可用
- **WHEN** name 含控制字符后为空，或 picture 不是合法头像地址
- **THEN** 跳过对应字段并继续登录

### Requirement: 首次填充空资料
Google 身份的账号资料填充 SHALL 遵循 user-basic-profile 的通用补填规则。display_name 为空时 SHALL 写入整理后 name 的前 128 个码位；avatar_url 为空且 picture 已通过头像地址校验时 SHALL 写入该地址。已有值 SHALL 保持。新建账号时始终填充；直接注册以外的已有账号路径（再次登录、绑定、补全关联）SHALL 仅在资料补填配置开启时补填。整理结果为空或地址非法 SHALL 跳过对应列，且不因此拒绝登录。放弃的补全 SHALL 不写入资料。

#### Scenario: 新账号填充
- **WHEN** 首次创建账号并写入该 Google 身份，两列为空，且 token 含可整理的 name 与合法 picture
- **THEN** 显示名不超过 128 个码位，头像为该 URL

#### Scenario: 已有资料保持
- **WHEN** 该 Google 身份关联或绑定到已有显示名和头像的账号
- **THEN** 两列保持原值，身份声明仍按元数据规则记录

#### Scenario: 再次登录不覆盖资料
- **WHEN** 同一 Google 身份再次登录且 token 中的 name 已变化，账号显示名非空
- **THEN** 账号显示名保持首次填充或用户后来设置的值

#### Scenario: 再次登录补填空字段
- **WHEN** 配置开启，同一 Google 身份再次登录，账号头像为空且 token 含合法 picture
- **THEN** 补填账号头像

### Requirement: 邮箱只作为提示
email_verified 为真时，系统 SHALL 把 email 作为 hint_email 返回。缺失或未验证时 SHALL 省略。hint_email SHALL 不创建 EMAIL 身份，也不用于查找或合并已有账号。

#### Scenario: 未验证邮箱
- **WHEN** token 含 email 且 email_verified 不为真
- **THEN** 响应省略 hint_email，不写入 EMAIL 身份

#### Scenario: 直接注册不按邮箱合并
- **WHEN** Required 为空，首次 Google 身份的已验证邮箱与另一个账号的 EMAIL 相同
- **THEN** 创建新账号并返回 user，不关联那个已有账号
