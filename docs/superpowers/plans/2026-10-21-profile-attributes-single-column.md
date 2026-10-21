# 单列用户资料属性 Implementation Plan

> 历史计划：本轮数据库定义及验收以[初始整合计划](2026-10-21-consolidate-profile-attributes-initial.md)替代。当前资料单列并入 0001、撤回生产 0002、新安装 version 1；下文保留原阶段记录，不代表旧库升级承诺或本轮验收。

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development。先行为测试、后实现，独立审查；本轮不提交、推送、合并或归档。

**Goal:** 按用户已确认决定，只保留 `profile_attributes JSONB`，移除逐行 Schema 版本、指纹及独立更新时间。

**Architecture:** 继续使用 Read Committed 账号行锁，锁内最新读取、RFC 7396 合并、按当前 Schema 校验完整结果并提交。无变化不写入；实际变化只更新资料与账号原 update_time。没有 Schema 身份绑定、revision、Redis 锁或自动资料迁移。

**Tech Stack:** Go 1.26.5、PostgreSQL 17、sqlc 1.31.1、既有 JSON Schema/精确 JSON 实现。

**Spec:** openspec/changes/archive/2026-10-21-add-schema-validated-user-extensions/ 的当前 proposal/design/specs/tasks；本计划取代前一行锁计划中元数据相关要求，原验收保留为历史。

## Global Constraints

- 最终只增加 `profile_attributes JSONB NOT NULL DEFAULT '{}'::jsonb`；未发布 0002 原路径可修订；0001、已发布 SQL/tag 和固定源保持。
- 配置 `ProfileAttributesConfig{Schema json.RawMessage}`，视图 `ProfileAttributesView{Data json.RawMessage}`；移除 SchemaVersion、SchemaFingerprint、独立 UpdateTime、ErrProfileAttributesSchemaMismatch 及其配置校验/行绑定。Patch/API 名称保持。
- 内部 Compile 只接收 Schema；移除版本/指纹机制，但保留精确数字、规范化容量检查及其他安全边界，不能因去掉 hash 而删掉必要资源限制。
- 读取只做通用存储边界检查，不套当前 Schema；更新完整结果按当前 Schema 校验，即使无变化也不能绕过校验。资料用途、状态与隐私规则保持。
- 宿主保证实例配置一致；新增可选属性、放宽约束、修改注解可直接写；收紧约束时无效旧资料更新拒绝但可在同次补丁修复。移除声明字段时未知删除仍拒绝，宿主需过渡 Schema/专门迁移，不自动丢字段。
- 旧临时四列/五列 v2、混合结构及 v1 残留无写拒绝，保留 catalog/数据/version/dirty；dirty 优先。基础账号结构和五个原时间列精度保护继续。
- 匿名化原事务清空 JSON；无需判断曾使用/时间标识，回调失败完整回滚。冷静期恢复保留 JSON。
- 成功审计仍使用 USER_PROFILE_ATTRIBUTES_UPDATED、固定服务 actor、账号目标和请求 ID，不保存资料、字段名、Schema 全文、版本/指纹或时间元数据；既有 OccurTime 保持。失败/no-op 不投递。
- 没有新 HTTP、JWT/内省字段、认证查询、动态 Schema 管理或通用导入能力；ClientInfo.Extensions 保持。
- sqlc 必须生成，不手改；必需测试入口不删减掩盖失败，失效元数据断言以新数据/账号时间/Schema 演进断言替代。

## Review Focus

1. 兼容定义调整可以更新；不兼容旧数据完整结果拒绝，原值/账号时间不变，同一补丁修复后可保存。
2. 去掉指纹不能移除 Schema 数字/容量预算；单列也不是任意 JSON 入口。
3. no-op、账号行锁等待及冻删竞争继续正确，匿名化和失败回滚不依赖已删字段。
4. 旧临时四列/五列 v2 不能因新单列存在被接管；纯数据备份恢复、账号时间和原历史保护不退化。
5. 审计/日志/DTO 不因简化泄露资料；活跃文档不得仍要求身份绑定或四列，旧证据须清晰标为历史。

## Task 1: 规格与文档

**Files:** 当前 change 工件、README、docs/compatibility.md、docs/migrations.md；旧计划仅增加替代提示。

- [x] 读取相关完整工件，按单列/当前 Schema 校验统一契约；新增 S1–S4 修订任务。
- [x] 更新 Schema 演进、配置一致性、未知字段清理及 API 迁移说明；原验证正文保持，顶部新增待验收节。

## Task 2: 存储、接口与行为测试

**Files:** migrations/0002_add_profile_attributes.*、profile_attributes_baseline.go/test；user/query.sql、profile_attributes.go、service_profile_attributes*.go、service.go、repo/import 测试；internal/profileattributes；config.go、accountkit*test、examples/embedded/profile_attributes.go、recovery*、required-tests.txt；生成 user/db/、audit/db/。

- [x] RED：先用现有可编译 API 保存资料，然后换同名不同定义但兼容的 Schema，断言后续补丁应成功；真实数据库复现原 SchemaMismatch。
- [x] 实施单列迁移、拒绝临时旧结构、生成 sqlc；移除配置及响应元数据、内部身份比较和审计元数据；保留全部安全检查。
- [x] GREEN：兼容变更、约束收紧失败/修复、无变化、当前 Schema 校验、读取历史、并发、账号时间、生命周期、隐私、导入和恢复夹具。
- [x] 更新必需测试映射，运行相关真实数据库及 JSON/schema 单元测试、vet、生成/历史检查；独立审查。

## Task 3: 完整验收

- [x] 独立任务资源运行 GOWORK=off build/vet、全量普通测试、Linux race 及必需测试门禁。
- [x] 独立空恢复源/目标执行真实备份恢复 seed/verify/source-unchanged，验证 JSON 和匿名化，保留证据。
- [x] OpenSpec strict、格式/差异、源哈希及最终独立审查通过；验收记录与任务状态反映当前事实。
- [x] 核验任务标签后仅清理本轮资源，保留宿主日志/备份；不自动提交或归档。
