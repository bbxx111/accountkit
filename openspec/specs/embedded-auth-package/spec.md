# embedded-auth-package Specification

## Purpose

让宿主应用通过独立 Go 包获得 完整账号认证能力，保持客户端协议、账号数据和安全行为兼容，并允许无关产品分别配置依赖、凭证和存储，实现独立部署和版本升级。

## Requirements

### Requirement: 独立消费和完整功能保留
包 SHALL 能独立构建、测试和被宿主导入，不依赖外部源码工作区。首版 SHALL 保留固定基线的全部公开认证能力；允许明确声明的包名迁移、迁移安全变更和本次验证码轮次契约升级；后者 SHALL 在新固定版本说明 Go/HTTP 破坏性变化，同步更新调用者，不保留旧无标识验证旁路。

本次身份策略与登录补全 SHALL 作为明确的 Go/HTTP 协议升级发布，提供调用迁移说明；旧登录即建号行为不能作为绕过新策略的生产旁路。

通用审计升级 SHALL 明确声明公开事件/读侧 Go 类型、审计 UUIDv7 资源名和旧审计游标的兼容边界，提供记录器、查询适配器和历史数据迁移说明；不扩大为修改消费者令牌或其他资源格式。

#### Scenario: 独立宿主构建
- **WHEN** 一个独立的 Go 项目依赖 accountkit 并注入所需依赖
- **THEN** 项目能够编译并挂载消费者与管理端接口，不需要外部宿主服务代码、accountsvc 进程或特定管理员 OIDC 实现包

#### Scenario: 自定义审计升级
- **WHEN** 宿主升级到通用审计版本并按迁移说明适配原自定义记录器
- **THEN** 能接收通用事件且不依赖 sqlc 行，源码变化和新增查询配置要求可明确核对

### Requirement: 登录及身份管理兼容
包 SHALL 保留手机号、邮箱验证码、微信和 Apple 登录、身份列表与绑定解绑行为，包括格式校验、验证码有效期和限流、第三方凭证验证及重复身份冲突处理。PHONE/EMAIL 的登录、绑定、重新认证和换绑 SHALL 使用必需 code_id 的轮次协议；错误码、过期码保持原错误类别，新增跨轮次失败预算以 TARGET_VERIFY_LIMIT 明确返回。

登录 SHALL 统一执行 EnabledIdentityKinds 与 RequiredIdentityKinds，完整性只支持 any-of。未知身份缺少必需类型时 SHALL 返回无 user_id 的待完成结果；已满足条件时直接注册，不提供可选延后注册参数。补充身份验证后 SHALL 自动关联其已有活动账号或原子注册，无需用户先选择注册或关联；普通绑定 SHALL 继续拒绝另一个已有账号占用的身份。

#### Scenario: 验证码登录
- **WHEN** 客户端使用新轮次协议发送验证码并携带同轮 code_id、目标、有效验证码及设备标识登录
- **THEN** 按身份归属和完整性返回保留原字段的令牌或待完成结果；错误验证码、过期验证码及原有发送额度耗尽保持原错误类别，目标累计验证失败上限按新预算协议返回

#### Scenario: 第三方登录与绑定
- **WHEN** 已配置的微信或 Apple 提供方返回有效凭证，或用户通过现有接口绑定与解绑身份
- **THEN** 保留原凭证验证、nonce 防重放（适用时）和身份唯一性，并按当前策略保护至少一个可用登录身份

#### Scenario: 未完成登录不是受限用户
- **WHEN** 未知第三方身份尚未满足 Required
- **THEN** 只签发待完成票据，不创建用户、身份或会话，不签发 user:bind 令牌

### Requirement: 令牌与会话兼容
包 SHALL 保留令牌签名与校验、scope、刷新轮换、宽限重试、会话查询及单个和其他会话撤销。默认访问令牌有效期为 15 分钟，刷新令牌为 30 天滑动有效期，刷新宽限为 30 秒。活跃会话查询 SHALL 排除已吊销或刷新期限已到的记录；刷新与重新认证 SHALL 在锁后复核会话期限。已签发access的验证及显式撤销覆盖 SHALL 保持既有契约，不把刷新期限当成新的JWT到期条件。

