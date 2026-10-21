# versioned-auth-storage Specification

## Purpose

使账号认证包自行携带完整数据库结构与迁移版本，同时与宿主业务迁移链分离，支持指定 PostgreSQL schema 的首次安装、重复执行及当前基线内的增量升级，并明确旧基线拒绝和历史数据、凭证格式的验证边界。

## Requirements

### Requirement: 包内数据库定义

包 SHALL 按本轮显式限定授权将资料单列并入当前 0001，撤回生产 0002 up/down。本轮用户明确要求“0002 并入 0001，无需考虑旧数据迁移”，仅授权把 `profile_attributes JSONB NOT NULL DEFAULT '{}'::jsonb` 单列合入当前工作树 0001，撤回生产 0002；新空 schema 安装 version=1/dirty=false，其他列、索引和业务行为保持。当前 initialFrozen 新哈希与 v0.4.0 已发布旧定义分别固定；v0.4.0 精确 tag `62f0799b6c6ef1603b33f81834d39033dd0d0c4d`、其他发布 tag/SQL 和冻结源保持。旧 v0.4.0 v1 缺资料列、其他历史 v1 及各代撤回 v2（含单列、四列、五列及混合资料 v2）均无写拒绝，dirty 优先，保留 catalog、数据及 version/dirty；不转换旧数据、不重放同号 SQL、不 Force、改版本或清库。本轮不提供旧数据迁移工具或升级承诺；后续真实增量从 0002 递增，测试 probe 使用 latest+1（当前为 2）。 JSON Schema/属性业务校验 SHALL 在 Go 执行，不新增外键、JSON 业务 CHECK、触发器、索引或逐行 Schema 元数据；公共 API、账号行锁、账号时间、认证和隐私行为保持。0001 down 仍删除整张账号表。

#### Scenario: 空数据库安装
- **WHEN** 对合法新空 schema 执行 Up
- **THEN** 只执行当前 0001，得到完整七表及单列资料，version=1/dirty=false，不影响其他 schema

#### Scenario: 非法 schema
- **WHEN** schema 不符合 `^[a-z][a-z0-9_]{0,62}$`
- **THEN** 返回配置错误，不执行数据库变更

#### Scenario: 已发布初始定义保持
- **WHEN** 检查 v0.3.0、v0.4.0 及其他正式 tag 内初始定义与当前工作树 0001
- **THEN** 已发布精确提交及原始 SQL 保持；当前仅按本轮明确授权纳入资料单列并固定新哈希，不把有限授权扩展为日常历史改写

#### Scenario: 直接定义仅调整名称
- **WHEN** 追溯 v0.3.0 发布前流程改名
- **THEN** 历史名称映射和 catalog 等价证据保持；本轮资料整合另核对与原 0001+单列 0002 的等价性，不改写旧记录或其他流程结构

#### Scenario: 整合结构等价
- **WHEN** 对比合并 0001 与原 0001+单列 0002 的结果
- **THEN** catalog 和默认值/非空语义等价，其他列、索引、约束、来源和客户端描述保持

#### Scenario: 历史与当前分别固定
- **WHEN** 校验完整历史、全部可达正式 tag 和当前初始定义
- **THEN** 仅更新当前 initialFrozen，v0.4.0 及其他发布精确提交/旧 SQL 哈希、冻结源独立保持，移动标签或改写历史仍失败

### Requirement: 版本化重复执行
迁移 SHALL 仅应用未成功执行的版本。重复调用成功迁移 SHALL 不重建表或破坏已有数据；这项保证不表示每条原始 SQL 可脱离迁移器重复执行。

#### Scenario: 连续迁移
- **WHEN** 对已完成版本的非空数据库再次迁移
- **THEN** 成功返回且版本、账号、身份和会话保持不变

### Requirement: 宿主迁移链隔离
认证迁移 SHALL 不修改宿主业务迁移记录，也不影响同库其他认证 schema。

#### Scenario: 两个独立版本记录
- **WHEN** 同库两个认证 schema 和业务 schema 均已存在
- **THEN** 对其中一个认证 schema 执行迁移，不改变另外两个 schema 的结构、数据与迁移版本

### Requirement: 整合基线的兼容边界

预检 SHALL 在写入前要求 source 可达版本具有完整当前资料结构，包括单列类型、默认值、非空、无旧资料元数据和账号原五个时间列精度；dirty 优先。旧 v0.4.0 v1、其他旧 v1 和各代撤回 v2 SHALL 无写拒绝，不重放同号 SQL、自动转换、Force、改版本或清库。未来增量从 0002 开始，测试 probe 为 latest+1。

