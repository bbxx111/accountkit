# 数据库运行契约修复 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** 修复已复现的隔离级别、单连接维护自锁和审计分页全量完整行处理问题。

**Architecture:** 库内部显式 RC 并保留既有数据库锁；New 无 I/O 检查池上限；审计以窄候选分页再回表，并只返回本页流程证据。任务 1/2/3 的写入文件互不重叠，可按仓库默认流程并行实施，主代理统一文档与最终验证。

**Tech Stack:** Go 1.26.5、pgx/v5 5.10.0、PostgreSQL 17、sqlc 1.31.1。

**Spec:** [OpenSpec design](../../../openspec/changes/archive/2026-10-20-harden-database-runtime/design.md)、该变更下全部 specs、proposal.md、tasks.md。

## Global Constraints

- 不更改 schema、0001、冻结源快照、历史固定校验、公开 Repo 签名和 HTTP 协议。
- 公开 `WithTx` / `WithTxRaw` 保持宿主默认隔离级别。
- `New` 无 I/O 拒绝 `MaxConns < 2`，不创建独立维护连接、不关闭宿主池。
- SQL 只改 `audit/query.sql`，生成代码仅由 `sqlc generate` 产生。
- 保留现有锁序、验证码消费、刷新宽限、授权、异步审计及隐私规则。
- 实施阶段先保留任务分支供审阅；用户后续已明确授权提交、归档及本地合并。原始日志放 `.test-output/harden-database-runtime/`，验收结论随变更归档；推送须另行授权。

## Review Focus

1. 宿主默认 RR/Serializable 下的来源和数量不变量：任务 1 真实并发测试。
2. 公开 Repo 宿主隔离契约：任务 1 保留既有 `TestRepoWithTxRawPreservesHostIsolation`。
3. New 的 no-I/O 与宿主所有权：任务 2 用禁止连接的 BeforeConnect 验证。
4. 同时间、重叠分支、跨账号共享 flow：任务 3 静态完整翻页及外部授权测试。
5. cursor/actions 不能限制证明有效性；generic 计划不能全表回表：任务 3 证明与 EXPLAIN 检查。

## Task 1: 内部领域事务

**Files:** 修改 `user/repo.go`、`user/service*.go` 的内部事务调用；新增 `user/service_isolation_test.go`；不改 audit、根包、查询或生成代码。

**Interfaces:** 消费既有 `Repo`、`pgx.TxOptions` 和 Service 接口；新增包内 RC 事务入口，不改变公开接口。

- [x] 读取相关身份、session、补全 specs 和既有并发 fixture；设计可观察调度，使解绑持用户锁、登录旧证明排队，避免仅 sleep 猜测时序。
- [x] 写 `TestServiceTransactionsUseReadCommitted`（可以子测试细分）行为测试：RR/Serializable 下解绑先提交，新设备登录失败且无新来源；两个不同目标竞争最后一个名额仅一成功；同 refresh 并发仍取同 pair；公开 Repo 仍遵循宿主设置。
- [x] 使用独立数据库执行新增测试并保存 RED，失败必须来自旧快照或序列化行为而非 fixture。
- [x] 实现包内 RC 入口，核对所有内部领域事务和重试都使用它；原始 pgx.Tx 回调保持同事务。
- [x] gofmt，运行新增测试与 `go test -count=1 ./user`，记录结果，说明 README/兼容文档需补充的内容供主代理整合。

## Task 2: 初始化容量校验

**Files:** 修改 `accountkit.go`、`accountkit_test.go`、`audit_activation_test.go`；必要时新增根包池测试，不改 user/audit 或共享文档。

**Interfaces:** 消费 `Deps.Pool.Config().MaxConns`；产出 `New` 配置错误（包含至少两连接要求，无敏感 DSN），保留错误风格。

- [x] 写 `TestNewRejectsInsufficientPoolCapacity`：最大数 1 失败，2 和默认成功；用禁止连接回调证明 New 不连接，失败后池仍归宿主管理。
- [x] 在现状运行测试保存 RED。
- [x] 在 New 的 nil Pool 校验之后加入容量检查；将既有假设单连接成功的普通初始化用例改为最小合法值 2，保留专门容量拒绝覆盖。
- [x] gofmt，运行相关根包与 accountsvc 测试；若真实维护验证使用最小池，检查取消及资源归還。报告兼容文字供主代理整合。

## Task 3: 审计有界完整记录及证据

**Files:** 修改 `audit/query.sql`、`audit/store.go`；新增 `audit/store_pagination_scale_test.go` 并扩展必要 audit 测试；`audit/db/` 仅生成。不改 user、根包、迁移或共享文档。

**Interfaces:** 保持 `Store.ListByResource(context.Context, Query) (Page,error)`、公开 Page/FlowLink/Query 类型；FlowLinks 只需覆盖当前页的流程引用，并按稳定引用去重。

- [x] 读 `audit-host-integration`、`generic-audit-events`、HTTP admin 的 FlowLink 验证与测试；建立不改变历史授权的查询设计。
- [x] 写 `TestStoreResourcePaginationAtScale` 和 `TestStoreFlowEvidenceIsPageScoped`：密集十万行候选，最终完整行 <= page_limit；大量重复证据仅为页内 flow 返回有效链接；空页不返回无关链接；证据独立于 cursor/actions；共享流程无串号。
- [x] 在现状运行新增测试保存 RED；规模断言使用 EXPLAIN 的实际行数/工作边界而非苛刻毫秒阈值。
- [x] SQL 各匹配分支按 `(occur_time,id)` 取 page_limit 个窄候选，合并去重后再取页并读取完整事件。流程归属在数据库内解析，只读取页内流程所需的去重证据。为 custom/generic 保留必要规划屏障，不能将页过滤用于历史证明。
- [x] 运行 `sqlc generate`，gofmt；执行 audit 全套、HTTP admin 审计过滤/授权/分页测试；10 万密集资源和稀疏资源分别检查 custom/generic 执行计划，保存数据/时间/行数/临时文件说明。
- [x] 报告生成差异、公开兼容性和剩余历史索引扫描边界，不声称 O(页大小) 的总查询时间。

## Task 4: 文档、审查与验证

**Files:** README.md、docs/compatibility.md、scripts/required-tests.txt、OpenSpec tasks/verification 和本计划。

**Interfaces:** 消费三个任务的最终代码、测试名称与报告，不改变其行为。

- [x] 每任务派独立审查，规格与代码质量均通过后标记完成；最终再作跨任务审查。
- [x] 同步隔离级别边界、池最低容量及审计按页证据说明，登记新增必需顶层测试。
- [x] GOWORK=off 下运行 build/vet/真实数据库全套测试，使用 Linux 镜像运行 race；核对 generated 和 migrations 检查。
- [x] 运行 `openspec validate --all --strict`，记录 verification.md（含未执行的发布恢复项，不把历史记录当当前结果）。
- [x] 检查 git diff、迁移无变化，清理仅本任务建立的容器，交付已验证的任务分支；提交、归档和本地集成按用户后续指令执行。
