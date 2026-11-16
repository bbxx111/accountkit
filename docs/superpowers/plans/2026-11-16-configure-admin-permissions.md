# 可配置管理权限 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 accountsvc 提供显式角色权限映射、资源级通配符和环境变量配置，兼容现有嵌入宿主。

**Architecture:** 管理路由声明类型化具体权限；可选 PermissionVerifier 优先，旧宿主保留 RequireRole。accountsvc 从显式绑定编译不可变策略，按 JWT 角色求权限并集。

**Tech Stack:** Go 1.26.5、Chi、现有 JWT/JWKS 验证器、PostgreSQL 17、Redis、现有 OpenAPI/SDK 验证工具；不新增运行依赖。

**Spec:** [OpenSpec设计](../../../openspec/changes/archive/2026-11-16-configure-admin-permissions/design.md)、同目录 proposal/tasks 与三份增量规格为唯一需求来源。

**Status:** OpenSpec 19/19任务已完成并归档。以下步骤状态据既有实现、审查和集成证据补齐，实际范围及未验证项见[验收记录](../../../openspec/changes/archive/2026-11-16-configure-admin-permissions/verification.md)。

## Global Constraints

- 权限名为资源.操作，不加 account 命名空间；仅支持已知资源组.*，新版本启动包含未来同组权限。
- 无 builtin/custom 或隐式 operator/super-admin 授权；空映射为已认证操作403。
- 保持 AdminVerifier.RequireRole 和成对主体注入；新拒绝不回退旧接口。
- 管理与服务权限目录独立；管理 scope 不参与授权，服务 scope/主体行为保持。
- 不改SQL、冻结迁移、路由、DTO或领域规则；相关重复字面量按语义集中维护。
- 按AGENTS.md在可独立验证的步骤完成并通过相关检查/审查后做本地提交；推送、合并、tag、发布和归档分别按授权执行。子代理不派生代理，使用现有任务分支，TDD记录写入 .test-output/admin-permissions/。

## Review Focus

- 包装验证器同时实现新旧接口时，新拒绝和空策略必须不可被旧角色放行。
- 别名含下划线、残留空值环境字段不得绕过校验；NAME中的逗号不是分隔符。
- identities.*只包括管理reveal，不跨到服务resolve；users.*不包含会话和审计。
- 配置和目录返回值被修改不能影响已装配策略或其他实例。
- 所有401/403/503、拒绝审计次数、reveal错误no-store及旧宿主行为要保持。

## Task 1: 管理目录、宿主扩展与显式策略（OpenSpec 1.1–1.2、2.1–2.3）

**Files:** 新建 httpapi/admin/permissions.go、permissions_test.go；修改handler.go/accountkit.go；新建internal/accountsvc/adminauth/permissions.go及测试；修改config.go/claims.go/verifier.go与已有测试。

**Interfaces:** Permission string类型、10个Permission常量、Permissions() []Permission独立副本；PermissionVerifier.RequirePermission(Permission)中间件；根AdminPermission/AdminPermissionVerifier别名；adminauth.RoleBinding{Name string, Permissions []string}，Config.RoleBindings []RoleBinding。

- [x] 写目录/路由优先级测试并运行，确认缺少功能导致失败；对照设计10权限11路由。
- [x] 实现目录与可选能力检测，保留旧接口和角色常量；新路径全部精确权限，禁止结果回退。
- [x] 写策略验证/通配符/深拷贝/空策略/多角色/scope无效用测试，运行确认失败。
- [x] 实现RoleBinding验证、编译与RequirePermission；同语义常量归属现有模块，删除内部super-admin继承；用测试目录验证未来扩权。
- [x] GOWORK=off go test -count=1 ./httpapi/admin ./internal/accountsvc/adminauth，审查规格与质量，报告TDD命令、结果与文件。

## Task 2: 环境装配、真实服务与SDK夹具（OpenSpec 3.1–3.3、部分5.2）

**Files:** 新建internal/accountsvc/config_admin_roles.go及测试；修改config.go、fixtures_integration_test.go、run装配（如需）、sdks/testsupport/fixture/accountsvc.go；新增真实管理授权集成测试并更新scripts/accountsvc-required-tests.txt。

**Interfaces:** 消费Task1的RoleBinding；loadAdminRoles()无网络，config.Admin.RoleBindings赋值；既有run verifier始终拥有新能力。

- [x] 先写配置矩阵并运行观察失败，覆盖全部规范字段/重复/空值/精确匹配/禁用/migrate。
- [x] 实现预期变量集合扫描，CSV规则明确区分别名拒绝重复与权限去重；复用envPrefix、保留服务CSV契约。
- [x] 夹具显式注入原角色映射；写真实11路由授权、空策略与重启撤权测试，保留领域失败与认证隔离。
- [x] 运行内部accountsvc和SDK fixture测试；真实依赖验证交给Task3统一执行，缺依赖不能标通过。
- [x] 审查配置/授权规格与质量，报告文件和TDD证据。

## Task 3: 文档协议与全量验证（OpenSpec其余任务）

**Files:** docs/accountsvc.md、go-integration.md、compatibility.md、openapi.md、openapi/admin.json、README、AGENTS、deploy/accountsvc/.env.example、scripts/check-openapi.cjs；必要共享常量；变更verification/tasks。

- [x] 补新接口宿主与角色策略文档、通配符和显式迁移示例；Compose不注入默认绑定。
- [x] OpenAPI增加x-required-permission、改x-legacy-required-role，同步检查器；覆盖登记按实际变化核对。
- [x] 审查相关重复字面量和流程并整理（不扩大范围），运行共享服务认证回归。
- [x] 建隔离Docker网络/PG17/Redis和Linux验证镜像，执行Go build/vet/test/race、verify-accountsvc.sh及适用verify.sh；测试日志写任务目录。
- [x] 固定Git快照构建无replace独立旧/新宿主；Node24.21.0/pnpm11.24.0执行TS单元、typecheck、真实服务；消费者夹具按影响验证。
- [x] 独立全分支审查、修复并复核，OpenSpec严格校验、链接和diff检查；仅在证据充分后勾选对应tasks并刷新apply状态。

## Execution Notes

- .worktrees被忽略，但创建新Git引用因沙箱拒绝；按using-git-worktrees回退在现有任务分支执行。
- 当前默认Go缓存不可写；验证使用提权或任务缓存。Docker可经正常提权访问。
- 本计划中的任务授权包含分阶段本地提交；推送、合并、tag、发布、归档和产品部署分别执行，不因本地提交自动获得授权。
