# migration-safety Specification

## Purpose

为独立账号认证包首次生产使用建立可验证的数据库变更与恢复保障，确保并发部署、部分失败和重复执行不会被误报为成功，防止默认回滚入口清空认证数据，并通过真实备份恢复演练证明操作流程可执行。

## Requirements

### Requirement: 升级保留已有数据

发布 SHALL 在真实 PostgreSQL 验证新安装 version 1、当前非空基线重复 Up、并发/多实例隔离及旧结构无写拒绝。本轮不提供旧数据升级路径。测试下一版本及失败 probe SHALL 仅放测试目录并使用 latest+1（当前为 2）。dirty 优先、取消、外层及引擎锁和连接释放契约保持。

#### Scenario: 非空数据库升级
- **WHEN** 完整当前 version 1 基线包含账号、身份、会话、审计、流程、来源、客户端描述及资料，在隔离测试源中升级到 latest+1 的测试目标版本
- **THEN** 版本前进，原记录、约束、资料精确数字、账号时间和认证能力保留，再次升级不重复执行；该路径仅验证当前完整基线的未来增量，不接管旧 v0.4.0 v1 或撤回 v2，不发布虚假生产迁移

#### Scenario: 当前非空基线重复执行
- **WHEN** 当前 version 1 含账号、身份、会话、审计、流程、来源、客户端描述及资料
- **THEN** 重复 Up 不改变 catalog、数据、账号时间或 version/dirty

#### Scenario: 测试版本继续独立
- **WHEN** 验证未来成功、失败和 dirty 阻断
- **THEN** 测试源使用 latest+1，生产源只含当前 0001，不含 probe 或失败 SQL

#### Scenario: 真实恢复证据
- **WHEN** 对本轮整合完整验收
- **THEN** 两独立空库完成真实 seed/verify/source-unchanged，核对 version 1、资料精确数字、账号时间、认证/匿名化及源库不变；未运行或 skip 不能称完成

### Requirement: 并发迁移串行执行
同一数据库同一 schema 的迁移 SHALL 串行执行，首次创建 schema 和版本表的并发竞争也 SHALL 受验证。

#### Scenario: 两个进程首次部署
- **WHEN** 两个独立迁移实例同时对不存在的同一目标 schema 发起升级
- **THEN** 最终版本干净且结构完整，不重复应用 SQL；等待方成功或返回明确可重试的锁等待错误，不产生 dirty 状态

### Requirement: 失败必须阻断后续升级
迁移失败 SHALL 返回错误并保留可诊断状态，dirty 状态 SHALL 阻止后续正常升级。包 SHALL 不自动清除 dirty、不盲目重放失败 SQL，也不将失败标记为成功。

#### Scenario: SQL 执行失败
- **WHEN** 某版本执行过程中失败并留下 dirty 状态
- **THEN** 下一次升级明确失败，错误可定位到失败版本；人工检查部分变更并按恢复手册处理后才允许继续

### Requirement: 默认拒绝破坏性回滚
常规全量回滚入口 SHALL 拒绝执行且不修改数据库。开发测试清库 SHALL 需要显式的危险操作意图和与目标一致的 schema 确认值，生产恢复流程 SHALL 不使用全量清库。

#### Scenario: 旧调用默认安全
- **WHEN** 调用原全量回滚入口
- **THEN** 返回可识别的破坏性操作拒绝错误，版本和数据均不变

#### Scenario: 清库目标确认不一致
- **WHEN** 显式清库请求缺少确认或确认的 schema 与目标不同
- **THEN** 在数据库 I/O 前拒绝操作

#### Scenario: 确认测试清库
- **WHEN** 操作者显式调用危险清库入口并准确确认隔离测试 schema
- **THEN** 只清除该实例的认证迁移对象，保留 schema 本身并不影响其他实例

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

### Requirement: 备份恢复可演练
首次生产发布 SHALL 完成隔离数据库的备份恢复演练，操作手册 SHALL 区分应用回退、前向修复和数据库备份恢复，并明确数据损失窗口及密钥、Redis 会话状态的处理。

#### Scenario: 恢复后验证
- **WHEN** 将迁移前备份恢复到独立测试数据库
- **THEN** 核对版本、关键记录及身份解密和认证行为，不影响原库；只通过恢复演练不能宣称真实产品已完成灾备验证

### Requirement: 必需集成验证不得静默跳过
发布检查 SHALL 要求真实 PostgreSQL 连接和指定集成测试实际执行。

#### Scenario: 缺少测试数据库
- **WHEN** 发布检查没有数据库配置或所需集成测试被跳过
- **THEN** 检查失败而非报告通过

### Requirement: 授权调整初始审计校验职责

本次 SHALL 按用户明确授权直接修改当前未发布 0001 的审计校验定义，移除五个自定义校验函数及相应业务 CHECK，保留基本结构约束，并同步当前初始 SQL 固定哈希；初始化版本 SHALL 保持 1，不新增生产迁移。其他表 CHECK、索引定义、固定源快照、manifest 和正式历史标签 SHALL 保持。该授权 SHALL 不成为后续日常改写初始 SQL 或绕过历史检查的开关。

#### Scenario: 新空实例初始化
- **WHEN** 对新空 schema 执行迁移
- **THEN** 安装无自定义审计校验函数的完整初始结构，version=1 且 dirty=false

#### Scenario: 旧同号库重复迁移
- **WHEN** 包含旧审计校验函数的完整十列 version=1 实例重复执行 Up
- **THEN** 不重放 0001、不自动删函数或约束、不清理数据或修改版本；需新定义时由操作者核验后准备新空资源

### Requirement: 身份类型文本化限定授权
本变更对当前 0001 的修改 SHALL 限于 identity.kind 的列类型（VARCHAR(32)）、COLLATE、格式 CHECK 及相关注释，并只更新当前 initialFrozen。已发布 tag 与其 SQL、v0.4.0 与 v0.5.0 的已发布定义、冻结源 SHALL 保持，历史检查继续独立固定各代基线。后续真实结构增量 SHALL 仍从 0002 开始。

#### Scenario: 历史检查
- **WHEN** 运行迁移历史检查
- **THEN** 已发布 tag 的 0001 哈希保持原值，当前 0001 与新的 initialFrozen 一致

#### Scenario: 旧同号库
- **WHEN** 已安装的旧 version 1 再次 Up
- **THEN** 不重放 0001，不自动转换列类型，不 Force、不改版本、不清库

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
