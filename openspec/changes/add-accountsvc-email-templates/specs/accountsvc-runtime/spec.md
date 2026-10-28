## ADDED Requirements

### Requirement: 服务邮件模板清单

serve SHALL 支持可选的 `ACCOUNTSVC_SMTP_TEMPLATES_FILE`，指向一份邮件模板清单 JSON。仅当 EMAIL 启用时，serve SHALL 在监听及创建运行依赖之前有界读取一次该本地普通文件并完成校验。不支持 URL，也不热加载。

**未设置或为空**：serve SHALL 使用嵌入的无品牌默认清单（简体默认，另含繁体与英文），并在启动时记录 WARN 提示设置该变量；默认清单 SHALL 经过与部署清单相同的校验。

**清单结构**：
- 清单 SHALL 为 UTF-8 JSON 对象，`version` 为 1，`default_language` 为 `languages` 中的一个键。
- `languages` SHALL 包含 1–8 种语言。语言标签 SHALL 为规范形式的 BCP 47 标签，不得为 `und`，且两两不同。
- 每种语言 SHALL 只含 `brand`（可选）、`logo_url`（可选）、`common`（必填）与 `templates`（必填）。
- `common` SHALL 只含 `code_label`、`expiry`、`safety`（必填）与 `footer`（可选）。
- `templates` SHALL 恰好包含 `SIGN_IN`、`BIND`、`REAUTH`、`SIGN_IN_COMPLETE`；每个用途 SHALL 恰好包含非空字符串 `subject`、`heading`、`intro`。
- 以下情况 SHALL 拒绝：未知字段、重复键、尾随内容、`null`、缺少或多出用途。任何语言或用途 SHALL 不得回落到其他语言或内置文案。

**版式**：
- 邮件 SHALL 由服务内置的布局渲染，同时包含 HTML 部分与纯文本部分，段落依次为标题、引导语、验证码、有效期、安全提示、页脚（`footer` 省略时无此段）。
- 提供 `logo_url` 时，HTML 部分 SHALL 显示该图片并以品牌作为替代文字，因此该语言 SHALL 有品牌；`logo_url` SHALL 为 HTTPS 地址，不含用户信息、空白或控制字符，不超过 2048 字节。
- 有品牌的语言 SHALL 在页脚显示 `© 品牌`；无品牌的语言 SHALL 省略该行。
- 服务 SHALL 不内嵌图片或附件，也不接受自定义布局。

**占位符与转义**：
- 清单文字 SHALL 只接受 `{{brand}}` 与 `{{ttl_minutes}}`，名称两侧不得有空白；`{{code}}` 与其他名称 SHALL 视为配置错误。单个 `{` 或 `}` SHALL 按字面处理；`{{` 后不是合法占位符时 SHALL 视为配置错误。
- `{{brand}}` SHALL 取所在语言的 `brand`，不继承其他语言；没有品牌的语言引用 `{{brand}}` SHALL 视为配置错误。`brand` SHALL 为 1–32 个码位，不含控制字符、Unicode 格式字符或行/段分隔符，无首尾空白。
- `{{ttl_minutes}}` SHALL 为有效期分钟数，向上取整。
- 清单文字插入 HTML 时 SHALL 整体做 HTML 转义，文字中的标签 SHALL 按字面显示；插入纯文本时不转义。替换后的值 SHALL 不再展开。渲染 SHALL 不执行任何模板函数、条件或循环。

**长度与字符**：
- `subject` SHALL 为 1–64 个码位，不得有首尾空白，不得含控制字符（含换行和制表符）、Unicode 格式字符、行分隔符或段分隔符。
- `heading`、`code_label` SHALL 为 1–64 个码位；`intro`、`expiry`、`safety`、`footer` SHALL 为 1–1024 个码位；这些文字去掉空白后非空，控制字符只允许 LF 和 TAB，不得含 CR、Unicode 格式字符、行分隔符或段分隔符。
- 每段文字 SHALL 最多包含 8 个占位符。清单 SHALL 非空，且不超过 64 KiB。

**失败与日志**：
- 读取失败、非普通文件、空文件、超限或内容非法时，serve SHALL 在监听前非零退出。错误 SHALL 只含变量名、固定的字段位置和安全分类，不含路径、文件内容或解码器原文。
- EMAIL 启用且配置了清单时，serve SHALL 记录一条 INFO 日志，附带格式版本、清单内容 SHA-256 的前 12 位十六进制与语言标签列表；未配置时 SHALL 记录 WARN。这些日志及其他日志 SHALL 不包含路径、品牌、文字、Logo 地址、渲染结果或验证码。
- EMAIL 禁用时，或执行 migrate 时，SHALL 不读取或校验该设置。
- 库 New/ConfigFromEnv SHALL 保持不做文件 I/O，也不新增公开 Go API。

#### Scenario: 合法清单生效

- **WHEN** serve 启用 EMAIL，配置了合法的三语言清单，并以默认语言发送登录补全验证码邮件
- **THEN** 邮件标题、HTML 和纯文本都按默认语言的补全文字和内置布局渲染，品牌、验证码和向上取整的分钟数被替换，Logo 以品牌为替代文字；启动日志说明使用了自定义清单，附带版本、摘要和语言列表，不含文字

