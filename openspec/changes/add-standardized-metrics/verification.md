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
