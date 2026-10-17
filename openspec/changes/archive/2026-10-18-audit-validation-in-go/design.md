## Context

参见 proposal.md。用户已确认仅调整审计校验职责、其他表 CHECK 保持，并明确要求直接改 0001；本次在任务分支直接实施已确认设计。现有 Async.Record 和 Store.InsertBatch 均使用 event.Freeze；Go 脱敏投影和 user/query.sql 的批量清理需要回归验证。

## Goals / Non-Goals

目标是让完整事件规则只有 Go 一份实现，同时保持数据库基本形状和并发完整性。范围不包括删除其他表 CHECK、改造异步可靠性、调整身份或会话、增加迁移及自动重建既有数据库。

## Decisions

1. 移除 audit_resource_valid、audit_resources_valid、audit_actor_valid、audit_context_valid、audit_json_bytes；actor/targets/context 用 jsonb_typeof 的直接 CHECK 替代，data 对象 CHECK 保留。保留 audit_event_id_v7、全部 NOT NULL、列类型、默认值及索引。移除 source/action 格式、occur_time 业务范围及容量 CHECK。VARCHAR(128) 保持存储结构；字段的完整格式和字节限制由 Go 执行。
2. event.Freeze 继续作为通用校验与规范化入口；认证映射保留既有领域校验。Async 入队与 Store 直写各自调用同一校验，不能依赖必经队列。Go 清理后重新 Freeze，校验失败回滚而非写入；读取仍校验，不为非法直接 SQL 数据提供容错查询承诺。
3. user/query.sql 批量匿名化只删除规定字段并重建基本 JSON 结构，保持原事务、锁序和批量性能；通过清理前后事件验证测试证明合法输入得到合法输出，不增加逐行 Go 回读。不把清理 SQL 改成数据库业务验证函数。
4. 将直接 SQL 的业务内容拒绝测试转为“数据库接受基础结构合法内容、Go 拒绝非法内容”的边界测试。保留并验证 UUIDv7、形状、非空、主键及其他表 CHECK。64 KiB 精确边界改在 Go/Store 上验证，不能通过放宽 Go 限制使测试通过。

## Risks / Trade-offs

- [直接 SQL 可产生非法业务数据] → 文档要求导入/修数复用 Go 校验；读侧继续明确拒绝不合规事件，查询错误不得伪装为空列表。
- [清理后规范化差异] → 保留事件 ID、稳定引用、时间和幂等语义，真实 PostgreSQL 覆盖迟到、重放和并发匿名化。
- [新旧同号初始定义并存] → 重复 Up 不主动移除旧函数和 CHECK；文档说明需操作者核验后准备新空资源，不扩大 baseline 检查或执行 Force。

## Migration Plan

直接修改当前未发布 migrations/0001_init.up.sql 和 down.sql，同步 internal/migrationcheck 的 initialFrozen；初始化版本仍为 1，不新增 0002。历史 frozen、manifest、源快照和 v0.1.0/v0.2.0 标签保持。sqlc 重新生成并核对差异。通过数据库直装/重复 Up/多实例、Go 与 Store 拒绝非法输入、匿名化并发、历史迁移门禁及生成一致性验证；结果统一记录 verification.md。不执行提交、推送、归档或部署。
