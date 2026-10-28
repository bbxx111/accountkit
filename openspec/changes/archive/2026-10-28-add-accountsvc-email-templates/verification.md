# add-accountsvc-email-templates 验收记录

日期：2026-10-28。分支 `feat/accountsvc-email-templates`，基于 develop `27ed492`。实施计划见 [plan](../../../../docs/superpowers/plans/2026-10-28-add-accountsvc-email-templates.md)。

## 演进说明

本变更经历了三版设计：
1. 首版（提交 `7e4ca34`、`6e1da7e`）：单个 JSON、纯文本正文、`{name}` 占位符，已实现并通过审查，但未推送。
2. 用户提出邮件需要 HTML、图片和多语言后，改为“清单 + 外部 HTML 文件”；再发现三套 HTML 只有文字不同。
3. 最终收敛为“内置布局 + 清单只配文字”，并合并了 R8 多语言需求。本记录只覆盖最终版本；首版的字符规则包 `internal/mailtext`、`ValidateBrand` 共享规则、有界文件读取和启动日志被沿用，其余首版代码已替换。

## 第二轮：删除品牌变量，内置无品牌默认清单

审查之后，用户决定删除 `ACCOUNTSVC_SMTP_BRAND`，并让服务不再使用 `LegacyChineseRenderer`，改为嵌入一份无品牌默认清单作为兜底。改动：
- `brand` 变为可选；无品牌的语言不得引用 `{{brand}}`、不能配置 `logo_url`，页脚省略 `©` 行。
- `emailtemplates.Default()` 返回嵌入的 `default.json`（简体、繁体、英文），经相同解析；`TestDefaultManifest`、`TestBrandlessLanguage` 覆盖。
- 未配置清单时 `warnUnconfigured` 记录 WARN；配置了清单记录 INFO。
- 依赖旧中文文案的集成测试断言改为默认清单文案；`TestConfigSMTPBrand` 删除；规格「服务邮件品牌前缀」改为 REMOVED。

本轮完成后在 Linux 镜像重新运行了下表全部检查，数字以本轮为准。

## 执行方式

- 本会话按顺序直接实施，采用 TDD：先写测试，确认失败，再实现。
- 实施完成后，由一个独立审查子代理审查整个分支（结论见下文）。

## 已运行的检查

Linux 验证镜像基于 `tests/Dockerfile`（golang:1.26.5）。测试用一次性的 PostgreSQL 17.4 与 Redis 7 容器，用完即清理。

| 检查 | 结果 |
|---|---|
| `bash scripts/verify-accountsvc.sh`（race、真实进程、TLS、伪 SMTP、必需测试门禁） | 通过：803 个测试通过，0 个失败。首次运行在模块下载阶段遇到代理网络中断（`unexpected EOF`），未进入测试；模块缓存后重跑通过。 |
| `go build ./...`、`go vet ./...` | 通过 |
| `go test -race -json -count=1 ./...`（`SERVER_TEST_DB_DSN` 与 `ACCOUNTSVC_TEST_REDIS_URL` 均设置） | 通过：3709 个测试通过，0 个失败 |
| `go run ./internal/testgate scripts/required-tests.txt` | 通过 |
| gofmt | 无差异 |
| `go mod tidy -diff` | 有差异，但与本变更无关：develop 基线（stash 后）同样有差异（`prometheus/client_model` 直接/间接标记与 go.sum 顺序）。本变更未改动 go.mod/go.sum，也没有新增依赖；`golang.org/x/text` 本来就是直接依赖。 |
| `openspec validate --all --strict` | 41 项全部通过 |
| 本机 Windows：`go test ./internal/accountsvc/ ./internal/accountsvc/emailtemplates/ ./cmd/accountsvc/ ./user/sender/smtp/ ./internal/mailtext/`（无 race） | 通过 |

## 覆盖要点

- **清单解析**（`emailtemplates_test.go`）：
  - 结构：version、default_language、1–8 种语言、规范 BCP 47 标签（拒绝 `zh-hant`、`und`、乱码）、语言块/`common`/用途的固定字段、未知字段、重复键、尾随内容、null、非法 UTF-8、缺少或多出用途；
  - 品牌：缺失、空、换行、超长；
  - Logo：http、含用户信息、含空格、`data:`、null、超过 2048 字节；
  - 占位符：`{{code}}`、未知名称、未闭合、带空格、空名称、每段 9 个；
  - 字符与长度：标题 CRLF/TAB/U+202E/尾部空白/65 码位；正文 CR、ESC、1025 码位；
  - 边界值：64、1024、7/8 个占位符、单个花括号、8 种语言、2048 字节 Logo；
  - 所有错误用哨兵字符串断言不回显内容，且位置前缀精确。
