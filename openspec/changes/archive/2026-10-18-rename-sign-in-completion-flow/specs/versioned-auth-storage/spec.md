## MODIFIED Requirements

### Requirement: 包内数据库定义
包 SHALL 随发布产物携带用户、身份、会话、审计和短期登录补全流程表及其迁移，宿主不需要复制 SQL；目标 schema SHALL 可配置，迁移记录 SHALL 与对应实例放在同一 schema。按用户本次明确授权，当前未发布 0001 SHALL 直接定义 sign_in_completion_flow，关联主键、格式约束和五个独立索引 SHALL 使用新表名；列及索引定义保持。配套 down 和当前固定校验 SHALL 同步，初始化版本保持 1；本分支新增的 0002 重命名迁移 SHALL 撤回。固定源快照、已发布历史 SQL 与精确标签提交 SHALL 保持，不提供绕过历史检查的开关。后续真实结构变更 SHALL 从 0002 递增。

#### Scenario: 空数据库安装
- **WHEN** 对合法且尚不存在的目标 schema 执行迁移
- **THEN** 0001 直接建立包含通用审计的五张业务表，流程表名为 sign_in_completion_flow，version=1 且 dirty=false，不在 public 或其他业务 schema 新建认证表

#### Scenario: 非法 schema
- **WHEN** schema 名不符合 ^[a-z][a-z0-9_]{0,62}$
- **THEN** 返回配置错误，不执行由该名称组成的数据库变更

#### Scenario: 直接定义仅调整名称
- **WHEN** 将当前初始 catalog 与改名前定义按授权名称映射后对比
- **THEN** 列、主键、CHECK、索引定义和表数量保持，标识符长度不超过 PostgreSQL 上限，当前固定校验与实际字节一致

### Requirement: 整合基线的兼容边界
迁移 SHALL 接受具有新流程表的完整当前初始结构及其正常递增版本，拒绝旧流程表名、旧审计列结构和已撤回旧增量结构，不因版本数字相同而返回虚假的安装成功。拒绝 SHALL 保留表、数据和版本标记；dirty SHALL 继续按原规则优先阻断。预检 SHALL 要求完整 sign_in_completion_flow 且不存在 identity_flow，继续要求十列 UUID 审计结构；不要求旧 flow_id 专属列，不自动重放初始 SQL、重命名、重建、清库或转换旧库。

#### Scenario: 旧初始库版本同为一
- **WHEN** 旧 version=1 数据库仍使用 identity_flow、缺少完整流程结构或仍具有旧审计列
- **THEN** 返回明确不兼容错误，旧记录、结构与版本保持，不自动重放 0001

#### Scenario: 旧开发增量库
- **WHEN** 当前单版本迁移源连接旧 version=2 数据库，包括此前重命名方案产生的新表名 v2
- **THEN** 返回明确不兼容错误，不降版本或清库，也不自动重放 0001

#### Scenario: 双表并存
- **WHEN** 当前实例同时存在新旧流程表
- **THEN** 返回明确不兼容错误，数据、结构和版本保持

#### Scenario: 新基线重复执行
- **WHEN** 当前初始结构已经安装且包含数据，再次执行迁移
- **THEN** 成功且结构、版本和数据不变，不重新分配资源 ID；不影响其他 schema

#### Scenario: 历史数据格式验证
- **WHEN** 将固定历史合成账号、身份密文和令牌直接装载到当前初始结构，并明确映射历史审计合成记录
- **THEN** 原账号等非审计 ID、密文解密、摘要查找及有效 access/refresh 仍可使用，审计映射保留旧 ID 可追溯关系；不把该验证称为旧库原地升级，不修改固定旧快照
