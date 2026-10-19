# 标准化指标提案验证

日期：2026-10-19。基准：develop 的 4956edc；分支：docs/standardized-metrics-proposal。

## 本次范围

用户确认“核心观测接口 + OTel／原生 Prometheus 两个适配器，宿主启动时选择”，并同意继续细化后的范围。本轮按 propose 工作流创建 proposal、design、两份新能力规格和 tasks；没有执行 apply，没有实现指标、修改依赖或创建实际示例。

两个新能力为 standardized-metrics 与 metrics-adapters。规划工件完成不等于功能完成；22 项实施任务均未勾选。

## 实际检查

使用仓库忽略目录 .test-output/contract-tools 中的 OpenSpec 1.11.0 CLI，以 node 调用其 bin/openspec.js。

| 检查 | 结果 |
|---|---|
| status --change add-standardized-metrics | proposal、specs、design、tasks 共 4/4 类工件存在 |
| validate --all --strict | 20 项通过、0 失败：19 个已有主规格及本次 change |
| list --json | 本次 change 为 0/22 任务完成，未把规划完成报告为实施完成 |
| 指标和场景检查 | 两份规格合计 15 条要求、34 个场景，13 个指标的两种名称各自唯一 |
| 文档引用与格式 | 本次工件和三份恢复记录的 21 个本地链接目标存在；无模板占位、连续问号损坏文本、Unicode replacement character 或尾随空白 |
| 历史记录保护 | 三份归档记录在损坏段落之前的正文与 HEAD 一致，按工作树 CRLF/LF 转换规范化比较 |
| git diff --check | 通过；新增未跟踪工件另经上述格式检查覆盖 |

内容检查脚本保存于被忽略的 .test-output/check-metrics-proposal.cjs，不作为产品代码或运行时测试。

## 自查结论与修订

- 将指标名称、类型、单位和标签的唯一对照表放入 metrics-adapters 规格，design 引用该表，避免归档后公共契约缺失或重复维护。
- 区分认证成功、pending、受限授权、业务拒绝和设施故障；只在公开领域操作正常返回时记录终态，不把原业务 panic 当作成功。
- 依据现有 Refresh 代码明确：宽限写失败的依赖计数可立即记录，但成功降级必须等待事务提交，回滚不能算成功轮换。
- 明确发码前置拒绝、所有实际验码入口、审计阶段增量、Runner 与直接维护调用去重、部分处理数量及旧别名边界。
- 明确两个适配器的默认桶等价条件、宿主覆盖聚合的边界、Prometheus 注册回滚与 OTel 实例标识的宿主责任。

以上为本轮自行复核，未冒充独立代码审查；实施阶段的步骤审查、集成和性能验证列在 tasks 中。

## 此前归档记录的中文恢复

在最后移动指标名称表时，临时 PowerShell 管道脚本无法匹配中文标记。检查发现当前 PowerShell 的 OutputEncoding 为 us-ascii；此前归档和合并时，通过相同管道写入的三个 verification.md 新增中文段落已变成问号。脚本在匹配失败后尚未写文件。

本轮使用 UTF-8 文件脚本和原会话操作证据，恢复 document-go-integration-and-http-contracts、rename-sign-in-completion-flow、refine-verification-code-delivery 三份归档验收记录中的新增段落，并补记恢复原因。恢复只涉及当时新增的归档／合并说明，之前的实施记录保持；不修改 Git 历史、迁移 SQL、固定源快照或历史测试结果。

## 未验证与交付边界

本轮只完成规划和上述文档恢复，未执行 Go 构建、运行时测试、数据库／Redis 集成、race、性能基准、恢复演练、真实指标后端或在线 CI。两种适配器尚未实现，未宣称已具有等价输出或性能保证。未提交、合并、推送、发布或归档本次变更。

## 后续实施阶段（2026-10-19，已完成）

用户进一步要求“commit, merge back, and apply”。规划与中文记录恢复提交为 d674796，并以独立 merge commit 48a633b 合入本地 develop；未推送。随后在忽略目录中的隔离工作树创建 feat/standardized-metrics，详细计划为 docs/superpowers/plans/2026-10-19-standardized-metrics.md，实施计划提交 e6e7551。本节记录后续实际动作，前文规划阶段边界保留为当时状态。

当前已实现并完成阶段审查的内容：