#### Scenario: 完整新基线重复执行
- **WHEN** 当前完整 version 1 已安装且含资料及认证数据，再次 Up
- **THEN** catalog、数据、账号原时间、版本和资源 ID 保持，连接及锁释放

#### Scenario: 旧发布库同号
- **WHEN** v0.4.0 version 1 缺资料列，无论空或非空
- **THEN** 无写拒绝，保持 catalog、数据和 version/dirty，不补列或重放 0001

#### Scenario: 撤回的资料与其他增量
- **WHEN** 单列/四列/五列/混合资料 v2 或各代撤回流程/会话 v2 连接当前只有 0001 的生产源
- **THEN** dirty 优先，无写拒绝并保留 catalog、数据及 version/dirty

#### Scenario: 旧基线及不完整资料结构
- **WHEN** 旧 v0.3.0 v1、b414fef 双快照 v1、旧流程/审计、双表并存，或 v1 缺资料/类型默认值非空漂移/旧元数据残留
- **THEN** 无写拒绝，不推断来源、转换快照或靠 IF NOT EXISTS 和手改版本接管

#### Scenario: 未来测试增量
- **WHEN** 测试源对完整当前基线提供 latest+1（当前为 2）的成功或失败 probe
- **THEN** 正常验证未来增量及失败/dirty 阻断，测试 SQL 不进入生产源

#### Scenario: 历史数据格式验证
- **WHEN** 固定历史合成凭据装载到当前结构并显式建立合法来源、映射旧审计
- **THEN** 非审计 ID、密文/摘要和 token 保持，legacy_event_id 可追溯，不将夹具称为生产转换，不改冻结源

#### Scenario: 旧已发布会话结构
- **WHEN** 旧 v0.3.0 version 1 缺少完整会话上下文，无论空或非空
- **THEN** Up 无写拒绝并保留 catalog、数据及 version/dirty，不执行撤回会话 0002、不补造来源或重放当前 0001

#### Scenario: 旧初始库版本同为一
- **WHEN** 旧 version 1 有旧流程名、审计、会话结构，或 v0.4.0 v1 缺少资料列
- **THEN** 明确无写拒绝并保留原记录、结构和版本；版本同号不代表当前完整结构，不自动补列或重放 0001

#### Scenario: 旧开发增量库
- **WHEN** 连接已撤回 version 2，包括流程改名、原会话或各代资料增量
- **THEN** 当前生产源无对应 0002，拒绝并保留 catalog、数据及 version/dirty，不降版本、清库、转换或自动重放

#### Scenario: 双表并存
- **WHEN** sign_in_completion_flow 和 identity_flow 同时存在
- **THEN** 无写拒绝并保留数据、结构和版本，不选择其一接管或自动删除旧表

#### Scenario: 撤回会话增量
- **WHEN** 完整原会话 0002 的 version 2 连接当前生产源
- **THEN** 无写拒绝，不因会话结构相似接管为当前 version 1，也不添加资料列、降版本或重放

#### Scenario: 新基线重复执行
- **WHEN** 完整当前初始定义及迁移源中合法后续增量已安装且非空
- **THEN** 重复迁移保持结构、版本、数据、账号时间和资源 ID，不影响其他 schema；当前生产安装仍为 version 1，测试未来增量不混入生产源

#### Scenario: 正式会话结构递增升级
- **WHEN** 旧 v0.3.0 v1 请求沿已撤回会话 0002 使用新结构
- **THEN** 无写拒绝且保留旧库；本轮不提供旧数据迁移，也不重新引入会话增量或转换工具

#### Scenario: 版本同号但结构不同
- **WHEN** 对比旧 v0.3.0 v1、b414fef 双快照 v1、v0.4.0 缺资料 v1 与当前完整资料 v1
- **THEN** 仅接受当前完整结构，其他同号库无写拒绝且数据/catalog/版本保持，不使用 Force 或补列接管

#### Scenario: 历史格式与失败清理
- **WHEN** 装载合成历史凭据或重复拒绝旧结构
- **THEN** 正确区分凭据格式验证与数据库升级，连接和锁释放，dirty 优先；来源缺失仍按既有不变量契约在使用时拒绝，不补造来源

#### Scenario: 前一整合基线拒绝
- **WHEN** 前一 b414fef v1 含 known 标记、snapshot_kind 或旧平坦快照列
- **THEN** 空或非空均无写拒绝，重复拒绝仍保持 catalog、数据及 version/dirty，不静默删列或选择 LATEST 转换，不因本轮资料整合放宽保护
