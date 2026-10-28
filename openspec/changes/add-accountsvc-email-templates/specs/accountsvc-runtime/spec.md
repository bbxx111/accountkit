## ADDED Requirements

### Requirement: 服务邮件模板文件

serve SHALL 支持可选的 `ACCOUNTSVC_SMTP_TEMPLATES_FILE`。仅当 EMAIL 启用时，serve SHALL 在监听及创建运行依赖之前，有界读取一次该本地普通文件并校验，不支持 URL，也不热加载。

**未设置或为空**：邮件文案 SHALL 与内置中文渲染器逐字一致。

**文件格式**：

- 文件 SHALL 为 UTF-8 JSON 对象，`version` 为 1，`templates` 恰好包含 `SIGN_IN`、`BIND`、`REAUTH`、`SIGN_IN_COMPLETE` 四个键。
- 每个用途恰好包含非空字符串 `subject` 与 `text`。
- 以下情况 SHALL 拒绝：未知字段、重复键、尾随内容、缺少或多出用途、`null`。

**占位符**：

- 模板 SHALL 只接受 `{code}`、`{ttl_minutes}`、`{brand}` 三个占位符，`{{` 与 `}}` 表示字面花括号。
- 以下情况视为配置错误：其他名称、单独的 `{` 或 `}`、空名称、未闭合。
- 每个 `text` SHALL 至少包含一次 `{code}`；`subject` SHALL 不包含 `{code}`。
- `{ttl_minutes}` SHALL 为有效期分钟数，向上取整。
- `{brand}` SHALL 取 `ACCOUNTSVC_SMTP_BRAND`；品牌为空时引用 `{brand}` SHALL 视为配置错误。
- 替换后的值 SHALL 不再展开为占位符。渲染 SHALL 不执行任何模板函数、条件或循环。

**长度与字符**：

- `subject` SHALL 为 1–64 个码位，不得有首尾空白，不得含控制字符（含换行和制表符）、Unicode 格式字符、行分隔符或段分隔符。
- `text` SHALL 为 1–4096 个码位，去掉空白后非空；控制字符只允许 LF 和 TAB，不得含 CR、Unicode 格式字符、行分隔符或段分隔符。
- 每个模板 SHALL 最多包含 8 个占位符。
- 文件 SHALL 非空，且不超过 64 KiB。

**失败与日志**：

- 读取失败、非普通文件、空文件、超限或内容非法时，serve SHALL 在监听前非零退出。错误 SHALL 只含变量名、固定的字段位置和安全分类，不含路径、文件内容或解码器原文。
- EMAIL 启用时，serve SHALL 记录一条 INFO 日志，说明使用的是内置文案还是自定义模板。自定义时附带格式版本和文件内容 SHA-256 的前 12 位十六进制，供运维核对各副本是否一致。该日志及其他日志 SHALL 不包含路径、模板原文、渲染结果或验证码。
- EMAIL 禁用时，或执行 migrate 时，SHALL 不读取或校验该设置。
- 库 New/ConfigFromEnv SHALL 保持不做文件 I/O，也不新增公开 Go API。

#### Scenario: 合法模板生效

- **WHEN** serve 启用 EMAIL，设置 `ACCOUNTSVC_SMTP_BRAND=食方` 和合法模板文件，并发送登录补全验证码邮件
- **THEN** 邮件标题和正文按该用途的模板渲染，`{brand}`、`{code}`、`{ttl_minutes}` 分别替换为品牌、验证码和向上取整的分钟数；启动日志说明使用了自定义模板，附带版本和摘要，不含模板原文

#### Scenario: 未配置模板

- **WHEN** serve 启用 EMAIL 但未设置模板文件
- **THEN** 邮件文案与内置中文渲染器一致，启动日志说明使用了内置文案

#### Scenario: 模板不完整或非法

- **WHEN** 模板文件缺少某一用途或字段，或含未知字段、未知占位符、标题含 `{code}` 或换行、正文缺少 `{code}`、超出长度或占位符数量上限
- **THEN** serve 在监听前失败，错误指出 `ACCOUNTSVC_SMTP_TEMPLATES_FILE` 及字段位置，不回显文件内容，不回落到内置文案

#### Scenario: 品牌为空却引用品牌

- **WHEN** 模板引用了 `{brand}`，但 `ACCOUNTSVC_SMTP_BRAND` 未设置
- **THEN** serve 在监听前失败，不渲染缺少品牌的邮件

#### Scenario: EMAIL 禁用与 migrate 隔离

- **WHEN** EMAIL 被显式禁用的 serve 或 migrate 设置了不存在或非法的模板文件路径
- **THEN** 忽略该设置，启动或迁移只受其他配置约束

#### Scenario: 配置变更

- **WHEN** 运行期间模板文件被修改
- **THEN** 当前实例的文案不变，重启后才加载新内容

## MODIFIED Requirements

### Requirement: 服务邮件品牌前缀

serve SHALL 读取可选的 `ACCOUNTSVC_SMTP_BRAND`。未配置模板文件时，SHALL 把它传给内置中文渲染器；配置了模板文件时，SHALL 将它作为模板 `{brand}` 占位符的值。未设置或为空，且未配置模板文件时，邮件文案 SHALL 不变。值不合法时（控制字符、换行、超过 32 个码位或首尾空白），serve SHALL 启动失败并指出该变量。EMAIL 被显式禁用时 SHALL 不解析该变量。

#### Scenario: 设置品牌
- **WHEN** serve 设置 `ACCOUNTSVC_SMTP_BRAND=食方`、未配置模板文件，并发送登录验证码邮件
- **THEN** 邮件标题为“食方登录验证码”
