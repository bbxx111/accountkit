## MODIFIED Requirements

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
