## Context

- 动机见 proposal.md「Why」。
- 现状：
  - `smtp.NewWithRenderer(Config, Renderer)` 已经允许宿主注入渲染器。
  - 发送器在渲染前校验 `CodeMessage`（用途、数字验证码、正 TTL），渲染后校验 `EmailContent`（标题/正文非空白、标题不含 CR/LF）。
  - 标题按 RFC 2047 编码，正文按 quoted-printable 写出：非二进制模式下 LF 输出为 CRLF。
- accountsvc 已有本地文件配置先例 `ACCOUNTSVC_PROFILE_ATTRIBUTES_SCHEMA_FILE`。它的规则是：
  - 打开前后各检查一次是否为普通文件；
  - 上限 64 KiB；
  - 错误只含变量名和安全分类；
  - migrate 不读取。
- 设计已由 ai-food 会话与用户评审，三个待定点的结论为：
  1. 内置文案不变；
  2. 解析器放 internal；
  3. 正文上限 4096 个码位。

## Goals / Non-Goals

**Goals:**
- 部署方不改代码、不重建镜像，即可为四种用途分别设置纯文本邮件标题与正文。
- 配置非法时在 serve 监听前失败，错误不回显文件内容。
- 默认行为逐字不变。
- 文件格式可在 version 1 内向多语言扩展，旧文件保持可用。

**Non-Goals:**
- 不做 HTML 邮件、条件或循环等模板逻辑，不做按用户/请求的语言协商，不做热加载。
- 不新增库的公开 Go API，不修改 `LegacyChineseRenderer` 文案，不涉及短信。
- 不对文件做签名或完整性校验。启动日志里的摘要只用于人工核对多个副本是否加载了同一份文件。

## Decisions

### D1 单个 JSON 文件，而非每个用途一组环境变量

- 每个用途一组变量需要 8 个变量，多行正文在 env/compose/k8s 中转义容易出错，难以原子地保证“全部提供”，扩展多语言时会膨胀成 8×N 个变量。
- 单文件可以纳入配置仓库审查。
- 选 JSON 而不选 YAML：YAML 在 go.mod 中只是间接依赖；JSON 只用标准库，并可做严格解码。

格式：
```json
{"version": 1, "templates": {"SIGN_IN": {"subject": "...", "text": "..."}, "BIND": {...}, "REAUTH": {...}, "SIGN_IN_COMPLETE": {...}}}
```

严格解码规则：
- 未知字段拒绝（DisallowUnknownFields）；
- 解码后不得有尾随内容；
- 用 token 扫描拒绝任意层级的重复键（标准库默认是后者覆盖前者）；
- `null` 视为缺失；
- 非法 UTF-8 一律拒绝：标准库会把非法字节替换为 U+FFFD，所以先对原始字节做 `utf8.Valid` 检查。

多语言扩展：以后在 version 1 下新增可选字段（如 `default_language`、`languages`），顶层 `templates` 仍为默认语言。旧文件在新服务上照常可用；新文件在旧服务上因未知字段启动失败（fail-closed）。只有语义不兼容时才升 version。

### D2 占位符：启动时编译，渲染只做拼接

- 语法 `{name}`，名称限定为 `code`、`ttl_minutes`、`brand` 三个。`{{` 和 `}}` 表示字面花括号；单独的 `{` 或 `}`、空名称、未闭合，都是错误。
- 启动时把每个模板编译为“字面量段 + 占位符段”的切片，渲染时按顺序拼接。替换值不再扫描，所以品牌中的 `{` 不会二次展开。
- 不选 `text/template`：它的函数、条件和循环超出需求，错误信息会包含模板原文，`{{` 的语义也与本格式冲突。
- `{brand}` 只取自 `ACCOUNTSVC_SMTP_BRAND`，品牌只有这一个来源。模板引用了 `{brand}` 而品牌为空时，启动失败，避免渲染出缺字的标题。品牌非空但模板未引用时允许。
- `{ttl_minutes}` 用 `math.Ceil(TTL.Minutes())`，与内置文案一致。每次发送按 `CodeMessage.TTL` 计算。

### D3 字符规则抽到 `internal/mailtext`

