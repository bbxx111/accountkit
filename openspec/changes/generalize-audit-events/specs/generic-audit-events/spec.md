## Purpose

定义可以由认证和其他业务共同使用的审计事件契约，使生产方、宿主存储和查询消费者对事件身份、操作者、目标、时间、扩展数据及隐私处理具有一致且可验证的理解，同时避免认证专属字段侵入公共模型。

## ADDED Requirements

### Requirement: 通用审计结构
系统 SHALL 使用 id、source、action、result、actor、targets、context、data、occur_time、create_time 十个顶层存储字段，不要求业务提供 user_id、session_id、flow_id 或 identity_kind 专属列。actor/context/data SHALL 为对象，targets SHALL 为数组；空集合分别表示为 {} 或 []。生产方 SHALL 提供实际 occur_time，存储方 SHALL 生成首次 create_time；不得以入库时间或 UUID 时间替换事件发生时间。不设置公共 schema_version。

#### Scenario: 非认证事件
- **WHEN** 宿主记录订单审批事件
- **THEN** 通过相同结构保存操作者、订单目标和业务数据，不需要提供认证流程字段

#### Scenario: 延迟投递
- **WHEN** 事件发生后经过排队才写入存储
- **THEN** occur_time 保持原发生时间，create_time 记录当前存储首次写入时间

### Requirement: UUIDv7 事件身份
事件 SHALL 在首次投递前生成 UUIDv7，数据库使用原生 UUID 全局主键，对外使用标准小写带连字符字符串。重投递 SHALL 保持事件 ID；其他账号、身份、会话和流程标识不因此改变。事件 ID SHALL 不被当作跨节点严格时间顺序或凭证。

#### Scenario: 多目标投递与重试
- **WHEN** 同一事件被重试或投递至不同存储
- **THEN** 保持同一 UUIDv7，接收方能关联同一事件而无需猜测请求 ID

#### Scenario: 非法标识
- **WHEN** 输入 UUID 格式、variant 或版本不符合 UUIDv7
- **THEN** 该事件被明确拒绝，不转换为截断字符串或静默接受另一版本

### Requirement: 来源动作与结果
source/action SHALL 为非空、最长 128 个 ASCII 字符的稳定标识；source SHALL 由可信配置提供，action SHALL 使用 UPPER_SNAKE_CASE 并允许各业务独立扩展。result SHALL 以 1 表示 SUCCESS、2 表示 FAILURE，未知或不适用为空；零值 UNSPECIFIED 不存储，业务状态不得混入结果枚举。非法输入 SHALL 被拒绝而非截断。

#### Scenario: 新业务动作
- **WHEN** 宿主增加 ORDER_APPROVED 等合法动作
- **THEN** 不要求向 accountkit 的认证事件枚举追加该业务动作

#### Scenario: 长度与结果非法
- **WHEN** 来源或动作为空、超长、格式非法，或结果为不支持的值
- **THEN** 不持久化该事件，并提供安全的校验失败观测

### Requirement: 操作者身份
actor SHALL 包含 type，并以 namespace + id 表示已知主体稳定身份，name 仅为可选展示快照。type SHALL 支持 USER、SERVICE、SYSTEM、ANONYMOUS、UNKNOWN；已知主体必须同时提供 namespace/id，匿名和未知主体省略二者。管理员与消费者 SHALL 保留各自身份来源，角色不得成为主体唯一标识，用户声称的未验证身份不得被视为已知操作者。

#### Scenario: 两个身份系统同号
- **WHEN** 消费者和外部管理员使用相同的主体字符串
- **THEN** 不同 namespace 使二者保持可区分，不合并认证或授权身份

#### Scenario: 未验证登录目标
- **WHEN** 登录失败且只知道请求提交的手机号或邮箱
- **THEN** 不将原始目标作为已认证 actor.id，不因此泄露完整目标

### Requirement: 目标和关联资源
targets SHALL 记录直接操作资源；context.related_resources SHALL 记录相关但未直接操作的资源。两者的资源引用 SHALL 使用 namespace、type、id 和可选 name，按完整引用去重，允许空数组且不赋予顺序授权含义。账号审计查询 SHALL 明确覆盖该账号作为操作者、直接目标及关联资源的适用认证事件，并保留既有 flow 归属关联覆盖。

#### Scenario: 管理员撤销会话
- **WHEN** 管理员撤销属于某账号的会话
- **THEN** actor 为管理员、target 为会话、related resource 为所属账号，账号审计查询能够找到事件

