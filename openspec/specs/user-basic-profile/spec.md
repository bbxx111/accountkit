# user-basic-profile Specification

## Purpose

为消费者与嵌入式宿主提供一致的基础账号资料，支持显示名和头像地址的读取及原子部分更新，并在账号冻结、注销恢复及最终匿名化时保持明确的资料访问和隐私边界。

## Requirements

### Requirement: 内置头像资料
账号 SHALL 保存可选 avatar_url；未设置以数据库 NULL 表示，Go 和 HTTP 读取返回空字符串。本人资料、管理列表及详情 SHALL 返回该字段。头像 SHALL 独立于可选 profile_attributes Schema，扩展顶层不得重复声明 avatar_url。头像文件上传、存储、默认头像和第三方头像的持续同步由宿主负责。其他规格规定的一次性填充以及登录补填提供方（ALIPAY、QQ、WEIBO）后续成功登录的空字段补填，可以把已通过地址校验的 URL 写入仍为空的 avatar_url；库 SHALL 不访问该地址。

#### Scenario: 新账号和已设置头像
- **WHEN** 读取新账号或已设置头像的账号
- **THEN** avatar_url 分别为空字符串或已保存的地址，不增加 JWT 或内省字段

#### Scenario: 一次性填充不转移文件责任
- **WHEN** 其他规格把通过地址校验的 URL 写入仍为空的 avatar_url
- **THEN** 读取返回该地址，库不访问它；宿主继续负责文件、默认头像和此后的同步

#### Scenario: 支付宝补填头像
- **WHEN** 支付宝后续登录取得可信且合法的头像 URL，写入时账号头像仍为空
- **THEN** 可以保存该 URL，不下载图片，不对非空头像做持续同步

#### Scenario: QQ 补填头像
- **WHEN** QQ 后续登录取得可信且经改写、净化后合法的头像 URL，写入时账号头像仍为空
- **THEN** 可以保存该 HTTPS URL，不下载图片，不对非空头像做持续同步

#### Scenario: 微博补填头像
- **WHEN** 微博后续登录取得可信且净化后合法的 HTTPS 头像 URL，写入时账号头像仍为空
- **THEN** 可以保存该 URL，不下载图片，不对非空头像做持续同步

### Requirement: 有界 HTTPS 地址

头像输入 SHALL 去除首尾空白，规范化后不超过 2048 字节；非空值必须是合法 UTF-8、具有非空主机的绝对 HTTPS URL，不含用户凭据、fragment、反斜线、内部空白或控制字符。校验失败 SHALL 返回参数错误，不回显原始地址。库 SHALL 不访问该地址。

#### Scenario: 地址校验
- **WHEN** 提交合法 HTTPS 地址或空字符串
- **THEN** 分别设置头像或清空头像

#### Scenario: 非法输入
- **WHEN** 提交 HTTP、相对路径、data URL、带凭据/fragment 的地址、非法编码或超长地址
- **THEN** 整次更新返回 400 INVALID_ARGUMENT，原资料及 update_time 保持

### Requirement: 原子部分更新与兼容

PATCH /users/me SHALL 支持 display_name 和 avatar_url，省略或 null 不修改，空字符串清空，无有效字段返回 NO_FIELDS。新增 Go 原子更新能力 SHALL 在账号行锁内复核 ACTIVE 状态；不存在账号返回未找到，非法 ID 返回参数错误。同次更新任一值错误 SHALL 不写入。既有 UpdateDisplayName 和消费者必需 Service 方法集 SHALL 保持兼容；旧适配器的显示名更新仍可用，头像请求缺少能力时返回 503 PROFILE_UPDATE_NOT_CONFIGURED。

#### Scenario: 两字段一次修改
- **WHEN** 本人提交合法显示名和头像
- **THEN** 两者一次原子保存并返回完整本人资料

#### Scenario: 并发部分更新
- **WHEN** 两个请求分别更新显示名和头像
- **THEN** 最终保留双方修改；等待锁期间变为非 ACTIVE 的新原子更新拒绝

#### Scenario: 旧适配器
- **WHEN** 宿主仅实现原消费者 Service
- **THEN** 原显示名请求继续工作，含非 null 头像的请求返回固定 503，不部分更新显示名

### Requirement: 头像隐私生命周期

冻结和注销冷静期 SHALL 保留头像，恢复后仍可读取；PENDING_DELETION/DELETED 的批量公开资料 SHALL 隐藏头像。最终匿名化 SHALL 在既有事务内清空 avatar_url，宿主回调失败时整体回滚。新原子更新 SHALL 不允许在匿名化后重新写入头像。

