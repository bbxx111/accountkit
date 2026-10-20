# embedded-auth-package Specification

## Purpose

让宿主应用通过独立 Go 包获得 完整账号认证能力，保持客户端协议、账号数据和安全行为兼容，并允许无关产品分别配置依赖、凭证和存储，实现独立部署和版本升级。

## Requirements

### Requirement: 独立消费和完整功能保留

包 SHALL 独立构建/测试/导入，不依赖外部源码工作区；保留固定基线公开能力，已明确声明的包名、迁移安全、验证码轮次、身份策略/补全与通用审计升级按调用迁移说明适配，不留旧验证旁路。会话上下文尚未发布，本轮 SHALL 明确移除known/initial字段和快照枚举，来源非空、客户端JSON嵌套；Go保留平坦描述字段并增加制造商/build/扩展，提供字段名初始化示例及位置式初始化风险。当前查询 SHALL 仍为可选宿主能力，不扩大必需接口方法集；JWT、refresh及非审计资源格式保持。

#### Scenario: 独立宿主构建
- **WHEN** 独立Go项目依赖accountkit并注入依赖
- **THEN** 可编译并挂载两面接口，不需要外部宿主源码、accountsvc进程或特定管理员OIDC包

#### Scenario: 自定义审计升级
- **WHEN** 宿主按通用审计迁移说明适配记录器
- **THEN** 接收通用事件而不依赖sqlc行，可核对公开Go类型、审计UUIDv7/游标、查询配置及历史数据迁移边界

#### Scenario: 旧自定义认证适配器
- **WHEN** 宿主只实现原消费者必需接口
- **THEN** 原接口仍工作，当前查询返回503 SESSION_QUERY_NOT_CONFIGURED而非空来源成功

#### Scenario: 未发布会话描述调用迁移
- **WHEN** 调用者使用前一开发版known/initial字段、快照枚举或平坦JSON
- **THEN** 按兼容说明移除旧引用，改读非空来源和单份nested描述；扩展由Go/回调提供并配置宿主白名单，不承诺旧源码无修改编译

### Requirement: 登录及身份管理兼容

包 SHALL 保留PHONE/EMAIL验证码、微信/Apple登录和绑定解绑，包括格式、有效期、限流、IdP验证及冲突。PHONE/EMAIL认证/绑定/reauth/换绑 SHALL 使用必需code_id及原错误分类，跨轮次预算保持TARGET_VERIFY_LIMIT。登录 SHALL 执行Enabled/Required及any-of完整性，缺条件返回无user_id的pending，满足则注册；补全自动关联或注册，绑定仍拒绝跨账号占用。解绑 SHALL 同事务撤销来源命中的同账号未吊销会话（含当前/自然过期未清理），来源缺失按不变量拒绝；Go/HTTP同样校验scope、新鲜度、当前会话与身份完整性，成功仍204，不自动转换旧库或批量退出。

#### Scenario: 验证码登录
- **WHEN** 使用轮次协议和同轮目标/code_id/验证码及设备登录
- **THEN** 返回令牌或pending，保留原错误类别和发送额度，累计错误预算按新协议返回

#### Scenario: 第三方登录与绑定
- **WHEN** IdP返回有效凭证或执行绑定解绑
- **THEN** 保留凭证、nonce防重放及身份唯一性，按当前策略保护至少一个有效身份

#### Scenario: 未完成登录不是受限用户
- **WHEN** 未知第三方身份未满足Required
- **THEN** 仅签发pending票据，不创建用户/身份/会话或user:bind令牌

#### Scenario: 解绑来源身份
- **WHEN** 其他身份满足策略且合法解绑当前来源
- **THEN** 软删除和命中会话撤销同事务提交，当前会话可撤销，返回204；缺失来源不当作unknown正常态

### Requirement: 令牌与会话兼容

