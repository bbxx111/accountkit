# 数据库迁移与恢复

包通过 migrations.Up 或 Auth.Migrate 应用未执行版本。SQL、schema_migrations 均随包管理并落入指定 schema；当前版本默认 account。迁移过程按数据库和 schema 加锁，从 schema 创建开始覆盖初始化和 SQL 执行。外层初始化锁和兼容旧工具的引擎锁，每次等待最多 15 秒，均尊重调用 context 的取消和更短期限；超时返回可重试错误，不意味着迁移已完成。取消等待不会执行 DDL；已经执行中的迁移 SQL 沿用底层引擎语义，不承诺取消会自动回滚。

Auth.Migrate 仍按 search_path 检查、Redis Ping、数据库迁移、历史身份密钥检查的顺序执行。最后的密钥检查失败不代表数据库迁移没有成功，排障时需同时检查当前版本和实际表结构。

## 当前初始基线

生产目录只有 `0001_init.up.sql` 与 `0001_init.down.sql`。新空 schema 安装后为 version=1、dirty=false，包含八张业务表（`user_account`、`identity`、`session`、`session_authentication_identity`、`session_client_info`、`audit_event`、`sign_in_completion_flow`、`purged_user`）和 `schema_migrations`。0001 down 删除这八张表，公共 `Down` 仍拒绝执行（见下文）。

0001 自 v1.0.0 tag 起字节冻结，两个文件的哈希记录在 `internal/migrationcheck/main.go` 的 `initialFrozen`，不能为了通过检查而改哈希或改文件。后续结构变更只能追加 `0002`、`0003` 等递增迁移；已发布的迁移不可修改、删除、重编号或 squash。测试专用的下一版本放在 `migrations/testdata/`（当前为 `0002_success.up.sql` 与 `0002_failure.up.sql`），测试 probe 使用 latest+1，不混入生产嵌入目录。

