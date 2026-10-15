# 验证码发送器与 SMTP 渲染 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 保留两个验证码渠道接口，以精确命名和可注入 SMTP Renderer 支持宿主文案及本地化。

**Architecture:** 核心负责轮次和投递结果，sender 包提供类型兼容别名。SMTP 可选包隔离 Renderer、兼容中文内容和 MIME/网络传输；accountsvc 显式装配，示例展示宿主语言上下文。

**Tech Stack:** Go 1.26.5、Go 标准库 MIME/SMTP、miniredis，真实 PostgreSQL 17/Redis 验证既有失败边界。

**Spec:** [design.md](../../../openspec/changes/refine-verification-code-delivery/design.md)、[spec.md](../../../openspec/changes/refine-verification-code-delivery/specs/verification-delivery/spec.md)。

## Global Constraints

- 保留 SendSMS / SendEmail 方法、现有消息字段及 Deps.SMSSender / Deps.EmailSender 字段名。
- 旧类型名称通过 Deprecated 类型别名兼容。
- 核心消息不增加 Locale、模板 ID 或服务商参数。
- 不合并两个渠道为 CodeDeliverer，不增加通知、审计订阅、消息队列或重试机制。
- 保留现有 Config 结构和 New(Config) 签名。
- Subject 与 Text 必须非空白，HTML 可为空。
- 渲染失败沿用 sender.ErrUnavailable；不回退默认文案，不退额度。
- 修改限定本任务，禁止手改 sqlc 结果、源基线和迁移；GOWORK=off，Go 文件 gofmt/LF。

## Review Focus

- 包装 nil 的 Renderer 不能通过构造校验：Task 2 的 nil 指针和 nil 命名函数类型用例。
- 渲染后的已取消 context 不得继续连接 SMTP：Task 2 零连接与共享预算用例。
- 旧消息别名必须保持方法集兼容：Task 1 的外部包编译和跨渠道不满足断言。
- 同一实例并发语言提示不串扰：Task 2 context 传递与 Task 3 示例并发用例。
- 渲染失败清理不能删除新轮次：Task 2 用真实 SMTP Renderer 触发服务层失败，与已有轮次竞争回归联合验证。

---

### Task 1: 精确类型名称和旧宿主源码兼容

**Files:**
- Modify: user/sender/sender.go、user/sender/disabled.go、user/sender/log.go
- Modify: accountkit.go、user/service.go、user/service_signin.go、internal/accountsvc/run.go
- Modify: 当前使用旧类型的测试引用：accountkit_db_test.go、code_key_rotation_test.go、user/service_test.go、user/service_import_test.go、user/service_challenge_test.go、user/identity_policy_service_test.go、user/sender/{log,disabled}_test.go、user/sender/smtp/{smtp.go,smtp_test.go}、internal/accountsvc/http_test.go
- Preserve legacy coverage: user/service_delivery_test.go 中 legacyDelivery 使用 sender.Message。
- Create: sender_compatibility_test.go（外部 accountkit_test 包）

**Interfaces:**
- Produces: sender.CodeMessage{Purpose enum.CodePurpose, Code string, TTL time.Duration}。
- Produces: sender.SMSCodeSender.SendSMS(context.Context,string,CodeMessage) error、sender.EmailCodeSender.SendEmail(context.Context,string,CodeMessage) error。
- Produces: SMSSender = SMSCodeSender、EmailSender = EmailCodeSender、Message = CodeMessage，带 Deprecated 注释；Deps 字段名不变。
- Consumes: 现有相同签名和渠道可用性检查，不更改其语义。

- [x] 写 TestCodeSenderCompatibility：旧 Message 签名的实现满足新旧接口；新旧消息赋值保持 Purpose/Code/TTL；实际调用分别收到原目标和消息；只实现 SMS 的对象不满足 EmailCodeSender；Deps 原字段可注入。
- [x] GOWORK=off go test -count=1 . -run '^TestCodeSenderCompatibility$'，记录新增正式名称缺失的 RED。
- [x] 定义正式类型及别名，迁移上述当前引用（仅名称），保留历史材料和兼容用例原名称；gofmt。
- [x] 运行上述测试及 go test -count=1 ./user/sender/... ./user ./internal/accountsvc；全套 go test -count=1 ./... 在提交前运行一次，明确数据库 skip。
- [x] 自查差异，提交 refactor(sender): clarify verification code sender types；报告 RED/GREEN 与文件列表到任务报告。控制器随后独立审查。

### Task 2: 可注入 SMTP 渲染、MIME 和失败语义

**Files:**
- Modify: user/sender/smtp/smtp.go、smtp_test.go
- Create: user/sender/smtp/renderer.go、legacy_renderer.go、message.go、renderer_test.go
- Create: user/service_renderer_test.go；必要时在 accountkit 层补真实 HTTP 集成文件，限制为验证 SMTP 渲染失败契约
- Modify: scripts/required-tests.txt（仅新增应纳入门禁的必需顶层测试）

