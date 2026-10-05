# HTTP API EndUser Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将终端用户 HTTP 包及门面直接迁移为 enduser / EndUserHandler，保持运行行为。

**Architecture:** 原目录及其引用一次迁移，不增加兼容层。领域层、HTTP 协议和可选 accountsvc 边界不变；当前文档更新，历史工件保留。

**Tech Stack:** Go 1.26.5、Chi、PostgreSQL 17、Redis；现有 OpenSpec、测试门禁和 Linux race 验证设施。

**Spec:** [proposal](../../../openspec/changes/rename-consumer-to-enduser/proposal.md)、[design](../../../openspec/changes/rename-consumer-to-enduser/design.md)、[delta](../../../openspec/changes/rename-consumer-to-enduser/specs/accountkit-package-identity/spec.md)。

## Global Constraints

- `httpapi/consumer` → `httpapi/enduser`，`Auth.ConsumerHandler() http.Handler` → `Auth.EndUserHandler() http.Handler`；旧包、别名和转发方法不保留。
- Handler、Deps、Service、IdentityReplacer、New、Router、AuthnOptions、RequireRecentAuth 保持原结构与签名；对应私有字段/参数采用 `endUser`，构造诊断前缀改为 `enduser:`。
- module、根 package accountkit、领域 user、HTTP 路由/DTO/错误/凭据/配置、SQL、迁移锁、依赖和服务行为不变。
- 原包 19 个 Go 文件整体移动，37 项必需选择器一对一迁移，不减少断言或门禁，不扩张 Service。
- 当前指南更新；历史归档、已完成计划及固定源基线保持原事实。临时宿主和本地 replace 仅放 ignored `.test-output/`。
- 在现有 `refactor/httpapi-enduser` 任务检出执行；不归档、合并或推送。产品环境验收延期。

## Review Focus

- 只消费根门面的外部宿主也必须能编译：分别运行根门面和直接包宿主构建。
- 旧接口意外残留：迁移后分别编译旧路径/旧方法，必须因入口不存在失败。
- 自定义 Service 无换绑能力：保留原接口及既有未配置能力 503 测试，通过新包测试核验。
- 测试选择器漏迁移：逐项比较规范化前后清单，并由完整 testgate 检查实际通过结果。
- 非命名改动混入：独立审查重命名 diff，验证协议、存储、依赖及历史文件没有改动。

### Task 1: 原子迁移 Go 接入与测试门禁

**Files:**
- Move: `httpapi/consumer/*.go` → `httpapi/enduser/*.go`（19 个）。
- Modify: `accountkit.go`、`accountkit_test.go`、`accountkit_db_test.go`、`code_key_rotation_test.go`、`identity_replacement_test.go`、`session_expiry_test.go`、`examples/embedded/main.go`。
- Modify: `internal/accountsvc/run.go`、`internal/accountsvc/http.go`、`internal/accountsvc/http_test.go` 及确有对应私有命名的调用测试；`scripts/required-tests.txt`。
- Create (ignored): `.test-output/enduser-rename/` 中基线、独立宿主和日志。

**Interfaces:** 消费原 `consumer.Service` 等类型；产出 `enduser.Service` 等同形类型和 `(*accountkit.Auth).EndUserHandler() http.Handler`。Task 2 据此更新文档。

- [x] 记录 19 文件、导出声明及原 37 选择器。临时外部模块 require 本库并 replace 当前检出，分别引用新根门面及 `enduser.New/Deps/Handler/Service/IdentityReplacer`；`GOWORK=off go build -mod=mod` 在改名前必须因新方法/新包不存在而失败，保存 RED 输出。
- [x] 原子移动目录、package 和外部 test package、导入限定符、门面、对应私有字段/参数；构造错误前缀同步，其他消费者语义不全局替换。
- [x] 迁移 37 选择器并逐项比对；若 `TestConsumerHandlerAndMiddlewareWiring` 改名为 `TestEndUserHandlerAndMiddlewareWiring`，记录额外映射并同步门禁。保留全部断言。
- [x] gofmt 后执行 `GOWORK=off go build ./...`、`go vet ./...`、`go test -count=1 ./httpapi/enduser . ./internal/accountsvc/... ./examples/embedded`；缺少数据库时明确只作快速验证。
- [x] 外部宿主 GREEN 构建成功；独立验证旧路径与旧方法编译失败；检查库依赖不含 accountsvc，go.mod/go.sum 不变；检查新旧文件内容规范化后仅名称差异。
- [x] 自审并只提交本任务代码/选择器；写报告（RED/GREEN、命令、结果、路径、选择器映射、未验证项），交由控制器独立审查。

### Task 2: 当前文档迁移与完整验收

**Files:**
- Modify: `README.md`、`AGENTS.md`、`docs/compatibility.md`，搜索所得其他当前接入指南如确有旧 Go 入口。
- Create (controller): `openspec/changes/rename-consumer-to-enduser/verification.md`。
- Update (controller): 本计划完成标记和 change `tasks.md`。

**Interfaces:** 消费 Task 1 的新导入/门面；产出清楚的宿主迁移表和实际验证记录，无运行接口新增。

- [x] 当前可执行示例改为新入口；兼容指南列出 import/type/门面旧新映射及源码破坏性变化，说明 HTTP/配置/数据无需迁移、回退只恢复源码依赖。
- [x] 明确兼容索引中的历史入口，分类剩余旧名为迁移说明或历史事实，不改归档、已完成计划和固定基线。检查本次文档相对链接及 diff。
- [x] 文档工作者自审、提交文档并报告；控制器安排独立任务审查。
- [x] 控制器使用本次创建的一次性 PG17/Redis 和独立空恢复源/目标库，Linux 执行 `bash scripts/verify.sh`、`bash scripts/verify-accountsvc.sh`；保留 JSONL、恢复材料及退出码，检查新包全部必需用例实际 PASS。执行生成检查与 `openspec validate --all --strict`。
- [ ] 控制器记录真实结果、RED/GREEN、接口/选择器比对、分步及最终审查结论到 verification.md；产品环境延期明确列出。全部证据满足后勾选 7 项任务，提交记录，清理仅本次测试资源。

## Controller Handoff

文档工作者只负责 Task 2 前三步，完整环境验证与验收记录由控制器负责，可与不修改 Go 的文档工作并行。最终审查覆盖完整分支和验收证据；若需修复由工作者实施并复核。
