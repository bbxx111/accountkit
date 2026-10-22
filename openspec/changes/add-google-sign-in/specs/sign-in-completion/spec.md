# Spec Delta

## MODIFIED Requirements

### Requirement: 独立补全端点与票据字段
系统 SHALL 仅新增 POST /users:sendSignInCompletionCode 和 POST /users:completeSignIn 两个登录补全业务端点。两者 SHALL 在请求体使用 sign_in_completion_token。pending 响应 SHALL 使用同名字段及 next_action=COMPLETE_IDENTITY、expire_time、启用和必需身份集合。初始证明含已验证邮箱时，pending 响应 SHALL 另外包含该 hint_email；没有该证明时 SHALL 省略。hint_email SHALL 只来自初始证明，不因该邮箱是否已有账号而改变，也不代替请求中的 target。补全发码 SHALL 与普通 sendSignInCode 保持独立，不能通过普通发码入口的可选票据或公开 purpose 参数切换用途。

#### Scenario: 补全发码
- **WHEN** 客户端向 sendSignInCompletionCode 提交有效 sign_in_completion_token、PHONE/EMAIL channel 和 target
- **THEN** 返回本流程 SIGN_IN_COMPLETE 用途的 code_id 与 expire_time，不创建账号

#### Scenario: 完成接口凭证限制
- **WHEN** 客户端调用 completeSignIn
- **THEN** 除有效 sign_in_completion_token 外必须恰好提供一个 phone 或 email 凭证成员，包含 target、code_id、code；缺失、多个成员或第三方凭证返回 400

#### Scenario: 普通发码不得切换为补全
- **WHEN** 客户端向普通 sendSignInCode 提交补全票据或 purpose 字段
- **THEN** 按严格请求 DTO 拒绝，不签发补全用途的验证码

#### Scenario: 已验证邮箱作为预填
- **WHEN** 初始 IdP 证明含已验证邮箱并返回 pending
- **THEN** 响应包含该 hint_email，以及 next_action、票据、expire_time 和身份集合，不含 user_id、access_token 或 refresh_token

#### Scenario: 没有已验证邮箱
- **WHEN** 初始证明没有已验证邮箱并返回 pending
- **THEN** 响应省略 hint_email

#### Scenario: 省略目标不能改用提示
- **WHEN** 客户端省略 target，期望服务改用 hint_email 发码或完成
- **THEN** 返回 400，不向 hint_email 发送验证码，不创建 EMAIL 身份

### Requirement: 隐私隔离与审计
未验证目标前的响应 SHALL 不泄露账号存在性。票据、验证码、第三方原始凭证、完整目标及完整摘要 SHALL 不进入日志、审计或 URL；凭证响应 SHALL no-store。pending 响应中的 hint_email SHALL 不进入日志、审计或 URL，且 SHALL 不因邮箱是否已注册而不同。暂存数据及结果 SHALL 按实例隔离、加密和有界清理。审计 SHALL 异步脱敏，失败不改变结果；账号删除/匿名化 SHALL 撤销或清理关联未完成记录及可重放结果。

#### Scenario: 发码不能枚举账号
- **WHEN** 尚未验证目标时分别向已注册和未注册目标请求补全码
- **THEN** 除原格式、渠道和额度错误外，响应不揭示账号是否存在

#### Scenario: 提示邮箱不揭示注册状态
- **WHEN** 两个初始证明含有相同的已验证邮箱，其中一个邮箱已注册、另一个未注册
- **THEN** 两个 pending 响应的 hint_email 相同，响应不揭示账号是否存在

#### Scenario: 依赖故障或跨实例请求
- **WHEN** 证明存储不可用或另一实例票据被提交
- **THEN** 拒绝完成，不降级为无状态验证

#### Scenario: 清理和轮换
- **WHEN** 流程到期、账号匿名化或密钥轮换涉及有效流程密文
- **THEN** 按边界清理，有效流程仍要求其密钥可用，不能提前移除旧 key 后宣称流程可继续
