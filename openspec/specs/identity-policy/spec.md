# identity-policy Specification

## Purpose

将部署允许使用的身份类型与账号完整性要求明确分离，使手机号、邮箱、微信和 Apple 的登录及身份管理遵循同一策略，并让客户端可以区分待完成注册、已有账号身份补全和完整登录，避免固定锚点规则限制不同部署方式。

## Requirements

### Requirement: 两项身份策略与配置校验
系统 SHALL 提供 EnabledIdentityKinds 和 RequiredIdentityKinds。Required SHALL 仅表示 any-of：至少具备其中一种有效身份，不支持 all-of；非空时只允许 PHONE、EMAIL，不允许第三方 Kind。Enabled SHALL 可包含 PHONE、EMAIL 及任一已登记提供方的类型。有效身份 SHALL 已验证、未软删除且 Kind 属于 Enabled；依赖的瞬时健康状态 SHALL 不改变身份完整性。显式 Enabled SHALL 非空，显式 Required SHALL 为 Enabled 的子集；格式非法或未登记的类型、空值、重复值、Required 含第三方类型及显式启用但缺少验证能力的配置 SHALL 在启动前被拒绝。配置 SHALL 在装配时复制为不可变快照，不提供热加载。

#### Scenario: 任一必需身份
- **WHEN** Enabled 为 PHONE、EMAIL、WECHAT，Required 为 PHONE、EMAIL，账号只有 WECHAT 和 EMAIL
- **THEN** 账号满足要求，通过 WECHAT 登录可以获得 user，不要求同时拥有 PHONE

#### Scenario: 非法配置
- **WHEN** Enabled 显式为空、Required 包含未启用类型或显式启用的提供方没有必要依赖
- **THEN** 启动失败并指出配置项，不静默忽略或输出秘密

#### Scenario: 第三方不能作为必需类型
- **WHEN** Enabled 包含 WECHAT 或 APPLE，且 Required 也包含其中任一类型
- **THEN** 配置失败；Required=[] 允许纯第三方账号不等于允许把第三方写进必需列表

#### Scenario: 启用未登记的类型
- **WHEN** 显式 Enabled 包含 CORP_SSO，但没有登记该类型的提供方
- **THEN** 启动失败并指出 Enabled 配置项

#### Scenario: 启用配置的 OIDC 类型
- **WHEN** 已配置 OIDC 类型 CORP_SSO，显式 Enabled 为 EMAIL、CORP_SSO，Required 为 EMAIL
- **THEN** 配置通过，CORP_SSO 可用于登录与普通绑定

### Requirement: 缺省和显式空列表
库 SHALL 区分未配置和显式空列表。Enabled 未配置时 SHALL 保留原装配选择：PHONE、EMAIL 以及全部已登记的第三方提供方类型；既有 Disabled 发送器仍可作为旧宿主的显式投递关闭能力。Required 未配置时 SHALL 为 PHONE、EMAIL；显式空列表 SHALL 不限定身份类型，但完整账号仍须至少一个 Enabled 中的有效身份。显式设置 Enabled 后 SHALL 对 Required 的默认值同样执行子集校验，不自动放宽要求。

#### Scenario: 保留旧宿主装配
- **WHEN** 库宿主未设置两项新配置，仍注入原发送器或 Disabled 发送器
- **THEN** 原装配可以构造，保持 PHONE 或 EMAIL 至少一个的完整性条件；Disabled 的投递关闭仍生效

#### Scenario: 独立第三方部署
- **WHEN** Enabled 为 WECHAT、APPLE，Required 显式为空且相关验证能力完整
- **THEN** 单个已验证的 WECHAT 或 APPLE 身份可以完成注册并获得 user

#### Scenario: 部分启用不能隐式解除要求
- **WHEN** 显式 Enabled 只有 EMAIL 而 Required 未配置
- **THEN** 默认 PHONE、EMAIL 不满足子集要求，启动报错并要求部署者显式设置 Required 为 EMAIL

#### Scenario: 默认启用已配置的 OIDC
- **WHEN** Enabled 未配置，部署配置了 OIDC 类型 CORP_SSO
- **THEN** CORP_SSO 默认启用，未配置的类型不启用

