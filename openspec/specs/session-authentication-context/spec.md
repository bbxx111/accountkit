# session-authentication-context Specification

## Purpose

建立会话与当前认证身份之间可更新、可查询的关联，使身份移除能准确撤销相关会话，同时让客户端通过服务端最新状态展示认证来源与退出提示，并明确存量未知来源、重新认证、并发和已有令牌的兼容边界。

## Requirements

### Requirement: 当前认证依据为具体身份集合

每个新会话 SHALL 保存此次成功认证的具体身份集合及来源已知标记。普通登录 SHALL 只记录本次验证的身份；登录补全 SHALL 记录参与该次成功流程并最终归属同一账号的全部已验证身份，集合去重。账号已绑定但未参与本次认证的身份 SHALL 不自动加入。身份引用 SHALL 在会话写入的同一事务内复核存在性、归属、启用及有效状态；客户端 SHALL 不能自行指定来源。新建可用会话的已知集合 SHALL 非空。

#### Scenario: 普通登录仅依赖登录身份
- **WHEN** 账号同时绑定微信 A、邮箱 B，用户通过 A 登录
- **THEN** 新会话来源已知且集合仅为 A，不自动加入 B

#### Scenario: 补全关联或注册
- **WHEN** pending 流程先验证微信 A，再验证邮箱 B，最终成功关联已有账号或创建新账号
- **THEN** 来源集合包含 A 和 B 的实际身份资源，使用原账号或新账号的同一 user_id；不把两个来源描述为 MFA 要求

#### Scenario: 多个同类型身份
- **WHEN** 账号拥有两个 EMAIL 身份但只验证其中一个
- **THEN** 集合精确指向被验证的身份，不按 EMAIL 类型扩大关联

#### Scenario: 提交前身份已解绑
- **WHEN** 普通登录或补全读取身份后，解绑先提交，再尝试建立原账号会话
- **THEN** 锁后复核拒绝旧归属证明，不建立依赖已移除身份的可用会话

### Requirement: 重新认证原子替换当前依据

成功重新认证 SHALL 用本次实际验证的单个有效身份替换当前会话的整个来源集合，并在同一事务内更新 auth_time 和来源已知标记。接口、验证码用途、近期认证窗口、同一 sid 及 refresh 保留规则 SHALL 沿用原契约。刷新、普通新增绑定以及新目标 BIND 证明 SHALL 不改变来源集合。失败 SHALL 不改变集合或 auth_time；会话过期或已撤销 SHALL 不能被重新认证恢复。第三方近期认证 SHALL 继续通过普通重新登录建立新会话。

#### Scenario: 从多来源切换为邮箱
- **WHEN** 当前会话来源为微信 A 和邮箱 B，B 的重新认证成功
- **THEN** 当前集合恰为 B、auth_time 更新，sid 和 refresh 保持，原 JWT 不被追溯修改

#### Scenario: 绑定或刷新不提升为来源
- **WHEN** 来源为 A 的会话新增绑定 B，或完成正常刷新及宽限重试
- **THEN** 来源仍为 A；宽限重试仍返回原 pair

#### Scenario: 重新认证失败
- **WHEN** 验证码错误、身份归属失效、会话过期或事务失败
- **THEN** 原来源及 auth_time 保持；已消费验证码不因后续失败恢复

### Requirement: 解绑联动撤销相关及未知会话

解绑 SHALL 同时验证完整 user scope、按既有敏感操作配置要求的近期认证、ACTIVE 账号、当前会话有效且属于账号，以及解绑前后身份完整性；HTTP 和直接 Go 调用 SHALL 一致执行。关闭新鲜度检查 SHALL 仅跳过近期认证要求，不能跳过 scope、会话和完整性校验。成功解绑 SHALL 在同一事务内软删除身份，并撤销同一账号所有尚未吊销且来源命中该具体身份或来源未知的会话，包括当前会话和已到刷新期限但尚未清理的会话。命中任一来源 SHALL 撤销整个会话，不自动删去一个来源继续使用。其他来源已知且未命中的会话 SHALL 保留。

#### Scenario: 任一来源命中
- **WHEN** S1 来源为 A/B，S2 来源仅为 B，成功解绑 A
- **THEN** S1 撤销、S2 保留，即使 S1 是发起操作的当前会话也不例外

#### Scenario: 未知存量会话
- **WHEN** 同一账号存在来源未知的 S0，另有来源为 B 的 S2，成功解绑 A
- **THEN** S0 撤销、S2 保留；其他账号的未知会话不受影响

#### Scenario: 解绑当前认证身份
- **WHEN** 操作发起会话来源命中被解绑身份且剩余身份仍满足策略
- **THEN** 解绑返回原 204 成功，当前会话同时撤销；不将成功操作伪装为认证错误或回滚

#### Scenario: 失败保持原状态
- **WHEN** 解绑触发最后身份保护、权限/新鲜度/会话校验失败，或事务回滚
- **THEN** 不删除身份、不撤销相关会话、不记录成功解绑审计

#### Scenario: 自然到期但 access 尚有效
- **WHEN** 来源命中的会话 refresh 已过期而 access 仍在自身期限内
- **THEN** 该会话仍被显式撤销并进入原吊销流程，不因活跃列表过滤而漏掉

