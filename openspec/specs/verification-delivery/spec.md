# verification-delivery Specification

## Purpose

提供可以由独立服务或嵌入式宿主选用的真实邮件验证码投递能力，明确未启用短信等渠道时的可观察行为，并保持验证码用途、发送额度、隐私保护和已有发送器集成方式兼容，形成可验收的邮箱认证闭环。

## Requirements

### Requirement: 真实 SMTP 邮件投递

项目 SHALL 提供可选 SMTP 邮件发送能力，支持登录、绑定、重新认证及登录补全用途，消息 SHALL 包含对应验证码和有效期。补全投递 SHALL 通过独立 /users:sendSignInCompletionCode 触发，普通 /users:sendSignInCode 不接收补全票据或切换用途。accountsvc SHALL 在 EMAIL 启用时使用 SMTP 邮件，未启用时无需邮件依赖；其现有未配置身份策略的默认部署继续使用 SMTP 并禁用短信，不使用日志发送器作为运行回退。

旧 smtp.New(Config) 函数签名及 Config 结构 SHALL 保持，继续采用独立的 LegacyChineseRenderer 提供既有四用途中文纯文本文案；新宿主 SHALL 能通过 NewWithRenderer 显式选择自己的渲染器。accountsvc SHALL 显式注入 LegacyChineseRenderer，保留原中文邮件内容、部署配置和 EMAIL 禁用行为；本变更 SHALL 不新增 accountsvc 语言协商或模板管理配置。

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

- **WHEN** accountsvc 启用 EMAIL，或运行 EMAIL 禁用的 serve / migrate
- **THEN** 前者显式选择兼容中文渲染器完成邮件投递；后者继续遵循原 SMTP 依赖边界，不新增模板或语言依赖

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

### Requirement: SMTP 传输与隐私

生产 SMTP SHALL 验证服务器证书，支持隐式 TLS 或强制 STARTTLS，认证和消息发送不得先于加密成功。发送 SHALL 支持请求取消和有限总超时，拒绝邮件头注入。日志、审计、错误响应 SHALL 不包含验证码、密码、完整收件地址或邮件正文。

#### Scenario: TLS 失败
- **WHEN** SMTP 证书不可信或要求 STARTTLS 但服务器不支持
- **THEN** 投递失败且不降级明文，不发送认证秘密或验证码

#### Scenario: 取消和敏感信息保护
- **WHEN** 投递超时、请求取消或 SMTP 返回带敏感地址的错误
- **THEN** 有界结束，向外返回固定安全错误，并过滤日志中的敏感服务端响应

### Requirement: 投递失败保留额度契约

SMTP 网络、超时和服务拒绝 SHALL 映射为 503 DEPENDENCY_UNAVAILABLE，保留原发送失败审计和已经消耗的额度/冷却。发送器 SHALL 不自动重发，也不得将 SMTP 接受等同于最终到达邮箱。投递失败 SHALL 不返回成功轮次；清理 SHALL 仅作废仍匹配本次 code_id 的轮次，不删除并发产生的较新轮次，不恢复已被替换的旧轮次。清理依赖故障 SHALL 通过原到期时间失效，不能退额度或降级验证。

SMTP 适配器的渲染错误、渲染结果校验错误及请求取消 SHALL 返回固定安全的 sender.ErrUnavailable，沿用上述领域错误与 HTTP 映射；不得透传或包装宿主渲染错误的原始文本，也不得在日志、审计或响应中输出验证码、完整地址、正文或渲染上下文。原自定义发送器返回普通错误的兼容映射 SHALL 保持，本要求不扩大为重写所有发送器错误。

#### Scenario: 发送失败后重试

- **WHEN** 已生成验证码的 SMTP 提交失败，客户端立即重试
- **THEN** 原额度和冷却仍生效，不退还次数、不触发后台无限重试，返回结果不声称已送达

#### Scenario: 失败清理与新轮次竞争

- **WHEN** A 轮次投递失败后执行清理，但 B 已成为最新轮次
- **THEN** A 的清理不删除 B，发送额度和冷却保持，不恢复此前旧轮次

#### Scenario: 含敏感内容的渲染错误

