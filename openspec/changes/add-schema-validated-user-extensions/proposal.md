# Proposal

## Why

accountkit 已提供显示名，但宿主尚无受约束的账号用户资料属性存储入口。宿主需要自行定义属性及类型，并能安全地部分更新；单纯增加任意 JSONB 字段无法保证数据结构、并发修改和注销清理的一致性。

## What Changes

- 增加可选的宿主 JSON Schema 配置，由 accountkit 在初始化时离线编译；采用 Draft 2020-12 和明确的账号资料使用约束。
- 增加宿主 Go 读取、部分更新接口。JSON Merge Patch 按 RFC 7396 合并，完整候选资料经 Schema 校验后原子保存；仅使用数据库账号行锁，锁内读取最新资料、合并并完整校验；不提供 revision、expected_revision 或版本冲突。
- 在 `user_account` 增加 `profile_attributes` JSONB 单列，复用账号 update_time；通过后续生产迁移升级，不改写 v0.4.0 的 `0001`。
- 普通写入仅允许 ACTIVE。冷静期保留资料，恢复沿用原值，最终匿名化在既有事务内清空，失败整体回滚。
- 明确容量、数字精度、未知属性删除、错误脱敏、Schema 演进和混合部署边界；新增单元、真实数据库及并发验收。
- 本次仅新增宿主 Go 能力；不向现有 HTTP DTO、JWT、内省和批量公开资料自动添加扩展内容，不新增上传、业务权限或在线 Schema 管理系统。

## Capabilities

### New Capabilities

- `user-profile-extensions`: 宿主声明的用户资料属性、离线 Schema 校验、Merge Patch、行锁串行更新、访问边界及资料生命周期。

### Modified Capabilities

- `embedded-auth-package`: 将可选用户资料属性纳入账号注销、恢复和匿名化的事务契约。
- `versioned-auth-storage`: 在已发布 v0.4.0 初始结构上通过生产递增迁移增加用户资料属性，并明确合法新 version 2 与已撤回历史 version 2 的区别。
- `migration-safety`: 用正式非空 v0.4.0 升级与备份恢复验收替代本次仅测试下一版本的路径，明确 v0.4.0 已发布初始定义不可改写。

## Impact

- 公开配置、`Auth.Users()` 提供的 `user.Service`、新增专用资料类型及领域错误；现有 HTTP 必需接口方法集保持。
- `user/query.sql`、sqlc 生成结果、生产迁移、结构预检、固定历史检查与恢复夹具。
- `user` 生命周期、异步审计接入及资料隐私覆盖测试。
- 评估并固定 `github.com/santhosh-tekuri/jsonschema/v6` 校验依赖；不将第三方校验器类型作为公开 API。
- README、兼容指南、迁移手册、必需测试清单与本变更验收记录。结构升级后不得未经验证回退 v0.4.0 应用；旧数据库和旧数据不自动改写到新的宿主 Schema。

## 本轮已确认修订

54afa4b 的实现尚未发布，本轮直接修订现有变更及未发布 `0002_add_profile_attributes`（沿当前文件名修订单列，version 2 保持）。属性仅用于 bio、locale、timezone、pronouns 等用户资料，禁止宿主业务状态、权益、权限；仅保留 JSONB，移除 Schema 版本、指纹及独立更新时间。`0001`、历史标签与冻结源不变。旧临时 version 2 无写拒绝，不自动转换、改版本或清库；旧验收仅证明旧实现。

## 单列配置演进

宿主保证同一数据库实例的 Schema 配置一致。库不保存逐行 Schema 身份，不提供 SchemaMismatch 错误。读取只检查通用存储边界；更新按当前 Schema 校验完整合并结果，无变化请求也不得绕过。新增可选属性、放宽约束、修改注解可直接更新配置。收紧规则可能使旧资料完整结果不合法，更新拒绝且原资料与账号 update_time 保持；同次补丁可修复后保存。移除声明属性后，遗留属性仍不能以未知属性 null 删除，须通过过渡 Schema 或专项迁移清理；库不自动丢弃字段。
