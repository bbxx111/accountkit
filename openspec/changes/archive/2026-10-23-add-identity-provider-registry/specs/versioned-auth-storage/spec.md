# Spec Delta

## ADDED Requirements

### Requirement: 身份类型文本列并入当前初始定义
本变更 SHALL 按用户“直接改 0001”的明确授权，把当前 0001 中 identity.kind 改为 `VARCHAR(32) COLLATE "C" NOT NULL` 并增加格式 CHECK，同步相关注释；索引定义文本、其他列与约束及 0001 down 的整表语义 SHALL 保持。SHALL 不新增生产 0002，只更新当前 initialFrozen。新安装 SHALL 为 version=1/dirty=false。该授权不得扩大为日常改写初始 SQL。

#### Scenario: 新空库安装
- **WHEN** 对新空 schema 执行迁移
- **THEN** identity.kind 为 VARCHAR(32) COLLATE "C" 且带格式 CHECK，version=1/dirty=false

### Requirement: 整数身份类型结构无写拒绝
运行时预检 SHALL 要求 identity.kind 为当前 VARCHAR(32) 定义，dirty 优先。identity.kind 仍为 SMALLINT 的 version 1（含 v0.5.0 及其后、本变更前的安装），以及所有既有历史拒绝结构 SHALL 无写拒绝，保留 catalog、数据和 version/dirty；不得转换列、重放同号 SQL、Force、改版本或清库。本变更 SHALL 不提供旧数据迁移。

#### Scenario: v0.5.0 安装
- **WHEN** identity.kind 为 SMALLINT 的 version 1 请求 Up，空库或含数据
- **THEN** 无写拒绝，数据、catalog、版本及 dirty 保持；反复拒绝不泄漏锁

#### Scenario: dirty 优先
- **WHEN** 同一旧库同时带 dirty 标记
- **THEN** 返回 dirty 错误
