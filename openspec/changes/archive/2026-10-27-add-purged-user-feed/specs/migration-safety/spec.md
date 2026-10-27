## ADDED Requirements

### Requirement: 已清理用户记录表限定授权

经用户明确要求“直接改 0001、不新增 0002”，本变更对当前 0001 的修改 SHALL 限于新增 `purged_user` 表及其主键、唯一约束、格式 CHECK 与保留期索引，并在 down 中先删除该表。除此之外，本变更 SHALL 满足以下约束：

- 其他表、列、索引与约束保持不变；
- 只更新当前 initialFrozen；
- v0.6.0 已发布的 0001 SHALL 作为独立 pin 固定，与 v0.3.0、v0.4.0、v0.5.0 的 pin 并列；
- 已发布 tag 与其 SQL、冻结源保持不变；
- 新空 schema 安装后为 version=1/dirty=false。

已安装的旧 version 1（包括 v0.6.0 及更早版本）缺少完整的 `purged_user` 结构时，迁移预检 SHALL 在任何写入之前拒绝；dirty 优先；不补表、不重放同号 SQL、不 Force、不修改版本、不清库，也不提供旧数据迁移。此授权不构成日常改写初始 SQL 的开关，后续真实结构增量 SHALL 仍从 0002 开始。

#### Scenario: 历史检查
- **WHEN** 运行迁移历史检查
- **THEN** v0.6.0 及更早已发布 tag 的 0001 哈希保持各自的 pin，当前 0001 与新的 initialFrozen 一致

#### Scenario: 旧 v0.6.0 数据库
- **WHEN** 对一个由 v0.6.0 安装、version=1 且缺少 purged_user 的 schema 执行 Up
- **THEN** 以不兼容基线错误拒绝，catalog、数据与 version/dirty 保持不变

#### Scenario: 新安装
- **WHEN** 对空 schema 执行 Up
- **THEN** 安装包含 purged_user 的完整结构，version=1、dirty=false，重复 Up 无变化
