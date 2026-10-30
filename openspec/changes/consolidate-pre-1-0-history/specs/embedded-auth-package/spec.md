## ADDED Requirements

### Requirement: 独立消费与 v1.0.0 公开能力

包 SHALL 独立构建/测试/导入，不依赖外部源码工作区；公开能力以 v1.0.0 为基线，JWT、refresh 及非审计资源格式保持。v1.0.0 SHALL 不再提供 v1.0.0 之前开发期入口的兼容别名或转发：`ids.IdentityFlow`、`user.Deps.IdentityFlowContext`、`CleanupIdentityFlows`、`sender.SMSSender`、`sender.EmailSender`、`sender.Message` 移除，调用者改用 `ids.SignInCompletionFlow`、`SignInCompletionFlowContext`、`CleanupSignInCompletionFlows`、`SMSCodeSender`、`EmailCodeSender`、`CodeMessage`。当前会话查询 SHALL 仍为可选宿主能力，不扩大必需接口方法集；会话来源非空、客户端描述为嵌套 JSON，Go 保留平坦描述字段并包含制造商/build/扩展。自 v1.0.0 起公开 Go API SHALL 受语义化版本约束。

#### Scenario: 独立宿主构建
- **WHEN** 独立 Go 项目依赖 accountkit 并注入依赖
- **THEN** 可编译并挂载两面接口，不需要外部宿主源码、accountsvc 进程或特定管理员 OIDC 包

#### Scenario: 自定义审计升级
- **WHEN** 宿主按通用审计说明实现记录器
- **THEN** 接收通用事件而不依赖 sqlc 行，可核对公开 Go 类型、审计 UUIDv7/游标及查询配置

#### Scenario: 旧自定义认证适配器
- **WHEN** 宿主只实现原消费者必需接口
- **THEN** 原接口仍工作，当前查询返回 503 SESSION_QUERY_NOT_CONFIGURED 而非空来源成功

#### Scenario: 开发期兼容入口已移除
- **WHEN** 调用者引用上述六个已移除符号
- **THEN** 编译失败；按发布说明替换为正式名称后行为不变

### Requirement: 生命周期和资料保留

包 SHALL 保留资料读取与显示名称更新、ACTIVE/FROZEN/PENDING_DELETION/DELETED 状态、冻结解冻、注销、恢复及最终匿名化。默认注销冷静期为 15 天。

现有消费者手机/邮箱恢复、管理端代办恢复和 user:undelete SHALL 保留。普通第三方登录 SHALL 继续拒绝 PENDING_DELETION，不新增第三方恢复认证。恢复后的新令牌 SHALL 按当前 Required 决定 user 或 user:bind；Required=[] 的纯第三方账号 SHALL 不被描述为具备手机/邮箱自助恢复能力。

可选宿主用户资料属性 SHALL 在冻结及冷静期保留，恢复不重置内容。最终匿名化 SHALL 在原事务中清空为 `{}`，复用账号原时间语义，不区分是否曾使用。清理不依赖当前 Schema 或配置；宿主回调失败 SHALL 回滚资料、账号时间及其他变更。

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
- **WHEN** 含用户资料属性的账号注销后在冷静期内恢复
- **THEN** 资料内容保持，账号时间沿用原生命周期语义，冷静期中普通扩展读写仍按状态契约拒绝

#### Scenario: 无 Schema 仍可匿名化
- **WHEN** 宿主撤掉扩展配置或改用不兼容 Schema，而账号进入到期匿名化
- **THEN** 资料在原匿名化事务中清空，不调用普通资料更新校验；不区分曾使用与未使用，沿用账号时间语义

#### Scenario: 匿名化失败保留扩展
- **WHEN** 用户资料属性已在事务内清空但后续宿主回调失败
- **THEN** 原内容、账号时间及账号状态全部回滚，后续维护可重试

#### Scenario: 已清理账号不重现旧值
- **WHEN** 匿名化成功后再次运行维护或查询公开资料
- **THEN** 扩展内容不会从历史 Schema、审计或默认值恢复，也不重复修改已完成清理的账号时间

## REMOVED Requirements

### Requirement: 独立消费和完整功能保留
**Reason**: 由「独立消费与 v1.0.0 公开能力」取代。原要求以抽取前原项目的固定源基线和未发布会话字段的调用迁移为参照，这些开发期参照在 v1.0.0 不再存在。
**Migration**: 调用者按发布说明把六个已移除符号替换为正式名称；无运行时迁移。

### Requirement: 生命周期和资料兼容
**Reason**: 由「生命周期和资料保留」取代，内容相同，仅移除针对开发期旧结构的“临时结构无写拒绝”场景；预检边界由 `versioned-auth-storage` 的「基线预检边界」统一描述。
**Migration**: 无。