- 模板解析器位于 `internal/accountsvc/emailtemplates`，无法调用 `smtp` 包的未导出函数；而导出新函数会扩大库的公开 API。因此把标题/正文字符规则放进模块内的 `internal/mailtext`。
- `smtp.ValidateBrand` 的签名和行为不变，内部改为调用同一套规则，避免两处规则分叉。
- 标题规则：合法 UTF-8；去掉首尾空白后与原文相同（不静默裁剪）；不含 Cc（含 CR、LF、TAB）、Cf（双向覆盖、零宽字符）、Zl、Zp。
- 正文规则：合法 UTF-8；去掉空白后非空；控制字符只允许 LF 和 TAB；不含 CR（统一用 LF，由 quoted-printable 输出 CRLF）以及 Cf、Zl、Zp。
- 长度在模板原文上校验：
  - 标题 1–64 个码位；正文 1–4096 个码位；
  - 每个模板最多 8 个占位符。
  - 因为品牌不超过 32 个码位、验证码长度和 TTL 位数有界，渲染后的长度也有确定上界。

### D4 `TemplateRenderer` 的构造与失败语义

- 只能通过解析函数构造，字段不导出，零值调用 `Render` 返回 `sender.ErrUnavailable`。
- `Render` 的行为：
  - 先检查 ctx 和 `CodeMessage`，失败一律返回固定的 `sender.ErrUnavailable`；
  - 不记录日志；
  - 同一实例只读，可安全并发调用。
- accountsvc 二选一：设置了文件就注入 `TemplateRenderer`，否则注入 `LegacyChineseRenderer{Brand}`。两者互斥，不存在按用途部分回落。

### D5 文件读取与错误信息

- 复用资料 Schema loader 的读取步骤，并把其中的读取部分提取为两者共用的函数：
  - 打开前后各检查一次是否为普通文件；
  - 用 LimitReader 限制在 64 KiB + 1；
  - 拒绝空文件。
- 错误形如 `invalid ACCOUNTSVC_SMTP_TEMPLATES_FILE (templates.SIGN_IN.subject: unknown placeholder)`。
  - 只含变量名、JSON 字段位置（由固定的用途名和字段名组成）及固定分类；
  - 不含路径、文件内容、占位符名或解码器原文。
  - JSON 语法错误只给出 `invalid JSON` 分类，不给偏移量。
- 只在 serve 且 EMAIL 启用时解析；EMAIL 禁用或执行 migrate 时即使变量非法也忽略，与 `SMTP_BRAND` 行为一致。

### D6 启动 INFO 日志

- EMAIL 启用时，监听启动日志之后记一条 INFO：
  - 自定义：`accountsvc: email templates: custom`，属性 `version=1`、`sha256=<原始文件字节 SHA-256 的十六进制前 12 位>`；
  - 未配置：`accountsvc: email templates: built-in`。
- 不记录路径和模板原文。EMAIL 禁用时不记录。
- 摘要只在配置阶段算一次，存放在 Config 中。

### D7 示例文件纳入测试

- `deploy/accountsvc/email-templates.example.json` 提供四种用途的推荐文案，SIGN_IN_COMPLETE 的标题为 `{brand}登录补全验证码`。
- 测试直接加载这个文件，并用非空品牌渲染四种用途，防止示例与解析规则漂移。
- 示例引用了 `{brand}`，所以使用它时需要同时设置 `ACCOUNTSVC_SMTP_BRAND`。文档会明确这一点。

## Risks / Trade-offs

- [JSON 中多行正文需要写 `\n`，可读性较差] → 提供示例文件和文档示例；正文上限放宽到 4096 个码位。
- [模板没有正确说明用途（例如补全邮件写成普通登录）] → 这是部署方的文案责任。文档提示补全邮件须说明“完成登录并关联身份”。服务端无法从语义上校验。
- [旧版本服务读取新扩展字段会启动失败] → 这是有意的 fail-closed，好过静默丢失语言；发布说明会注明。
- [ValidateBrand 改为调用共享规则后可能出现行为偏差] → 现有品牌测试保持不变；新增等价性测试。

## Migration Plan

纯增量，没有数据迁移。未设置变量的部署行为不变。要回滚，删除变量并重启即可。
