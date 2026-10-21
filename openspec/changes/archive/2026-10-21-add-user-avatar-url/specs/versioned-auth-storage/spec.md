# Spec Delta

## ADDED Requirements

### Requirement: 头像列初始定义限定授权

本轮 SHALL 按用户“不新增 0002 迁移，改现有 0001”的明确授权，在当前账号表增加 avatar_url TEXT NULL，无默认值、索引或业务 CHECK；只更新当前 initialFrozen。此前资料单列整合的“当前”结构以本轮追加头像后的定义为准，其历史授权及验收保持。生产源 SHALL 仍仅含 0001，新安装 version=1/dirty=false，已发布标签、SQL 与冻结源保持。后续真实增量和测试 probe 仍从 latest+1 开始。

#### Scenario: 新安装及重复执行
- **WHEN** 新空 schema 安装并写入头像后重复 Up
- **THEN** 得到完整当前结构，重复执行保留 catalog、头像和其他数据、version/dirty

### Requirement: 旧头像结构无写拒绝

运行时预检 SHALL 要求完整当前头像列类型、可空及无默认值，dirty 优先。缺少头像列的前一资料 v1，以及所有既有历史拒绝结构 SHALL 无写拒绝，保留 catalog、数据和 version/dirty；不得补列、重放同号 SQL、Force、改版本或清库。本轮 SHALL 不提供旧数据迁移或升级承诺。

#### Scenario: 前一资料版本一
- **WHEN** 有 profile_attributes 但无 avatar_url 的 version 1 请求 Up
- **THEN** 无写拒绝，空和非空情况均保持数据、catalog、版本及 dirty；反复拒绝不泄漏锁

#### Scenario: 头像列结构漂移
- **WHEN** avatar_url 类型、可空性或默认值与当前定义不符
- **THEN** 无写拒绝；dirty 标记存在时优先返回 dirty 错误