- **渲染**：
  - 四种用途逐字比对纯文本，HTML 断言 lang、title、Logo、标题、验证码、有效期、页脚和 `©`；
  - HTML 转义（品牌与引导语含 `&`、`<`、引号）、标题与纯文本不转义、换行转 `<br>`；
  - 可选 Logo 和页脚缺省时的输出；
  - TTL 向上取整；ctx 取消、非法 CodeMessage、零值、nil；race 下并发渲染。
- **语言匹配**：表驱动固定 zh-TW/HK/MO → zh-Hant、zh-CN/SG/zh → zh-Hans、en-US/GB → en、ja/空/`*`/超长/非法 → 默认；前 8 项上限；`RenderIn` 按标签渲染；单语言清单忽略偏好。
- **样例清单**：`TestExampleManifest` 直接加载 `deploy/accountsvc/email-templates.example.json`，断言三种语言的补全标题。
- **配置**（`config_email_templates_test.go`）：未设置时使用默认清单、合法清单的摘要与英文渲染、文件不存在/目录/空/超限/JSON 非法/标题含 `{{code}}`/默认语言不存在，错误不含路径与内容；migrate 与 EMAIL 禁用时忽略。
- **启动日志**：配置清单时 INFO（version、sha256、languages），未配置时 WARN（`TestWarnUnconfiguredInterfaces`），EMAIL 禁用不记录，不含文字、品牌、路径。
- **中间件**：en-US、zh-TW、缺失、乱码、超长五种请求头的选择结果。
- **检查命令**：成功输出；`-out` 创建目录并写出固定文件；重跑可覆盖；目录含其他文件时拒绝；清单非法时非零退出、不回显、不写预览；用法错误。
- **集成测试**（`TestServiceIntegrationEmailTemplates`）：真实进程 + 伪 SMTP，分别以 zh-HK、en-GB、ja、无请求头发码，断言标题、纯文本全文、HTML 中的验证码、Logo 与品牌；启动日志含 custom、sha256、`languages=zh-Hans,en,zh-Hant`；未配置清单时以 en 发码得到默认英文文案、无 `©`，日志含 WARN；文字、品牌、Logo 域名与路径登记为不得出现在进程输出中的秘密。伪 SMTP 夹具改为解析 `multipart/alternative`。
- **回归**：`ValidateBrand` 原有测试通过并补充了 C1、U+2029、非法 UTF-8；资料 Schema loader 提取为共用函数后错误文本不变；cmd 层对未知命令的安全提示测试更新为新文案。

## 审查结论

独立审查子代理（审查 27ed492..67f3257 的累计差异，并实测了 x/text v0.31.0 匹配器与 `strings.Replacer` 的单遍语义）：没有 Critical 问题；安全面（头注入、HTML 注入、单遍替换、信息泄露、`Accept-Language` 有界）、规格一致性和回归点均核实无误；Important 项全部是设计/记录层面。处理如下：

1. **低置信度匹配**：决定采用（例如只配简体时繁体请求收到简体），写入 design D6、规格「邮件语言选择」和文档，并新增 `TestLanguageMatchingAcrossScripts` 固定四种组合。
2. **黄金文件**：新增 `TestLayoutGolden`，用 `testdata/full.golden.html`、`minimal.golden.html` 固定 HTML 布局输出（`go test -update` 刷新）。
3. **design 与实现的偏离**：D6 改为"中间件包裹消费者路由"，D2 页脚描述改为实际行为。
4. 样例清单品牌改为占位（案例 / Example），`TestExampleManifest` 同步。
5. Minor：补 `TestSinglePassSubstitution`（品牌为 `{{code}}` 时保持字面）；`canonicalTag` 拒绝 `x-` 私有标签并加测试；文档说明 `{` 不能紧贴占位符；修正深度注释。接受不改：长度上限作用于模板原文（design D4 已说明）、预览目录只检查顶层、check 失败时的错误前缀与 serve 相同。

审查意见处理后重跑：`go test ./internal/accountsvc/... ./cmd/accountsvc/ ./user/sender/smtp/ ./internal/mailtext/`（Windows，无 race）通过。

## 未验证项

- 没有在 Windows 上运行 race：本机缺少 CGO 工具链，race 在 Linux 镜像中运行。
- 没有运行 `scripts/check-migrations.sh` 和恢复演练：本次变更不涉及迁移、SQL 或数据结构，这两项留到发布前的完整验证。
- 没有向真实邮件服务商投递，也没有在真实邮件客户端里查看版式；只用伪 SMTP 验证了 MIME 结构和内容。建议 ai-food 用 `email-templates check -out` 预览后再用 Mailpit 或真实邮箱确认显示效果。
- 线上 CI 尚未运行，分支还未推送。
