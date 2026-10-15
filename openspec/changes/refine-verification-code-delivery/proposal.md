## Why

现有 SMSSender、EmailSender 和 Message 的名称未体现验证码专用边界，内置 SMTP 又将固定中文文案与网络传输耦合，集成方难以复用传输能力并独立控制本地化和模板。将两个渠道接口命名精确化，并开放 SMTP 渲染扩展点，可在保持现有验证码行为和宿主源码兼容的前提下解决这些问题。

## What Changes

- 以 sender.SMSCodeSender、sender.EmailCodeSender、sender.CodeMessage 作为正式名称；保留 SendSMS / SendEmail 方法、现有消息字段及 Deps.SMSSender / Deps.EmailSender 字段名。旧类型名称通过 Deprecated 类型别名兼容。
- 为可选 SMTP 发送器增加显式注入 Renderer 的构造入口，渲染结果包含 Subject、Text 和可选 HTML；传输实现只负责验证、编码及 SMTP 投递。
- 将现有中文文案提取为独立的兼容渲染器。旧 smtp.New(Config) 继续使用该渲染器；新入口要求显式提供渲染器，accountsvc 显式装配兼容渲染器，保持部署行为。
- 宿主按验证码用途选择模板、品牌和语言，提供固定配置及请求 context 的本地化接入示例。核心消息不增加 Locale、模板 ID 或服务商参数。
- 补充新旧名称兼容、四种验证码用途、本地化隔离、MIME 编码、渲染失败和隐私验证，更新长期接入文档。
- 范围仅为验证码发送；不引入 CodeDeliverer、通用消息/事件机制、账号安全通知或审计扩展，不修改验证码轮次、配额和存储结构。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `verification-delivery`：明确两个验证码发送接口的精确名称和兼容契约；新增宿主可注入的 SMTP 渲染、本地化上下文及渲染失败行为，保留既有投递安全与额度语义。

## Impact

- 公共类型：user/sender 的新名称及旧别名；user/sender/smtp 的 Renderer、EmailContent、兼容渲染器和新增构造入口。
- 实现与装配：user/sender/smtp、accountkit.go、user.Service 依赖声明、internal/accountsvc，以及受影响的示例与测试引用。
- 文档：README.md、docs/compatibility.md、docs/accountsvc.md；本次验证记录保存在本变更 verification.md。
- 新入口为增量 API；保留原 Config 结构、smtp.New 函数签名、Deps 字段、发送方法及消息字段，旧调用无须强制迁移。无新增运行依赖、数据库迁移、Redis 格式或 HTTP 协议变更。
