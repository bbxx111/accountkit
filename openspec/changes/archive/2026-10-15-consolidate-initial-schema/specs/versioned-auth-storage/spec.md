## MODIFIED Requirements

### Requirement: 包内数据库定义
包 SHALL 随发布产物携带用户、身份、会话、审计和短期登录补全流程表及其迁移，宿主不需要复制 SQL；目标 schema SHALL 可配置，迁移记录 SHALL 与对应实例放在同一 schema。本次整合后的全部初始结构 SHALL 由一个 0001 up SQL 直接建立，配套 down SHALL 覆盖所有业务表；生产迁移 SHALL 不再包含独立的 identity_flow 增量。

#### Scenario: 空数据库安装
- **WHEN** 对合法且尚不存在的目标 schema 执行迁移
- **THEN** 创建目标 schema 及完整认证表、流程表和版本记录，version=1、dirty=false，不在 public 或其他业务 schema 新建认证表

#### Scenario: 非法 schema
- **WHEN** schema 名不符合 ^[a-z][a-z0-9_]{0,62}$
- **THEN** 返回配置错误，不执行由该名称组成的数据库变更

## REMOVED Requirements

### Requirement: 保留源存储兼容性
**Reason**: 用户确认尚无产品接入，明确要求一次整合初始结构；旧 schema 的版本编号与新基线不再足以表达兼容性。
**Migration**: 已有一次性开发库经人工核验后重建；源快照和历史标签保持，数据/令牌格式继续通过装载到新基线的合成夹具验证。不自动 Force、清库或迁移业务数据。

## ADDED Requirements

### Requirement: 整合基线的兼容边界
迁移 SHALL 接受完整整合基线及其正常递增版本，拒绝不兼容的旧初始化结构及已撤回旧增量版本，不因版本数字相同而返回虚假的安装成功。拒绝 SHALL 保留表、数据和版本标记；dirty SHALL 继续按原规则阻断。

#### Scenario: 旧初始库版本同为一
- **WHEN** 旧 version=1 数据库缺少整合基线必须的流程结构
- **THEN** 返回明确不兼容错误，旧记录、版本和数据保持，不自动重放 0001

#### Scenario: 旧开发增量库
- **WHEN** 当前单版本迁移源连接整合前 version=2 的开发库
- **THEN** 返回明确不兼容错误，不降版本或清库

#### Scenario: 新基线重复执行
- **WHEN** 已完整安装的新 version=1 数据库包含数据并再次执行迁移
- **THEN** 成功且结构、版本和数据不变

#### Scenario: 历史数据格式验证
- **WHEN** 将固定历史合成账号、身份密文和令牌数据装载到整合后初始结构
- **THEN** 原 ID、密文解密、摘要查找及有效 access/refresh 仍可使用，不把该验证称为旧数据库原地升级
