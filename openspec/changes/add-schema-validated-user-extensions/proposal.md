# Proposal

## Why

accountkit 已提供显示名，但宿主尚无受约束的账号附加资料存储入口。宿主需要自行定义属性及类型，并能安全地部分更新；单纯增加任意 JSONB 字段无法保证数据结构、并发修改和注销清理的一致性。

## What Changes

- 增加可选的宿主 JSON Schema 配置，由 accountkit 在初始化时离线编译；采用 Draft 2020-12 和明确的账号资料使用约束。
- 增加宿主 Go 读取、部分更新接口。JSON Merge Patch 按 RFC 7396 合并，完整候选资料经 Schema 校验后原子保存；使用独立 revision 拒绝过期写入。
- 在 `user_account` 增加 JSONB 扩展资料及对应 Schema 标识、revision、更新时间；通过后续生产迁移升级，不改写 v0.4.0 的 `0001`。
- 普通写入仅允许 ACTIVE。冷静期保留资料，恢复沿用原值，最终匿名化在既有事务内清空，失败整体回滚。
- 明确容量、数字精度、未知属性删除、错误脱敏、Schema 演进和混合部署边界；新增单元、真实数据库及并发验收。
- 本次仅新增宿主 Go 能力；不向现有 HTTP DTO、JWT、内省和批量公开资料自动添加扩展内容，不新增上传、业务权限或在线 Schema 管理系统。

## Capabilities

### New Capabilities

- `user-profile-extensions`: 宿主声明的账号扩展资料、离线 Schema 校验、Merge Patch、并发版本、访问边界及资料生命周期。

### Modified Capabilities

- `embedded-auth-package`: 将可选扩展资料纳入账号注销、恢复和匿名化的事务契约。
- `versioned-auth-storage`: 在已发布 v0.4.0 初始结构上通过生产递增迁移增加扩展资料，并明确合法新 version 2 与已撤回历史 version 2 的区别。
- `migration-safety`: 用正式非空 v0.4.0 升级与备份恢复验收替代本次仅测试下一版本的路径，明确 v0.4.0 已发布初始定义不可改写。

## Impact

- 公开配置、`Auth.Users()` 提供的 `user.Service`、新增专用资料类型及领域错误；现有 HTTP 必需接口方法集保持。
- `user/query.sql`、sqlc 生成结果、生产迁移、结构预检、固定历史检查与恢复夹具。
- `user` 生命周期、异步审计接入及资料隐私覆盖测试。
- 评估并固定 `github.com/santhosh-tekuri/jsonschema/v6` 校验依赖；不将第三方校验器类型作为公开 API。
- README、兼容指南、迁移手册、必需测试清单与本变更验收记录。结构升级后不得未经验证回退 v0.4.0 应用；旧数据库和旧数据不自动改写到新的宿主 Schema。