### Requirement: 身份使用与安全禁用
系统 SHALL 在登录、发码、凭证提交、补全、关联、绑定、换绑和重新认证使用身份前执行 Enabled 检查，包括已经签发的验证码和待完成票据。策略拒绝 SHALL 在验证码消费或外部提供方调用前发生，已有身份记录 SHALL 不被删除或改写。已有禁用身份 SHALL 仍可被授权查询并按解绑规则移除；Disabled 发送器的既有发送错误语义 SHALL 保留。

#### Scenario: 禁用后提交旧码
- **WHEN** PHONE 被显式移出 Enabled，客户端提交仍未过期的 PHONE 验证码
- **THEN** 拒绝身份使用，不消费轮次、不建立会话、不新增绑定

#### Scenario: 禁用后查看身份
- **WHEN** 账号仍有一条 Kind 已禁用的历史活动身份
- **THEN** 身份列表仍返回脱敏资源，但它不计入当前完整性要求，也不能用于新认证

### Requirement: 账号权限与绑定范围
ACTIVE 账号 SHALL 根据全部有效身份判断完整性；满足要求返回 user，不满足返回 user:bind。Required SHALL 不限制每次登录所用的身份，也不限制普通新增绑定的 Kind。普通绑定 SHALL 允许 Enabled 中的类型；用于补全必需身份的入口 SHALL 优先展示 Required 中的选项，登录补全只接受已启用的 PHONE/EMAIL 证明；第三方身份仍可用于原账号登录及登录后的普通绑定。已有归属的身份 SHALL 不经普通绑定转移给另一个账号。

#### Scenario: 手机登录也可能需要补全
- **WHEN** Required 为 EMAIL，已有账号只绑定 PHONE，且 PHONE 已启用并验证成功
- **THEN** 沿用该账号的 user_id，返回 user:bind 并允许补充 EMAIL

#### Scenario: 邮箱账号添加第三方
- **WHEN** Required 为 EMAIL，完整账号添加未被占用的 WECHAT，且 WECHAT 已启用
- **THEN** 允许绑定，之后可用 EMAIL 或 WECHAT 登录同一账号

#### Scenario: 普通绑定冲突
- **WHEN** 已登录账号 A 尝试绑定属于账号 B 的身份
- **THEN** 返回 409 IDENTITY_ALREADY_BOUND，不迁移身份或合并账号

### Requirement: 解绑与换绑保持完整性
解绑成功后的账号 SHALL 至少保留一个有效的 Enabled 身份，并继续满足非空 Required 的 any-of 条件。换绑 SHALL 在同一事务中维持这些条件及原有身份数量限制，不通过先解绑后绑定产生不完整状态。已经不满足条件的账号 SHALL 先走补全流程，不通过解绑扩大不可用状态。

#### Scenario: 保留任一必需类型
- **WHEN** Required 为 PHONE、EMAIL，账号同时具备两者并请求解绑 PHONE
- **THEN** 可以按原授权规则解绑，EMAIL 继续满足要求

#### Scenario: 保护最后一个可用身份
- **WHEN** Required 为空而账号只剩一个已启用的 WECHAT 身份
- **THEN** 拒绝解绑，不留下无法登录的账号

### Requirement: 配置变更与令牌边界
新登录、重新认证和新一轮刷新 SHALL 使用当前配置及数据库身份推导 scope；宽限重试 SHALL 返回此前同一 token pair。绑定或部署配置变更 SHALL 不改写已签发 access token，既有 JWT 有效期、吊销及 fail-open 边界 SHALL 保留。所有副本 SHALL 使用一致策略；未完成流程在策略发生变化后 SHALL 被拒绝并要求重新开始。

#### Scenario: 绑定后刷新
- **WHEN** user:bind 账号补齐要求并进行新一轮刷新
- **THEN** 返回 user；对上一轮旧 refresh 的合格宽限重试仍返回上一轮相同结果

#### Scenario: 策略调整后存量账号
- **WHEN** Required 改为 EMAIL 而某存量账号只有 PHONE
- **THEN** 新登录或新一轮刷新返回 user:bind，不删除账号，也不声称旧 access 立即失效
