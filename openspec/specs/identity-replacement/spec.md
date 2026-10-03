# identity-replacement Specification

## Purpose

允许已经能够验证账号控制权的消费者安全更换同类手机号或邮箱，在默认只有一个同类身份的限制下保持原账号连续可用，并明确授权、新身份验证、事务边界、并发、会话处置和结果重试规则，供嵌入式库与可选服务共用。

## Requirements

### Requirement: 明确的同类身份替换操作

系统 SHALL 提供 `POST /v1/users/me/identities/{identity}:replace`，库内使用相对路由。请求 SHALL 指定一个 phone 或 email 的 target/code 凭证，旧身份与新身份 SHALL 同属 PHONE 或同属 EMAIL。成功 SHALL 返回200及新身份的既有掩码资源格式，保留 user_id，使用新身份 ID，不返回 token pair。原绑定、解绑及第三方身份接口 SHALL 保持原义。

#### Scenario: 唯一邮箱换绑
- **WHEN** 账号只有一个邮箱身份且同类数量上限为1，提交合法换邮箱请求
- **THEN** 原账号保留一个新的活动邮箱身份，旧身份不再活动，返回新 ID 的掩码资源，账号 ID 不变

#### Scenario: 不支持的替换
- **WHEN** 请求跨 PHONE/EMAIL 类型、替换第三方身份、同时提供多个凭证或提交非法请求体
- **THEN** 返回400，不修改身份或会话，不消费新目标验证码

### Requirement: 账号授权与有效当前会话

换绑 SHALL 要求完整 user scope、ACTIVE 账号和属于该账号的有效当前会话。近期认证 SHALL 沿用现有 ReauthMaxAge 和 SensitiveOpVerification 配置，默认5分钟且启用；显式关闭仅跳过新鲜度检查。直接库调用 SHALL 同样执行这些规则。近期认证 SHALL 不被描述为强制向被替换地址再次发码的证明。

#### Scenario: 近期认证不足
- **WHEN** 已启用新鲜度检查且 access 的 auth_time 缺失或超过配置窗口
- **THEN** 返回400 REAUTHENTICATION_REQUIRED，客户端可通过现有重新认证流程继续，未消费新地址验证码

#### Scenario: 凭据作用域不足或会话失效
- **WHEN** 请求只有 user:bind/user:undelete，或数据库当前会话已吊销、过期、缺失或属于其他用户
- **THEN** 作用域不足返回403，会话失效返回401，均不完成换绑；Redis 吊销查询 fail-open 不绕过数据库写路径检查

#### Scenario: 账号禁止操作
- **WHEN** 账号处于 FROZEN、PENDING_DELETION 或 DELETED
- **THEN** 沿用对应领域状态拒绝，不能完成换绑或恢复该账号

### Requirement: 新目标证明与资源隐私

换绑 SHALL 使用现有 sendBindCode 发出的 BIND 码验证新目标，保持一次性消费、用途隔离、尝试计数及失败时的额度/冷却约束。旧身份 SHALL 按当前账号限定查询；新目标占用状态 SHALL 仅在其验证码验证后判定。归一化后与原目标相同 SHALL 返回400 IDENTITY_UNCHANGED；新目标已由任一账号绑定 SHALL 返回409 IDENTITY_ALREADY_BOUND，不隐式合并身份。

#### Scenario: 旧资源归属不可探测
- **WHEN** 初次检查发现旧身份 ID 格式合法但不存在、已软删除或属于其他用户
- **THEN** 统一返回404 NOT_FOUND，不披露实际归属，不消费新目标验证码

#### Scenario: 错误用途或验证失败
- **WHEN** 客户端提交 SIGN_IN/REAUTH 码、错误或过期的 BIND 码，或验证码依赖不可用
- **THEN** 沿用已有400/503错误，失败尝试计数仍生效，身份与会话保持原样

#### Scenario: 占用冲突
- **WHEN** 新目标验证码有效，但该目标已由别的账号或本账号另一条活动身份使用
- **THEN** 返回409通用占用错误，保留旧身份及会话；已消费验证码不予恢复

### Requirement: 原子替换与并发一致性

旧身份软删除、新身份创建与其他会话的数据库撤销 SHALL 在同一事务中提交；新身份 SHALL 使用当前配置的加密/摘要版本，冲突查询兼容已有摘要版本。失败回滚 SHALL 不留下无锚点账号、额外活动身份或部分会话撤销。最终活动身份数量 SHALL 符合配置上限，不通过修改全局上限完成换绑。

