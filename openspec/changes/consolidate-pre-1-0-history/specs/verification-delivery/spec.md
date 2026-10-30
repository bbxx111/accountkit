## ADDED Requirements

### Requirement: 验证码专用公共类型

系统 SHALL 提供 sender.SMSCodeSender、sender.EmailCodeSender 和 sender.CodeMessage 作为验证码投递的唯一公共名称，分别保留 SendSMS(context.Context, string, CodeMessage) error 与 SendEmail(context.Context, string, CodeMessage) error。CodeMessage SHALL 保留 Purpose、Code、TTL 的字段名称、类型及语义。开发期别名 SMSSender、EmailSender、Message SHALL 不再提供；既有 Deps.SMSSender / Deps.EmailSender 字段名及渠道可用性契约 SHALL 保持。两个接口 SHALL 继续区分短信与邮件，不要求宿主增加新的必需方法。

#### Scenario: 正式名称直接实现
- **WHEN** 宿主用 sender.CodeMessage 实现 SendSMS / SendEmail 并通过 Deps 字段注入
- **THEN** 源码编译，发送器收到对应用途、验证码和有效期；引用已移除别名的源码编译失败

#### Scenario: 接口保持渠道区分
- **WHEN** 宿主只实现 SendSMS
- **THEN** 该类型满足 SMSCodeSender，不会自动满足 EmailCodeSender

#### Scenario: 各用途保持渠道和数据
- **WHEN** 分别发起 SIGN_IN、BIND、REAUTH、SIGN_IN_COMPLETE 的合法 PHONE 或 EMAIL 发码请求
- **THEN** 仅调用所选渠道发送器，传入对应用途和实际签发的验证码，TTL 继续遵循普通发码与受流程到期时间约束的补全发码规则

## REMOVED Requirements

### Requirement: 验证码专用公共类型与源码兼容
**Reason**: 由「验证码专用公共类型」取代。原要求保留 SMSSender、EmailSender、Message 作为 Deprecated 别名以兼容从未正式使用过的开发期入口；v1.0.0 起不再提供。
**Migration**: 宿主把 sender.Message 改为 sender.CodeMessage、SMSSender 改为 SMSCodeSender、EmailSender 改为 EmailCodeSender；方法签名与 Deps 字段名不变。
