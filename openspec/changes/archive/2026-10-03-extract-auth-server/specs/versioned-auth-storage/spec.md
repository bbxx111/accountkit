## Purpose

使账号认证包自行携带数据库结构与迁移版本，同时与宿主业务迁移链分离，支持指定 PostgreSQL schema 的首次安装、重复执行及符合冻结基线的已有数据的连续使用，避免包提取造成重建表或账号失效。

## ADDED Requirements

### Requirement: 包内数据库定义
包 SHALL 随发布产物携带用户、身份、会话、审计表及其迁移，宿主不需要复制 SQL；目标 schema SHALL 可配置，迁移记录 SHALL 与对应实例放在同一 schema。

#### Scenario: 空数据库安装
- **WHEN** 对合法且尚不存在的目标 schema 执行迁移
- **THEN** 创建目标 schema 及认证表和版本记录，不在 public 或其他业务 schema 新建认证表

#### Scenario: 非法 schema
- **WHEN** schema 名不符合 ^[a-z][a-z0-9_]{0,62}$
- **THEN** 返回配置错误，不执行由该名称组成的数据库变更

### Requirement: 版本化重复执行
迁移 SHALL 仅应用未成功执行的版本。重复调用成功迁移 SHALL 不重建表或破坏已有数据；这项保证不表示每条原始 SQL 可脱离迁移器重复执行。

#### Scenario: 连续迁移
- **WHEN** 对已完成版本的非空数据库再次迁移
- **THEN** 成功返回且版本、账号、身份和会话保持不变

### Requirement: 保留源存储兼容性
提取 SHALL 保留源 0001 SQL、迁移版本编号、表和字段、ID 格式、密文与摘要格式。仅调整模块路径 SHALL 不要求重建数据库或强制所有用户重新登录。

#### Scenario: 接管源实例
- **WHEN** accountkit 连接由固定源基线建立的数据库，且 schema、Redis 前缀、令牌域及密钥均保持一致
- **THEN** 识别已有版本并正常读取用户和身份，验证未到期访问令牌及刷新会话，不重放 0001

### Requirement: 宿主迁移链隔离
认证迁移 SHALL 不修改宿主业务迁移记录，也不影响同库其他认证 schema。

#### Scenario: 两个独立版本记录
- **WHEN** 同库两个认证 schema 和业务 schema 均已存在
- **THEN** 对其中一个认证 schema 执行迁移，不改变另外两个 schema 的结构、数据与迁移版本