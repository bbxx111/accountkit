# 验收记录

## 范围与状态

变更：`harden-database-runtime`；任务分支：`fix/database-runtime-safety`；基线：`924c4f03083f4072625c27594c77a7b89624981e`。用户已确认内部事务显式 RC、初始化拒绝单连接池、审计窄候选分页和按页证据方案。实现、最终完整验证、三项任务审查和整体审查均已完成。

## 环境与基线

- 主机 Go 1.26.5，GOWORK=off；独立 PostgreSQL 17 容器使用合成数据。
- 修改前 `go test -count=1 -json ./...`：exit 0，42 个包通过，2293 个测试/子测试通过，50 个 skip，0 fail。audit、migrations、user 的数据库用例均实际运行。
- 基线 skip 包含未配置真实 Redis、Windows 上的 Linux 专用服务集成、普通套件预期跳过的 TestRecoveryFixture；基线不代表完整发布验证。
- 原始日志与合成材料存放在被忽略的 `.test-output/harden-database-runtime/`；此前 SQL 问题复现位于 `.test-output/dba-review/`，不作为修复后通过证据。

## 实施验证

### 内部事务

- RED：`go test -count=1 -run '^TestServiceTransactionsUseReadCommitted$' -v ./user` 在旧实现下出现旧身份登录、新绑定超限及 40001；公开 Repo 的宿主隔离子场景保持通过。
- GREEN：同命令的 RR/Serializable 八个子场景通过；五次重复通过。测试调用真实 Service，以触发器/advisory gate 和 `pg_blocking_pids` 确认锁等待时序。
- `go test -count=1 ./user` 真实数据库通过（71.895s）；`go vet ./user` 通过。
- 独立审查：内部事务及重试覆盖、公开 Repo 保持默认、宿主回调同事务、导入外部事务保持；规格与质量均通过，无实质发现。

### 连接池

- RED：`TestNewRejectsInsufficientPoolCapacity` 在旧实现下单连接场景未被拒绝。
- GREEN：该测试和 `TestMigrateAuditConfigurationMinimumPoolCapacity` 的全部子场景通过；禁止连接的 BeforeConnect 证明 New/Close 无 I/O，后续宿主 Acquire 证明池未被关闭。
- `go test -count=1 . ./internal/accountsvc ./cmd/accountsvc` 及对应 vet 通过；真实数据库迁移用例已执行。
- 独立审查：规格与质量均通过，无实质发现。测试改名和新增测试已同步必需清单。

### 审计分页

- RED：旧实现页内返回 202 个流程链接，十万条完整事件进入 UNION/Sort/CTE；另一个多流程重叠用例发现分支 LIMIT 前重复占位，修复后先去重再取页。
- GREEN：`go test ./audit/... ./httpapi/admin -count=1 -v` 全部通过，无 skip；相关 vet 通过，sqlc 生成仅改变 audit/db。
- 新测试包括 `TestStoreFlowEvidenceIsPageScoped`、`TestStoreFlowPaginationDeduplicatesBeforeLimit`、`TestStoreResourcePaginationAtScale`。覆盖无效投影之后的有效证明、页范围外证明、live/历史共存、空页和重叠分支。
- PostgreSQL 17、100003 条合成事件、约 1 KiB data：两个主体分布 × custom/generic × 密集/稀疏共八组计划。密集页完整回表均为 51 行，稀疏页均为 3 行；窄候选临时写 415 个 8 KiB 块（约 3.24 MiB），稀疏页无临时写。多主体稀疏计划最大候选扫描为 3。
- 极偏单主体 generic 计划仍可能扫描 100003 条候选，这是旧实现已存在的统计选择限制；新方案完整回表仍只有 3/51 行。总扫描量不承诺常数，异常同名无效证明可能需要多轮候选查询。八组原始 JSON、CSV 汇总和旧 RED 均保留。
- 审计独立审查指出非空 owned_flows 的规模验证盲区，追加 `TestStoreHistoricalFlowPaginationAtScale` 后发现并修复第二页逐流程来源时间线扫描。具体过程和最终计划见下节。聚焦复审 Approved，无未解决项。

### 历史流程和第二页补充验证

- 夹具：201 个纯历史流程，其中目标账号 200 个流程关联十万条同发生时间的早期事件，另一个账号一流程关联三条事件；证明发生更晚，且 action 不在页面过滤范围。
- 初次真实 Store 的第二页主查询约 94 秒，证据查询约 43 毫秒。进一步固定真实连接的 custom plan 和 jit=off，快速估计计划仍复现每个 flow 走来源时间线的错误选择；保存 `task-3-flow-cursor-red.log`，未用 JIT 设置掩盖问题。
- 修复将动作/游标过滤移到精确归属资源匹配的规划屏障之外，保留授权条件和窄候选。首屏与第二页、custom 与 generic 的八份实际计划均通过；密集页每页共检查 100200 个相关候选（200 次 GIN，每次 501 条），最终完整回表 51 条；稀疏页检查 4 个候选，首/次页完整回表为 3/0。没有每个流程重复扫描全部来源时间线。
- `go test ./audit -run '^TestStore(HistoricalFlowPaginationAtScale|FlowEvidenceIsPageScoped|FlowPaginationDeduplicatesBeforeLimit|FlowClosureDoesNotLeakOtherProofOwners|ResourcePagination)$' -count=1 -timeout=2m -v` 通过；新增规模顶层测试已加入必需清单。
- 复审细化计划断言：仅豁免无 Subplan Name 的基础扫描字段展示，任何 CTE 生产者仍检查完整 data 是否被物化。该纯测试修改后，四个历史流程场景再次聚焦通过（31.814s，期间与完整验证并行）；生产代码不变。
- 独立计划、耗时和原始诊断保存在 `task-3-flow-fixed-*.json`、`task-3-flow-fixed-summary.csv`、`task-3-flow-green.log` 与 `task-3-flow-materialization-review.log`。相关历史窄候选仍可随数据量增长，时间仅为合成环境观测，不宣称恒定查询时间或严格加速倍数。

## 最终检查与未验证项

- `bash scripts/verify.sh` 在 `accountkit-verify:local` Linux 镜像中 exit 0：迁移历史检查、`go build ./...`、`go vet ./...`、`go test -race -json -count=1 ./...`、必需测试检查及恢复演练均通过。
- 第二页 SQL 修复后的最终全仓 race：42 个包通过、2402 个测试/子测试通过、0 fail；唯一测试级 skip 是普通套件预期的 `TestRecoveryFixture`（另有无测试文件的包标记 skip）。Redis 与 Linux 服务集成此次均实际执行。
- 最终恢复演练使用本任务新建、实际不同的两个空库，seed / verify / source-unchanged 三阶段通过；PostgreSQL 17.11 的 pg_dump / pg_restore 实际执行，材料位于 `.test-output/recovery.2pSFhI/`。首轮验证及恢复材料另外保留，不覆盖原记录。
- `bash scripts/check-generated.sh` exit 0；user/db 无生成差异，migrations 与 source-baseline 无修改。
- `openspec validate --all --strict`：25 项通过、0 失败。
- 三项独立审查及审计修正聚焦复审均 Approved；最终跨任务审查 Approved，确认内部 RC/公开 Repo/导入回调边界、无 I/O 池所有权、审计授权和文档一致，无未解决实质问题。
- 只删除本任务创建的 PostgreSQL 与 Redis 容器及其匿名卷；原始测试、计划和恢复材料保留在 `.test-output/`，已有产品/开发容器未操作。
- 本次为独立合成环境验证，未接入产品数据库、未评估生产实际分布或在线 CI。当前未归档、未提交、未推送、未合并。