#### Scenario: 未配置清单

- **WHEN** serve 启用 EMAIL 但未设置清单，客户端以 `Accept-Language: en` 请求邮箱登录验证码
- **THEN** 服务照常启动并记录 WARN；邮件使用默认清单的英文文字，无品牌、无 Logo、无 `©` 行，仍同时含 HTML 与纯文本部分

#### Scenario: 无品牌语言

- **WHEN** 清单中某语言省略 `brand`，其文字不引用 `{{brand}}` 且未配置 `logo_url`
- **THEN** 该语言正常渲染，页脚省略 `©` 行；若其文字引用了 `{{brand}}` 或配置了 `logo_url`，serve 在监听前失败

#### Scenario: 清单不完整或非法

- **WHEN** 清单缺少某种语言的某一用途或字段，或含未知字段、未知占位符、`{{code}}`、不完整的 `{{`，或标题含换行，或超出长度或占位符数量上限，或 `logo_url` 不是 HTTPS
- **THEN** serve 在监听前失败，错误指出 `ACCOUNTSVC_SMTP_TEMPLATES_FILE` 及字段位置，不回显内容，不回落到其他语言或内置文案

#### Scenario: HTML 中的文字转义

- **WHEN** 某语言的 `brand` 或 `intro` 含有 `<`、`&` 或引号
- **THEN** HTML 部分输出转义后的实体，版式不受影响；标题和纯文本部分显示原字符

#### Scenario: EMAIL 禁用与 migrate 隔离

- **WHEN** EMAIL 被显式禁用的 serve 或 migrate 设置了不存在或非法的清单路径
- **THEN** 忽略该设置，启动或迁移只受其他配置约束

#### Scenario: 配置变更

- **WHEN** 运行期间清单被修改
- **THEN** 当前实例的文案不变，重启后才加载新内容

### Requirement: 邮件语言选择

当清单包含多种语言时，serve SHALL 读取每个请求的 `Accept-Language`，并在发送邮件验证码时从已配置的语言中选出一种渲染。

- 匹配 SHALL 遵循 BCP 47 语言匹配，并识别地区隐含的文字：zh-TW、zh-HK、zh-MO 匹配 `zh-Hant`；zh-CN、zh-SG、zh 匹配 `zh-Hans`；en-US、en-GB 匹配 `en`。
- 近似匹配（如只配置简体时的繁体请求）SHALL 采用匹配到的语言。以下情况 SHALL 使用 `default_language`，且不得因此使请求失败：请求头缺失、超过 512 字节、格式非法，或完全没有匹配。
- 未配置清单，或清单只有一种语言时，serve SHALL 不读取 `Accept-Language`。
- 语言选择 SHALL 只影响邮件标题、内容和品牌，不改变验证码用途、额度、审计、HTTP 响应或短信内容。日志 SHALL 不记录逐次选中的语言。

#### Scenario: 按界面语言发送繁体邮件

- **WHEN** 清单配置了 zh-Hans（默认）、zh-Hant、en，客户端以 `Accept-Language: zh-TW` 请求邮箱登录验证码
- **THEN** 邮件使用 zh-Hant 的文字和品牌渲染

#### Scenario: 无法匹配时使用默认语言

- **WHEN** 同一部署收到 `Accept-Language: ja`、没有该请求头，或请求头格式非法、超长
- **THEN** 邮件使用默认语言，发码请求照常成功

### Requirement: 邮件模板检查命令

accountsvc SHALL 提供子命令 `email-templates check <清单路径>`，使用与 serve 相同的规则离线校验清单。

- 该命令 SHALL 不读取 `ACCOUNTSVC_` 环境变量，也不连接数据库、Redis 或 SMTP。
- 校验失败时 SHALL 非零退出，错误格式与 serve 相同，不回显内容；成功时 SHALL 输出版本、摘要和语言列表。
- 指定 `-out <目录>` 时，SHALL 用示例验证码和固定有效期，为每种语言、每种用途写出渲染后的 HTML、纯文本和标题汇总。目录中存在本命令固定文件名之外的文件时 SHALL 拒绝写入。

#### Scenario: 部署前预览

- **WHEN** 运维对合法清单执行 `accountsvc email-templates check templates.json -out preview`
- **THEN** 命令成功退出，`preview` 下包含每种语言、四种用途的 `.html` 与 `.txt` 渲染结果及标题汇总，可在浏览器中查看

#### Scenario: 离线发现错误

- **WHEN** 对缺少用途或含未知占位符的清单执行该命令
- **THEN** 命令以非零码退出，指出字段位置，不写出任何预览文件

## REMOVED Requirements

### Requirement: 服务邮件品牌前缀

**Reason**: 品牌改为写在邮件模板清单的各语言 `brand` 中，服务不再使用内置中文渲染器；保留 `ACCOUNTSVC_SMTP_BRAND` 会让同一品牌有两个来源。产品尚未发布，无兼容负担。

**Migration**: 删除 `ACCOUNTSVC_SMTP_BRAND`；需要品牌时提供 `ACCOUNTSVC_SMTP_TEMPLATES_FILE` 清单并在其中设置 `brand`。嵌入式宿主继续使用库的 `smtp.LegacyChineseRenderer{Brand}`，不受影响。
