# accountsvc Prometheus 验证记录

日期：2026-10-19。变更：[proposal](proposal.md)、[design](design.md)；计划：[实施计划](../../../../docs/superpowers/plans/2026-10-19-accountsvc-prometheus.md)。

## 范围与环境

任务分支 `feat/accountsvc-prometheus`，基线 `d357d51`。复用已有 Go 1.26.5 Linux 验证镜像，创建标签 `accountkit.task=svc-metrics-20261019` 的专用 PostgreSQL 17、Redis 7、runner 与网络；未使用既有业务数据库。原始日志保留在 `.test-output/`，不提交凭据或数据。

## 已执行检查

- Windows 基线 `GOWORK=off go test ./internal/accountsvc/...`：三个包通过；未提供真实依赖，集成测试跳过，不能作为集成通过证据。
- OpenSpec 工件初次 `validate --all --strict`：22 项通过。
- 集成 TDD RED：在未装配指标的基线副本加入新测试，Linux `go test -count=1 -run '^TestServiceIntegrationMetrics' ./internal/accountsvc` 退出 1；两项测试均因 `GET /metrics` 返回 404 失败，证明覆盖缺失装配。
- 文档子任务：118 个本地链接目标存在；Compose 使用合成变量 `config --quiet` 退出 0；不等于部署验收。

## 实现与审查证据

- 核心 TDD 覆盖配置缺失、路由 404、错误原文泄露、缺少 no-store 及第六个采集进入 Gatherer；逐步实现后六个顶层测试通过。实现者最终 Windows 三个服务包测试及 vet 通过。
- Linux `go test -count=1 -run Metrics ./internal/accountsvc`：退出 0，包含两项真实进程集成及六项核心测试。真实 SMTP 登录、验证码、内省的计数/耗时、实例标签、Redis 故障期间采集、禁用回退及真实 TCP 来源网关隔离均通过。
- 初次集成 GREEN 暴露测试边界错误：把 user_id 加入全局 secrets 后，合法审计资源引用触发隐私断言。修正为只在指标输出禁止该 ID，保留原审计隐私核验；未改生产代码或既有 fixture 来回避断言。
- 独立审查已阅读全部差异、新文件和锁定版本 promhttp 源码，未发现未解决的实质问题；复核了流式错误响应、gzip、并发限制、生命周期、应用隔离及测试修正。
- Linux `GOWORK=off go build ./...` 和 `go vet ./...`：均退出 0。
- 初次 Linux 全包测试的迁移用例因测试副本缺少 `.git` 历史而报 `git show` exit 128；服务及其他包没有该失败。补充完整历史后重新验证，不修改迁移、测试或门禁。
- 完整历史补齐后 `go test -count=1 ./migrations` 退出 0。
- Linux `bash scripts/verify-accountsvc.sh` 退出 0：5 包、417 个测试通过事件、0 失败、0 测试级跳过；23/23 个必需顶层测试满足。脚本原有 race、5 分钟上限、真实服务子进程及 testgate 均保持。
- 最终核心与集成新增 Go 文件已经 SHA-256 核对；服务 race 使用最终测试整理版本。最终 `go vet ./...` 退出 0。
- 最终 Linux `GOWORK=off go test -json -count=1 ./...` 退出 0：41 个包、2047 个测试通过事件、0 失败、1 个预期跳过 `TestRecoveryFixture`。后者按仓库约定由恢复脚本专门执行，本次未声称恢复演练通过。
- 最终 `openspec validate --all --strict`：22 项通过；`git diff --check` 通过。

## 收尾与结论

本次配置、装配、路由、文档、真实进程与公网代理边界已实现并通过上述验证，无未解决审查问题。实施完成时在任务分支保留改动；随后用户明确要求提交、归档并本地合并，执行记录见下节。

全部日志复制到工作区 `.test-output/accountsvc-prometheus-*`。清理前逐项核验任务标签，仅删除本任务三个容器、临时数据卷及网络，保留既有容器与编译缓存。实施过程原始报告保留在忽略目录，长期结论以本记录为准。

## 未验证项

未部署实际 Prometheus 或产品网关/防火墙，未执行生产环境验收；本地网关夹具不代替产品网络策略。未运行发布完整 `scripts/verify.sh` 恢复演练、全库 race、sqlc 重生成和独立历史迁移脚本；本次未改 SQL、schema、迁移或生成配置，已执行全库真实依赖测试及服务专用完整 race 门禁。

## 提交与归档核验

- 实现提交：`85f357e`（`feat(accountsvc): add opt-in Prometheus metrics`）。
- 提交前重新执行 Windows `GOWORK=off go build ./...`、服务包 vet 和 `go test -count=1 ./internal/accountsvc/...`，均退出 0；未配置真实依赖，不能据此替代上文 Linux 集成和 race 证据。
- 归档前核对 5/5 任务完成；新增 `accountsvc-metrics` 主规格并同步 `metrics-adapters` 的 accountsvc 场景，逐一比对增量要求与主规格完整一致；主规格严格校验 22 项通过。
- 已移动至 `openspec/changes/archive/2026-10-19-add-accountsvc-prometheus/`，保留元数据和验证记录，修复计划、服务手册及本记录的相对链接。未推送远端。
