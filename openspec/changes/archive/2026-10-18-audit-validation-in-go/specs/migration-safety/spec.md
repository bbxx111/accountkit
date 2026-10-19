## ADDED Requirements

### Requirement: 授权调整初始审计校验职责

本次 SHALL 按用户明确授权直接修改当前未发布 0001 的审计校验定义，移除五个自定义校验函数及相应业务 CHECK，保留基本结构约束，并同步当前初始 SQL 固定哈希；初始化版本 SHALL 保持 1，不新增生产迁移。其他表 CHECK、索引定义、固定源快照、manifest 和正式历史标签 SHALL 保持。该授权 SHALL 不成为后续日常改写初始 SQL 或绕过历史检查的开关。

#### Scenario: 新空实例初始化
- **WHEN** 对新空 schema 执行迁移
- **THEN** 安装无自定义审计校验函数的完整初始结构，version=1 且 dirty=false

#### Scenario: 旧同号库重复迁移
- **WHEN** 包含旧审计校验函数的完整十列 version=1 实例重复执行 Up
- **THEN** 不重放 0001、不自动删函数或约束、不清理数据或修改版本；需新定义时由操作者核验后准备新空资源