ACTIVE 账号的新登录、新一轮刷新及重新认证 SHALL 按全部有效身份计算 scope，满足 Required 返回 user，否则 user:bind；原宽限结果 SHALL 不重新计算。现有手机/邮箱重新认证 SHALL 保留会话及 refresh；第三方账号的近期认证需求 SHALL 使用正常重新登录，不新增专用证明端点。重新登录 SHALL 沿用同设备会话替换和新 token pair 契约。

#### Scenario: 刷新重试
- **WHEN** 同一刷新请求在宽限内重试且会话仍未吊销、未到刷新期限
- **THEN** 按基线返回同一轮换结果；宽限外的旧刷新凭证遵循原失效规则

#### Scenario: 敏感操作重新认证
- **WHEN** 默认启用敏感操作验证且用户认证时间超过 5 分钟
- **THEN** 受保护操作要求重新认证；会话仍可续用时重新认证结果保持原协议，不要求客户端丢弃现有刷新令牌；会话到期时要求重新登录

#### Scenario: 活跃会话展示
- **WHEN** 会话已经到刷新期限且未明确吊销
- **THEN** 不计入消费者/管理员活跃会话列表及管理员计数，但保留期限内的数据及显式撤销能力仍存在

#### Scenario: 锁等待不延缓过期
- **WHEN** 刷新或重新认证请求在等待锁期间到达刷新期限
- **THEN** 不根据请求开始时的未过期状态签发或续期，分别返回原协议的invalid_grant或认证失效响应

#### Scenario: 第三方重新登录满足近期认证
- **WHEN** 第三方账号重新取得合法授权凭证并在相同设备登录
- **THEN** 使用原验证规则建立新会话、替换同设备旧会话并返回新 token pair；客户端核对账号并更新凭据，不把它当作原会话的无感重新认证

### Requirement: 生命周期和资料兼容
包 SHALL 保留资料读取与显示名称更新、ACTIVE/FROZEN/PENDING_DELETION/DELETED 状态、冻结解冻、注销、恢复及最终匿名化。默认注销冷静期为 15 天。

现有消费者手机/邮箱恢复、管理端代办恢复和 user:undelete SHALL 保留。普通第三方登录 SHALL 继续拒绝 PENDING_DELETION，不新增第三方恢复认证。恢复后的新令牌 SHALL 按当前 Required 决定 user 或 user:bind；Required=[] 的纯第三方账号 SHALL 不被描述为具备手机/邮箱自助恢复能力。

#### Scenario: 冷静期恢复
- **WHEN** 待注销用户在冷静期内使用恢复专用权限发起恢复
- **THEN** 按基线恢复账号；普通用户权限不能替代恢复专用权限

#### Scenario: 业务匿名化失败
- **WHEN** 到期清理中的宿主匿名化回调失败
- **THEN** 同一数据库事务内的认证数据及业务数据改动一起回滚，后续可重试

#### Scenario: 恢复后仍需补全
- **WHEN** 仅有 PHONE 的冷静期账号按原恢复流程恢复，但当前 Required 为 EMAIL
- **THEN** 新一轮刷新返回 user:bind，账号继续使用原 user_id；普通 user 凭据仍不能替代 user:undelete

### Requirement: 管理接口及审计兼容
包 SHALL 保留管理端用户查询、生命周期操作、身份敏感信息查看、会话和审计查询及角色限制。管理员身份由宿主注入，未配置管理认证时管理接口 SHALL 返回 503 ADMIN_NOT_CONFIGURED，消费者能力仍可独立使用。

审计 SHALL 支持通用事件及宿主外部查询，认证管理路由继续返回明确的认证 DTO，不直接序列化任意业务数据。审计资源 ID SHALL 显式升级为 UUIDv7，旧审计游标返回 400 INVALID_PAGE_TOKEN；非审计资源名和游标保持。仅注入审计写侧时，已授权审计查询 SHALL 返回 503 AUDIT_QUERY_NOT_CONFIGURED，不静默读取本地空表。默认本地模式 SHALL 保持可用。

#### Scenario: 管理员权限不足
- **WHEN** 已识别的管理员缺少目标操作角色
- **THEN** 拒绝操作并沿用基线审计机制记录禁止事件

#### Scenario: 审计服务失败
- **WHEN** 审计记录失败或默认有界队列耗尽
- **THEN** 不改变认证操作结果，保留基线失败与丢弃观测行为

