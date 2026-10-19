## Context

见 [proposal.md](proposal.md)。本次用户在既有改名实施后明确要求撤回新增 0002，直接修改未发布 0001。该要求覆盖此前增量迁移方案，限于流程对象改名及必要固定校验更新。

## Goals / Non-Goals

**Goals:** 新初始化直接使用 sign_in_completion_flow，版本为 1；旧实例明确拒绝且保留数据；公开 Go 与认证行为保持兼容。

**Non-Goals:** 自动接管旧库、重编号历史发布、修改固定源快照、Redis 替换、提交/发布/归档。

## Decisions

1. 将 0001 中 identity_flow 表及主键、格式 CHECK、五个独立索引命名替换为 sign_in_completion_flow；列、约束表达式、索引定义与数据语义保持。配套 down 使用新表名；删除本分支 0002 up/down，仅更新 internal/migrationcheck/main.go 中当前 initialFrozen 两项哈希，legacy 固定哈希和发布提交保持。
2. baseline 对所有受支持版本要求新流程表且不存在旧流程表，继续检查十列审计及完整流程字段。旧表名 v1、旧审计、双表及任何不在迁移源的旧 v2 均拒绝；dirty 优先阻断。重复 Up 不自动转换同号旧实例。
3. 已完成的新 Go 名称和 sqlc 查询保留。旧 ids.IdentityFlow、IdentityFlowContext、CleanupIdentityFlows 继续 deprecated 兼容。新上下文字段非空优先，否则回退旧字段；原指纹序列化、AAD、f_、IDENTITY_FLOW 和 flow_id 协议保持。
4. 用新初始定义/非空重复安装/隔离测试替代撤回的 v1→v2 升级测试；旧 HEAD 固定提交 732639ccddd379552edb58c8b5b128ebb7bbf2a6 的 0001 用于拒绝回归，不修改其原始 SQL。历史 catalog 对比仅将旧对象名称归一化后比较完整定义；不删约束断言。
5. 更新当前文档、计划、tasks 和 verification，旧验证材料留作历史，不把此前 v2 方案通过记录当成本方案验证结果。测试专用下一版本仍使用最新版本+1，不放入生产 SQL 目录。

## Risks / Trade-offs

- [旧同号库不会自动获得新表名] → 明确拒绝，保留数据和版本；采用新基线须人工核验后使用新空资源，需保留数据时另行制定并验证迁移方案。
- [当前哈希变更误伤历史] → 只改 current initialFrozen；完整历史门禁和源快照字节比对继续执行。
- [旧测试依赖旧表名] → 显式区分历史 fixture 与新初始结构，旧库拒绝测试保持五表数据、catalog 和版本快照断言。

## Migration Plan

新空 schema 直接执行 0001，得到 version=1/dirty=false 和新表名。新结构重复 Up 无变化。旧表名库或旧 v2 拒绝；不 Force、不修改版本、不自动清库。此方案不提供旧库原地升级路径。