- 核心事件、共享投影与安全记录：6a2c326，独立审查通过；全库快速测试通过，真实依赖 skip 不计入集成证据；禁用 Recorder 为零额外分配。
- OTel 1.38.0／Prometheus client_golang 1.23.2 适配器：3f3fc6d，独立审查通过；测试加固 c726e1e 已定向复审通过，强制要求目标直方图序列存在。
- 审计阶段与 Apple 缓存回退：258b4c2，真实 PostgreSQL 包测试、vet 和独立审查通过。
- Runner／观测归属标记：08c8740，真实 PostgreSQL 锁测试与阶段审查通过；领域维护数量及根装配尚待整合。
- 认证与验证码：7a520fa，Windows 真实 PostgreSQL／Redis 全库测试 1996 个通过事件、17 个预期环境／恢复用例 skip、0 失败；50 个定向事件通过且无 skip；独立审查进行中。
- 合成示例、指南和基准：5782386，阶段审查通过；完整行匹配的测试加固及根门面注入片段待完成。

### 已执行的 Linux 检查

使用本任务创建的隔离网络、PostgreSQL 17.5、Redis 7 和原生文件系统中的完整历史 clone。验证镜像包含 Go 1.26.5、CGO/gcc、PostgreSQL 17.11 客户端，sqlc 为固定 1.31.1。普通测试库和两个初始为空且互不相同的恢复库均属本任务，未修改既有 postgres／redis 容器。所有 Go 命令 GOWORK=off。

| 精确快照 | 命令 | 实际结果 |
|---|---|---|
| 6a2c326 | go test -race -count=1 ./observability/... ./internal/obstest | 通过 |
| 6a2c326 | bash scripts/check-generated.sh；bash scripts/check-migrations.sh | 通过，无生成或历史迁移差异 |
| 258b4c2 | go test -race -count=1 ./audit/... ./user/idp | 带真实 PostgreSQL／Redis 环境通过 |
| 3f3fc6d | go test -race -count=1 ./observability/... | 两种适配器与真实本地聚合器通过 |
| 08c8740 | go test -race -count=1 ./maintenance ./internal/maintenancectx | 带真实 PostgreSQL 环境通过 |
| 7a520fa | go test -race -count=1 ./user/... | user 56.861s、code 13.230s、idp 3.588s、sender 1.010s、smtp 5.401s，全部通过 |
| 5782386 | go test -race -count=1 ./examples/metricsotel ./examples/metricsprometheus ./observability/... | 通过，原始日志保存在 .test-output/standardized-metrics-linux/examples-race.log |
| 5782386 | go test -run '^$' -bench '^Benchmark(RecorderDisabled\|MetricRecording)$' -benchmem -benchtime=500ms -count=1 ./observability | 通过，原始数据保存在 .test-output/standardized-metrics-linux/benchmarks-linux.txt |

Linux amd64 基准运行于 Intel Core Ultra 7 155H：实际禁用 Recorder 2.709 ns/op、0 B/op、0 allocs/op；外层禁用快路径亦为零分配和零 goroutine 增量。OTel 串行 3198 ns/op、1856 B/op、11 allocs/op，Prometheus 串行 677.7 ns/op、528 B/op、5 allocs/op；并发数据见原始日志。结果描述单个合成观测的本地记录开销，不能代替真实认证链路性能或跨机器保证。

一次 Linux 示例／基准命令因 PowerShell 到 Bash 的引号解析在测试前失败；改用 UTF-8 脚本后通过。Windows 部分构建曾有外部缓存写权限警告，干净 Linux 快照的最终构建仍待完整门禁，未把该警告或 skip 隐藏为完整发布验证。

### 实施中的明确解释

- Apple 指标表示最近一次刷新已失败后，实际命中旧缓存密钥的观测，直到成功刷新恢复；不新增拉取，不声称最终认证必然成功。
- 维护去重须同时匹配任务和共享且启用的安全 Recorder 所有者，未观测的 Runner 不得吞掉领域服务指标。
- 低层既有 ErrUnavailable 包装若丢失 context 原因，则按可确认依赖故障记录，不根据返回时 ctx.Err 猜测取消原因。
- 已确认的宽限读取故障按 Auth dependency_error/grace_read_error 分类，Go／HTTP 原 invalid_grant 保持；miss 和 pair 到期为正常拒绝。
- 自定义 Observer 仍须快速返回，不得同步重入持轮次锁的同一 Runner，亦不得递归等待其所在事务的锁；panic 隔离不能中断任意阻塞回调。该约束已写入指标指南。

最终全库组合验证、必需测试检查、服务门禁、备份恢复、最后审查与资源清理尚未完成，后续结果继续补记。当前未将未完成实施任务勾选，也未归档或把实现合回 develop。

## 最终组合验收（2026-10-19）

冻结验证快照为 **09064b1**。该快照包含全部运行时代码、宿主编译示例、维护测试加固、幂等整理后的依赖和 630 项必需测试清单。此后只更新本记录、任务复选框和实施计划状态，运行时及测试源码保持。

### 实际结果

