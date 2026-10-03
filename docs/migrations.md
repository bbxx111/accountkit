# 数据库迁移与恢复

包通过 migrations.Up 或 Auth.Migrate 应用未执行版本。SQL、schema_migrations 均随包管理并落入指定 schema；默认 auth。迁移过程按数据库和 schema 加锁，从 schema 创建开始覆盖初始化和 SQL 执行。外层初始化锁和兼容旧工具的引擎锁，每次等待最多 15 秒，均尊重调用 context 的取消和更短期限；超时返回可重试错误，不意味着迁移已完成。取消等待不会执行 DDL；已经执行中的迁移 SQL 沿用底层引擎语义，不承诺取消会自动回滚。

Auth.Migrate 仍按 search_path 检查、Redis Ping、数据库迁移、历史身份密钥检查的顺序执行。最后的密钥检查失败不代表数据库迁移没有成功，排障时需同时检查当前版本和实际表结构。

## 升级前

1. 核对包版本、目标数据库/schema、旧版本、密钥版本、Redis 前缀及业务匿名化配置。
2. 保留能够恢复的数据库备份与对应密钥，记录备份时间点；确认应用兼容窗口与停止写入安排。
3. 在预发布环境执行旧数据升级测试。首次包发布只有原样 0001，下一版本升级路径通过仅测试用的 0002 验证；未来每个真实新迁移都要增加对应旧数据升级用例。
4. 运行发布检查，确认历史 SQL 未改写；历史迁移只追加，禁止重新编号或编辑已发布文件。

## 诊断失败

使用明确 schema 限定名查询（示例 auth，请替换为已经核验的合法 schema）：

```sql
SELECT version, dirty FROM auth.schema_migrations;
SELECT tablename FROM pg_tables WHERE schemaname = 'auth' ORDER BY tablename;
```

首次失败可能发生在版本表建立前，此时没有记录表；不要据此声称数据库未发生任何变化。dirty=true 时停止自动重试，保留失败 SQL/版本、日志和备份，检查实际结构及数据。包不自动 Force、不清除 dirty、不盲目重放失败版本。不要把手工将 dirty 改为 false 当成修复。

核实失败事务的实际效果，再选择前向修复或备份恢复。确有必要用外部迁移工具修复版本标记时，必须先证明实际结构与对应版本一致；只改标记不会恢复丢失的数据。

## 回退选择

- 应用回退：只有旧代码已验证兼容当前数据库结构时才可回退二进制。
- 前向修复：通过新的版本修正问题，保持历史 SQL 不变。
- 数据库恢复：先恢复到独立数据库核对版本、数据、身份解密和认证，再按产品维护流程切换；恢复会丢失备份时间点之后的写入，产品方需确定可接受的数据损失窗口。

数据库备份不包含密钥存储和 Redis。恢复时须保留对应的历史签名/HMAC/加密密钥，协调 session 数据、吊销集、刷新宽限缓存及验证码有效期；无法证明状态一致时，在产品切换流程中明确撤销受影响会话并要求重新登录，不能把清空 Redis 当成安全会话恢复。包内演练使用合成账号和独立 Redis，不代替产品生产灾备验收。

## BREAKING：全量 Down 默认禁用

原 migrations.Down(ctx, connCfg, schema) 签名保留，但始终返回 ErrDestructiveOperation，并且不访问数据库。常规生产升级与回退均不得调用它。

一次性开发测试数据库清理须显式调用：

```go
err := migrations.UnsafeReset(ctx, connCfg, schema,
    migrations.UnsafeResetOptions{ConfirmSchema: schema})
```

此操作删除目标 schema 的认证迁移对象，保留 schema 本身；确认值必须完全一致。它不是权限控制，也不是生产回滚。现有调用 Down 的测试清理应迁移到该接口或使用测试自己持有的临时 schema 清理逻辑。

## 历史检查与恢复演练

bash scripts/check-migrations.sh 校验固定源 0001 和所有可达正式 vX.Y.Z tag 的历史 SQL，包括合并分支及 HEAD 上的 tag；发布过的内容不能被后续 tag 覆盖；浅克隆必须先获取完整历史和 tags。源码基线哈希来自固定 Git 提交经 git cat-file 读取的原始 LF 字节，不受 Windows checkout 的换行策略影响。

bash scripts/verify-recovery.sh 需要 psql/pg_dump/pg_restore 17、Go 和两个明确指定的独立空测试库。脚本通过集群 system_identifier 和实际数据库名称判断同库，要求具备读取 pg_control_system 的测试权限，并拒绝非空库。只生成合成数据，不删除或覆盖既有库。恢复后检查四类表、版本、解密和旧访问/刷新令牌，并验证源库未改变。证据保留在被 Git 忽略的 .test-output/recovery.* 下；重跑需提供新的空库。