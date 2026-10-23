# Spec Delta

## MODIFIED Requirements

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
