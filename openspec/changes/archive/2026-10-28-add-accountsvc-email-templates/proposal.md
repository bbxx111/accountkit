## Why

accountsvc 在 `run.go` 中固定注入 `smtp.LegacyChineseRenderer{Brand}`，部署方只能通过 `ACCOUNTSVC_SMTP_BRAND` 加品牌前缀：
- 四种用途的标题由“品牌 + 用途 + 验证码”拼成，登录补全用途会变成“食方完成登录并关联身份，未注册时自动注册验证码”，不适合作标题。
- 邮件只有纯文本，没有品牌 Logo 和样式。
- 不能按用户使用的文字（简体、繁体、英文）发送不同内容。

ai-food 内嵌 accountkit 时能自己控制这些，改为独立部署 accountsvc 后失去了这一能力。本变更合并了 R7（可配置邮件文案）与 R8（多语言）。

设计经用户逐步确认：
- 邮件为 HTML 加纯文本；版式由**内置布局**提供，部署方只配置一份清单（文字、品牌、Logo 地址），自定义布局推后；
- 图片只用远程 HTTPS 地址；
- 品牌全部写在清单里；
- 按请求的 `Accept-Language` 在简体、繁体、英文之间选择；
- 提供模板检查与预览子命令。

## What Changes

- serve 新增可选的 `ACCOUNTSVC_SMTP_TEMPLATES_FILE`，指向一份清单 JSON：
  - `version` 为 1，`default_language` 指定默认语言；
  - `languages` 把 BCP 47 语言标签映射为 `brand`、可选 `logo_url`、`common`（四种用途共用的文字）和 `templates`；
  - `templates` 必须为 SIGN_IN / BIND / REAUTH / SIGN_IN_COMPLETE 各提供 `subject`、`heading`、`intro`。
- 邮件由内置布局渲染为 `multipart/alternative`（HTML + 纯文本），段落固定为：标题、引导语、验证码、有效期、安全提示、页脚（可选）。
- 文字中只允许 `{{brand}}` 和 `{{ttl_minutes}}` 两个占位符；验证码由布局放置，文字里不得出现 `{{code}}`。单个花括号按字面处理；`{{` 后不是合法占位符即为配置错误。插入 HTML 时整体转义，文字里不能写标签。
- 品牌只来自清单的各语言 `brand`（可选；没有品牌的语言不得引用 `{{brand}}`，也不能配置 Logo）。**BREAKING（未发布）**：删除 `ACCOUNTSVC_SMTP_BRAND`。
- 未设置清单时，服务使用嵌入的无品牌默认清单（简体、繁体、英文）并记录 WARN；accountsvc 不再使用 `LegacyChineseRenderer`，原纯文本中文邮件由新布局的默认文案替代。库的 `smtp.New` 默认值不变。
- serve 按发码请求的 `Accept-Language` 进行 BCP 47 匹配，识别地区隐含的文字（zh-TW/zh-HK → zh-Hant，zh-CN/zh → zh-Hans，en-GB → en）；匹配不上、缺失或非法时使用默认语言。
- 校验在 serve 监听前完成（只在 EMAIL 启用时），失败即退出；错误只给变量名、字段位置和固定分类，不回显内容；EMAIL 禁用或执行 migrate 时不解析。
- 新增子命令 `accountsvc email-templates check <清单> [-out 目录]`：用与 serve 相同的规则校验，并用示例验证码把每种语言、每种用途渲染为 `.html`、`.txt` 及标题汇总，供部署前预览。
- 配置了清单时记录一条 INFO（版本、清单 SHA-256 前 12 位、语言列表）；未配置时记录 WARN。日志不含路径、品牌、文字和验证码。
- 新增样例 `deploy/accountsvc/email-templates.example.json`（简体、繁体、英文三套文字），由测试保证可以通过校验。
- 不新增库的公开 Go API 和第三方依赖；`smtp` 包、HTTP 请求和响应结构不变。无破坏性变化。

## Capabilities

### New Capabilities

（无）

### Modified Capabilities

- `verification-delivery`：「真实 SMTP 邮件投递」中，accountsvc 的装配由“只注入兼容中文渲染器、不新增模板或语言配置”改为：使用内置布局加多语言清单（未配置时为嵌入的无品牌默认清单），并按请求语言选择；库的兼容渲染器不变。
- `accountsvc-runtime`：新增「服务邮件模板清单」「邮件语言选择」「邮件模板检查命令」三项要求；删除「服务邮件品牌前缀」。

## Impact

- 代码：
  - 新增 `internal/accountsvc/emailtemplates`：清单解析、内置布局（嵌入的 HTML 与纯文本）、渲染、语言匹配；
  - 新增 `internal/mailtext`：字符规则，与 `smtp.ValidateBrand` 共用；
  - 修改 `internal/accountsvc` 的 `config.go`、`run.go` 与命令分发；新增 `Accept-Language` 中间件。
- 依赖：使用已有依赖 `golang.org/x/text/language` 做语言匹配，不新增 module。
- 文档与部署：`docs/accountsvc.md`、`docs/gateway-integration.md`（须透传 `Accept-Language`）、`docs/compatibility.md`、`deploy/accountsvc/.env.example`、`compose.yaml`、样例清单。
- 下游：ai-food 挂载清单；客户端在发码请求中把界面语言放进 `Accept-Language`；网关不得剥离该请求头。
