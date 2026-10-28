## Context

- 动机见 proposal.md。
- 发送路径：`smtp.NewWithRenderer(Config, Renderer)` 允许注入渲染器；`EmailContent.Text` 必须非空，`HTML` 非空时发送器写出 `multipart/alternative`；标题按 RFC 2047 编码并校验不含 CR/LF；发送器不支持内嵌图片。
- 发码请求的 context 会经 `user/observability.go` 传给 `SendEmail` 和 `Render`，规格要求发送器保留 context 值。
- 配置文件先例：资料 Schema 文件（普通文件、64 KiB 上限、错误只含变量名和分类、migrate 不读取）。
- 演进过程：R7 首版实现了“单个 JSON、纯文本正文、`{name}` 占位符”；讨论 HTML 和多语言后，先改成“清单 + 外部 HTML 文件”，再发现三套 HTML 只有文字不同，最终收敛为“内置布局 + 清单只配文字”。首版的字符规则（`internal/mailtext`）、单次替换、有界文件读取和启动日志沿用。

## Goals / Non-Goals

**Goals:**
- 部署方只维护一份清单，就能得到带品牌 Logo 与样式的三语言验证码邮件，并按请求语言发送。
- 配置错误在监听前暴露，并可用子命令离线预览。
- 不配置时行为逐字不变；库和 HTTP 契约不变；不新增依赖。

**Non-Goals:**
- 不支持自定义 HTML 布局（格式为此预留 `html_file` 字段名，本次不实现）。
- 不内嵌图片或附件，不支持 `data:` 图片。
- 不保存用户语言偏好，不按 IP、手机号推断语言，不新增请求体语言字段。
- 不改短信，不做热加载，不做复数或日期等本地化格式。

## Decisions

### D1 清单格式

```json
{
  "version": 1,
  "default_language": "zh-Hans",
  "languages": {
    "zh-Hans": {
      "brand": "食方",
      "logo_url": "https://static.example.com/email/logo.png",
      "common": {"code_label": "您的验证码", "expiry": "验证码 {{ttl_minutes}} 分钟内有效。", "safety": "…", "footer": "…"},
      "templates": {
        "SIGN_IN": {"subject": "{{brand}}登录验证码", "heading": "登录验证", "intro": "您正在登录{{brand}}，…"},
        "BIND": {…}, "REAUTH": {…}, "SIGN_IN_COMPLETE": {…}
      }
    },
    "zh-Hant": {…}, "en": {…}
  }
}
```

- 解析沿用 R7 的严格规则（`internal/boundedjson`）：拒绝重复键、尾随内容、未知字段、`null`、非法 UTF-8。
- `version` 必须为 1；`default_language` 必须是 `languages` 的键。
- `languages` 1–8 项；标签必须是规范形式的 BCP 47（等于 `language.Parse` 后的 `String()`），不能是 `und`，两两不同。
- 语言块只允许 `brand`（必填）、`logo_url`（可选）、`common`（必填）、`templates`（必填）。
- `common` 只允许 `code_label`、`expiry`、`safety`（必填）和 `footer`（可选）。
- 每个用途只允许 `subject`、`heading`、`intro`，都必填。
- 字段都是固定的，所以不需要自定义变量和布局文件引用。以后加自定义布局时增加可选 `html_file`，现有清单不受影响。

### D2 内置布局

- HTML 和纯文本两份布局用 `embed` 放在 `emailtemplates` 包内，是可信内容；槽位用 `{{lang}}`、`{{logo}}`、`{{brand}}`、`{{heading}}`、`{{intro}}`、`{{code_label}}`、`{{code}}`、`{{expiry}}`、`{{safety}}`、`{{footer}}`。
- 版式：表格布局、行内样式；`<style>` 里只有一条移动端适配；验证码用大号等宽字体显示；页脚有“自动发送”提示和 `© 品牌`。
- `{{logo}}` 槽位由 Go 代码填入：有 `logo_url` 时是一段 `<img>`（`src` 做属性转义，`alt` 为品牌），否则为空。布局本身不含条件逻辑。
- `{{footer}}` 为空时整段省略（Go 代码判断，同上）。
- 纯文本布局：标题、引导语、验证码标签与验证码、有效期、安全提示、页脚，段落之间空一行，使用 LF。
- 内置布局与一组固定清单的渲染结果用黄金文件测试固定，避免无意改动版式。

### D3 占位符与转义

