# self-profile-http Specification

## Purpose
让用户通过内置消费者 HTTP 接口完整读取和修改本人资料属性，复用既有 Schema、并发及账号生命周期规则，使嵌入式宿主与可选服务能够提供一致的自助资料管理能力，同时隔离其他用户和既有公开响应。

## Requirements

### Requirement: 本人资料资源与完整访问

消费者面 SHALL 提供相对 GET/PATCH `/users/me/profile`，目标仅来自认证主体。所有 profile_attributes SHALL 允许本人完整读取和按当前 Schema 修改或删除，不提供字段权限过滤，readOnly/writeOnly 注解 SHALL 不改变访问。该明确契约取代基础 user-profile-extensions 首版“仅 Go、不新增内置路由”的限制；Go 宿主直接调用仍自行授权。

#### Scenario: 本人完整读取
- **WHEN** 配置有效且本人已认证，资料读取符合生命周期约束
- **THEN** GET 返回 200 和 `{"name":"users/{实际用户ID}/profile","profile_attributes":{...}}`，包含所有存储资料；空资料返回 {}，无 Schema/revision/时间字段

#### Scenario: 不可指定他人
- **WHEN** 用户尝试请求其他用户路径或在请求体提供 user_id/name
- **THEN** 未注册的用户路径返回 404，PATCH 非法外层字段返回 400；查询参数不能改变目标账号，其他用户资料不被读写

#### Scenario: Schema 注解不限制本人
- **WHEN** 已声明属性带 readOnly 或 writeOnly 注解且补丁通过实际校验
- **THEN** 本人仍可修改并完整读取该属性

### Requirement: 认证范围与生命周期

两个路由 SHALL 要求有效消费者 Bearer 及 user scope，不要求近期重新认证。GET SHALL 延续 ACTIVE/FROZEN 领域读取，PATCH SHALL 仅允许 ACTIVE；身份补全或注销恢复令牌 SHALL 不获得此能力。SHALL 不改变现有登录、吊销或冻结认证规则。

#### Scenario: 缺失凭据或作用域不足
- **WHEN** 缺失/无效凭据或仅有 user:bind/user:undelete
- **THEN** 前者 401 且含 WWW-Authenticate，后者 403 INSUFFICIENT_SCOPE，不读取或修改资料

#### Scenario: 状态改变
- **WHEN** 已通过认证的请求执行时账号为 FROZEN、PENDING_DELETION 或 DELETED
- **THEN** FROZEN 可读取但不能更新，后两者不能读写；领域状态错误为 400 FAILED_PRECONDITION / INVALID_ACCOUNT_STATE，失败不改变资料

### Requirement: 显式部分更新协议

PATCH SHALL 接受 application/json 外层对象，唯一且必需字段 profile_attributes 为对象，使用 RFC 7396 内层合并规则；请求与响应外层不是通用 Merge Patch 文档。SHALL 拒绝外层缺失、null、重复键、未知字段或尾随内容。成功 SHALL 返回 200 及与 GET 相同的完整资源。内层未知属性（含 null 删除）和不满足完整 Schema 的结果 SHALL 整次拒绝。

#### Scenario: 修改和删除
- **WHEN** 请求为 `{"profile_attributes":{"bio":null,"preferences":{"locale":"zh-CN"}}}` 且字段均已声明、完整结果合法
- **THEN** 删除 bio，合并 locale，保留其他属性及 preferences 其他子属性，返回完整更新后资料

#### Scenario: 空补丁与错误外壳
- **WHEN** 分别提交 `{"profile_attributes":{}}`、`{}`、`{"profile_attributes":null}`
- **THEN** 第一个在当前 Schema 合法时不改变资料、时间或审计；后两者拒绝，绝不清空资料

#### Scenario: 数组和并发
- **WHEN** 合法补丁替换数组或两个请求并发修改不重叠属性
- **THEN** 数组整体替换；不重叠修改均保留，重叠以后取得账号锁并执行的事务为准，无 revision/版本冲突协议

### Requirement: 保真边界与安全错误

HTTP SHALL 保留内层原始字节和数值精度；外层总大小遵循配置的 HTTP body 上限，内层原始补丁与规范化资料分别不超过 16 KiB，延续深度 8、512 值节点、重复键、非法 Unicode 和 null 存储限制。SHALL 使用既有 apierror 外壳和固定新增错误消息，不返回或记录补丁、资料值、未知键或底层解析错误。

#### Scenario: 大整数和重复键
- **WHEN** 请求含合法大整数 9007199254740993 或任意层重复键
- **THEN** 前者往返精度保持，后者拒绝且无写入

#### Scenario: 参数错误
- **WHEN** 请求 JSON 语法错误（包括内层）、外层字段/编码/总大小错误，或语法可解析的内层非对象/重复键/编码/容量/Schema 错误
- **THEN** 分别返回 400 INVALID_ARGUMENT 下的 MALFORMED_BODY、INVALID_PROFILE_ATTRIBUTES，消息固定且不泄露请求内容

#### Scenario: 存储与依赖错误
- **WHEN** 账号不存在、已识别依赖不可用、未知存储错误或提交结果不确定
- **THEN** 分别返回 404 NOT_FOUND、503 DEPENDENCY_UNAVAILABLE、固定 500 INTERNAL；不把存储失败转成 401，不自动重放补丁

### Requirement: 可选能力兼容与隐私隔离

SHALL 保持消费者必需 Go 方法集不变，以可选能力支持新路由。能力或 Schema 未配置 SHALL 在认证后返回 503 PROFILE_ATTRIBUTES_NOT_CONFIGURED，而非空资料成功；已实现能力的格式错误可先于领域配置缺失返回。全部新路由响应 SHALL 带 Cache-Control: no-store，包括认证失败。既有用户/管理/公开资料、JWT、内省 SHALL 不自动加入资料属性。

#### Scenario: 旧适配器和关闭配置
- **WHEN** 宿主仅实现原必需方法集，或资料 Schema 为 nil
- **THEN** 原端点及源码兼容，合法认证及合法请求访问新路由得到 503，不删除已存资料

#### Scenario: 响应和日志隔离
- **WHEN** 新端点成功、认证失败或发生含私密输入的错误
- **THEN** 响应均 no-store；错误和日志不包含私密输入，原端点不增加 profile_attributes

#### Scenario: 修改审计
- **WHEN** 更新实际改变资料并成功提交
- **THEN** 沿用 USER_PROFILE_ATTRIBUTES_UPDATED 的 SERVICE/accountkit-host actor 和 request_id，不记录内容；读取、失败、无变化不产生成功修改审计，审计失败不回滚资料
