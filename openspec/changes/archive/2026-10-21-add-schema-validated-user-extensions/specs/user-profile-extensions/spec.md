# Spec Delta

## Purpose

为嵌入式宿主提供结构可声明、修改可验证的账号用户资料属性能力。宿主使用 JSON Schema 定义属性，库负责离线校验、JSON Merge Patch、数据库账号行锁并发控制及隐私生命周期，使业务能够扩展用户资料而不绕开账号状态、事务和匿名化契约。

当前资料单列按本轮限定授权合入工作树 0001，新安装 version 1；无旧数据迁移，旧发布与当前定义独立固定。此整合不改变下述公开 API、Schema、账号行锁、时间和隐私行为，见[存储规格](../versioned-auth-storage/spec.md)。

## ADDED Requirements

### Requirement: 可选且离线的 Schema 配置

宿主 SHALL 通过 Go 配置提供Draft 2020-12 Schema 内容。初始化 SHALL 校验并编译一次，拒绝无效或超出支持范围的定义，不做数据库、文件或网络 I/O。仅支持当前文档内的无环引用，拒绝外部引用和运行时替换。未配置时 SHALL 关闭扩展读写而不影响既有认证能力；不得把未配置解释为允许任意 JSON。

#### Scenario: 配置缺失
- **WHEN** 宿主未配置扩展 Schema
- **THEN** 原有登录和资料接口可用，新增扩展读写返回可识别的未配置错误

#### Scenario: 错误配置尽早失败
- **WHEN** Schema 非法、引用外部资源或引用形成循环
- **THEN** 初始化失败且不访问数据库、网络或 Schema 文件

#### Scenario: 配置与调用方内存隔离
- **WHEN** 初始化成功后宿主修改原始 Schema 字节
- **THEN** 已初始化实例的校验规则保持不变，不发生数据竞争

### Requirement: 明确声明的资料属性

Schema SHALL 定义顶层对象并接受空对象。每个对象层级 SHALL 使用固定 `properties` 及 `additionalProperties: false`，属性名采用 lower_snake_case。对象、数组、字符串、布尔和数值 SHALL 有明确类型；支持无环本地复用及已声明字段间的条件约束，不允许动态属性名或由条件分支引入新属性。资料 SHALL 不存储 JSON null；现有账号内置属性不得在扩展顶层重复声明。未支持关键字或 format SHALL 在配置时明确拒绝。

#### Scenario: 字段名与类型
- **WHEN** 输入未声明属性或将字符串写入数值属性
- **THEN** 更新被拒绝，不丢弃未知字段或隐式转换类型

#### Scenario: 嵌套对象与字段间约束
- **WHEN** 部分更新破坏嵌套对象的 required 或宿主定义的字段间条件
- **THEN** 完整结果校验失败，其他属性也不保存

#### Scenario: 空资料与默认值
- **WHEN** 账号首次创建或原有账号升级后尚未写入扩展
- **THEN** 资料为 `{}`，不自动注入 Schema default，也不要求新增登录输入

#### Scenario: 注解不授予权限
- **WHEN** Schema 包含 readOnly、writeOnly、default 或 description
- **THEN** 注解不自动授予或限制业务访问权限、不隐藏响应字段、不生成默认资料

### Requirement: 有界且精确保真的 JSON

库 SHALL 限制 Schema 原始内容为 64 KiB，补丁原始内容和规范化资料分别为 16 KiB；资料及补丁根节点深度为 1，最大深度为 8、总值节点数为 512。Schema 最大 JSON 深度为 32、展开后属性声明为 128、校验节点为 1024，展开超限或循环 SHALL 拒绝。所有 JSON SHALL 拒绝重复键、非法 Unicode、尾随内容和 JSONB 不可存储值；数值解析、校验、比较和存储 SHALL 不经有损 float64 转换。

#### Scenario: 大整数往返
- **WHEN** 更新值为 `9007199254740993` 且满足字段约束
- **THEN** 校验、存储和读取保持精确数值

#### Scenario: 整数的标准语义
- **WHEN** integer 字段分别收到 `1.0`、`1.5` 和 `"1"`
- **THEN** 第一个按整数值校验，后两个拒绝，不按文本是否含小数点判断

#### Scenario: 容量与编码边界
- **WHEN** 原始补丁、规范化结果、深度或节点数超限，或者出现重复键、NUL、孤立代理项和尾随 JSON
- **THEN** 返回稳定参数错误，不先截断、替换字符或写入部分资料

### Requirement: 宿主专用资料接口

