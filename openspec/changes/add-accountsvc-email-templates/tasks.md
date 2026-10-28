## 1. 共享字符规则（沿用首版实现）

- [x] 1.1 新增 `internal/mailtext`：标题与正文的字符规则，附单元测试
- [x] 1.2 `smtp.ValidateBrand` 改为使用共享标题规则，签名与行为不变；品牌回归用例补全

## 2. 清单解析与渲染（`internal/accountsvc/emailtemplates`）

- [x] 2.1 严格解析清单：version=1；`default_language` 属于 `languages`；1–8 种规范 BCP 47 语言，不含 `und`，不重复；语言块、`common`、用途的固定字段集合；拒绝未知字段、重复键、尾随内容、null；错误只含字段位置与固定分类
- [x] 2.2 占位符 `{{brand}}` / `{{ttl_minutes}}`：单个花括号字面；`{{code}}`、未知名称或不完整的 `{{` 报错；每段最多 8 个；各字段的长度与字符规则；`logo_url` 校验
- [x] 2.3 内置布局：嵌入 HTML 与纯文本布局；Logo 与页脚槽位由 Go 代码按有无填入；HTML 槽位整体转义
- [x] 2.4 渲染：先渲染各段文字再填入布局；有效期向上取整；ctx 取消、CodeMessage 非法、零值或 nil 时返回 ErrUnavailable；并发安全
- [x] 2.5 语言匹配：`language.NewMatcher`，默认语言在前；`WithLanguagePreference` 与读取函数；无偏好或置信度为 No 时用默认语言；单语言不读 context
- [x] 2.6 摘要与语言列表：清单 SHA-256 前 12 位；按清单顺序的标签列表
- [x] 2.7 单元测试：拒绝矩阵（含哨兵断言不回显）、占位符边界、HTML 转义、Logo 有无、footer 有无、黄金文件固定布局输出、匹配表、并发渲染、样例清单

## 3. accountsvc 装配

- [x] 3.1 `LoadConfig`：只在 serve 且 EMAIL 启用时加载；与 `ACCOUNTSVC_SMTP_BRAND` 互斥；EMAIL 禁用或 migrate 时不解析
- [x] 3.2 `Accept-Language` 中间件：只在多语言时安装；超过 512 字节、解析失败或缺失时不设置；最多前 8 项
- [x] 3.3 `run.go`：二选一注入渲染器；启动 INFO 加入 custom/built-in、version、sha256、languages
- [x] 3.4 子命令 `email-templates check <清单> [-out 目录]`：不读环境变量；失败非零退出；输出目录只允许覆盖固定文件名；写出 `<lang>/<PURPOSE>.html`、`.txt`、`subjects.txt`
- [x] 3.5 配置与命令测试：未设置、品牌冲突、各类文件错误、EMAIL 禁用或 migrate 时忽略、check 成功/失败/拒绝脏目录
- [x] 3.6 集成测试：伪 SMTP 抓取 multipart/alternative；分别带 zh-HK、en-GB、ja 和不带请求头发码，断言标题、HTML、纯文本和品牌；启动日志含 custom、语言列表和摘要；进程输出不含文字、品牌或路径

## 4. 样例、文档与部署

- [x] 4.1 `deploy/accountsvc/email-templates.example.json`：简体、繁体、英文三套文字，由测试加载并渲染
- [x] 4.2 `docs/accountsvc.md`：配置表、品牌说明（互斥）、改写“邮件模板清单”一节（格式、占位符、Logo 要求、布局段落、语言匹配、check 命令）
- [x] 4.3 `docs/gateway-integration.md`（透传 `Accept-Language`）、`docs/compatibility.md`、`deploy/accountsvc/.env.example`、`compose.yaml`
- [x] 4.4 必需测试清单：新增条目，删除已被替代的首版条目

## 5. 验证

- [x] 5.1 gofmt、go vet；Linux 镜像跑 race 与 `scripts/verify-accountsvc.sh`；`go test -race ./...` 加 required-tests 门禁
- [ ] 5.2 `openspec validate --all --strict`；代码审查；改写 verification.md