#### Scenario: 写入失败
- **WHEN** 任一身份或会话写入失败且事务已确认回滚
- **THEN** 旧身份、原活动身份数量及原会话撤销状态保持不变，不记录成功换绑

#### Scenario: 并发换绑
- **WHEN** 两个请求同时替换同一个旧身份，或两个账号争抢同一新目标
- **THEN** 至多一个对应替换成功；同一旧身份的后到请求返回404，新目标争抢失败返回409，不破坏锚点和唯一性约束

#### Scenario: 生命周期及刷新竞争
- **WHEN** 换绑与解绑、冻结、注销、当前会话退出或刷新同时发生
- **THEN** 结果符合事务生效顺序，没有死锁、部分替换或被撤销会话通过刷新复活

### Requirement: 旧身份证明不得越过换绑边界

登录和重新认证 SHALL 在写入会话前确认所依赖的身份关系仍有效，不因使用换绑前的读结果而在换绑后取得原账号的新会话或更新认证时间。旧地址后续正常登录 SHALL 保持既有自动注册语义，不能被描述为地址永久封禁。

#### Scenario: 旧登录读取与换绑交错
- **WHEN** 登录已读到旧身份，换绑先提交，登录之后才取得账号写锁
- **THEN** 本次旧证明以400 CODE_INVALID 被拒绝，不为原账号创建会话；重新发起的独立登录遵循现有身份查找与注册流程

#### Scenario: 旧重新认证与换绑交错
- **WHEN** 重新认证已初步确认旧锚点，但在更新 auth_time 前换绑先提交
- **THEN** 重新认证返回400 TARGET_NOT_ANCHOR，不更新会话认证时间或签发新的原账号凭据

### Requirement: 保留当前会话并撤销其他会话

成功换绑 SHALL 保留当前会话及其原 refresh token、auth_time，撤销同一账号其余会话并标记 IDENTITY_REPLACED 原因。正常依赖下，其余会话的后续 refresh（含宽限重试）、access 和内省 SHALL 受撤销限制。Redis 吊销写入/查询失败 SHALL 沿用既有 fail-open 与告警，不能宣称所有旧 access 立即失效，也不能将已提交换绑伪装为回滚。

#### Scenario: 多设备换绑
- **WHEN** 账号有多个会话并由其中一个完成换绑
- **THEN** 当前会话继续访问和刷新，其他会话不能继续刷新；正常 Redis 下其 access 和内省无效

#### Scenario: 提交后吊销缓存失败
- **WHEN** 身份与会话事务已提交，随后 Redis 吊销集写入失败
- **THEN** 换绑保持成功，其他 refresh 仍受数据库撤销约束，记录安全警告并保持现有 access fail-open 边界

### Requirement: 一次性码与重试边界明确

系统 SHALL 不把验证码消费、审计、Redis 吊销与数据库变更宣称为跨存储原子事务。成功消费的新目标验证码 SHALL 不因后续数据库回滚或事务内复核拒绝而恢复。旧身份路径成功替换后重复请求 SHALL 返回404，不执行第二次换绑；客户端在结果未知时 SHALL 先通过身份列表确认状态，不能盲目自动重发。

#### Scenario: 校验成功但提交失败
- **WHEN** 新验证码已消费，之后事务明确回滚
- **THEN** 旧身份仍可用，但客户端必须按原冷却/额度规则重新取码，错误尝试或发送次数不会退还

#### Scenario: 成功响应丢失
- **WHEN** 换绑已提交但客户端未收到响应，随后重试同一旧身份路径
- **THEN** 返回404，客户端通过身份列表看到新活动身份；提交结果未知的故障不被描述为已确定回滚

### Requirement: 审计与可选能力兼容

成功换绑 SHALL 以相同 request_id 关联旧身份解绑、新身份绑定及相应会话撤销审计，reason 为 IDENTITY_REPLACED；可归因领域拒绝 SHALL 记录专用拒绝事件。审计 SHALL 保持异步和脱敏，既有枚举值不得重编号。库与 accountsvc SHALL 共享领域实现；旧自定义消费者服务实现 SHALL 不因缺少新方法而无法编译。

#### Scenario: 脱敏与回滚审计
- **WHEN** 换绑成功、失败或审计存储不可用
- **THEN** 记录中不包含验证码、完整目标、密文或完整摘要；回滚不产生成功事件，审计失败不改变业务结果

#### Scenario: 旧自定义消费者实现
- **WHEN** 宿主继续使用只实现原消费者服务接口的实现
- **THEN** 原有接口可编译且行为不变；已认证换绑请求返回503 IDENTITY_REPLACEMENT_NOT_CONFIGURED，标准 accountkit 服务自动提供新能力