库 SHALL 通过 Config/Deps.ProfileAttributes、ProfileAttributesConfig/View/Patch 和 GetProfileAttributes/PatchProfileAttributes 提供独立的宿主 Go 读取和部分更新能力；Patch 只含 Document，Config 仅含 Schema，View 仅含 Data，无 Revision 或 Schema/时间元数据。宿主 SHALL 在调用前完成业务授权及字段访问控制；库 SHALL 验证资源 ID、存在性与生命周期，返回独立复制的数据。当前 HTTP 必需接口、公开资料、JWT、内省和认证 DTO SHALL 不隐式承载扩展，新增接口不把数据作为认证或授权依据。

后续 [self-profile-http](../../../2026-10-21-add-self-profile-http/specs/self-profile-http/spec.md) SHALL 提供独立本人资料 HTTP，所有资料对本人完整可读写；上段宿主授权指 Go 直接调用，不要求内置本人路由再执行字段授权。既有 HTTP 响应和必需方法集保持不变。启用 Schema 的部署 SHALL 不保存本人不可见或不可改字段，Schema 注解不限制本人访问。

#### Scenario: 未知账号与非法 ID
- **WHEN** 宿主传入格式错误的账号 ID 或合法但不存在的 ID
- **THEN** 分别得到参数错误或未找到错误，不创建账号或附属资料

#### Scenario: 数据不隐式公开
- **WHEN** 扩展中保存宿主私有资料，再调用现有 GetMe、管理详情、批量公开资料、JWT 或内省
- **THEN** 现有响应不增加扩展字段，也不改变认证结果

#### Scenario: 读取生命周期
- **WHEN** 已授权宿主读取 ACTIVE 或 FROZEN 账号扩展
- **THEN** 返回存储资料；读取 PENDING_DELETION 或 DELETED 返回可识别的状态拒绝，不返回原扩展内容

#### Scenario: 输入输出深拷贝
- **WHEN** 调用者在操作完成后修改补丁或返回资料的字节
- **THEN** 不影响后续查询、已提交数据或其他请求

### Requirement: 资料用途与隐私边界

`profile_attributes` SHALL 仅承载宿主审核过的用户资料（如 bio、locale、timezone、pronouns），不得存放宿主业务状态、权益或权限，不提供字段级加密。宿主 SHALL 管理字段用途及隐私白名单，不通过扩展复制明文手机号/邮箱或保存验证码、令牌和密钥；这些数据继续遵循既有身份及凭证契约。Schema 通过 SHALL 不被解释为已验证任意值的隐私安全，扩展 SHALL 不成为认证权限判断来源。

#### Scenario: 配置资料字段
- **WHEN** 宿主接入扩展 Schema 并开放自己的资料接口
- **THEN** 接入说明明确字段白名单、可见性及数据用途，不能把结构校验或敏感键检查称为完整隐私识别

### Requirement: JSON Merge Patch 部分更新

部分更新 SHALL 使用 RFC 7396 合并规则，且顶层补丁必须为对象。缺失属性保持、对象递归合并、数组整体替换、属性 null 删除。所有涉及的对象属性 SHALL 属于声明目录，包括值为 null 的属性；不存在但已声明属性的删除为无变化操作。库 SHALL 校验合并后的完整资料，任一错误整次拒绝；不能用完整资料 Schema 直接校验补丁或将部分成功写入。

#### Scenario: 修改部分嵌套属性
- **WHEN** 原 preferences 含 locale 和 timezone，补丁仅修改 locale
- **THEN** locale 更新且 timezone 保留

#### Scenario: 删除属性与替换数组
- **WHEN** 补丁以 null 删除可选 bio 并以空数组更新 tags
- **THEN** bio 不再存在，tags 保存为空数组，其他属性保持

#### Scenario: 未知属性删除
- **WHEN** 补丁为 `{"unknown_field":null}` 且字段未声明
- **THEN** 在保存前拒绝，不能因合并后不存在该键而绕过白名单

#### Scenario: 空对象不是清空指令
- **WHEN** 已存在的 preferences 对象收到空对象补丁
- **THEN** 其子属性保持；删除整个 preferences 需要 null

#### Scenario: 删除必填子属性
- **WHEN** 补丁删除已有嵌套对象中的必填属性
- **THEN** 完整候选资料不符合 Schema，原始资料及账号 update_time 保持

### Requirement: 账号行锁与原子更新

普通写入 SHALL 仅允许 ACTIVE。并发控制 SHALL 仅使用数据库账号行锁，在内部 Read Committed 领域事务中依次完成锁内最新读取、RFC 7396 Merge Patch、完整 Schema 与存储边界校验、原子写入和提交。SHALL 不使用 Redis 锁、revision、expected_revision 或版本冲突错误。实际变化 SHALL 更新资料与账号 update_time；无变化仍检查配置、锁后状态与 Schema，但不改变资料或账号时间。