#### Scenario: 恢复与清理回滚
- **WHEN** 账号在冷静期内恢复，或最终匿名化的宿主回调失败
- **THEN** 原头像保留；成功匿名化后头像为空

### Requirement: 显示名长度
显示名 SHALL 去除首尾空白。空字符串 SHALL 清空该字段。非空值 SHALL 为合法 UTF-8，不含控制字符或双向格式控制符，且不超过 128 个 Unicode 码位。超限或含禁止字符 SHALL 拒绝整次资料更新，不回显原值，原显示名和 update_time SHALL 保持。

#### Scenario: 接受较长显示名
- **WHEN** 本人提交不超过 128 个码位的合法显示名
- **THEN** 保存该值，本人资料和公开资料返回它

#### Scenario: 超过上限或含禁止字符
- **WHEN** 本人提交超过 128 个码位或含禁止控制符的显示名
- **THEN** 返回 400 INVALID_ARGUMENT，原显示名和 update_time 保持

### Requirement: 提供方登录按字段补填空资料
登录补填提供方集合 SHALL 为 ALIPAY、QQ 与 WEIBO。集合内提供方的首次及后续成功登录 SHALL 使用本次可信并净化后的 name/picture 声明分别填充仍为空的 display_name/avatar_url，不覆盖任一非空字段。空值 SHALL 按现有 NULL 或空字符串表示判断；用户主动清空也属于可补填空值，不新增自动补填开关或来源标记。资料缺失或无效时 SHALL 保持对应字段和既有元数据，不使用历史元数据替代本次失败的资料结果。集合外的提供方 SHALL 保持仅首次写入身份时填充。

#### Scenario: 首次失败后成功
- **WHEN** 首次支付宝、QQ 或微博登录资料接口失败但身份登录成功，后续登录取得有效昵称及头像
- **THEN** 在后续登录提交时补填仍为空的字段，既有用户 ID 和该提供方身份 ID 不变

#### Scenario: 已有昵称只有头像为空
- **WHEN** 登录取得昵称与头像，而账号显示名已经非空
- **THEN** 仅补填头像，不将提供方昵称覆盖到账号显示名

#### Scenario: 主动清空后的登录
- **WHEN** 用户主动清空头像，之后一次支付宝、QQ 或微博登录取得合法头像
- **THEN** 按空字段规则补填，文档明确主动清空不表示永久关闭提供方补填

#### Scenario: 资料失败不使用旧缓存
- **WHEN** 本次资料查询失败，身份元数据中有历史 profile，而账号资料为空
- **THEN** 本次不使用历史 profile 自动回填，允许登录并等待下一次取得可信资料

#### Scenario: 集合外提供方不补填
- **WHEN** 已有 GOOGLE 身份再次登录并返回资料，而账号显示名为空
- **THEN** 按既有规则只更新提供方元数据，不补填账号显示名

### Requirement: 提供方资料补填的原子性
资料补填 SHALL 与相应账号、身份及会话操作处于同一领域事务，在写入时按既有账号锁序复核 ACTIVE 状态与每个字段是否仍为空。失败、身份冲突、账号生命周期拒绝或事务回滚 SHALL 不留下补填结果。pending 完成 SHALL 只使用已保存的净化声明，不重新访问提供方；仅创建 pending 不得改写任何账号资料。该要求适用于登录补填提供方集合内的每个提供方。

#### Scenario: 用户并发设置资料
- **WHEN** 登录取得资料后，用户的 PATCH 已先提交非空昵称或头像
- **THEN** 登录写入时重新检查字段，保留用户已提交的值，不使用查询前快照覆盖

#### Scenario: 冻结或注销竞争
- **WHEN** 验证身份后账号变为 FROZEN、PENDING_DELETION 或 DELETED
- **THEN** 按既有账号状态规则拒绝对应操作，未提交的资料补填回滚，不在匿名化后补回资料

#### Scenario: pending 关联到已有账号
- **WHEN** 首次支付宝、QQ 或微博登录进入 pending，完成时关联到已有 ACTIVE 账号
- **THEN** 在完成事务内仅填充目标账号的空字段，保留并发或此前设置的非空资料

#### Scenario: pending 并发取得同一身份
- **WHEN** pending 创建后同一提供方身份已由另一成功流程关联到最终目标账号
- **THEN** 完成流程按原冲突与幂等规则复核归属，仅在允许完成的事务中执行空字段补填

#### Scenario: 登录最后阶段失败
- **WHEN** 补填后创建会话或保存客户端信息失败并回滚
- **THEN** 账号资料、身份元数据及会话保持事务前状态