| 检查 | 结果 |
|---|---|
| Windows GOWORK=off go build ./... | 退出 0 |
| Windows go vet ./... | 退出 0 |
| Windows go test -count=1 -json ./... | 41 个测试包通过，1316 个通过事件、547 个依赖／平台 skip、0 失败；此行仅为快速验证 |
| Linux bash scripts/check-generated.sh | sqlc 1.31.1，退出 0，无生成差异 |
| Linux bash scripts/verify.sh | 退出 0，包含迁移历史、独立 build、vet、全库 race、必需测试检查及真实备份恢复 |
| 全库 Linux race | 41 个测试包通过、2025 个通过事件、0 失败，630/630 个必需顶层测试通过；测试级仅 TestRecoveryFixture 常规 skip |
| 恢复演练 | 专用源库与目标库不同且最初为空；seed、verify、source-unchanged 三阶段通过，pg_dump／pg_restore 成功，保留认证数据、密文解密、access／refresh、登录补全流程和源库未改变核验 |
| Linux bash scripts/verify-accountsvc.sh | 退出 0；5 个测试包、395 个通过事件、0 测试级 skip，15/15 个服务必需测试通过；保留脚本原 5 分钟时限与真实子进程 race |
| 依赖整理 | go.mod 只固定已选版本及 direct 分类，无 replace；重复 go mod tidy 的文件哈希不变，全部必需项均可按实际包与测试名找到 |
| HTTP 文档对应 | 原生脚本确认 enduser 21、admin 11 个操作对应，引用／路径参数／主要 DTO 检查通过 |
| OpenSpec／格式／链接 | 全量严格校验 20 项通过、0 失败；工件与文档格式、链接、gofmt 和 git diff --check 在交付前再次核对 |
| 存储保护 | 相对提案合并基点 48a633b，migrations、固定源快照、user/db、audit/db 无差异 |

计数包含子测试事件；无测试文件的包不计为测试级 skip。常规套件的 TestRecoveryFixture 已由恢复脚本强制执行三阶段，不能把常规 skip 误解为恢复未测。

### 审查收口

核心、适配器、领域认证、审计／Apple、维护／根装配及示例文档均完成独立阶段审查；全分支最终审查结论为 **Approved**，无 Critical、Important 或新的 Minor。审查者没有把源码审查冒充自行执行的测试，最终门禁由协调者在冻结快照运行。

所有审查发现均已处理：完整指标描述表独立断言、authenticate 直方图必须存在、Prometheus 示例完整行匹配、维护耗时因果上界及真实 CAS 未命中计数。最后两项为 d9cb2c4 的测试增量并经限定复审；真实行锁用 pg_blocking_pids 确认，不以固定 sleep 猜测竞争次序。

### 设计解释及其影响

本轮没有遗留搁置发现；以下四项解释均已同步设计并经最终审查核对：

1. Apple 回退指失败刷新之后的实际缓存命中，不改变刷新／认证；若宿主需要“本次网络失败后回退成功”的另一种含义，须另行调整指标语义，不能直接推断登录成功。
2. 维护终态去重同时匹配任务和共享启用的 Recorder；独立观测器组合不会互相吞计数。同一逻辑采集器的手工装配应复用安全 Recorder，否则可能观察到两个独立边界，需按文档装配。
3. 低层包装已丢失 context 原因时仍记依赖故障，避免猜测取消；影响是取消专项计数可能少于实际底层取消，设施故障总量仍保留。
4. 已知宽限读取故障按 dependency_error 分类，虽然公开仍为 invalid_grant；可用率与错误原因查询据运行事实解释，不单按 HTTP 状态判断。

自定义 Observer 的同步快速返回与不重入同一 Runner／事务锁的契约已在指南明确；内置适配器只记录本地聚合，不执行网络导出。上述范围不新增 accountsvc 指标端点。

### 原始证据

- [完整库日志](../../../../.test-output/standardized-metrics-linux/library.log)、[全库测试事件](../../../../.test-output/standardized-metrics-linux/tests.jsonl)、[630 项核对汇总](../../../../.test-output/standardized-metrics-linux/library-summary.json)。
- [服务日志](../../../../.test-output/standardized-metrics-linux/accountsvc.log)、[服务测试事件](../../../../.test-output/standardized-metrics-linux/accountsvc-tests.jsonl)、[服务汇总](../../../../.test-output/standardized-metrics-linux/accountsvc-summary.json)。
- [合成恢复材料](../../../../.test-output/standardized-metrics-linux/recovery.X24t9y/)、[Linux 环境](../../../../.test-output/standardized-metrics-linux/environment.txt)、[精确快照](../../../../.test-output/standardized-metrics-linux/verified-commit.txt)。
- [Linux 基准](../../../../.test-output/standardized-metrics-linux/benchmarks-linux.txt)、[示例与适配器 race](../../../../.test-output/standardized-metrics-linux/examples-race.log)。
- [Windows 构建日志](../../../../.test-output/standardized-metrics-windows/build.log)、[vet 日志](../../../../.test-output/standardized-metrics-windows/vet.log)、[快速测试事件](../../../../.test-output/standardized-metrics-windows/tests.jsonl)。Windows JSON 保留 PowerShell 重定向的 UTF-16LE 原始编码，按 BOM 解码统计；Linux JSON 为 UTF-8。
- [阶段报告、TDD、审查与裁决 ledger](../../../../.test-output/standardized-metrics-sdd/)，含 task-1..7 报告、限定复审和 final-review.md。材料保留于忽略目录，不提交真实凭证或备份。