包 SHALL 保留签名校验、scope、轮换、宽限、查询和撤销；access默认15分钟、refresh滑动30天、宽限30秒。活跃列表排除已撤销/refresh到期，刷新/reauth锁后复核期限；已签发access仍按原到期/显式吊销。ACTIVE登录/新刷新/reauth按全部有效身份计算user或user:bind，宽限结果不重算。PHONE/EMAIL reauth保留sid/refresh并原子替换非空来源及auth_time；第三方用正常重新登录/同设备替换。刷新/绑定不改来源。当前详情查库返回最新来源及可选nested描述，JWT/内省不加字段，普通Authenticate不加DB，描述缺失不阻断认证。

#### Scenario: 刷新重试
- **WHEN** 宽限内重试且会话未撤销/未到期
- **THEN** 返回同轮结果且不更新描述/时间；宽限外旧凭据按原规则失效

#### Scenario: 敏感操作重新认证
- **WHEN** 默认敏感检查启用且认证超过5分钟
- **THEN** 要求重新认证；有效会话保留原refresh协议，到期需重新登录

#### Scenario: 活跃会话展示
- **WHEN** refresh到期但尚未吊销
- **THEN** 两面活跃列表和计数排除，保留期内数据及显式撤销能力仍存在

#### Scenario: 锁等待不延缓过期
- **WHEN** 等待锁期间refresh/reauth会话到期
- **THEN** 不按请求开始时状态续期，分别返回invalid_grant或认证失效

#### Scenario: 第三方重新登录满足近期认证
- **WHEN** 第三方重新取得合法证明并同设备登录
- **THEN** 建新会话、替换旧会话、返回新pair，客户端核对账号更新凭据，不视作旧会话无感reauth

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

### Requirement: 内部领域事务保持锁后最新状态

库内部账号、身份和会话领域事务 SHALL 在取得既有互斥锁后读取最新已提交状态，使登录来源、身份数量、生命周期、刷新轮换和引用完整性不受宿主默认 Repeatable Read 或 Serializable 设置影响。公开仓储事务入口 SHALL 保持宿主配置的默认隔离级别。此保证 SHALL 保留既有锁序、事务原子性、验证码消费和错误契约。

#### Scenario: 解绑先于新设备登录取得账号锁
- **WHEN** 宿主默认 Repeatable Read，解绑先持有账号锁且提交身份删除，使用旧证明的新设备登录随后取得锁
- **THEN** 登录拒绝已失效证明，不创建引用该已解绑身份的新会话

#### Scenario: 不同目标并发绑定达到上限
- **WHEN** 宿主默认 Repeatable Read，同一账号的两次不同目标绑定竞争最后一个同类型名额
- **THEN** 最多一个绑定成功，另一个返回既有数量上限错误

#### Scenario: 并发刷新仍可返回宽限结果
- **WHEN** 宿主配置较高默认隔离级别，两个请求使用相同 refresh token 并等待同一会话锁
- **THEN** 成功轮换后另一个请求仍按既有宽限协议取得同一令牌对，不因继承隔离级别产生非预期序列化失败

#### Scenario: 宿主直接使用公开仓储事务
- **WHEN** 宿主通过公开仓储事务入口开启事务
- **THEN** 仍沿用宿主默认隔离级别，库内部修复不静默修改该公共契约

### Requirement: 初始化校验维护所需连接容量

初始化 SHALL 在不执行数据库 I/O 的前提下拒绝最大连接数小于 2 的宿主连接池，并返回说明容量要求的配置错误。容量为 2 或 pgx 默认容量的池 SHALL 可按既有依赖规则完成初始化；池生命周期、默认容量、维护锁和宿主连接所有权 SHALL 保持。

#### Scenario: 单连接池
- **WHEN** 宿主注入最大连接数为 1 的池
- **THEN** 初始化失败，不启动维护任务、不建立连接，也不关闭宿主的池

#### Scenario: 满足最低容量
- **WHEN** 宿主注入最大连接数为 2 或使用 pgx 默认配置的池
- **THEN** 容量校验通过，维护锁持有期间任务仍有可用连接容量