#### Scenario: UUID 审计列表
- **WHEN** 管理员查询有权限访问的账号审计
- **THEN** 资源名使用 users/{user}/auditEvents/{uuid}，原字段语义保留，其他账号及通用扩展敏感内容不泄露

### Requirement: 加密和维护兼容
包 SHALL 保留敏感身份加密、带版本的检索摘要、令牌密钥轮换及历史密钥检查，提供会话清理、账号清理、审计保留、摘要重建和密文重加密维护能力。

有效待完成流程的身份快照及幂等结果 SHALL 纳入加密、密钥版本核验与有界清理。新增流程标识 SHALL 使用独立类型前缀 f_ 的 TSID，u_/i_/s_/f_ 格式保持不变；通用审计原始定义使用 UUIDv7，历史 e_ 合成格式验证通过明确映射完成，不承诺旧库自动升级。随机 sign_in_completion_token SHALL 与可猜测资源标识分离。

本地审计保留和账号/流程匿名化 SHALL 适配通用字段并覆盖迟到投递；外部副本治理 SHALL 由宿主承担，不宣称异步记录成功等同于跨库清理完成。

#### Scenario: 缺少历史密钥
- **WHEN** 存储中仍引用已移除的身份密钥版本
- **THEN** 启动迁移检查返回错误，不将问题延迟到用户登录才暴露

#### Scenario: 多实例维护
- **WHEN** 同一存储实例的多个进程同时执行维护
- **THEN** 使用基线互斥机制避免同一维护批次重复运行；重复启动和关闭遵循原幂等约定

#### Scenario: 非审计数据格式保持
- **WHEN** 当前通用审计结构装载非空历史合成数据并运行维护
- **THEN** 原账号、身份、会话和流程 ID、有效密文和令牌仍可使用；审计通过明确映射装载，不把此验证称为旧库原地升级

### Requirement: 产品部署隔离

包 SHALL 允许宿主分别设置 PostgreSQL schema、Redis 前缀、签发者、受众、密钥、发送器及管理员验证器。默认 schema 为 account，Redis 前缀为 auth:；默认值不代表自动获得跨产品隔离。未指定或为空的 Schema SHALL 采用 account，显式合法 Schema SHALL 保持用户配置；环境变量名称 AUTH_SCHEMA SHALL 保持不变。

#### Scenario: 两个实例共享基础设施
- **WHEN** 两个实例显式使用不同 schema、Redis 前缀、签发者、受众和密钥
- **THEN** 验证码、身份、会话、吊销与维护互不影响，一个实例拒绝另一个实例签发的令牌

#### Scenario: 默认账号 schema
- **WHEN** 库宿主或 accountsvc 未指定 Schema 或将其设置为空
- **THEN** 使用 account schema，首次迁移将包表及迁移记录建在其中，不自动创建旧默认 auth schema

#### Scenario: 显式 schema
- **WHEN** 宿主通过配置或 AUTH_SCHEMA 环境变量指定合法 schema
- **THEN** 沿用该值，连接的 search_path 与迁移位置一致

### Requirement: users 资源契约
系统 SHALL 在 C 端和管理端沿用 users 账号集合，包含登录动作、me、身份、会话及管理账号操作；返回 name 和账号资源引用 SHALL 使用 users/{user_id} 及对应子资源形式。宿主挂载前缀与两个身份面 SHALL 保持独立。此前未发布的 accounts 路径 SHALL 返回原格式 404，不注册别名或重定向。管理列表 JSON users、user_id、u_…、JWT claims、scope、Go User 类型、user 包及 Auth.Users() 门面、数据库表名 SHALL 保持。

#### Scenario: 两面使用统一账号资源
- **WHEN** 宿主分别在 /v1 和 /admin/v1 挂载两个 handler
- **THEN** 消费者使用 /v1/users/me 等路由，管理面使用 /admin/v1/users 等路由，继续各自认证和授权；响应资源名使用 users 前缀

#### Scenario: 未发布路由与既有令牌边界
- **WHEN** 调用者请求此前未发布的 accounts 路径或使用升级前仍有效的令牌访问 users 路由
- **THEN** 前者返回 404；后者仍按原 JWT 与吊销规则验证，sub、user_id 和 scope 保持原契约
