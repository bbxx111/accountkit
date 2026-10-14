# sign-in-completion Specification

## Purpose

在首次验证的身份不足以完成注册时暂存其证明而不提前创建用户，随后通过独立端点验证手机或邮箱，自动确定应关联已有账号还是创建新账号，并用专用短期票据保证用途隔离、隐私、并发唯一性及失败后的可恢复操作。

## Requirements

### Requirement: 未完成身份验证不提前建号
未关联账号的身份不满足 Required 时 SHALL 返回登录补全票据，不创建 user_account、identity 或 session，不返回 user_id、scope、access_token 或 refresh_token。票据 SHALL 只用于本次补全，不能作为 Bearer、refresh token 或 user:bind。未关联身份已满足要求时 SHALL 直接注册，不提供可选延后注册参数。

#### Scenario: 微信需要补充邮箱
- **WHEN** Required 为 EMAIL，首次 WECHAT 证明有效且身份无归属
- **THEN** 返回 pending，用户、身份和会话数量不增加

#### Scenario: 手机不足以满足要求
- **WHEN** Required 为 EMAIL，首次 PHONE 证明有效且身份无归属
- **THEN** 返回 pending，不创建仅有 PHONE 的用户

#### Scenario: 单身份已足够
- **WHEN** Required 为空，首次启用的第三方身份验证成功且无归属
- **THEN** 直接注册并返回 user，不强制另一种身份；不能凭第三方邮箱提示识别别的已有账号

### Requirement: 独立补全端点与票据字段
系统 SHALL 仅新增 POST /users:sendSignInCompletionCode 和 POST /users:completeSignIn 两个登录补全业务端点。两者 SHALL 在请求体使用 sign_in_completion_token。pending 响应 SHALL 使用同名字段及 next_action=COMPLETE_IDENTITY、expire_time、启用和必需身份集合。补全发码 SHALL 与普通 sendSignInCode 保持独立，不能通过普通发码入口的可选票据或公开 purpose 参数切换用途。

#### Scenario: 补全发码
- **WHEN** 客户端向 sendSignInCompletionCode 提交有效 sign_in_completion_token、PHONE/EMAIL channel 和 target
- **THEN** 返回本流程 SIGN_IN_COMPLETE 用途的 code_id 与 expire_time，不创建账号

#### Scenario: 完成接口凭证限制
- **WHEN** 客户端调用 completeSignIn
- **THEN** 除有效 sign_in_completion_token 外必须恰好提供一个 phone 或 email 凭证成员，包含 target、code_id、code；缺失、多个成员或第三方凭证返回 400

#### Scenario: 普通发码不得切换为补全
- **WHEN** 客户端向普通 sendSignInCode 提交补全票据或 purpose 字段
- **THEN** 按严格请求 DTO 拒绝，不签发补全用途的验证码

### Requirement: 验证后自动确定账号
系统 SHALL 在手机或邮箱补充证明通过后查询其归属，不根据未验证输入或第三方邮箱提示关联。目标属于 ACTIVE 账号 B 时 SHALL 关联尚未占用的暂存身份到 B；目标无归属时 SHALL 仅在已验证身份满足完整性条件后创建一个账号。界面 SHALL 说明“验证并继续”会关联本次身份、未注册时自动注册，不要求用户选择两条分支。

#### Scenario: 输入已有邮箱
- **WHEN** 待完成 WECHAT 身份和本流程 EMAIL 证明有效，EMAIL 属于活动账号 B
- **THEN** 原子关联并返回 B 的 user_id、is_new_user=false，不创建临时账号 A

#### Scenario: 输入未注册邮箱
- **WHEN** 同样的证明通过但 EMAIL 无归属，集合满足 Required
- **THEN** 一次创建用户、WECHAT 与 EMAIL 身份和会话，返回 is_new_user=true

#### Scenario: 目标账号不可用
- **WHEN** 已验证目标属于 FROZEN、PENDING_DELETION 或其他不可关联账号
- **THEN** 返回对应拒绝，不通过新建账号绕过状态限制

#### Scenario: 目标账号还需补全
- **WHEN** 补充的已启用 PHONE/EMAIL 指向 B，关联后 B 仍不满足 Required
- **THEN** 沿用 B 并返回 user:bind，不创建第二个账号，也不固定返回 user

#### Scenario: 仍无归属且不足条件
- **WHEN** 补充手机或邮箱后仍无账号归属且集合不满足 Required
- **THEN** 保持同一 pending 流程供继续补充手机或邮箱，不延长原期限、不创建用户

