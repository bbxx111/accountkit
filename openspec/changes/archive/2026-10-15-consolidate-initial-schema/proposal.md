## Why

用户确认目前尚无实际产品接入，要求将现有数据库定义整合成一个初始 SQL。当前结构由 0001 和尚未发布的 0002 共同建立，继续保留两段初始化增加首次安装与开发环境维护的复杂度。

## What Changes

- **BREAKING**：执行一次明确的迁移基线整合，将 identity_flow、audit_event.flow_id 及索引并入 0001_init；生产目录仅保留一对初始 up/down SQL，初始化版本为 1。
- 旧版本建立的开发数据库不作为新基线的自动升级来源；迁移拒绝不兼容旧结构，保留数据和 dirty 状态，不自动 Force、清库或改版本。开发者核验目标后自行重建一次性库。
- 原始 source-baseline 和已发布 v0.1.0/v0.2.0 标签保持；历史检查明确区分这两个固定历史发布与整合后的不可变初始 SQL，继续检查所有可达正式标签、完整历史和递增迁移。
- 更新测试下一版本、数据/令牌格式兼容夹具和真实恢复演练；保留未来非空数据库的增量升级、并发、锁释放、dirty 阻断与默认 Down 拒绝。
- 同步迁移与兼容文档、README 和 AGENTS；原有归档记录保留为历史事实。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `versioned-auth-storage`: 单一初始版本、完整表结构及旧基线拒绝边界。
- `migration-safety`: 本次明确基线例外、历史发布核验和整合后的升级/恢复保障。

## Impact

涉及 migrations、internal/migrationcheck、根及 accountsvc 集成夹具、sqlc 生成一致性和长期文档。用户/身份/会话/审计/流程的最终结构、HTTP/Go 认证接口、ID、令牌、密文、schema 隔离和宿主回调契约不变；不操作现有数据库、不改历史标签或源快照、不提供业务数据迁移或新部署动作。
