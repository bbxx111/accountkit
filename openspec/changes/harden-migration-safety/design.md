## Context

依赖 extract-auth-server 的提取结果。源 migrations.Up 使用 golang-migrate，仅忽略 ErrNoChange；版本表记录当前 version/dirty。现有 Down 调用 m.Down()，会删除全部认证表。源代码在迁移器锁之前创建 schema，首次并发初始化须专门验证。生产 SQL 当前只有 0001。

## Goals / Non-Goals

**Goals:** 覆盖旧数据升级、失败阻断、并发初始化和备份恢复；默认阻止误清库，保持认证功能完整。
**Non-Goals:** 不承诺任意 DDL 自动回滚，不实现生产通用降级 CLI，不新增运行时 checksum 或完整历史表，不为测试发布无业务意义的 0002。

## Decisions

1. **保留现有迁移引擎。** 在 migrations/migrations.go 内分离仅内部可用的迁移源注入入口，生产仍只使用嵌入 SQL；测试使用 testdata 中的成功、失败下一版本。这比替换引擎更容易验证现有版本兼容，内部接口不得导出为任意 SQL 执行器。
2. **默认拒绝 Down，显式暴露危险操作。** 原 Down(ctx context.Context, connCfg *pgx.ConnConfig, schema string) error 签名保留，直接返回 ErrDestructiveOperation，不连接数据库。新增 UnsafeResetOptions{ConfirmSchema string} 以及 UnsafeReset(ctx context.Context, connCfg *pgx.ConnConfig, schema string, opts UnsafeResetOptions) error；先验证合法 schema 和完全一致确认，再执行原全量 Down。函数名和文档明确仅开发测试清库。该确认防误操作，不声称能识别或阻止拥有数据库权限的人蓄意操作生产。测试清理改用显式入口；普通 Start/Migrate 不引用它。
3. **失败不自动 Force。** 复用引擎 dirty 阻断和错误链，报告版本和 schema（不记录 DSN 或密钥）。不假定全部失败 SQL 自动回滚。恢复手册要求检查实际结构、选择前向修复或备份恢复；只在人工核验结构与目标一致后讨论外部工具修复版本标记。
4. **并发覆盖启动阶段。** 两个独立连接对空 schema 和已有 schema 同时迁移。若引擎锁未覆盖 schema/版本表初始化，则增加外层 PostgreSQL session advisory lock，从首次创建到迁移完成覆盖整个生命周期；使用数据库和 schema 派生、与引擎内锁不同的命名空间，专用连接持锁并在取消或错误后释放。现有引擎内锁保留；在专用单连接建立后、任何 DDL 前，用可取消且最长 15 秒的等待先持有同一引擎锁，后续引擎在同一 session 重入，连接关闭时释放。避免旧工具持锁绕过外层等待限制，也不引入跨 schema 串行瓶颈。
5. **脚本冻结由发布检查完成。** tests/testdata/source-baseline 保存固定源 SQL 及出处；发布脚本比较所有可达正式 tag（包括合并分支和 HEAD）内 migrations/*.sql，禁止修改/删除历史文件，只接受递增追加。首次无 tag 则比较固定源基线。不新增运行时 checksum 表；禁止通过同时改写测试基线来规避检查，评审核对源提交。
6. **恢复演练使用独立数据库。** scripts/verify-recovery.sh 要求明确传入互不相同的测试源库与恢复库 DSN，运行 pg_dump/pg_restore 并核对版本和数据；恢复库必须是专用空库，脚本不删除或覆盖已有库。数据库恢复并不还原 Redis 和密钥管理系统，手册说明时间点一致性、撤销缓存和宽限缓存过期、必要时撤销会话及重新登录的选择，禁止宣称无损回退。
7. **发布门禁。** scripts/verify.sh 执行 go test -race -json ./... 并验证必需集成测试出现 pass 而非 skip。本地无 DB 的快速单元测试仍可保留原 skip 习惯，发布路径不得依赖它。

## Risks / Trade-offs

- [Down 语义变化影响调用方测试清理] → 保留签名但明确标注 BREAKING，提供 UnsafeReset 迁移示例；常规认证能力不受影响。
- [备份恢复会丢失备份之后写入] → 手册要求产品方确定维护窗口、备份时点和恢复目标；包内演练不能代替生产恢复验收。
- [当前无真实 0002] → 同时验证源库接管及测试专用下一版本，未来实际新迁移必须补相应旧数据升级测试。
- [CI 假绿] → 缺少 PostgreSQL、必需测试 skip、恢复工具失败均返回非零状态。
- [迁移超过锁等待或被取消] → 并发测试检查明确错误与锁释放，不把等待失败包装成已升级成功。

## Migration Plan

在独立包提取并通过兼容测试后实施。先添加失败/升级/并发测试，再实现安全入口和必要锁修复，然后冻结基线、运行备份恢复演练并填写发布检查记录。首次生产发布前两个变更所有必需任务均须完成。应用回退必须先核实旧代码与当前数据库兼容；不兼容时选择前向修复或已演练的备份恢复，不调用 Down。