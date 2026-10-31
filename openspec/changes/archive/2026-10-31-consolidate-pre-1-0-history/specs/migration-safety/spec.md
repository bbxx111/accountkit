## MODIFIED Requirements

### Requirement: 升级保留已有数据

发布 SHALL 在真实 PostgreSQL 验证新安装 version 1、当前非空基线重复 Up、并发/多实例隔离，以及当前基线到 latest+1 的增量升级保留数据。测试下一版本及失败 probe SHALL 仅放测试目录并使用 latest+1（当前为 2）。dirty 优先、取消、外层及引擎锁和连接释放契约保持。v1.0.0 之前的开发期版本从未被正式使用，发布 SHALL 不提供也不验证从它们升级的路径。

#### Scenario: 非空数据库升级
- **WHEN** 完整当前 version 1 基线包含账号、身份、会话、审计、流程、来源、客户端描述、资料及已清理记录，在隔离测试源中升级到 latest+1 的测试目标版本
- **THEN** 版本前进，原记录、约束、资料精确数字、账号时间和认证能力保留，再次升级不重复执行；该路径只验证当前基线的未来增量，不发布虚假生产迁移

#### Scenario: 当前非空基线重复执行
- **WHEN** 当前 version 1 含账号、身份、会话、审计、流程、来源、客户端描述及资料
- **THEN** 重复 Up 不改变 catalog、数据、账号时间或 version/dirty

#### Scenario: 测试版本继续独立
- **WHEN** 验证未来成功、失败和 dirty 阻断
- **THEN** 测试源使用 latest+1，生产源只含当前 0001，不含 probe 或失败 SQL

#### Scenario: 真实恢复证据
- **WHEN** 对本变更完整验收
- **THEN** 两独立空库完成真实 seed/verify/source-unchanged，核对 version 1、资料精确数字、账号时间、认证/匿名化及源库不变；未运行或 skip 不能称完成

## ADDED Requirements

### Requirement: 正式发布迁移不可改写

发布 SHALL 核验完整历史与全部可达的 v1.0.0 及以上正式标签：当前 0001 up/down 与固定哈希一致；每个可达正式标签内的迁移文件与基线逐字节相同，且不得在后续标签中被改写、删除或重编号；新迁移版本号 SHALL 大于已发布最大版本。主版本为 0 的标签 SHALL 被忽略，不参与比对，也不获得任何豁免。v1.0.0 标签打上后，当前 0001 SHALL 不再按用户限定授权改写；后续真实结构变更从 0002 递增。

#### Scenario: 修改历史 SQL
- **WHEN** v1.0.0 及以上标签内的迁移被修改、删除、重编号，或标签内迁移与当前基线不同
- **THEN** 历史检查失败，不能以更新当前固定哈希豁免

#### Scenario: 当前定义固定
- **WHEN** 当前工作树 0001 与记录的固定哈希不一致
- **THEN** 历史检查失败；采用新定义须通过新增递增版本，而不是改写 0001

#### Scenario: 开发期标签不参与检查
- **WHEN** 检出中仍存在或被重新创建主版本为 0 的标签
- **THEN** 历史检查跳过这些标签，不比对其迁移内容，也不因它们失败；禁止创建此类标签由仓库规则与发布检查表的人工核对承担

#### Scenario: 整合后发布
- **WHEN** 新正式 tag 或合并分支新增迁移
- **THEN** 校验当前初始定义及全部可达正式标签的已发布迁移，HEAD 标签不能掩盖篡改；当前生产只有 version 1，后续真实增量从 0002 递增

## REMOVED Requirements

### Requirement: 已发布迁移不可改写
**Reason**: 由「正式发布迁移不可改写」取代。原要求固定源快照、v0.1.0 至 v0.6.0 各代 pin，并追溯多轮对未发布 0001 的限定授权；这些开发期版本从未被正式使用，v1.0.0 起只保护 v1.0.0 及以上标签与当前 0001。
**Migration**: 无运行时迁移。历史授权与验收原文保留在 `openspec/changes/archive/`，不再作为规格要求。

### Requirement: 授权调整初始审计校验职责
**Reason**: 该要求记录的是 v1.0.0 之前对未发布 0001 的一次限定授权。所有开发期版本未被正式使用，历史授权不再需要作为规格要求保留；其结构事实（审计表无自定义校验函数，业务校验在 Go 执行）已体现在当前 0001 与 `generic-audit-events` 规格。
**Migration**: 无运行时迁移。历史过程记录保留在 `openspec/changes/archive/`。

### Requirement: 身份类型文本化限定授权
**Reason**: 同上，属已完成的开发期限定授权。identity.kind 为 `VARCHAR(32) COLLATE "C"` 加格式 CHECK 的事实由 `versioned-auth-storage` 的「当前初始定义」描述。
**Migration**: 无运行时迁移。

### Requirement: 已清理用户记录表限定授权
**Reason**: 同上。`purged_user` 表属于当前 0001 的一部分，由 `versioned-auth-storage` 的「当前初始定义」与 `purged-user-feed` 规格描述；v0.6.0 pin 随开发期标签退役。
**Migration**: 无运行时迁移。