数据库不设外键，引用一致性由领域写路径和测试保证；因此手工修数没有外键兜底，须单独核验引用完整性。设计取舍见 [AGENTS.md](../AGENTS.md#数据库设计与实例隔离)。

## 预检规则

每次 `Up` 在持有两把锁之后、执行任何 DDL 之前运行只读预检，只有两条规则：

1. dirty 优先：`schema_migrations` 中 dirty=true 时直接返回迁移引擎的 dirty 错误，不继续升级。
2. 版本超前：记录的版本高于当前嵌入源的最高版本（例如较新的二进制已迁移过的库交给较旧的二进制接管）时，返回可由 `errors.Is` 识别的 `migrations.ErrIncompatibleBaseline`。

两种拒绝都发生在任何写入之前，保留 catalog、数据、version 与 dirty，并释放连接和锁；不 Force、不修改版本、不清库。预检不反查表结构：version=1 对应当前 0001 的结构由迁移引擎的版本记录保证。`schema_migrations` 缺失、没有记录或 version 小于 1 的库按首次安装处理；重复 Up 在已是最新版本时只忽略无新迁移。

## 升级前

1. 核对包版本、目标数据库/schema、当前 version/dirty、密钥版本、Redis 前缀及业务匿名化配置。
2. 保留能够恢复的数据库备份与对应密钥，记录备份时间点；确认应用兼容窗口与停止写入安排。
3. 在预发布环境验证：新空库安装得到 version=1/dirty=false；对已是最新版本的库重复 Up 不改变 catalog 与数据；用 latest+1 的测试迁移验证升级路径；dirty 与版本超前两类拒绝；以及多实例（不同 schema、Redis 前缀）互不影响。
4. 运行发布检查（见[历史检查与恢复演练](#历史检查与恢复演练)）。迁移只追加，禁止重新编号或编辑已发布文件。

## 诊断失败

使用明确 schema 限定名查询（示例 account，请替换为已经核验的合法 schema）：

```sql
SELECT version, dirty FROM account.schema_migrations;
SELECT tablename FROM pg_tables WHERE schemaname = 'account' ORDER BY tablename;
```

首次失败可能发生在版本表建立前，此时没有记录表；不要据此声称数据库未发生任何变化。dirty=true 时停止自动重试，保留失败 SQL/版本、日志和备份，检查实际结构及数据。包不自动 Force、不清除 dirty、不盲目重放失败版本。不要把手工将 dirty 改为 false 当成修复。

核实失败事务的实际效果，再选择前向修复或备份恢复。确有必要用外部迁移工具修复版本标记时，必须先证明实际结构与对应版本一致；只改标记不会恢复丢失的数据。

## 回退选择

- 应用回退：只有旧代码已验证兼容当前数据库结构时才可回退二进制。
- 前向修复：通过新的版本修正问题，保持历史 SQL 不变。
- 数据库恢复：先恢复到独立数据库核对版本、数据、身份解密和认证，再按产品维护流程切换；恢复会丢失备份时间点之后的写入，产品方需确定可接受的数据损失窗口。

数据库备份不包含密钥存储和 Redis。恢复时须保留对应的历史签名/HMAC/加密密钥，协调 session 数据、吊销集、刷新宽限缓存及验证码有效期；无法证明状态一致时，在产品切换流程中明确撤销受影响会话并要求重新登录，不能把清空 Redis 当成安全会话恢复。包内演练使用合成账号和独立 Redis，不代替产品生产灾备验收。

## 全量 Down 禁用与开发测试清库

migrations.Down(ctx, connCfg, schema) 始终返回 ErrDestructiveOperation，并且不访问数据库。常规生产升级与回退均不得调用它。

一次性开发测试数据库清理须显式调用：

```go
err := migrations.UnsafeReset(ctx, connCfg, schema,
    migrations.UnsafeResetOptions{ConfirmSchema: schema})
```

此操作删除目标 schema 的认证迁移对象，保留 schema 本身；确认值必须完全一致，否则在数据库 I/O 前返回 ErrDestructiveOperation。它不是权限控制，也不是生产回滚。测试清理使用该接口或测试自己持有的临时 schema 清理逻辑。

## 历史检查与恢复演练

`bash scripts/check-migrations.sh`（即 `go run ./internal/migrationcheck`）固定当前 `initialFrozen`，并核验从 HEAD 可达的全部 v1.0.0 及以上正式 `vX.Y.Z` tag（包括合并分支）：每个 tag 内的 0001 必须与当前一致，其余迁移不可改写、不可消失，新增版本号须大于初始版本，较新的 tag 不能改写较旧 tag 已发布的迁移。主版本为 0 的 tag 被忽略，既不比对也不获得豁免。浅克隆直接失败，须先获取完整历史和 tags。

`bash scripts/verify-recovery.sh` 需要 psql/pg_dump/pg_restore 17、Go 和两个明确指定的独立空测试库。脚本通过集群 system_identifier 和实际数据库名称判断两库是否相同，要求具备读取 pg_control_system 的测试权限，并拒绝非空库；不删除或覆盖既有库。流程分三阶段运行 `TestRecoveryFixture`：在源库用合成数据 seed，`pg_dump` / `pg_restore` 到目标库后 verify，最后在源库跑 source-unchanged。核验内容包括 version/dirty、账号/身份/会话/审计/流程数据快照与会话来源、客户端描述表的逐行一致，身份解密，旧 access 与 refresh 令牌，账号资料属性与 update_time，以及登录补全流程的加密载荷、期限和账号/会话引用；会话来源须为有效非空，客户端描述为单份 nested JSON（含 extensions 与原观察时间）。数据快照覆盖其中七张业务表，`purged_user` 随 `pg_dump` 一并备份恢复但不在快照比对内。普通 `TestRecoveryFixture` 预期 skip，只有脚本三阶段强制运行才是恢复演练证据。

Redis 验证码状态不在数据库备份中；恢复后的 PENDING 流程缺少验证码轮次时必须拒绝，不能仅凭数据库票据放行。COMPLETED 的相同请求只有在原期限内、账号和会话有效且 refresh 未轮换时可读取原结果；期限自然经过后应拒绝票据，仍可验证已完成会话的 refresh。演练不延长 5 分钟流程期限，也不宣称数据库备份实现零数据损失，不是生产灾备验收。

证据保留在被 Git 忽略的 `.test-output/recovery.*` 下；重跑需提供新的空库。
