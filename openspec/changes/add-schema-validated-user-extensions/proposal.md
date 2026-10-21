# Proposal

## Why

accountkit 已提供显示名，但宿主尚无受约束的账号用户资料属性存储入口。宿主需要自行定义属性及类型，并能安全地部分更新；单纯增加任意 JSONB 字段无法保证数据结构、并发修改和注销清理的一致性。

## What Changes

- 增加可选的宿主 JSON Schema 配置，由 accountkit 在初始化时离线编译；采用 Draft 2020-12 和明确的账号资料使用约束。
- 增加宿主 Go 读取、部分更新接口。JSON Merge Patch 按 RFC 7396 合并，完整候选资料经 Schema 校验后原子保存；仅使用数据库账号行锁，锁内读取最新资料、合并并完整校验；不提供 revision、expected_revision 或版本冲突。
- 在 `user_account` 增加 `profile_attributes` JSONB 单列，复用账号 update_time；按本轮限定授权并入当前工作树 0001，撤回生产 0002，新安装 version 1；v0.4.0 tag 内 SQL 保持。
- 普通写入仅允许 ACTIVE。冷静期保留资料，恢复沿用原值，最终匿名化在既有事务内清空，失败整体回滚。
- 明确容量、数字精度、未知属性删除、错误脱敏、Schema 演进和混合部署边界；新增单元、真实数据库及并发验收。
- 本次仅新增宿主 Go 能力；不向现有 HTTP DTO、JWT、内省和批量公开资料自动添加扩展内容，不新增上传、业务权限或在线 Schema 管理系统。

## Capabilities

### New Capabilities

- `user-profile-extensions`: 宿主声明的用户资料属性、离线 Schema 校验、Merge Patch、行锁串行更新、访问边界及资料生命周期。

### Modified Capabilities

- `embedded-auth-package`: 将可选用户资料属性纳入账号注销、恢复和匿名化的事务契约。
- `versioned-auth-storage`: 当前初始定义整合单列资料，新安装 version 1；历史与当前分别固定，旧结构无写拒绝。
- `migration-safety`: 验证当前安装、重复执行、真实备份恢复、历史拒绝与独立测试下一版本。

## Impact

- 公开配置、`Auth.Users()` 提供的 `user.Service`、新增专用资料类型及领域错误；现有 HTTP 必需接口方法集保持。
- `user/query.sql`、sqlc 生成结果、生产迁移、结构预检、固定历史检查与恢复夹具。
- `user` 生命周期、异步审计接入及资料隐私覆盖测试。
- 评估并固定 `github.com/santhosh-tekuri/jsonschema/v6` 校验依赖；不将第三方校验器类型作为公开 API。
- README、兼容指南、迁移手册、必需测试清单与本变更验收记录。结构升级后不得未经验证回退 v0.4.0 应用；旧数据库和旧数据不自动改写到新的宿主 Schema。

## 本轮已确认修订

本轮用户明确要求“0002 并入 0001，无需考虑旧数据迁移”，仅授权把 `profile_attributes JSONB NOT NULL DEFAULT '{}'::jsonb` 单列合入当前工作树 0001，撤回生产 0002；新空 schema 安装 version=1/dirty=false，其他列、索引和业务行为保持。当前 initialFrozen 新哈希与 v0.4.0 已发布旧定义分别固定；v0.4.0 精确 tag `62f0799b6c6ef1603b33f81834d39033dd0d0c4d`、其他发布 tag/SQL 和冻结源保持。旧 v0.4.0 v1 缺资料列、其他历史 v1 及各代撤回 v2（含单列、四列、五列及混合资料 v2）均无写拒绝，dirty 优先，保留 catalog、数据及 version/dirty；不转换旧数据、不重放同号 SQL、不 Force、改版本或清库。本轮不提供旧数据迁移工具或升级承诺；后续真实增量从 0002 递增，测试 probe 使用 latest+1（当前为 2）。 旧验收仅证明对应历史实现。

## 单列配置演进

宿主保证同一数据库实例的 Schema 配置一致。库不保存逐行 Schema 身份，不提供 SchemaMismatch 错误。读取只检查通用存储边界；更新按当前 Schema 校验完整合并结果，无变化请求也不得绕过。新增可选属性、放宽约束、修改注解可直接更新配置。收紧规则可能使旧资料完整结果不合法，更新拒绝且原资料与账号 update_time 保持；同次补丁可修复后保存。移除声明属性后，遗留属性仍不能以未知属性 null 删除，须通过过渡 Schema 或专项迁移清理；库不自动丢弃字段。