- **WHEN** 宿主渲染器返回包含合成验证码、完整地址或正文的错误
- **THEN** SMTP 返回固定安全错误，不输出原错误；发码响应为 503 且不包含成功轮次，已占用额度和冷却保留，发送失败审计仍脱敏

#### Scenario: SMTP 已接受后的 QUIT 失败

- **WHEN** SMTP 服务器已接受 DATA，但随后 QUIT 失败
- **THEN** 仍按已接受投递返回成功，不触发重发或作废该次成功轮次

### Requirement: 验证码专用公共类型与源码兼容

系统 SHALL 提供 sender.SMSCodeSender、sender.EmailCodeSender 和 sender.CodeMessage 作为验证码投递的正式公共名称，分别保留 SendSMS(context.Context, string, CodeMessage) error 与 SendEmail(context.Context, string, CodeMessage) error。CodeMessage SHALL 保留 Purpose、Code、TTL 的字段名称、类型及语义。旧 SMSSender、EmailSender、Message SHALL 作为带 Deprecated 文档的类型别名保留，既有 Deps.SMSSender / Deps.EmailSender 字段名及渠道可用性契约 SHALL 保持。两个接口 SHALL 继续区分短信与邮件，不要求宿主增加新的必需方法。

#### Scenario: 旧发送器无需改写

- **WHEN** 宿主用 sender.Message 声明原 SendSMS / SendEmail 方法，并通过原 Deps 字段注入
- **THEN** 源码继续编译，能够赋值给对应新接口，继续收到同样的用途、验证码和有效期

#### Scenario: 新名称保持双向类型兼容

- **WHEN** 宿主混合使用新旧消息名及新旧发送器接口名
- **THEN** 不需要显式转换或复制数据，单独实现 SendSMS 的类型不会自动满足 EmailCodeSender

#### Scenario: 各用途保持渠道和数据

- **WHEN** 分别发起 SIGN_IN、BIND、REAUTH、SIGN_IN_COMPLETE 的合法 PHONE 或 EMAIL 发码请求
- **THEN** 仅调用所选渠道发送器，传入对应用途和实际签发的验证码，TTL 继续遵循普通发码与受流程到期时间约束的补全发码规则

### Requirement: 宿主可注入 SMTP 验证码渲染

可选 SMTP 发送器 SHALL 提供 smtp.NewWithRenderer(Config, Renderer) (*Sender, error)，使用宿主 Renderer.Render(context.Context, sender.CodeMessage) (EmailContent, error) 生成消息内容。EmailContent SHALL 包含 Subject、Text、HTML 字符串字段，Subject 与 Text 必须非空白，HTML 可为空。新构造入口 SHALL 拒绝缺失渲染器，且构造时不执行渲染、不进行网络 I/O。渲染器 SHALL 能独立决定模板、品牌及语言，SMTP 传输 SHALL 不再拼接业务文案。宿主渲染器 SHALL 正确表达用途、验证码与有效期，并对同一实例并发调用安全。

发送器 SHALL 在渲染前校验收件地址、有效用途、非空数字验证码和正 TTL，渲染后校验内容；渲染或内容校验失败 SHALL 不连接 SMTP，且不回退默认模板。渲染器只能生成内容，发件人和收件人 SHALL 仍由传输配置和 SendEmail 的目标参数确定。

#### Scenario: 使用自定义邮件文案

- **WHEN** 宿主注入渲染器并发送任一种有效用途的验证码
- **THEN** 渲染器收到对应 CodeMessage，SMTP 邮件的标题、正文来自该渲染器，传输层不附加固定中文文案

#### Scenario: 未提供渲染器

- **WHEN** 调用 NewWithRenderer 时 Renderer 为 nil 或包装了 nil 指针/函数的接口值
- **THEN** 构造失败，错误只标识配置问题，既不启用默认模板也不连接 SMTP

#### Scenario: 渲染之前拒绝非法输入

- **WHEN** 收件地址非法、Purpose 无效、Code 为空或含非数字字符、TTL 非正
- **THEN** 不调用宿主渲染器且不进行 SMTP 连接，返回固定安全投递错误

#### Scenario: 纯文本与 HTML 邮件

- **WHEN** 渲染器返回合法 Subject 和 Text，HTML 为空或同时提供 HTML
- **THEN** 前者发送 UTF-8 纯文本邮件；后者发送可解码的 multipart/alternative 邮件，包含先 Text 后 HTML 两部分，各部分独立正确编码

