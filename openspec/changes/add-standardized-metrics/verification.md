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

## 后续实施阶段（2026-10-19，进行中）

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
