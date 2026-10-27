## Why

ai-food 计划改为独立部署 accountsvc（需求 R2，P1）。accountsvc 只匿名化自己的表，ai-food 业务库中以 user_id 关联的数据无法跟着清理：

- 嵌入式宿主可以用同事务匿名化回调，独立部署用不了；
- `USER_PURGED` 审计按设计只是尽力通知，不能作为清理完成的依据。

因此需要一个持久、可分页、可补拉的“已清理用户”记录，供外部业务服务幂等地清理自己的数据。必须在 ai-food 第一张含 user_id 的业务表上线前具备。

用户已明确要求：数据库变更直接修改当前 `0001`，不新增 0002 迁移。

## What Changes

- **数据库（直接修改当前 0001，经用户明确授权）**：新增 `purged_user` 表。
  - `user_id` 为主键；`seq` 为单调递增的拉取游标；`purge_time` 为记录写入时间；另建保留期清理用的索引。
  - down 同步删除该表。
  - 同步更新 `initialFrozen`，并把 v0.6.0 已发布的 `0001` 作为独立 pin 固定。
  - 新安装仍为 version=1/dirty=false。
  - 缺少该表的既有 version 1（含 v0.6.0）在迁移预检中被拒绝，不写入、不补表、不改版本。
- **库**：
  - 账号清理（purge）事务内写入一条已清理记录，与匿名化同提交、同回滚；写入在表锁下串行，保证游标顺序与提交顺序一致，拉取方不会漏读。
  - 新增 Go 方法按游标分页读取已清理记录。
  - 新增可选保留期配置：默认长期保留；设置时不得短于 30 天，由维护任务清理过期记录。
- **accountsvc**：
  - 新增内部路由 `GET /v1/purgedUsers?page_token=&page_size=`，使用独立的服务凭据列表 `ACCOUNTSVC_PURGE_FEED_CLIENTS`（HTTP Basic，不复用内省凭据）。
  - 未配置凭据时返回 503 `PURGE_FEED_NOT_CONFIGURED`。
  - 响应始终带有当前位置的 `next_page_token`，空列表表示已追平。
- **文档**：服务手册增加拉取契约、保留期与业务方幂等清理要求；网关手册要求该路由与内省一样不对公网开放；兼容说明与迁移手册写明本次 0001 调整与旧库拒绝边界。
- **AGENTS.md**：记录本轮“直接改 0001”的限定授权，作用范围与历次授权写法一致。

## Capabilities

### New Capabilities

- `purged-user-feed`：已清理用户记录的写入时机、顺序与一致性、分页拉取契约、保留期与清理、服务鉴权，以及业务方的幂等清理责任。

### Modified Capabilities

- `migration-safety`：新增“已清理用户记录表限定授权”，限定本次对当前 0001 的修改范围、v0.6.0 pin 与旧 version 1 的拒绝边界。
- `accountsvc-runtime`：单监听器路由增加 `/v1/purgedUsers`，新增凭据配置要求。
- `gateway-integration`：入口策略增加该内部路由的阻断要求。

## Impact

- **代码**：
  - `migrations/0001_init.{up,down}.sql`，以及新增的 `purged_user` 预检；
  - `internal/migrationcheck`：v0.6.0 pin 与新的 `initialFrozen`；
  - `user/query.sql` 与 sqlc 生成代码，`user/service_purge.go`，新增读取方法；
  - `config.go`：保留期，`accountkit.go`：维护任务；
  - `internal/accountsvc`：配置、路由与 handler。
- **数据库**：本次是对当前 0001 的直接修改，不是增量迁移。已安装 v0.6.0 或更早版本的数据库无法直接使用新版本，须按迁移手册改用经人工核验的新空资源。本次不提供旧数据迁移工具；用户已确认当前没有正式部署。
- **接入方**：业务服务保存游标、按 user_id 幂等清理，并须在保留期内完成拉取。
