## 规划工件检查

本 Change 创建于 accountkit 本地仓库，分支 docs/host-account-contracts；仅创建和整理规划工件，不表示库功能已经实施。

- proposal、design、六项 specs 增量及 tasks 已创建，全部实施任务保持未勾选。
- 详细实施计划位于 docs/superpowers/plans/2026-10-08-host-account-contracts.md，引用本 Change。
- openspec status 显示 4/4 规划工件齐备；openspec validate add-host-account-contracts --strict 通过；openspec validate --all --strict 为 13 passed、0 failed，原生退出码 0。

## 实施验收

尚未实施，因此没有本轮 Go 构建、单元/真实依赖测试、并发、恢复或发布结果。执行 apply 时在本文件记录新命令、原生退出码、审查结论和未验证项；不得沿用历史发布日志作为本轮证据。

- 原库详细计划已迁入 accountkit，迁移前核对任务正文一致，再将宿主示例策略改为通用可配置表达；duopandian 移除重复库计划并引用外部 Change。

- 已读回并校验跨仓文档链接；6 项能力增量、24 项实施任务均未勾选。宿主接入计划的写入异常已按本轮计划恢复，并核对 7 项宿主任务及共享接口。
- git diff --exit-code 对 Go/SQL/go.mod/go.sum 无差异；本次没有源代码、依赖、数据库或发布变更。
