## ADDED Requirements

### Requirement: 提供方资料声明统一处理
任何提供方返回的 name 与 picture SHALL 采用同一净化及元数据规则：记入该身份的提供方元数据，后续非空值覆盖。账号显示名与头像的填充 SHALL 对所有提供方采用 user-basic-profile 的同一补填规则：新建账号始终填充空字段，已有账号按资料补填配置补填空字段。未返回这两项的提供方 SHALL 不写入资料声明。系统 SHALL 不再按提供方类型区分补填行为。

#### Scenario: 微信不写资料
- **WHEN** 首次微信登录创建账号
- **THEN** 显示名与头像保持为空，身份元数据不含 profile

#### Scenario: OIDC 返回姓名
- **WHEN** 首次 OIDC 登录的 token 含 name
- **THEN** 账号显示名按新建账号填充规则写入

#### Scenario: 支付宝后续补填
- **WHEN** 配置开启，支付宝首次登录未取得头像，后续成功登录取得合法头像且账号头像仍为空
- **THEN** 可以补填头像，已有显示名不覆盖

#### Scenario: QQ 后续补填
- **WHEN** 配置开启，QQ 首次登录资料查询失败，后续成功登录取得合法昵称且账号显示名仍为空
- **THEN** 可以补填显示名，已有头像不覆盖

#### Scenario: 微博后续补填
- **WHEN** 配置开启，微博首次登录资料查询被限流，后续成功登录取得合法昵称且账号显示名仍为空
- **THEN** 可以补填显示名，已有头像不覆盖

#### Scenario: 其他提供方同样补填
- **WHEN** 配置开启，已有 GOOGLE、FACEBOOK 或 OIDC 身份再次登录并返回合法资料，账号显示名为空
- **THEN** 更新非空提供方元数据，并补填账号显示名

#### Scenario: 配置关闭时只更新元数据
- **WHEN** 配置关闭，任一提供方的已有身份再次登录并返回新的非空资料
- **THEN** 身份元数据中的 profile 按非空值更新，账号显示名与头像不变

## REMOVED Requirements

### Requirement: 提供方资料声明不按类型区分
**Reason**: 原要求把 ALIPAY、QQ、WEIBO 列为补填例外并要求其他提供方仅首次填充；现由“提供方资料声明统一处理”取代，补填不再按类型区分。
**Migration**: 元数据记录规则不变；账号资料填充改按 user-basic-profile 的“第三方资料统一补填”及 `PROVIDER_PROFILE_BACKFILL` 配置执行。
