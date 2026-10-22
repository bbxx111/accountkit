# Spec Delta

## ADDED Requirements

### Requirement: Google 身份注释并入当前初始定义
本变更 SHALL 按用户“数据库变更直接修改 0001 即可”的授权，只修改当前 0001 中 identity.kind 与 provider_meta 的注释，使注释包含 GOOGLE 以及 profile 的 name 和 picture。SHALL 不新增列、索引、CHECK 或生产 0002，且只更新当前 initialFrozen。已发布标签、SQL、冻结源和 0001 down SHALL 保持。新安装 SHALL 仍为 version=1/dirty=false。该授权不得扩大为日常改写初始 SQL。

#### Scenario: 新空库安装
- **WHEN** 对新空 schema 执行迁移
- **THEN** 只执行当前 0001，version=1/dirty=false，身份表的列和约束与修改注释前一致

#### Scenario: 旧同号库
- **WHEN** 已安装的 version 1 再次 Up
- **THEN** 不重放 0001，保留原 catalog、数据和 version/dirty