### Requirement: 证明与流程绑定
初始证明、验证码轮次和提交 SHALL 绑定同一实例、流程和操作意图。补充证明 SHALL 仅为 PHONE/EMAIL 的 SIGN_IN_COMPLETE 轮次；初始 IdP 登录保留原应用允许列表、issuer/audience、nonce 及防重放验证。票据默认 SHALL 创建后 5 分钟到期，已知原证明先到期时使用更早期限；补充验证和重试 SHALL 不延长。

#### Scenario: 跨流程替换
- **WHEN** 流程甲的补全验证码被提交到流程乙
- **THEN** 拒绝，不消费乙的正确轮次或完成身份关联

#### Scenario: 跨用途验证
- **WHEN** 客户端向补全接口提交 SIGN_IN、BIND 或 REAUTH 码，或者把补全码用于普通登录
- **THEN** 拒绝，不能把一种操作意图转换为另一种授权

#### Scenario: 过期或放弃
- **WHEN** 票据到期或用户停止流程
- **THEN** 不创建用户，有界清理敏感暂存数据；继续操作须重新验证

### Requirement: 所有权与事务一致性
完成 SHALL 在事务中复核账号状态、身份归属、E/R、数量限制和期限。身份绑定、必要的新用户、会话和完成状态 SHALL 原子提交。一个流程 SHALL 至多成功一次；多个流程争用相同身份 SHALL 遵循唯一性约束，不留下重复账号或部分提交。已有两个账号的身份 SHALL 不因补全而自动迁移或合并。

#### Scenario: 同票据并发完成
- **WHEN** 多个请求并发完成同一个已验证流程
- **THEN** 至多一个业务事务提交，其余读取受约束的相同完成结果或明确错误，不再次建号

#### Scenario: 提交前归属变化
- **WHEN** 证明验证后、锁后复核前，身份被其他流程绑定或被原账号换绑
- **THEN** 不使用旧归属建立会话；拒绝或仅按同一已提交操作的结果处理

#### Scenario: 身份属于不同已有账号
- **WHEN** 暂存身份和补充身份最终分属两个已有账号
- **THEN** 返回身份冲突，不转移身份或改写业务 user_id

### Requirement: 跨存储失败与重试
系统 SHALL 不宣称 Redis 验证码消费与 PostgreSQL 写入为跨存储原子事务。已经消费的验证码或初始 IdP nonce SHALL 不因数据库失败恢复，额度和错误预算 SHALL 保留。只有持久化成功的证明可继续使用。相同最终请求在票据有效期内 SHALL 支持受账号/会话状态约束的幂等结果读取；已撤销、过期或 refresh 已轮换的结果 SHALL 不重新发放。

#### Scenario: 消费后持久化失败
- **WHEN** 验证码已消费，但验证结果持久化失败
- **THEN** 不创建用户或退还验证码；客户端按原发送限制重新取证

#### Scenario: 成功响应丢失
- **WHEN** 完成已提交但客户端未收到响应，随后以相同票据及最终请求重试
- **THEN** 返回同一仍有效的完成结果，不创建第二个用户、身份或会话

#### Scenario: 已完成结果不能重放
- **WHEN** 票据仍在有效期内但对应会话已撤销、结果到期或 refresh 已轮换
- **THEN** 返回 400 FAILED_PRECONDITION / SIGN_IN_ALREADY_COMPLETED，不能回发旧 pair 或创建新会话；票据本身失效则返回 SIGN_IN_COMPLETION_INVALID

### Requirement: 隐私隔离与审计
未验证目标前的响应 SHALL 不泄露账号存在性。票据、验证码、第三方原始凭证、完整目标及完整摘要 SHALL 不进入日志、审计或 URL；凭证响应 SHALL no-store。暂存数据及结果 SHALL 按实例隔离、加密和有界清理。审计 SHALL 异步脱敏，失败不改变结果；账号删除/匿名化 SHALL 撤销或清理关联未完成记录及可重放结果。

#### Scenario: 发码不能枚举账号
- **WHEN** 尚未验证目标时分别向已注册和未注册目标请求补全码
- **THEN** 除原格式、渠道和额度错误外，响应不揭示账号是否存在

#### Scenario: 依赖故障或跨实例请求
- **WHEN** 证明存储不可用或另一实例票据被提交
- **THEN** 拒绝完成，不降级为无状态验证

#### Scenario: 清理和轮换
- **WHEN** 流程到期、账号匿名化或密钥轮换涉及有效流程密文
- **THEN** 按边界清理，有效流程仍要求其密钥可用，不能提前移除旧 key 后宣称流程可继续
