## MODIFIED Requirements

### Requirement: 真实 SMTP 邮件投递

项目 SHALL 提供可选 SMTP 邮件发送能力，支持登录、绑定、重新认证及登录补全用途，消息 SHALL 包含对应验证码和有效期。补全投递 SHALL 通过独立 /accounts:sendSignInCompletionCode 触发，普通 /accounts:sendSignInCode 不接收补全票据或切换用途。accountsvc SHALL 在 EMAIL 启用时使用 SMTP 邮件，未启用时无需邮件依赖；其现有未配置身份策略的默认部署继续使用 SMTP 并禁用短信，不使用日志发送器作为运行回退。

#### Scenario: 邮件登录闭环
- **WHEN** 用户请求邮箱登录验证码且 SMTP 接受邮件
- **THEN** 返回 code_id 和 expire_time，邮件验证码与该轮标识按配置有效期和次数完成登录；SMTP 接受不表示最终送达

#### Scenario: 区分验证码用途
- **WHEN** 分别请求登录、绑定或重新认证邮件
- **THEN** 邮件说明正确用途，验证码保持现有用途隔离，不可跨用途使用

#### Scenario: 补全邮件说明用途
- **WHEN** 用户在待完成登录流程请求邮件验证码
- **THEN** 邮件明确说明用于完成本次登录和身份关联，不伪装成普通登录或营销消息，发送器接口签名保持

### Requirement: 显式禁用渠道

宿主 SHALL 能显式选择禁用发送器；已有发送器接口签名及默认启用行为 SHALL 保持兼容。对于格式合法但发送渠道已禁用的请求，系统 SHALL 在生成验证码、占用冷却或额度及外部投递前返回 400 `CHANNEL_NOT_ENABLED`，业务错误 status 为 `FAILED_PRECONDITION`，并记录不含凭据的发送拒绝审计。

新增身份策略显式关闭 PHONE/EMAIL 时 SHALL 同样在发码及旧验证码提交前拒绝。旧发送器只实现投递方法时 SHALL 不要求新增方法；显式新策略启用某渠道却提供 Disabled 发送器 SHALL 在运行时装配前被判为配置错误，未设置新策略的旧宿主保留原装配兼容。

#### Scenario: 禁用短信的三类发码
- **WHEN** 客户端以满足原端点身份约束的合法请求发送短信登录、绑定或重新认证验证码
- **THEN** 均返回渠道禁用错误，不创建验证码、不消耗目标/IP 额度和冷却，不伪装为发送成功

#### Scenario: 旧宿主无需改造
- **WHEN** 宿主继续注入只实现原发送方法的短信和邮件发送器
- **THEN** 代码仍可编译，两个渠道继续按原行为工作，不要求新增必需方法或配置

#### Scenario: 禁用渠道的补全发码
- **WHEN** 客户端为有效待完成流程请求已关闭渠道的补全码
- **THEN** 拒绝且不创建验证码、不扣额度、不泄露目标账号是否存在