### Requirement: 并发撤销和令牌边界

身份变更、重新认证来源替换和会话状态检查 SHALL 遵循一致事务与锁序，不能使已撤销会话重新变为可用。数据库提交后 SHALL 按既有 sid 吊销和异步审计契约处理 access；refresh、宽限刷新及已完成 pending 的结果重放 SHALL 拒绝数据库已撤销会话。Redis 故障 SHALL 保留现有 fail-open 与告警，不宣称所有 access 即时失效，不回滚已成功的身份变更。

#### Scenario: 重新认证先提交
- **WHEN** 来源 A 的当前会话先通过 B 重新认证提交，随后解绑 A
- **THEN** 解绑按集合 B 计算，该会话保留

#### Scenario: 解绑先提交
- **WHEN** 解绑 A 先提交并撤销来源 A 的会话，随后该会话重新认证
- **THEN** 重新认证锁后返回认证失效，不通过更新集合恢复会话

#### Scenario: 缓存仍有旧结果
- **WHEN** 身份移除已撤销会话，Redis 中仍有旧 refresh 宽限结果或流程仍有完成结果
- **THEN** 后续刷新或完成重试拒绝返回可继续使用的旧 pair，不重新建立会话

#### Scenario: 吊销缓存故障
- **WHEN** 数据库撤销已提交但 Redis 写入失败
- **THEN** 身份操作保持成功、refresh 被数据库拒绝，access/内省沿用已有故障边界并记录告警

### Requirement: 当前会话详情查询

系统 SHALL 提供相对路由 GET /users/me/sessions/current，并从已验证凭据的账号与 sid 定位当前会话；支持 user、user:bind，user:undelete SHALL 返回 403 INSUFFICIENT_SCOPE。响应 SHALL 返回真实会话 name、原设备/时间字段、is_current=true、数据库最新 auth_time、authentication_identities_known，以及 authentication_identities 的 name/kind 摘要数组，按具体身份 ID 稳定排序；来源未知 SHALL 明确返回 false 和空数组。auth_time SHALL 为 RFC3339，JWT/内省既有时间格式不变。身份摘要 SHALL 不包含完整目标、摘要、密文、第三方 subject 或凭据。

查询 SHALL 保持会话、auth_time 与来源集合的一致性快照，且不修改数据。当前会话缺失、所属关系不符、已撤销或刷新期限已到 SHALL 返回 401 TOKEN_INVALID 并带 Bearer challenge；可识别依赖不可用 SHALL 返回 503，不能伪装为认证无效。响应 SHALL no-store；自定义宿主缺少可选查询能力 SHALL 返回 503 SESSION_QUERY_NOT_CONFIGURED，不破坏其他既有接口。

#### Scenario: 根据当前会话展示身份类型
- **WHEN** 来源已知的会话包含 WECHAT A 和 EMAIL B，客户端查询当前会话
- **THEN** 返回两个身份的真实资源名与类型，可用 name 精确匹配解绑目标，不仅按 kind 判断

#### Scenario: 查询重新认证后的状态
- **WHEN** 同一会话重新认证已提交，而客户端仍持有有效的较早 access
- **THEN** 查询返回数据库最新来源和 auth_time；不会改写旧 JWT 或自动赋予其新的近期认证证明

#### Scenario: Redis 故障且数据库会话已撤销
- **WHEN** Bearer 初步校验因既有 fail-open 放行，而详情查询发现会话已撤销
- **THEN** 详情返回 401，不改变普通 Authenticate/内省的原行为

#### Scenario: 未知不是解绑安全
- **WHEN** 迁移来的旧会话尚未重新认证
- **THEN** 返回 authentication_identities_known=false 和 []，客户端不能据此提示解绑不会退出

#### Scenario: 并发替换不返回混合版本
- **WHEN** 查询与来源替换并发
- **THEN** 返回提交前或提交后完整快照，不组合新 auth_time 与旧身份集合

### Requirement: 来源数据生命周期与兼容

升级 SHALL 不从账号现有身份列表或尽力审计反推旧会话来源，不因升级本身批量撤销会话。旧会话成功重新认证 SHALL 变为已知来源；仅 refresh SHALL 保持未知。关联 SHALL 随有界会话清理删除，账号匿名化 SHALL 覆盖全部历史会话关联并与宿主回调同事务。JWT、refresh、scope 和普通业务鉴权热路径 SHALL 保持既有格式与数据库访问边界。

#### Scenario: 非空升级继续使用
- **WHEN** 合法 v1 的账号、身份与会话升级到新版本
- **THEN** 原 ID 和有效 token 保留，来源标为未知；解绑时按已确认的未知来源撤销规则执行

#### Scenario: 重新认证建立来源
- **WHEN** 来源未知的旧会话通过当前绑定邮箱重新认证
- **THEN** 原会话记录该邮箱为唯一已知来源，后续按精确匹配撤销

#### Scenario: 清理和匿名化回滚
- **WHEN** 会话达到清理保留边界，或账号执行匿名化
- **THEN** 关联不成为孤儿；匿名化回调失败时新增关联数据的清理也回滚
