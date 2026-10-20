# Spec Delta

## MODIFIED Requirements

### Requirement: 生命周期和资料兼容

包 SHALL 保留资料读取与显示名称更新、ACTIVE/FROZEN/PENDING_DELETION/DELETED 状态、冻结解冻、注销、恢复及最终匿名化。默认注销冷静期为 15 天。

现有消费者手机/邮箱恢复、管理端代办恢复和 user:undelete SHALL 保留。普通第三方登录 SHALL 继续拒绝 PENDING_DELETION，不新增第三方恢复认证。恢复后的新令牌 SHALL 按当前 Required 决定 user 或 user:bind；Required=[] 的纯第三方账号 SHALL 不被描述为具备手机/邮箱自助恢复能力。

可选宿主扩展资料 SHALL 在冻结及注销冷静期保留，恢复不重置其内容或 Schema 标识、revision 和更新时间。最终匿名化 SHALL 在同一既有事务内清空扩展内容及 Schema 标识，推进已使用的 revision、记录清理时间；revision 达上限时保留上限，不阻断清理；从未写入的空资料无需初始化元数据。清理 SHALL 不依赖当前宿主 Schema 或功能启用，不能被配置移除或不兼容定义阻断；宿主回调失败 SHALL 同时回滚扩展清理。

#### Scenario: 冷静期恢复
- **WHEN** 待注销用户在冷静期内使用恢复专用权限发起恢复
- **THEN** 按基线恢复账号；普通用户权限不能替代恢复专用权限

#### Scenario: 业务匿名化失败
- **WHEN** 到期清理中的宿主匿名化回调失败
- **THEN** 同一数据库事务内的认证数据及业务数据改动一起回滚，后续可重试

#### Scenario: 恢复后仍需补全
- **WHEN** 仅有 PHONE 的冷静期账号按原恢复流程恢复，但当前 Required 为 EMAIL
- **THEN** 新一轮刷新返回 user:bind，账号继续使用原 user_id；普通 user 权限不能替代 user:undelete

#### Scenario: 冷静期及恢复保留扩展
- **WHEN** 含扩展资料的账号注销后在冷静期内恢复
- **THEN** 内容、Schema 标识、revision 及扩展更新时间保持，冷静期中普通扩展读写仍按状态契约拒绝

#### Scenario: 无 Schema 仍可匿名化
- **WHEN** 宿主撤掉扩展配置或改用不兼容 Schema，而账号进入到期匿名化
- **THEN** 资料与 Schema 标识在原匿名化事务中清空，不调用普通资料更新校验；已使用 revision 推进、到上限时保持，均不重置为零

#### Scenario: 匿名化失败保留扩展
- **WHEN** 扩展资料已在事务内清空但后续宿主回调失败
- **THEN** 原内容、Schema 标识、revision、时间及账号状态全部回滚，后续维护可重试

#### Scenario: 已清理账号不重现旧值
- **WHEN** 匿名化成功后再次运行维护或查询公开资料
- **THEN** 扩展内容不会从历史 Schema、审计或默认值恢复，也不重复推进已完成清理的 revision
