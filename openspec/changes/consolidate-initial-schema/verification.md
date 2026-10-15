## Scope

2026-10-15，用户确认尚无实际产品接入并要求将数据库定义整合为一个初始 SQL。本次改动在 `refactor/consolidate-initial-schema`，以 `511ac6e` 为起点；完整验证对象是当前未提交工作区，实际源码摘要保存在 [verified-source-checksums.json](../../../.test-output/consolidate-initial/verified-source-checksums.json)。未提交、合并、推送、归档或发布。

生产 SQL 现为一对 `0001_init.up.sql` / `.down.sql`，直接定义五个业务表及全部最终字段、索引和约束，初始化 version=1。独立 `0002_identity_flow` 已撤回，测试下一版本恢复为 0002/0003；audit_event.flow_id 列顺序保持。sqlc 重新生成后无生成文件差异。

原 source-baseline 原始字节和 manifest 未改；v0.1.0、v0.2.0 精确提交仍为 `48603d6d349c57910328d393d5e17decec934f11`、`e19af8542f099f14103627140c0fe23f14c99b7e`。迁移历史检查独立固定新初始 SQL 哈希，并核验全部可达正式标签与后续递增增量。

旧基线在双锁内、任何 DDL 前明确拒绝；可用 errors.Is 识别 `migrations.ErrIncompatibleBaseline`，不修改原数据或版本。dirty 仍优先返回原引擎错误；UnsafeReset 同样拒绝旧基线，只有人工核验后的开发资源重建适用。新基线重复迁移、测试增量升级、锁等待/释放、默认 Down 拒绝保持。

## Verification

使用本次新建的 PostgreSQL 17.11、Redis、三个独立测试数据库和 Linux Go 1.26.5 / CGO 环境；数据均为合成数据。

| 检查 | 实际结果与证据 |
|---|---|
| SQL/运行迁移 TDD | 原两段迁移的版本、旧库接受行为先 RED；完整迁移包 20 个顶层测试 GREEN、无 skip；[RED](../../../.test-output/consolidate-initial/task1-red.log)、[GREEN](../../../.test-output/consolidate-initial/task1-green.log) |
| 真实结构等价 | PG catalog 对比原 511ac6e 两段最终结构与新初始结构，列顺序、类型、NULL、默认值、collation、约束及索引一致；包含在迁移 GREEN |
| 历史门禁 TDD | 新基线及首发布版本下界分别 RED，再 GREEN；11 个顶层测试通过，覆盖精确旧标签、篡改、删除、HEAD/合并发布和浅克隆；[初始 RED](../../../.test-output/consolidate-initial/task-2-red.log)、[下界 RED](../../../.test-output/consolidate-initial/task-2-increment-red.log)、[GREEN](../../../.test-output/consolidate-initial/task-2-green.log)、[真实仓库历史脚本](../../../.test-output/consolidate-initial/task-2-history.log) |
| 调用方回归 | 默认版本要求先 RED；历史数据/令牌格式、默认 schema、宿主与实例隔离真实专项通过，相关 vet 通过；[RED](../../../.test-output/consolidate-initial/caller-red.log)、[GREEN](../../../.test-output/consolidate-initial/caller-green.log) |
| 完整 scripts/verify.sh | 脚本与容器均退出 0；GOWORK=off build、vet、race、历史门禁及 PG17 恢复全部通过；[主日志](../../../.test-output/consolidate-initial/full/verify.log) |
| race 与必需测试 | 30 个测试包、1378 个测试/子测试通过，0 fail；450 项必需测试全部实际通过；[JSON](../../../.test-output/consolidate-initial/full/tests.jsonl)、[汇总](../../../.test-output/consolidate-initial/full/summary.json) |
| PG17 备份恢复 | 独立空库 seed / verify / source-unchanged 三阶段通过，包含五类记录、旧密钥、完整完成 pair、缺 Redis 证明拒绝与源库保持；[恢复证据](../../../.test-output/consolidate-initial/full/recovery.jpBfuj/verify.jsonl) |
| sqlc 与生成检查 | sqlc generate、check-generated.sh 退出 0，user/db 和 audit/db 无差异；[生成检查](../../../.test-output/consolidate-initial/generated.log) |
| OpenSpec | validate --all --strict 18/18 通过；[严格校验](../../../.test-output/consolidate-initial/openspec-final.json) |
| 文档与差异 | 相关路径/锚点检查及 git diff --check 通过；[文档与源码一致性](../../../.test-output/consolidate-initial/docs-and-source.log) |

普通 suite 唯一 skip 是按契约另行执行的 TestRecoveryFixture；恢复脚本已强制运行三阶段，没有用 skip 代替数据库或服务验证。原 TestSourceDatabaseTakeover 一对一更名为 TestHistoricalFormatsOnInitialSchema，保留数据、密文、JWT/refresh、摘要命中和无重复账号断言；新增 3 项迁移和 6 项历史必需测试，总数由 441 增至 450。verify.sh、恢复脚本和 CI 门禁未放宽。

## Review

迁移实现、历史检查分别完成独立规格/代码审查，整分支与调用夹具再由新的审查者检查，无未处理 Critical / Important / Minor。审查发现的恢复文档表数量遗漏已修正并限定复核。迁移实施报告列出的 vet 日志最初未落盘，协调者补跑并保存实际退出 0 证据；完整 verify 也包含全仓库 vet。

实施、审查及执行台账保存在 [review-evidence.zip](../../../.test-output/consolidate-initial/review-evidence.zip)。[实施计划](../../../docs/superpowers/plans/2026-10-15-consolidate-initial-schema.md) 与 OpenSpec 任务保持对应。

## Boundaries And Resources

本次只整合定义及其校验，不操作任何已有数据库，不提供历史 schema 数据迁移。新初始化成功不表示旧开发库可以直接接管；需保留数据时须另行安排迁移。真实微信/Apple 联调仍按前一变更记录保留未验证，本次没有新增或声称完成外部服务商验收，也不代表在线 CI 或产品部署已通过。

仅本任务创建的测试容器、缓存卷和网络已回收，共享验证镜像保留；[回收记录](../../../.test-output/consolidate-initial/cleanup.log)、[环境记录](../../../.test-output/consolidate-initial/environment.txt)。原始 JSON、备份及日志留在 ignored .test-output/consolidate-initial；临时执行目录归档后单独清理，不包含源码或用户数据删除。