#### Scenario: 邮件头注入及空内容

- **WHEN** 渲染器返回含 CR 或 LF 的标题，或空白标题/纯文本正文
- **THEN** 发送失败且不建立 SMTP 连接，不能注入额外邮件头或发送仅 HTML、无纯文本替代的邮件

### Requirement: 宿主控制语言上下文与回退

系统 SHALL 将调用发送器的请求 context 及其值传递给渲染器，允许使用派生的取消/超时 context；核心 CodeMessage SHALL 不增加语言、模板或服务商字段。宿主 SHALL 自行定义类型化 context 键、语言选择和默认语言回退，不由核心解析 Accept-Language、查询用户偏好或修改共享渲染器的全局语言状态。项目 SHALL 提供固定语言配置及请求级语言选择的可编译示例，使用合成数据展示至少两种语言和缺省回退。

#### Scenario: 请求级语言隔离

- **WHEN** 同一个 SMTP 发送器并发接收带不同宿主语言提示的请求
- **THEN** 各次渲染读取各自 context 值，得到对应语言的验证码邮件，不交叉污染

#### Scenario: 缺少或不支持的语言

- **WHEN** context 不含宿主语言提示，或提示不在示例支持列表中
- **THEN** 示例按宿主明确的默认语言回退，验证码用途、Code 和 TTL 不变

#### Scenario: 请求取消和渲染预算

- **WHEN** 请求已取消，或遵守 context 的渲染器在总投递超时内未完成
- **THEN** 发送失败，不继续建立 SMTP 连接；渲染器与网络投递共用该次总预算，超时不得在渲染后重置

### Requirement: 发送器声明不支持的号码

库 SHALL 支持发送器可选实现的号码支持能力。发码流程（登录、绑定、重新认证、登录补全）在目标规范化之后、占用验证码轮次、冷却与额度之前，SHALL 询问该能力。发送器声明不支持时：

- SHALL 返回不支持目标错误；
- HTTP 层映射为 400 `TARGET_NOT_SUPPORTED`；
- SHALL 记录 `CODE_SEND_REJECTED` 审计，原因为 `TARGET_NOT_SUPPORTED`；
- SHALL 不生成验证码，不扣冷却或额度，不调用发送接口。

未实现该能力的发送器 SHALL 保持原行为（接受全部规范化后的目标）；包装发送器的装饰器须转发该能力，否则预检不生效。指标沿用既有的参数拒绝分类，不新增标签值。

#### Scenario: 不支持的号码在占额度之前被拒绝
- **WHEN** 短信发送器只支持中国大陆号码，客户端请求向 `+14155550123` 发送登录验证码
- **THEN** 返回 400 `TARGET_NOT_SUPPORTED`，没有新的验证码轮次、冷却或额度变化，发送器未被调用

#### Scenario: 旧发送器不受影响
- **WHEN** 宿主注入的短信发送器未实现号码支持能力
- **THEN** 任意合法 E.164 号码按原流程发送

### Requirement: 内置中文渲染器品牌前缀

内置中文邮件渲染器 SHALL 支持可选的品牌前缀。设置后：

- 标题为“<品牌><用途>验证码”；
- 正文以“【<品牌>】”开头，其余内容不变。

未设置或为空时，标题与正文 SHALL 与现有输出逐字一致。品牌值 SHALL 为 1 至 32 个 Unicode 码位，不得包含换行或其他控制字符、格式字符（如双向覆盖、零宽字符）或行/段分隔符，不得有首尾空白；不合法的品牌 SHALL 在发送器构造或服务配置阶段被拒绝，渲染时 SHALL 不输出未经校验的品牌。

#### Scenario: 带品牌的登录邮件
- **WHEN** 品牌为“食方”，渲染登录验证码
- **THEN** 标题为“食方登录验证码”，正文以“【食方】您正在进行登录。”开头

#### Scenario: 未设置品牌
- **WHEN** 品牌为空
- **THEN** 四种用途的标题与正文与变更前逐字一致

#### Scenario: 拒绝邮件头注入
- **WHEN** 品牌包含 CR/LF、其他控制字符、超过 32 个码位或首尾空白
- **THEN** 配置被拒绝并指出配置项，不发送邮件