- 清单文字中只允许 `{{brand}}` 和 `{{ttl_minutes}}`；`{{code}}` 不允许，验证码位置由布局决定。
- 单个 `{` `}` 按字面处理；出现 `{{` 但后面不是合法占位符即报错，拼错的占位符不会原样发给用户。
- 编译方式沿用 R7：启动时切分成字面量段和占位符段，渲染只做拼接，替换值不再展开。
- 渲染顺序：先用品牌和分钟数渲染各段文字（得到纯文本），再把这些纯文本作为槽位值填入布局。填入 HTML 布局时整体 `html.EscapeString`；填入纯文本布局时不转义。
- `logo_url`：必须以 `https://` 开头、能被 `url.Parse` 解析、无用户信息、不含空白和控制字符、不超过 2048 字节。

### D4 字符与长度

- `subject`：1–64 个码位，头部规则（`mailtext.HeaderText`），最多 8 个占位符。
- 其他文字：正文规则（`mailtext.BodyText`，只允许 LF 和 TAB 两种控制字符），`heading`、`code_label` 1–64 个码位，`intro`、`expiry`、`safety`、`footer` 1–1024 个码位，各最多 8 个占位符。
- `brand`：与 `ValidateBrand` 相同。
- 清单不超过 64 KiB。
- 由于每个槽位都有上限，渲染结果的长度有确定上界，不另做渲染后检查。

### D5 品牌只在清单里

- `{{brand}}` 只取所在语言的 `brand`，不继承。
- 设置了 `ACCOUNTSVC_SMTP_TEMPLATES_FILE` 的同时又设置了非空的 `ACCOUNTSVC_SMTP_BRAND`，启动失败，错误同时指出两个变量名。`ACCOUNTSVC_SMTP_BRAND` 仍只服务于内置中文渲染器。

### D6 语言选择

- 中间件只在清单有多于一种语言时安装，覆盖所有端点：读取 `Accept-Language`；超过 512 字节、解析失败或缺失时不设置偏好；否则用 `language.ParseAcceptLanguage` 解析，最多取前 8 项，写入 context。
- 启动时用 `language.NewMatcher` 构造匹配器，默认语言放第一位。渲染时无偏好直接用默认语言；匹配置信度为 `language.No` 时也用默认语言。
- 用表驱动测试固定：zh-TW、zh-HK、zh-MO → zh-Hant；zh-CN、zh-SG、zh → zh-Hans；en-US、en-GB → en；ja → 默认。
- 不放进库的 HTTP 层：语言选择是部署策略；嵌入宿主可以用自己的 Renderer 实现。
- 网关必须透传 `Accept-Language`；客户端应把它设为界面语言。

### D7 检查子命令

- `accountsvc email-templates check <清单路径> [-out 目录]`，复用 serve 的解析代码；不读取 `ACCOUNTSVC_` 环境变量，不连接任何依赖。
- 失败时非零退出，错误格式与 serve 相同。
- `-out`：用验证码 `123456`、有效期 5 分钟渲染每种语言、每种用途，写出 `<lang>/<PURPOSE>.html`、`<lang>/<PURPOSE>.txt` 与 `subjects.txt`。目录不存在则创建；已存在时只允许覆盖这些固定文件名，发现其他文件则拒绝。
- 成功时打印版本、摘要和语言列表。

### D8 启动 INFO

- EMAIL 启用时记录一条：`accountsvc: email templates: built-in`，或 `accountsvc: email templates: custom` 附 `version=1`、`sha256=<清单 SHA-256 前 12 位>`、`languages=<按清单顺序，默认语言在前>`。
- 不记录路径、品牌、文字、Logo 地址、渲染结果、逐次选中的语言或验证码。

## Risks / Trade-offs

- [版式固定，部署方不能改配色和排版] → 用户已接受；后续可加可选的自定义布局，纯增量。
- [远程 Logo 默认被客户端屏蔽，或图床不可用] → `alt` 为品牌；验证码只用文本；文档要求 Logo 放在部署方自己的 HTTPS 域名下。
- [客户端没带 `Accept-Language`，或网关剥离] → 回到默认语言，不失败；文档和网关验收清单写明要求。
- [x/text 升级改变匹配结果] → 表驱动测试固定。
- [`{{` 误判] → 文字里极少出现 `{{`；错误给出字段位置。

## Migration Plan

纯增量，没有数据迁移。首版格式未发布，不保留兼容。回滚时删除变量并重启即可。
