## Why

accountsvc 在 `run.go` 中固定注入 `smtp.LegacyChineseRenderer{Brand}`，部署方只能通过 `ACCOUNTSVC_SMTP_BRAND` 加品牌前缀。四种用途的标题由“品牌 + 用途 + 验证码”拼接，登录补全用途会变成“食方完成登录并关联身份，未注册时自动注册验证码”，不适合作邮件标题。ai-food 内嵌 accountkit 时可自定义文案，改为独立部署 accountsvc 后失去了这一能力（R7）。

## What Changes

- serve 新增可选 `ACCOUNTSVC_SMTP_TEMPLATES_FILE`：本地 JSON 文件，`version` 为 1，`templates` 须为 SIGN_IN / BIND / REAUTH / SIGN_IN_COMPLETE 四种用途各提供一套纯文本 `subject` 与 `text`；不允许部分回落到内置文案。
- 固定占位符：`{code}`、`{ttl_minutes}`（沿用向上取整）、`{brand}`（取自 `ACCOUNTSVC_SMTP_BRAND`）；用 `{{` 和 `}}` 表示字面花括号。正文必须含 `{code}`，标题不得含 `{code}`；未知占位符视为配置错误。不执行任意模板逻辑，不支持 HTML。
- 字符与长度规则：标题 1–64 个码位，字符规则与 `ValidateBrand` 相同；正文 1–4096 个码位，只允许 LF 和 TAB 两种控制字符；每个模板最多 8 个占位符；文件不超过 64 KiB，只读本地普通文件。
- 配置只在 EMAIL 启用时由 serve 在监听前读取并校验一次，失败即退出；错误只给变量名及安全分类/字段位置，不回显内容。EMAIL 禁用时或执行 migrate 时不解析。
- 启动时输出一条 INFO：`email templates: custom`（含版本与文件 SHA-256 前 12 位）或 `email templates: built-in`。日志不包含模板原文和验证码。
- 未设置时行为与现在逐字一致；`LegacyChineseRenderer` 及其内置文案不变。
- 新增 `deploy/accountsvc/email-templates.example.json`，作为四种用途的推荐文案，并由测试保证其可通过校验。
- 不新增库的公开 Go API；`smtp.New` / `NewWithRenderer` / `Renderer` 不变。无破坏性变化。

## Capabilities

### New Capabilities

（无）

### Modified Capabilities

- `verification-delivery`：「真实 SMTP 邮件投递」的 accountsvc 装配由“只注入兼容中文渲染器、不新增模板管理配置”改为：默认仍注入兼容中文渲染器，部署方可改用纯文本模板文件。
- `accountsvc-runtime`：新增「服务邮件模板文件」要求；「服务邮件品牌前缀」补充品牌同时供模板 `{brand}` 使用。

## Impact

- 代码：
  - 新增 `internal/accountsvc/emailtemplates`，负责解析、编译和渲染；
  - 新增 `internal/mailtext`，放标题与正文字符规则，供 `user/sender/smtp` 与 accountsvc 共用，不对外公开；
  - 修改 `user/sender/smtp/legacy_renderer.go`，让 `ValidateBrand` 改用共享规则，行为不变；
  - 修改 `internal/accountsvc/config.go` 和 `run.go`。
- 文档与部署：`docs/accountsvc.md`、`docs/compatibility.md`、`deploy/accountsvc/.env.example`、`deploy/accountsvc/compose.yaml`、新增示例模板文件。
- 依赖：仅用标准库，不新增第三方库。
- 下游：ai-food 将在 adopt-standalone-accountsvc 中挂载模板文件。
