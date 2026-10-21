# 验收记录

日期：2026-10-21。以下为最初规划阶段记录；用户随后要求“提交，apply”，当前实施验收见本文后续章节。

## 工件与审查

- proposal、design、self-profile-http 与 accountsvc-runtime 两份 delta specs、tasks 已生成；实施任务全部保持未完成。
- 自审覆盖本人全字段访问、可选 Go 适配器兼容、账号状态、精确保真和容量边界、错误隐私、accountsvc 配置、基础 change 依赖及访问契约迁移。
- 修正请求语法错误与可解析内层语义错误的分类，分别归为 MALFORMED_BODY 与 INVALID_PROFILE_ATTRIBUTES。
- 基础 change 尚未归档；本变更明确覆盖其原“仅 Go”范围限制，历史验证记录保持。归档是后续独立动作。

## 实际检查

使用仓库既有 `.test-output/contract-tools/node_modules/@fission-ai/openspec/bin/openspec.js`，通过 node 执行，未安装或替换 CLI。

| 检查 | 结果 |
|---|---|
| openspec status --change add-self-profile-http | proposal/specs/design/tasks 4/4 齐备，仅表示规划完成 |
| openspec validate --all --strict | 26 项通过，0 失败 |
| git diff --check | 通过；新工件未跟踪，另行执行文件空白及相对链接检查 |
| 新工件空白与相对链接检查 | 6 个 Markdown 文件通过 |

未运行 Go 构建、测试、数据库集成或 race：本轮没有产品代码变更，HTTP 行为仍待实施和验证。未提交、推送或归档。

## 实施范围与审查

用户要求提交并 apply 后，规划提交为 `e06ffdb`，HTTP/服务配置实现提交为 `22e7a28`，工作分支 `feat/self-profile-http` 基于已有资料存储实现。新增 GET/PATCH `/users/me/profile`、可选适配器、保真有界补丁解析、安全错误，以及 accountsvc 的 Schema 文件装配。既有 SQL、迁移、sqlc 结果、令牌格式与依赖文件无变化。

实施按[详细计划](../../../docs/superpowers/plans/2026-10-21-self-profile-http.md)分为 HTTP、服务配置、真实集成、文档与验证。HTTP 和服务配置分别先运行新增行为测试观察缺失能力失败，再实现；真实 HTTP 集成也先在合法登录后观察 404，再验证通过。外层 null/缺字段为 MALFORMED_BODY，profile_attributes:null 按内层非对象归 INVALID_PROFILE_ATTRIBUTES；设计已明确这一区分。

独立适配审查与最终全变更审查均通过。唯一 Minor 是 README 把开发版新增接口描述为 v0.4.0 已交付，已修复并复核；无遗留问题。审查原始记录保存在 `.test-output/self-profile-http/review-adapters.md` 和 `final-review.md`。

真实测试使用本任务新建的 PostgreSQL 17 与 Redis 7 容器，合成资料与随机 schema/Redis 前缀；未访问已有产品数据库。根包集成覆盖完整资料、双账号隔离、注解可读写、大整数、Merge Patch、16 KiB 边界、无变化时间/审计、冻结/注销状态、数据库阻塞图证明的并发及锁后状态复核；服务集成覆盖文件 LoadConfig → newApplication → serviceHandler 实际挂载、启用/禁用与数据保持。原 HTTP/JWT/内省隐私回归继续通过。

## 实施检查过程

- `GOWORK=off go build ./...` 与 `go vet ./...` 通过。
- Windows `go test -json -count=1 ./...` 通过：44 个有测试包、870 个顶层测试通过；14 个顶层 skip 为 Linux 服务进程用例及 TestRecoveryFixture，不作为这些集成场景通过证据。日志：`.test-output/self-profile-http/windows-tests.jsonl`。
- 根包、user、服务包的资料属性定向测试在真实 PostgreSQL 上通过；独立审查者另外执行根包及服务新 HTTP 集成并通过。
- Linux 配置定向测试通过；root 容器中权限文件测试按预期 skip，另用 UID/GID 65534 的一次性容器运行 `TestProfileAttributesUnreadableFile` 实际通过。FIFO/设备文件、读取 EIO 与 Windows 文件关闭/独占权限场景均有执行证据。
- 首轮 Linux `go test -race -json -count=1 ./...` 的 43 个包通过，但 accountsvc 包在 600 秒总时限超时，不能记为全量通过。栈位于 `newIntegration` 的子进程 `go build`，没有断言或 race 失败；源码来自 Windows bind mount，多次构建造成较高耗时。保留原始失败日志 `.test-output/self-profile-http/linux-race.jsonl`。随后将同一源码快照复制到容器本地文件系统，以 `-timeout=20m` 重跑完整 suite 与两份必需测试门禁，不改任何测试断言或仓库默认超时。
- OpenAPI 两份文档的 Swagger Parser 校验和 `scripts/check-openapi.cjs` 通过：消费者 24、管理面 11 操作；最终文档 140 个本地路径及新设计 JSON 示例检查通过。

## 最终实施验收

完整 Linux 重跑 `GOWORK=off CGO_ENABLED=1 ACCOUNTSVC_TEST_RACE=1 go test -race -json -count=1 -timeout=20m ./...` 退出 0：44 个有测试包、884 个顶层测试通过，0 失败；accountsvc 包耗时 546.683 秒。两项顶层 skip 为普通套件预期跳过的 TestRecoveryFixture，以及 root 权限下的 TestProfileAttributesUnreadableFile；后者另以非 root 容器实际运行通过。没有省略任何功能测试来处理首轮总超时。

两份必需测试门禁均使用本次完整重跑 JSONL，退出 0：`go run ./internal/testgate scripts/required-tests.txt` 核对 783 项，`go run ./internal/testgate scripts/accountsvc-required-tests.txt` 核对 29 项。原始日志位于 `.test-output/self-profile-http/linux-native-race.jsonl`，门禁输出文件为 `required-gate.log`、`service-gate.log`（成功时无输出）；首轮失败日志单独保留。

OpenSpec 全量严格检查 26 项通过；差异空白检查、两份 OpenAPI schema 校验、路由/DTO 契约检查和相对路径检查通过。实施任务 12/12 完成，当前 change 保持未归档。代码、集成测试、文档与验证工件在任务分支提交；没有推送、合并或发布。

本次不涉及数据库结构变化，未执行完整发布脚本中的 pg_dump/pg_restore 恢复演练；也未执行生产部署、真实邮件送达或外部身份提供方联调。未推送、合并或归档。

收尾已核对任务标签并删除本次 PostgreSQL/Redis 容器及其匿名卷；验证容器以 --rm 退出，任务标签容器清单为空。既有容器未操作，测试日志保留于忽略目录。
