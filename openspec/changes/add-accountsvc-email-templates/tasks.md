## 1. 共享字符规则

- [x] 1.1 新增 `internal/mailtext`：实现标题与正文的字符规则和码位计数，附单元测试（含 CR/LF/TAB、U+202E、U+200B、U+2028、首尾空白、非法 UTF-8）
- [x] 1.2 `smtp.ValidateBrand` 改为使用共享标题规则，签名与行为保持不变，现有品牌测试全部通过

## 2. 模板解析与渲染（`internal/accountsvc/emailtemplates`）

- [x] 2.1 严格 JSON 解码：version=1；四种用途恰好齐全；拒绝未知字段、重复键、尾随内容、null 和非法 UTF-8；错误只含字段位置与固定分类
- [x] 2.2 占位符编译：三个名称、`{{`/`}}` 转义；拒绝单独的花括号、空名称、未知名称；text 必须含 `{code}`，subject 不得含；`{brand}` 需要非空品牌；subject 与 text 各自最多 8 个占位符；subject 不超过 64、text 不超过 4096 个码位
- [x] 2.3 `emailtemplates.Renderer.Render`：校验 ctx 与 CodeMessage；单次展开；有效期向上取整；零值返回 ErrUnavailable；并发安全
- [x] 2.4 单元测试覆盖解析与渲染矩阵，包括边界值 64/65、4096/4097、8/9，并断言错误信息不回显内容

## 3. accountsvc 装配

- [x] 3.1 把资料 Schema 的有界文件读取提取为共用函数，资料 Schema 行为与错误分类保持不变
- [x] 3.2 `LoadConfig` 仅在 serve 且 EMAIL 启用时读取 `ACCOUNTSVC_SMTP_TEMPLATES_FILE`，失败即返回只含变量名的错误；EMAIL 禁用或执行 migrate 时不解析
- [x] 3.3 `run.go` 二选一注入渲染器；监听后记录 `email templates: custom`（version 与 sha256 前 12 位）或 `built-in` 的 INFO 日志
- [x] 3.4 配置测试：未设置、EMAIL 禁用时非法路径、migrate 时非法路径、各类文件错误、品牌为空却引用 `{brand}`
- [x] 3.5 集成测试：用伪 SMTP 抓取自定义模板邮件，断言标题与正文；启动日志包含 custom、版本与摘要且不含模板原文和验证码；未配置时日志为 built-in

## 4. 示例、文档与部署

- [x] 4.1 新增 `deploy/accountsvc/email-templates.example.json`（四种用途的推荐文案，补全标题为“{brand}登录补全验证码”），并用测试直接加载和渲染该文件
- [x] 4.2 更新 `docs/accountsvc.md`：配置表、品牌说明、新增“邮件模板文件”一节
- [x] 4.3 更新 `docs/compatibility.md`、`deploy/accountsvc/.env.example`、`deploy/accountsvc/compose.yaml`（变量透传与注释掉的只读挂载示例）
- [x] 4.4 新增的必需顶层测试写入 `scripts/accountsvc-required-tests.txt`

## 5. 验证

- [x] 5.1 gofmt、go vet、相关包测试；用 Linux 镜像跑 race 与 `scripts/verify-accountsvc.sh`
- [x] 5.2 `openspec validate --all --strict`；代码审查；结果写入 verification.md
