## 1. 迁移升级与失败测试

- [x] 1.1 确认 extract-auth-server 的兼容验证已通过；在 migrations/testdata/ 新增只供测试的 0002 成功和失败迁移，并在 migrations/migrations.go 抽取内部迁移源注入入口，生产 Up 仍只能使用嵌入 SQL；验证生产发布目录仍只有真实业务迁移。
- [x] 1.2 在 migrations/upgrade_test.go 新增 TestUpgradePreservesDataAndIsRepeatable，先写断言：带用户、身份、会话、审计的版本 1 升至测试版本 2 后数据不变，第二次 Up 不重复执行；运行测试，再以最小改动修复实际发现的问题，go test ./migrations -run TestUpgradePreservesDataAndIsRepeatable -count=1 通过。
- [x] 1.3 在 migrations/failure_test.go 新增 TestFailedMigrationBlocksRetry，注入下一版本 SQL 失败，断言返回错误、dirty=true、重试被阻断且未执行后续版本；验证不自动 Force 或清除 dirty，必要修复后 go test ./migrations -run TestFailedMigrationBlocksRetry -count=1 通过。

## 2. 并发启动和清库防护

- [x] 2.1 在 migrations/concurrency_test.go 新增 TestConcurrentFreshSchema 和 TestConcurrentExistingSchema，使用独立连接同步发起 Up，先验证空 schema 创建、版本表初始化及已有 schema 的竞争行为；断言最终版本干净，SQL 不重复，其他 schema 不受影响，失败必须是明确可重试的锁等待错误。
- [x] 2.2 根据 2.1 结果修复锁覆盖不足，若需外层锁则在 migrations/lock.go 使用数据库/schema 派生的独立 session advisory lock 覆盖初始化至结束；新增 TestMigrationLockReleasedOnFailure 和 TestMigrationLockWaitCancellation，断言失败释放锁和取消等待后不继续执行 DDL，相关测试及 go test -race ./migrations 通过。
- [x] 2.3 在 migrations/reset_test.go 先新增 TestDownRefusesWithoutIO，使用不可用连接配置断言 Down 返回 errors.Is(err, ErrDestructiveOperation) 且不连接；修改 migrations/migrations.go 保留原签名并拒绝执行，验证测试由失败变为通过。
- [x] 2.4 在 migrations/reset.go 实现设计中的 UnsafeResetOptions{ConfirmSchema string}、UnsafeReset(ctx context.Context, connCfg *pgx.ConnConfig, schema string, opts UnsafeResetOptions) error；先写空确认、确认不匹配、非法 schema、确认清理单个测试实例的测试，断言前三种均无 I/O，成功只清除目标实例并保留 schema，另一实例数据不变。
- [x] 2.5 将原测试中的 Down 清理调用更新为显式 UnsafeReset，并在 docs/migrations.md 写出 BREAKING 调用迁移说明；运行完整 go test -race -count=1 ./...，确认认证行为未改变。

## 3. 冻结历史迁移与恢复演练

- [x] 3.1 实现 scripts/check-migrations.sh：有正式发布 tag 时逐个比较该 tag 的历史 SQL，无 tag 时比较固定源基线；用临时 Git fixture 验证改写、删除、重编号、非递增新增会失败，合法追加会通过，运行时不新增 checksum 数据表。
- [x] 3.2 编写 docs/migrations.md 的升级和故障恢复步骤，包含 version/dirty 检查 SQL、部分 DDL 检查、停止自动重试、应用回退条件、前向修复和备份恢复选择，以及密钥与 Redis 状态协调；逐项对照 migration-safety 规格确认全部故障场景有操作路径。
- [x] 3.3 创建 scripts/verify-recovery.sh，要求不同且经实际数据库身份核验的源库与专用空恢复库，拒绝同库或非空目标；执行 pg_dump/pg_restore，恢复后核对版本、四类记录、身份解密和可用认证行为，禁止覆盖或删除现有数据库。
- [x] 3.4 在隔离环境实际执行备份恢复演练，将命令、工具版本、成功结果和数据损失窗口说明写入 docs/recovery-verification.md；核验原测试库和其他 schema 未受影响，不用虚构结果替代演练。

## 4. 首次生产发布验收

- [x] 4.1 将迁移升级、dirty、并发、显式清库防护和恢复演练加入 scripts/verify.sh 的发布检查路径；缺少 DB/备份工具、必需测试 skip 或历史脚本被改写时验证失败返回非零。
- [x] 4.2 执行完整独立构建、race 集成测试、脚本冻结检查和恢复演练，更新 docs/release-checklist.md 对应结果；确认两个 OpenSpec 变更所有必需任务完成后才将候选版本标记为具备首次生产发布条件，实际产品部署另行执行。