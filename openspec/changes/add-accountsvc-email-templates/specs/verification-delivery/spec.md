## MODIFIED Requirements

### Requirement: 真实 SMTP 邮件投递

项目 SHALL 提供可选的 SMTP 邮件发送能力，支持登录、绑定、重新认证和登录补全四种用途。消息 SHALL 包含对应的验证码和有效期。补全投递 SHALL 通过独立的 /users:sendSignInCompletionCode 触发，普通的 /users:sendSignInCode 不接收补全票据，也不切换用途。accountsvc SHALL 在 EMAIL 启用时使用 SMTP 邮件，EMAIL 未启用时不需要邮件依赖；现有未配置身份策略的默认部署继续使用 SMTP 并禁用短信，不使用日志发送器作为运行时回退。

旧的 smtp.New(Config) 函数签名及 Config 结构 SHALL 保持，继续采用独立的 LegacyChineseRenderer 提供既有的四用途中文纯文本文案。新宿主 SHALL 能通过 NewWithRenderer 显式选择自己的渲染器。accountsvc 未配置邮件模板清单时 SHALL 显式注入 LegacyChineseRenderer，保留原中文邮件内容、部署配置和 EMAIL 禁用行为。配置了模板清单时，SHALL 改为使用内置布局与清单提供的多语言文字渲染 HTML 和纯文本两部分，不得按用途或语言部分回落到内置文案；语言 SHALL 按请求的 `Accept-Language` 在已配置的语言间选择，不按 IP、手机号或保存的用户偏好推断。accountsvc SHALL 不内嵌图片或附件。

#### Scenario: 邮件登录闭环

- **WHEN** 用户请求邮箱登录验证码且 SMTP 接受邮件
- **THEN** 返回 code_id 和 expire_time，邮件验证码与该轮标识按配置有效期和次数完成登录；SMTP 接受不表示最终送达

#### Scenario: 区分验证码用途

- **WHEN** 分别请求登录、绑定或重新认证邮件
- **THEN** 邮件说明正确用途，验证码保持现有用途隔离，不可跨用途使用

#### Scenario: 补全邮件说明用途

- **WHEN** 用户在待完成登录流程请求邮件验证码
- **THEN** 邮件明确说明用于完成本次登录和身份关联，不伪装成普通登录或营销消息，发送器接口签名保持

#### Scenario: 旧 SMTP 调用保持行为

- **WHEN** 旧宿主继续调用 smtp.New(cfg)，未接入自定义渲染器
- **THEN** 无需修改配置，解码后的标题和正文保持既有中文文案及有效期向上取整规则，继续覆盖四种验证码用途

#### Scenario: 独立服务显式装配

- **WHEN** accountsvc 启用 EMAIL 且未配置模板清单，或运行 EMAIL 禁用的 serve / migrate
- **THEN** 前者显式选择兼容中文渲染器完成邮件投递；后者继续遵循原 SMTP 依赖边界，不读取模板清单或引入语言依赖

#### Scenario: 独立服务使用模板清单

- **WHEN** accountsvc 启用 EMAIL 并配置了含 zh-Hans、zh-Hant、en 的合法模板清单，两个请求分别携带 `Accept-Language: zh-HK` 与 `Accept-Language: en-GB`
- **THEN** 前者收到繁体文字及繁体品牌的邮件，后者收到英文文字及英文品牌的邮件，两封邮件都同时含 HTML 与纯文本部分；验证码用途隔离、有效期与额度规则不变
