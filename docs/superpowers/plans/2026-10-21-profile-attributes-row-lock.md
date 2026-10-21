> 历史计划：已由[单列计划](2026-10-21-profile-attributes-single-column.md)替代；下文保留原文供追溯，不作为当前实现要求。

# 用户资料属性与数据库行锁 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development。按任务实施并独立审查；本轮不自动提交、推送或归档。

**Goal:** 将已实现但未发布的扩展资料明确为 `profile_attributes`，移除 revision，使用数据库账号行锁串行合并更新。

**Architecture:** 沿用既有 Read Committed 领域事务，获取账号行锁后读取最新状态和资料，再执行 RFC 7396 合并及完整 Schema 校验。每次成功变化写入资料、Schema 标识和时间；并发不检查调用方版本，不添加 Redis 锁。同属性以后执行的事务为准，不重叠属性分别保留。

**Tech Stack:** Go 1.26.5、PostgreSQL 17、pgx/v5、sqlc 1.31.1、既有 Draft 2020-12 校验器。

**Spec:** [变更设计](../../../openspec/changes/add-schema-validated-user-extensions/design.md) 及该变更全部增量规格；本计划修订前一实施计划的命名、资料边界及并发模型，前次验收仅证明旧提交。

## Global Constraints

- 用户已确认 `profile_attributes` 命名及仅存用户资料；宿主业务属性、权益、权限、凭据保持原领域归属。
- 移除数据 revision、ExpectedRevision、资料版本冲突错误、审计 revision；SchemaVersion、SchemaFingerprint、UpdateTime 保留，Schema 不匹配仍拒绝。
- 四列为 `profile_attributes`、`profile_attributes_schema_version`、`profile_attributes_schema_fingerprint`、`profile_attributes_update_time`；三个 Schema 基础 CHECK 名称须不超过 PostgreSQL 63 字节，使用清楚缩写。
- `0001`、已发布 SQL/tag、冻结源不可改；修订本 change 未发布的 `0002_add_profile_attributes`；本次命名维护统一文件路径为 profile_attributes，SQL 字节和 version 2 不变。旧五列开发 v2 和 v1 残留必须无写拒绝，不能自动迁移、Force 或清库。
- 无变化不更新时间、不初始化 Schema 标识、不审计。清空后保留绑定标识；匿名化清空内容和标识，曾使用资料记录清理时间，从未使用保持 NULL；失败回滚全部字段。
- 不新增 HTTP 路由、DTO 字段、JWT 或内省资料，不增加认证查询；不修改客户端 ClientInfo.Extensions。
- 只改本轮范围，sqlc 结果仅生成，测试材料留 `.test-output/profile-attributes-row-lock/`；不提交或发布。

## Review Focus

1. 同账号不同属性请求必须都成功并保留两项；锁等待后不得使用旧快照。
2. 同属性、数组整体替换及父对象删除按实际锁序生效；后一个补丁仍须验证完整结果，失败不破坏前一个提交。
3. 删除 revision 后，首次写入/清空/匿名化的元数据不变量仍能区分；无变化和失败不写审计。
4. 迁移拒绝旧五列 v2、混合新旧列及 v1 残留，完整基础结构与时间精度保护保留；约束名不得静默截断。
5. 不得把 ClientInfo.Extensions 或历史归档资料批量重命名；恢复夹具和必需测试清单覆盖新契约。

## Task 1: 规格与接入文档

**Files:** 现有 change 工件、README、docs/compatibility.md、docs/migrations.md、前一计划。

- [x] 按已确认决定统一名称、四列、锁内最新合并、用户资料边界；旧验证明确标为历史。
- [x] 新增修订任务，检查活跃工件没有仍要求 revision 的冲突文本。

## Task 2: 存储、宿主 API 与行为回归

**Files:** migrations/0002_add_profile_attributes.*、migrations/profile_attributes*、user/query.sql、user/service_profile_attributes*、user/profile_attributes.go、user/service.go、user/repo_test.go、user/service_import_test.go；config.go、accountkit.go、accountkit_profile_attributes_test.go、examples/embedded/profile_attributes.go；internal/profileattributes/；recovery_profile_attributes_test.go、recovery_test.go、scripts/required-tests.txt；sqlc 生成 user/db/ 与 audit/db/。

**Interfaces:** `ProfileAttributesConfig{SchemaVersion, Schema}`；`ProfileAttributesView{Data, SchemaVersion, SchemaFingerprint, UpdateTime}`；`ProfileAttributesPatch{Document}`；`Config.ProfileAttributes`、`user.Deps.ProfileAttributes`；`GetProfileAttributes(ctx,userID)`、`PatchProfileAttributes(ctx,userID,patch,meta)`。错误 `ErrProfileAttributesNotConfigured`、`ErrProfileAttributesSchemaMismatch`；沿用安全校验错误结构，改名 `ProfileAttributesValidationError/Violation`。不保留未发布旧名称兼容入口。内部文件路径可保留，领域名统一；客户端描述 Extensions 保持。

- [x] 先增加真实数据库行为测试：旧接口使用同一 expected revision 的两个不重叠补丁，断言都成功且两项保留，运行确认旧实现因 revision 冲突失败。
- [x] 迁移去 revision 并重命名四列及基础 CHECK，预检识别新结构且拒绝旧/混合结构，生成 sqlc。
- [x] 公开 API/配置/领域/审计重命名，移除 revision 检查、CAS、溢出分支及计数；保留锁、状态、Schema 标识与安全错误处理。
- [x] 将行为测试转为新 API；扩展锁等待和同字段/嵌套对象/数组/完整结果校验并发覆盖。调整生命周期、import、no-op、隐私、审计、恢复和上限测试；保留等价保护，不用删除测试掩盖失败。
- [x] 必需测试清单仅对重命名逐项映射、失效 revision 用例用锁语义等价替代；更新示例为无需读取版本的补丁。
- [x] 运行相关真实 PostgreSQL 测试、go vet、gofmt、sqlc 一致性和历史迁移检查，记录 RED/GREEN。
- [x] 独立规格及质量审查通过后结束任务。

## Task 3: 综合验证和最终审查

**Files:** change/verification.md、tasks.md、本计划；忽略的测试日志及临时资源。

- [x] 在本任务隔离数据库/Redis上运行 GOWORK=off build、vet、全量测试及 Linux race；核对必需测试实际执行，TestRecoveryFixture 普通套件预期 skip。
- [x] 独立空源/目标数据库执行真实恢复演练，检查完整资料/Schema/时间及匿名化，保留源不变证据。
- [x] `sqlc generate` 一致性、历史迁移、`openspec validate --all --strict`、差异与格式检查。
- [x] 独立审查本轮相对 54afa4b 的完整差异，解决实质问题；更新验收记录及任务状态。
- [x] 仅清理本任务标签确认的临时资源，保留日志；报告完成及未验证范围。
