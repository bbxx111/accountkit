## MODIFIED Requirements

### Requirement: OIDC 资料声明映射
每个 OIDC 提供方 SHALL 可分别配置昵称与头像的声明名列表，按顺序取第一个非空的顶层字符串声明。未配置时昵称默认 `name`、头像默认 `picture`；显式空列表 SHALL 表示不读取该项。SHALL 不支持嵌套路径。取得的值 SHALL 按提供方资料声明规则清洗、记入身份元数据并参与“第三方资料统一补填”：新建账号时填充空资料，已有账号按 `PROVIDER_PROFILE_BACKFILL` 资料补填配置补填空资料。声明名 SHALL 非空且不重复，否则启动失败。

#### Scenario: 默认声明
- **WHEN** 未配置映射，首次 OIDC 登录创建账号，token 含可整理的 name 与合法 picture
- **THEN** 账号显示名与头像被填充，规则与其他第三方提供方相同

#### Scenario: 按顺序回退
- **WHEN** 昵称映射为 `name,preferred_username`，token 只有非空 preferred_username
- **THEN** 使用 preferred_username 的值

#### Scenario: 关闭回填
- **WHEN** 昵称与头像映射均显式为空
- **THEN** 不记录资料声明，不填充显示名与头像，登录照常完成

#### Scenario: 非字符串声明
- **WHEN** 映射的头像声明是对象或数字
- **THEN** 跳过该声明，继续尝试后续声明名，不拒绝登录
