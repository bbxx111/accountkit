# Spec Delta

## ADDED Requirements

### Requirement: 身份类型文本化限定授权
本变更对当前 0001 的修改 SHALL 限于 identity.kind 的列类型（VARCHAR(32)）、COLLATE、格式 CHECK 及相关注释，并只更新当前 initialFrozen。已发布 tag 与其 SQL、v0.4.0 与 v0.5.0 的已发布定义、冻结源 SHALL 保持，历史检查继续独立固定各代基线。后续真实结构增量 SHALL 仍从 0002 开始。

#### Scenario: 历史检查
- **WHEN** 运行迁移历史检查
- **THEN** 已发布 tag 的 0001 哈希保持原值，当前 0001 与新的 initialFrozen 一致

#### Scenario: 旧同号库
- **WHEN** 已安装的旧 version 1 再次 Up
- **THEN** 不重放 0001，不自动转换列类型，不 Force、不改版本、不清库
