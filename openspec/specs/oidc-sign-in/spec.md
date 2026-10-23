# oidc-sign-in Specification

## Purpose
让部署方仅通过配置接入任意标准 OIDC 提供方，以其签发的 ID token 登录和绑定账号，并沿用与 Google 相同的校验强度、声明处理和故障语义。

## Requirements

### Requirement: OIDC 提供方配置
每个 OIDC 提供方 SHALL 配置类型名、issuer、JWKS 地址、非空 client ID 列表，nonce 登记下限可选、默认 10 分钟。issuer 与 JWKS 地址 SHALL 为绝对 http(s) URL。缺失或非法 SHALL 在启动前失败并指出配置项。装配 SHALL 不发起网络请求。未显式配置 Enabled 时，已配置的 OIDC 类型 SHALL 默认启用；OIDC 类型 SHALL 不能进入 Required。

#### Scenario: 缺少 client ID
- **WHEN** 配置了类型 CORP_SSO 但 client ID 列表为空
- **THEN** 启动失败并指出该类型的 client ID 配置项

#### Scenario: 不能作为必需身份
- **WHEN** Required 包含 CORP_SSO
- **THEN** 启动失败

### Requirement: OIDC ID token 校验
系统 SHALL 只接受 RS256 签名且密钥来自所配置 JWKS 的 ID token。iss SHALL 与配置完全相等；aud SHALL 命中 client ID 列表，存在 azp 时 azp 也 SHALL 命中；exp SHALL 存在，超过 30 秒偏差后拒绝。nonce 声明 SHALL 与请求原始 nonce 常量时间相等。通过后 SHALL 按类型隔离登记 nonce，期限覆盖 token 剩余有效期加偏差。

#### Scenario: 签发者不符
- **WHEN** token 的 iss 与配置不完全相等
- **THEN** 返回 400 `IDP_CREDENTIAL_INVALID`，不创建身份

#### Scenario: 受众不允许
- **WHEN** aud 或 azp 不在该类型的 client ID 列表中
- **THEN** 返回 400 `IDP_APP_NOT_ALLOWED`

#### Scenario: nonce 重放
- **WHEN** 同一 nonce 在登记期限内对同一类型再次提交
- **THEN** 返回 400 `IDP_NONCE_REPLAYED`，不创建会话

#### Scenario: 非 RS256
- **WHEN** token 使用 HS256 或 none
- **THEN** 返回 400 `IDP_CREDENTIAL_INVALID`

### Requirement: OIDC 身份主键
已验证 OIDC 身份的 subject SHALL 为 sub，唯一性按（类型，sub）判定。邮箱、姓名、头像 SHALL 不作为主键或用于合并账号。

#### Scenario: 同一 sub 再次登录
- **WHEN** 同一类型下同一 sub 用另一个已允许的 client ID 登录
- **THEN** 命中原 OIDC 身份，不创建第二个账号

### Requirement: OIDC 邮箱提示
email_verified 为真时，系统 SHALL 把 email 声明作为 hint_email 返回，且不创建 EMAIL 身份或据此查找账号。邮箱与 email_verified 的声明名 SHALL 固定，不可配置。

#### Scenario: 未验证邮箱
- **WHEN** token 含 email 且 email_verified 不为真
- **THEN** 响应省略 hint_email

### Requirement: OIDC 资料声明映射
每个 OIDC 提供方 SHALL 可分别配置昵称与头像的声明名列表，按顺序取第一个非空的顶层字符串声明。未配置时昵称默认 `name`、头像默认 `picture`；显式空列表 SHALL 表示不读取该项。SHALL 不支持嵌套路径。取得的值 SHALL 按提供方资料声明规则清洗、记入身份元数据并参与首次填充空资料。声明名 SHALL 非空且不重复，否则启动失败。

#### Scenario: 默认声明
- **WHEN** 未配置映射，首次 OIDC 登录创建账号，token 含可整理的 name 与合法 picture
- **THEN** 账号显示名与头像被填充，规则与 Google 相同

#### Scenario: 按顺序回退
- **WHEN** 昵称映射为 `name,preferred_username`，token 只有非空 preferred_username
- **THEN** 使用 preferred_username 的值

#### Scenario: 关闭回填
- **WHEN** 昵称与头像映射均显式为空
- **THEN** 不记录资料声明，不填充显示名与头像，登录照常完成

#### Scenario: 非字符串声明
- **WHEN** 映射的头像声明是对象或数字
- **THEN** 跳过该声明，继续尝试后续声明名，不拒绝登录

### Requirement: OIDC 依赖故障
无可用签名密钥缓存且 JWKS 刷新失败，或 nonce 存储不可用时，系统 SHALL 返回 503 `IDP_UNAVAILABLE`，不得表示为凭证无效。使用缓存密钥继续验证 SHALL 作为独立降级场景可观测。

#### Scenario: JWKS 不可用
- **WHEN** 首次验证时 JWKS 地址不可达
- **THEN** 返回 503，不创建会话
