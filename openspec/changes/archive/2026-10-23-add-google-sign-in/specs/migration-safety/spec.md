# Spec Delta

## MODIFIED Requirements

### Requirement: 已发布迁移不可改写

发布 SHALL 核验完整历史与全部可达正式标签，分别固定当前 initialFrozen、已发布精确提交/SQL 和冻结源。用户对当前初始定义的有限授权 SHALL 仅按对应场景执行，保留历史授权及验收原文，不扩大成日常改写权限或新增跳过检查开关。旧结构 SHALL 无写拒绝且 dirty 优先，保留 catalog、数据和 version/dirty，不转换、重放同号 SQL、Force、改版本或清库。资料整合不提供旧数据迁移工具或升级承诺；后续真实增量 SHALL 从 0002 递增，测试 probe 使用 latest+1（当前为 2）。本变更另按用户“数据库变更直接修改 0001 即可”的授权，只更新当前 0001 中 identity.kind 与 provider_meta 的注释；不新增列、索引、约束或生产 0002。

#### Scenario: 修改历史 SQL
- **WHEN** 已发布迁移修改、删除、重编号或 tag 移动
- **THEN** 历史检查失败，不能以当前 initialFrozen 新哈希豁免旧发布定义

#### Scenario: 本轮限定授权
- **WHEN** 按用户“0002 并入 0001，无需考虑旧数据迁移”的限定授权，在当前 0001 合入 `profile_attributes JSONB NOT NULL DEFAULT '{}'::jsonb` 单列并撤回生产 0002
- **THEN** 仅当前哈希更新，旧发布 pin 独立保持，其他列、索引和业务行为不变，新空 schema 安装 version=1/dirty=false；后续头像列授权单独遵循存储规格，不扩大本次资料整合范围

#### Scenario: 历史授权保持
- **WHEN** 追溯通用审计、流程改名、独立索引命名及会话简化初始整合
- **THEN** 保留原限定授权和验收原文，不将旧记录当本轮通过证据

#### Scenario: 旧结构无写拒绝
- **WHEN** 缺资料列的旧 v0.4.0 v1、其他历史 v1 或各代撤回 v2（含单列、四列、五列及混合资料 v2）连接当前生产源
- **THEN** dirty 优先，无写拒绝并保留 catalog、数据及 version/dirty，不转换、重放同号 SQL、Force 或改版本

#### Scenario: 固定历史发布
- **WHEN** 检查固定 v0.1.0、v0.2.0、v0.3.0、v0.4.0 及其他可达正式发布
- **THEN** 核对各代精确提交及原始 SQL；v0.4.0 固定为 `62f0799b6c6ef1603b33f81834d39033dd0d0c4d`，其旧 pin、其他发布 tag/SQL、冻结源与当前 initialFrozen 分别保持，移动同名 tag 或覆盖旧哈希不能获得豁免

#### Scenario: 用户授权调整原始定义
- **WHEN** 追溯此前通用审计直接调整当时未发布 0001 的工作
- **THEN** 保留当时限定授权和历史记录；本轮另依据资料单列整合的明确授权仅调整当前 0001 和当前哈希，不改已发布 SQL、冻结源或旧验收事实

#### Scenario: 整合后发布
- **WHEN** 新正式 tag 或合并分支新增迁移
- **THEN** 校验当前初始定义及全部可达正式标签的已发布迁移，HEAD 标签不能掩盖篡改；当前生产只有 version 1，后续真实增量从 0002 递增

#### Scenario: 用户授权统一独立索引名称且不新增迁移
- **WHEN** 追溯此前未发布 0001 索引改名
- **THEN** 保留 22 个普通 idx_ 与 4 个独立唯一 uk_ 的当次限定授权和原定义证据；本轮资料整合不调整其他索引，不扩大旧授权或自动修改旧库索引

#### Scenario: 本轮授权简化会话初始结构
- **WHEN** 追溯 v0.4.0 发布前移除 known/双快照并改为单行 JSONB 的会话简化
- **THEN** 保留该历史阶段 version 1、无生产 0002 及当时哈希/验收原文；本轮只整合资料单列，已发布会话 SQL 保持，旧 v0.3.0 v1、前一整合 v1 及撤回 v2 的无写拒绝保护继续保留

#### Scenario: Google 登录注释并入当前初始定义
- **WHEN** 按用户“数据库变更直接修改 0001 即可”，只把 GOOGLE 与 profile 声明补进当前 0001 的 identity.kind、provider_meta 注释
- **THEN** 生产源仍只有 0001，新空 schema 为 version=1/dirty=false，仅当前 initialFrozen 更新；已发布 tag、冻结源和 down 整表语义保持；旧同号库不重放 0001，不 Force、不改版本、不清库