#### Scenario: 资源被删除
- **WHEN** 被记录的资源后来被删除
- **THEN** 历史审计引用仍保留，不要求资源当前存在才能解释事件

### Requirement: 扩展数据与输入边界
context SHALL 允许受控请求/追踪/客户端信息；data SHALL 承载业务自定义对象，认证专属字段进入 data.accountkit。主体及资源 namespace/id 各最多 512 字节、可选 name 最多 256 Unicode 字符，资源 type 最多 128 ASCII 字符；完整 UTF-8 JSON 事件最多 64 KiB，targets/related_resources 各最多 32 项。写入 SHALL 校验结构并冻结可变内容，拒绝重复 JSON 键。既有更严格输入约束保持；新限制若影响旧合法请求 SHALL 在实施前明确兼容处理，不静默截断。

#### Scenario: 调用方修改原对象
- **WHEN** 调用方记录事件后修改原 data 或资源数组
- **THEN** 已进入异步处理的事件身份、内容和去重结果不受后续修改影响

#### Scenario: 跨域敏感数据
- **WHEN** 认证事件包含凭证、完整目标、完整摘要或宿主原始错误
- **THEN** 映射和校验阻止其进入审计、错误响应与错误日志，不能借 JSON 扩展绕过脱敏

### Requirement: 无公共版本列的兼容演进
同一 action 的已有字段 SHALL 保持类型、单位和含义，新增可选字段 SHALL 不破坏旧消费者。结构不兼容变化 SHALL 使用新字段、具有新含义的动作或业务命名空间内明确的格式标识；历史缺失字段 SHALL 不被伪造为已知信息。

#### Scenario: 扩展历史事件
- **WHEN** 新事件增加可选业务字段而历史记录没有该字段
- **THEN** 两者均可读取，消费者忽略未知可选字段，历史缺失保持缺失

### Requirement: 幂等与冲突可区分
相同 ID 的重投递 SHALL 不创建第二条记录、不更新 create_time、不覆盖已有事件。真正的同 ID 不同事件 SHALL 明确判定为冲突，不通过重新生成 ID 隐藏问题。允许隐私清理造成的受控字段差异 SHALL 通过清理投影规则处理，不恢复已清理内容。

#### Scenario: 并发重复投递
- **WHEN** 多个写入者并发提交同一 ID 的同一事件
- **THEN** 最多保存一条且首次 create_time 不变，重复与失败计数可区分

#### Scenario: 标识冲突
- **WHEN** 相同 ID 的事件具有不同 source、action、稳定资源引用或发生时间
- **THEN** 拒绝冲突事件，已存在记录保持不变

#### Scenario: 脱敏后重放
- **WHEN** 已保存事件经过合法隐私清理后收到其原始副本
- **THEN** 不恢复已清除的个人信息，不将受控清理差异误当作新的事件

### Requirement: 账号匿名化与迟到审计
本地账号匿名化 SHALL 保留事件事实及稳定资源关联，清理规定的身份提示、请求侧及展示隐私字段，并覆盖同一账号关联的补全流程。并发插入、异步迟到和重复投递 SHALL 不在清理后重新保存这些字段；未知或已消失流程的迟到事件 SHALL 保守去除隐私信息。已解绑身份和既有历史归属覆盖 SHALL 保持。

#### Scenario: purge 与事件入库竞争
- **WHEN** 审计入库与账号或相关流程清理并发
- **THEN** 无论谁先完成，最终存储不保留已要求清理的字段，账号与其他事务保持既有锁序和回滚约束

#### Scenario: 流程已清除
- **WHEN** 无账号标识的补全事件在流程被清除后才到达
- **THEN** 不依赖已丢失的流程对象保留请求侧隐私，也不泄露或错误关联其他账号

### Requirement: 审计排序与游标
审计时间线 SHALL 按 occur_time 和唯一 id 排序；游标 SHALL 绑定查询范围且仅用于翻页，不代替权限检查。更换审计 ID 格式后旧审计游标 SHALL 返回 400 INVALID_PAGE_TOKEN 并提示重新开始；用户列表等其他游标契约保持。

#### Scenario: 同一时间多事件
- **WHEN** 多条审计事件具有相同 occur_time
- **THEN** 静态数据集翻页以 id 消除并列，不重复或遗漏已有记录

#### Scenario: 旧游标或跨账号游标
- **WHEN** 使用旧 e_ 审计游标或将另一查询范围的游标用于当前账号
- **THEN** 明确拒绝，不改变授权范围或返回其他账号事件