#### Scenario: 不重叠属性并发更新
- **WHEN** 两个请求并发修改同一账号不同且不重叠的资料属性
- **THEN** 两者按行锁顺序执行并可成功，后者读取前者提交的最新资料，最终保留双方修改

#### Scenario: 同属性并发更新
- **WHEN** 两个请求更新同一属性
- **THEN** 以后取得账号行锁并执行的事务为准，不返回版本冲突

#### Scenario: 数组与父对象竞争
- **WHEN** 后执行补丁替换数组或以 null 删除父对象
- **THEN** 按 RFC 7396 整体替换或删除，即使影响先执行事务写入的数组元素或子属性；完整结果仍须满足 Schema

#### Scenario: 空补丁与重复值
- **WHEN** 合法请求提交 {}、删除不存在的已声明属性或重复同值
- **THEN** 返回现有资料，不改变账号 update_time或发出成功修改事件

#### Scenario: 锁等待后状态变化
- **WHEN** 请求等待账号锁时账号被冻结或进入注销冷静期
- **THEN** 锁后复核拒绝更新，不按请求开始时的状态写入

#### Scenario: 较高宿主隔离级别
- **WHEN** 连接池默认 Repeatable Read 或 Serializable，多个更新或注销请求竞争账号锁
- **THEN** 内部领域事务仍使用 Read Committed 并读取锁后最新资料；公开仓储隔离级别保持

#### Scenario: 失败与取消
- **WHEN** 合并校验、SQL、事务提交或取消导致更新未成功提交
- **THEN** 不返回成功，不留下部分资料、账号时间或成功修改审计

#### Scenario: 提交结果不确定
- **WHEN** 提交时连接中断而无法确认事务结果
- **THEN** 返回错误，不承诺确定回滚；宿主重新读取核对，库不盲目重放补丁

### Requirement: 当前 Schema 与历史资料

库 SHALL 按当前 Schema 校验完整合并结果。宿主保证同一数据库实例的 Schema 配置一致。库不保存逐行 Schema 身份，不提供 SchemaMismatch 错误。读取只检查通用存储边界；更新按当前 Schema 校验完整合并结果，无变化请求也不得绕过。新增可选属性、放宽约束、修改注解可直接更新配置。收紧规则可能使旧资料完整结果不合法，更新拒绝且原资料与账号 update_time 保持；同次补丁可修复后保存。移除声明属性后，遗留属性仍不能以未知属性 null 删除，须通过过渡 Schema 或专项迁移清理；库不自动丢弃字段。

#### Scenario: 兼容调整
- **WHEN** 一致部署新增可选属性、放宽约束或修改注解的 Schema
- **THEN** 补丁可直接保存，无逐行身份匹配

#### Scenario: 收紧规则与同次修复
- **WHEN** 当前 Schema 收紧使旧资料不合法
- **THEN** 读取仍可返回，未修复的更新拒绝且原值与账号时间保持，同次补丁修复后可保存

#### Scenario: 移除声明后的遗留属性
- **WHEN** 已存属性不再声明且补丁以 null 删除它
- **THEN** 拒绝未知删除，宿主使用过渡 Schema 或专项迁移，库不自动丢弃

### Requirement: 错误和审计隐私

库 SHALL 暴露 ErrProfileAttributesNotConfigured 及稳定可识别的配置缺失、参数、Schema 校验及状态错误；Schema 校验错误只含受控属性路径和规则原因。成功内容变更 SHALL 在事务提交后通过 USER_PROFILE_ATTRIBUTES_UPDATED 既有尽力异步审计记录固定服务 actor、账号目标及请求 ID，不记录原值、新值、补丁、完整 Schema 、版本/指纹/资料时间元数据或任意原始校验器错误。审计失败 SHALL 不回滚已提交资料。

#### Scenario: 校验器携带输入值
- **WHEN** 第三方校验器错误包含个人简介、未知属性名或 enum 内容
- **THEN** 公开错误和日志不透传原始内容；未知属性只报告已声明的最近父路径

#### Scenario: 更新审计失败
- **WHEN** 更新提交后审计投递失败
- **THEN** 更新仍返回成功，按既有方式观测失败，不制造资料回滚或重试假象

#### Scenario: 普通操作不会写成功修改事件
- **WHEN** 读取、无变化更新或失败请求发生
- **THEN** 不发出成功修改事件，失败诊断不包含用户资料属性内容