### 交付与边界

22/22 实施任务已完成；归档作为后续明确步骤。本轮仅在 feat/standardized-metrics 保存实施提交，提案已按用户要求合入本地 develop。未推送、发布、部署或将实施分支再次合回 develop。

实际采集使用本地 OTel SDK／ManualReader 和 Prometheus Registry，未验证远程 Collector／监控平台、真实外部服务商或产品部署。指标保持进程内尽力语义，不保证精确账本；最终历史存储、导出网络、访问控制和生命周期仍由宿主负责。

全部验证和证据复制完成后，逐一核验 accountkit.task 标签及缓存卷使用者，已删除本任务三个容器、隔离网络和两个构建缓存卷；既有 postgres、redis 容器未操作。48 份计划专属报告／TDD 原始输出已复制并逐文件 SHA-256 核对，随后仅移除对应 SDD 临时目录。隔离实施工作树、Windows 缓存和忽略目录中的日志／合成恢复材料保留。

Linux、Windows 和 SDD 三组证据亦复制到主工作区的同名 .test-output 子目录，以便后续明确合并或清理工作树后仍能核对原始材料；没有覆盖既有目录。

最终文档检查覆盖 9 份 Markdown 的 123 个本地链接，49 个变更 Go 文件均已 gofmt；验证快照之后仅三份计划／任务／验收 Markdown 变化，没有运行时或测试源码差异。

## 本地合并复核（2026-10-19）

用户随后明确要求“Merge back”。合并前 develop 仍为 48a633b，实施分支为 bafbabe，两处工作树均干净；相对完整 Linux 验证快照 09064b1，运行时、依赖、测试与门禁文件没有差异，仅计划／任务／验收 Markdown 更新。

本次重新执行 GOWORK=off 的全库快速测试：41 个包、1316 个通过事件、547 个依赖／平台 skip、0 失败；OpenSpec 全量严格校验 20 项通过、git diff --check 通过。真实依赖 race、630 项库必需检查、服务门禁和恢复演练沿用上文同一代码树的精确证据，本次未重新创建测试数据库或容器。

按仓库约定使用 git merge --no-ff 将 feat/standardized-metrics 合入本地 develop；合并结果需与实施分支树完全一致，并在主工作区再跑快速套件和规格检查。原始日志、合成恢复材料及工作树内辅助脚本在清理前保留到主工作区忽略目录。本次授权为本地合并，不包含推送、发布或归档。

## 归档核验（2026-10-19）

用户随后要求“归档并提交”。实施已以独立 merge commit 0dad763 合入本地 develop；本次从该干净基点创建 chore/archive-standardized-metrics 分支，归档目录为 openspec/changes/archive/2026-10-19-add-standardized-metrics。

归档前检查全部工件存在、22/22 任务完成，并将 standardized-metrics 与 metrics-adapters 的 15 条新增要求、34 个场景及 Purpose 同步到两份新主规格；同步后逐块比较一致，主规格使用单一 Requirements 标题。归档保留 .openspec.yaml 及全部七份工件，修复内部父级相对链接和四份外部文档引用，指标指南的公共契约改为指向主规格。

本次实际检查：主规格及全量严格校验均为 21 项通过、0 失败；活动变更列表为空。12 份 Markdown 的 79 个本地链接目标存在，无旧活动目录引用；七份归档文件的既有内容按工作树换行规范化并还原路径调整后与合并基点一致，既有实施／验证记录保留。git diff --check 通过。

本次仅同步规格、移动工件和维护引用，未修改 Go、依赖、SQL、固定源或生成代码，未重复运行时测试／数据库／race／恢复演练；此前证据仍按其精确快照解释。本次提交留在归档任务分支，不包含再次合并、推送或发布。

## 归档合并复核（2026-10-19）

用户随后明确要求合并归档分支。合并前 develop 为 0dad763，归档提交为 919515c，工作区干净；差异仅为文档、归档工件与两份主规格。重新执行 OpenSpec 全量严格校验，21 项通过、0 失败，沿用文档范围检查，不重跑运行时测试。按仓库约定使用独立 merge commit 合入本地 develop，合并结果与归档分支内容保持一致；未推送或发布。
