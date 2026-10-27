## Why

ai-food 计划在下个版本发布后，从嵌入 accountkit 改为独立部署 accountsvc。两个 App 的主要登录方式是手机号验证码，但 accountsvc 没有短信服务商：显式启用 PHONE 时拒绝启动。这一点阻塞了切换，对应 ai-food 需求 R1（P0）。

另外，accountsvc 的邮件标题固定为“<用途>验证码”，无法带上产品品牌，对应需求 R4（P2）。两项都只改发送装配，不涉及认证语义，因此合为一个变更。

## What Changes

- **阿里云短信发送器**：新增可选包 `user/sender/aliyun`，实现 `sender.SMSCodeSender`。
  - 通过标准库完成签名后的 RPC 调用（SendSms），不引入阿里云 SDK。
  - 嵌入式宿主也可以直接使用。
  - 发送失败与超时沿用现有发送器契约：返回依赖不可用，保留已占额度与冷却，不自动重发。
  - 日志与错误不包含完整手机号、验证码或 AccessKey Secret。
- **accountsvc 短信服务商配置**：新增可选的服务商配置，首版只支持 `aliyun`，包括：
  - AccessKey ID 与 Secret；
  - 签名名称；
  - 四个用途（SIGN_IN、BIND、REAUTH、SIGN_IN_COMPLETE）各自的模板 code；
  - 仅供测试或代理部署使用的 endpoint。

  配置了服务商时，才允许显式启用 PHONE；配置不完整时，serve 启动失败并指出变量名，不回显值。migrate 不解析这些配置。
- **身份策略**：省略 `ACCOUNTKIT_ENABLED_IDENTITY_KINDS` 时，服务行为不变（不因配置了短信而自动启用 PHONE），PHONE 必须显式启用。未配置服务商时显式启用 PHONE 仍被拒绝。
- **邮件品牌前缀**：accountsvc 新增可选的 `ACCOUNTSVC_SMTP_BRAND`，同时作用于内置中文渲染器的标题和正文开头。未设置时文案与现状逐字一致。品牌值拒绝换行与控制字符并限制长度，防止邮件头注入。
- 嵌入式宿主的 `SMSSender`/`SMSCodeSender` 接口与 `smtp.Renderer` 契约不变。

## Capabilities

### New Capabilities

- `aliyun-sms-delivery`：阿里云短信验证码发送器的请求签名、用途到模板的映射、号码格式、错误分类、超时、隐私与可测试的 endpoint 覆盖。

### Modified Capabilities

- `accountsvc-runtime`：原规定“首版无短信服务商，显式启用 PHONE 拒绝启动”，改为“配置服务商后允许显式启用 PHONE”，并补充短信配置的校验与生产要求，以及 migrate 不解析短信配置。另新增 SMTP 品牌前缀配置。
- `verification-delivery`：内置中文渲染器支持可选品牌前缀；未设置时输出保持不变。

## Impact

- **代码**：
  - 新增 `user/sender/aliyun/`；
  - `internal/accountsvc/`：配置解析、校验、装配与 production 要求；
  - `user/sender/smtp/legacy_renderer.go`：品牌前缀。
- **依赖**：无新增第三方依赖。
- **配置**：新增 `ACCOUNTSVC_SMS_*` 与 `ACCOUNTSVC_SMTP_BRAND` 环境变量，均为可选，未设置时行为不变。
- **文档**：`docs/accountsvc.md`、README、`deploy/accountsvc/.env.example`、`docs/compatibility.md`。
- **数据库**：无结构变化、无迁移。
- **待 ai-food 确认后写入 design 与 specs 的细节**：是否只支持中国大陆号码，阿里云频控的错误映射，模板是否共用，签名数量，品牌长度上限。
