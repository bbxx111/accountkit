## Purpose

为管理应用提供 accountkit 官方 TypeScript 账号管理客户端，统一管理 HTTP 操作、资源模型、分页过滤和错误语义。管理员登录与会话生命周期由调用方提供，SDK 可在没有特定前端框架、身份提供方或宿主工作区的环境中独立消费。

## ADDED Requirements

### Requirement: 管理 API 覆盖与独立身份边界

SDK SHALL 覆盖正式基线的用户列表/详情、冻结/解冻、删除/恢复、身份明文查看、会话列表/单个撤销/全部撤销和认证审计查询。服务地址与前缀 SHALL 可配置，默认前缀为 /admin/v1。SDK SHALL 不提供消费者登录、管理员账号 CRUD、OIDC 登录、UserInfo、管理员探针、服务内省或 purge-feed，不解析 token 或替服务端判角色。

#### Scenario: 两个管理服务并存
- **WHEN** 同一应用同时访问账号管理 API 和其他业务管理 API
- **THEN** 此 SDK 仅生成账号管理协议请求，不探测业务服务或要求业务服务提供管理员探针

#### Scenario: 服务端角色拒绝
- **WHEN** 有效管理员令牌调用未授权的身份明文查看操作得到 403
- **THEN** SDK 暴露原权限错误，不先根据本地角色缓存放行或把拒绝变成成功

### Requirement: 外部令牌提供与无内置续期

每次发送请求前，SDK SHALL 调用注入的令牌提供方取得可用 Access Token。提供方失败、返回空值或请求已取消时 SHALL 不发送匿名请求；提供方错误 SHALL 保留可识别来源，不伪装成 HTTP 网络错误。SDK SHALL 不存储 refresh token、不调用登录/刷新端点、不清理应用会话或跳转页面；并发续期协调由令牌提供方承担。

#### Scenario: 获取令牌失败
- **WHEN** 令牌提供方报告续期依赖暂不可用
- **THEN** SDK 不调用 Fetch，不把错误转换为 401，不删除任何浏览器存储

#### Scenario: 并发请求
- **WHEN** 多个管理请求并发取得令牌
- **THEN** SDK 接收提供方协调后的结果，不另起第二套刷新队列或缓存过期令牌

### Requirement: 请求构造与单一响应解析责任

SDK SHALL 接收遵循标准 Fetch 语义的可选传输实现：HTTP 非成功状态仍返回响应，只有传输或取消失败才拒绝。SDK SHALL 统一完成 URL、JSON、Bearer 和请求 ID 构造并解析一次响应体。SDK SHALL 只向配置的账号 API 地址发送令牌，不接受任意资源 URL 作为资源名；重定向 SHALL 被拒绝。资源 ID/名称 SHALL 校验结构和父资源一致性，不能通过简单截取最后一段接受不匹配的资源。

#### Scenario: 自定义 Fetch 返回错误响应
- **WHEN** 传输返回包含 AIP 错误体的 400 Response
- **THEN** SDK 读取一次响应体并抛出类型化 API 错误，不要求调用方预解析，也不发生二次读取

#### Scenario: 子资源归属不一致
- **WHEN** 调用方指定用户 A，却传入用户 B 的完整会话资源名
- **THEN** SDK 在请求前拒绝该组合，不悄悄截取 ID 并对 A 发起操作

### Requirement: 类型化错误与未知字段兼容

SDK SHALL 区分本地参数、令牌提供、取消、传输、API 和协议错误。API 错误 SHALL 暴露 HTTP 状态、status、reason、message、可选等待秒数和响应请求 ID；未知 reason SHALL 原样保留。响应新增字段 SHALL 可忽略，未知合法枚举 SHALL 保留原值供调用方处理；畸形必填结构和非预期成功响应 SHALL 为协议错误。非 JSON 失败响应 SHALL 保留 HTTP 状态并使用固定安全消息，不泄露 HTML、请求凭据或原始响应体。

#### Scenario: 服务不可用
- **WHEN** API 返回 503 DEPENDENCY_UNAVAILABLE
- **THEN** SDK 抛出可识别错误，调用方可显示重试提示，SDK 不清理登录态