**Interfaces:**
- Consumes: Task 1 的 CodeMessage 和 EmailCodeSender。
- Produces: smtp.EmailContent{Subject,Text,HTML string}、Renderer.Render(context.Context,sender.CodeMessage)(EmailContent,error)。
- Produces: smtp.NewWithRenderer(Config,Renderer)(*Sender,error)、LegacyChineseRenderer.Render 同签名；旧 New 内部选用兼容渲染器，Config 无新字段。

- [x] 先给旧 New 补四用途解码内容/TTL 向上取整断言，再给新构造与 Renderer 写测试，运行 go test -count=1 ./user/sender/smtp 验证新增 API 缺失的 RED。
- [x] renderer_test.go 覆盖构造无渲染/无连接、nil 接口/指针/函数拒绝、自定义文案与不附加中文、非法地址/Purpose/Code/TTL 不调用 Renderer。
- [x] 提取 LegacyChineseRenderer，保持四用途中文原文；Sender 存储 Renderer，NewWithRenderer 共享原连接配置校验。
- [x] 增加 MIME 用例：Unicode 标题；纯文本；Text+HTML 的 multipart/alternative、纯文本先于 HTML；标题 CR/LF 与空白 Subject/Text 被拒绝且零连接。在 message.go 使用标准库编码。
- [x] 增加 context/错误用例：请求值透传、并发语言不串扰、预取消/合作式渲染超时/渲染返回时取消均零连接；总预算从 Render 前开始；原敏感错误被转换为固定 ErrUnavailable，不包装、不记录、不回退、不重试。
- [x] 在 user/service_renderer_test.go 使用真正 smtp.NewWithRenderer 和失败 Renderer，验证失败轮次、冷却、目标/IP 额度及脱敏审计。覆盖四用途（补全绑定有效 flow 与截止时间）及失败清理与新轮次竞争。HTTP 503 使用现有错误映射回归并补能连接实际渲染失败的适当用例。
- [x] 运行 SMTP 全包（包含原 TLS/STARTTLS/AUTH/取消/DATA 后 QUIT 行为）、user/httpapi 相关测试与 vet；全套单元测试在提交前运行一次。真实依赖和 race 由控制器统一执行，报告不能把 skip 当通过。
- [x] 自查、提交 feat(sender): support host-rendered verification emails，记录 RED/GREEN、测试结果、所需集成命令到任务报告；控制器独立审查后进入 Task 3。

### Task 3: 宿主装配、本地化示例和接入文档

**Files:**
- Modify: internal/accountsvc/run.go 及适当装配测试
- Create: examples/codeemail/renderer.go、renderer_test.go、example_test.go
- Modify: README.md、docs/compatibility.md、docs/accountsvc.md
- Modify: scripts/required-tests.txt（将新必需顶层示例测试纳入门禁）

**Interfaces:**
- Consumes: Task 2 的 NewWithRenderer、LegacyChineseRenderer、Renderer、EmailContent。
- Produces: 示例 codeemail.New(defaultLanguage string) *Renderer、WithLanguage(context.Context,string) context.Context、(*Renderer).Render(context.Context,sender.CodeMessage)(smtp.EmailContent,error)。
- 示例私有类型化 context 键；New 不支持的默认语言选择 zh-CN；请求提示支持 zh-CN/en，其余回退到实例默认。配置不可变，标准库 text/template/html/template 分别渲染 Text/HTML。

- [x] 先写四用途中文/英文、固定配置、缺省/未知语言回退、并发 context 隔离、HTML 与 Text 的验证码、用途和有效期一致且非数字 Code 被拒绝、非法输入固定安全错误的示例测试；首次运行记录 RED。
- [x] 实现纯本地 Renderer 与示例装配，不打印验证码、不发送网络请求、不读取真实环境凭据；示例不增加核心语言 helper。
- [x] 将 accountsvc EMAIL 启用路径改为 NewWithRenderer(cfg.SMTP, smtp.LegacyChineseRenderer{})；用现有服务装配/集成测试证明原中文、EMAIL 关闭、migrate 无新依赖。
- [x] 文档给出新旧名称映射、旧 New 保持原中文、新入口、宿主语言选择/并发/取消责任、服务商托管模板直接适配方式，链接示例；无新环境变量。
- [x] 运行 go test -count=1 ./examples/codeemail ./internal/accountsvc、go vet 对应包和全套单元测试一次；自查所有链接及输出不泄露合成验证码。
- [x] 提交 feat(sender): document localized verification delivery integration；报告测试与 RED/GREEN，控制器独立审查。

## Controller Verification and Completion

- [x] 按任务依赖顺序执行，每步审查通过后更新 OpenSpec tasks；保持原未完成项不虚勾。
- [x] 在一次性 PG/Redis 环境运行 GOWORK=off build/vet/test 和 Linux race，执行真实服务邮件流程；条件允许执行仓库完整 verify.sh/verify-accountsvc.sh 和生成结果检查，保存原始输出于 .test-output。
- [x] 运行全分支独立审查，处理实质问题并定向复验。
- [x] 在本变更 verification.md 记录全部当前证据和未验证项；openspec validate --all --strict、git diff --check 通过后交付。本轮用户明确要求的合并是提案合并，实现完成后保留实现分支供审阅，不推送、不归档。
