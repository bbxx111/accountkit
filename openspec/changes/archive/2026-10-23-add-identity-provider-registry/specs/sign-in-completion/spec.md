# Spec Delta

## MODIFIED Requirements

### Requirement: 独立补全端点与票据字段
系统 SHALL 仅新增 POST /users:sendSignInCompletionCode 和 POST /users:completeSignIn 两个登录补全业务端点。两者 SHALL 在请求体使用 sign_in_completion_token。pending 响应 SHALL 使用同名字段及 next_action=COMPLETE_IDENTITY、expire_time、启用和必需身份集合。初始证明含已验证邮箱时，pending 响应 SHALL 另外包含该 hint_email；没有该证明时 SHALL 省略。hint_email SHALL 只来自初始证明，不因该邮箱是否已有账号而改变，也不代替请求中的 target。补全发码 SHALL 与普通 sendSignInCode 保持独立，不能通过普通发码入口的可选票据或公开 purpose 参数切换用途。

#### Scenario: 补全发码
- **WHEN** 客户端向 sendSignInCompletionCode 提交有效 sign_in_completion_token、PHONE/EMAIL channel 和 target
- **THEN** 返回本流程 SIGN_IN_COMPLETE 用途的 code_id 与 expire_time，不创建账号

#### Scenario: 完成接口凭证限制
- **WHEN** 客户端调用 completeSignIn
- **THEN** 除有效 sign_in_completion_token 外必须提供 kind 为 PHONE 或 EMAIL 的凭证，credential 包含 target、code_id、code；kind 或 credential 缺失返回 400 `CREDENTIAL_INCOMPLETE`，第三方类型返回 400 `CREDENTIAL_KIND_NOT_ALLOWED`

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