#### Scenario: 网关非 JSON 错误
- **WHEN** 上游返回 502 HTML 页面
- **THEN** 调用方得到带 502 状态的安全错误，不因 JSON 解析失败丢失 HTTP 状态或输出 HTML 正文

#### Scenario: 未来枚举与畸形数据
- **WHEN** 合法响应出现未知 state 字符串，或必填用户资源名缺失
- **THEN** 前者保留原状态值，后者报告协议错误，不伪造默认用户

### Requirement: 分页过滤与审计语义

SDK SHALL 支持用户 filter、show_deleted、page_size、page_token，以及认证审计分页。游标 SHALL 作为不透明字符串传递，不解码、拼造或跨集合复用。过滤构造工具 SHALL 覆盖正式允许的字段与 AND 组合并正确转义值；调用方也可显式提供原始 filter。SDK SHALL 返回每页及后续游标，不维护页面列表、不自动重置累计结果。INVALID_PAGE_TOKEN SHALL 作为错误交给应用决定恢复。审计 DTO SHALL 保留 null 和省略的差异，flow_id 无值时保持省略，不能泛化为任意业务审计数据。

#### Scenario: 审计旧游标
- **WHEN** 认证审计查询返回 INVALID_PAGE_TOKEN
- **THEN** SDK 返回原错误，不自动请求第一页或合并旧结果，应用可按自身代次清理并恢复

#### Scenario: 过滤与分页
- **WHEN** 调用方提交带特殊字符的合法过滤值和不透明 page_token
- **THEN** 过滤表达式按协议转义、查询参数按 URL 编码，游标内容保持不变

### Requirement: 敏感结果不缓存与安全可打印对象

SDK SHALL 不持久化身份明文、令牌、审计结果或完整过滤目标；身份 reveal 请求 SHALL 禁用浏览器缓存，普通请求不启用 SDK 响应缓存。错误与默认诊断 SHALL 不包含 Authorization、完整请求/响应正文或身份明文。SDK SHALL 保留消费者身份和管理员主体的不同模型含义，不输出内部存储列。

#### Scenario: 明文查看
- **WHEN** 授权调用 reveal 成功
- **THEN** 仅向调用方返回所需明文，不写入 localStorage、sessionStorage、SDK 日志或共享缓存

#### Scenario: reveal 失败
- **WHEN** reveal 返回 403 或协议错误
- **THEN** 错误消息和诊断不包含潜在明文，调用方仍能依据状态与 reason 处理

### Requirement: 取消与单次执行

所有网络方法 SHALL 支持取消。取令牌期间取消后 SHALL 不发送请求；发送后取消 SHALL 使用传输取消机制，并明确这不保证服务端事务回滚。SDK SHALL 不自动重试 HTTP 操作，包括 401、429、5xx 和网络故障；调用方可按业务语义决定重新发起。请求 ID SHALL 在单次调用中稳定，不替代幂等保证。

#### Scenario: 写操作响应丢失
- **WHEN** 冻结已可能提交但客户端遇到网络失败
- **THEN** SDK 不再次发送冻结，报告传输故障，调用方自行查询最终状态

#### Scenario: 取令牌期间取消
- **WHEN** 令牌提供方尚未完成，调用方取消请求
- **THEN** 即使提供方随后返回令牌也不发送该请求，取消不被显示为身份过期

### Requirement: 框架无关与可注入测试

SDK SHALL 不依赖 React、浏览器全局存储或具体 OIDC 库，纯导入 SHALL 不读取 window 或发起网络。调用方 SHALL 能注入令牌、Fetch 和请求 ID 来源，以测试完整 HTTP 与错误行为；使用环境 SHALL 提供标准 Fetch 及 AbortSignal 能力。

#### Scenario: 无浏览器环境导入
- **WHEN** 在具备标准 Fetch 的测试运行环境导入 SDK 并注入固定令牌
- **THEN** 不要求 window、DOM、管理员登录页或 Keycloak 实例即可构造和测试客户端
