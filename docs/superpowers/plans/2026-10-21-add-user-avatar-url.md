# 用户头像属性 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** 为现有基础账号资料增加 avatar_url，按用户授权直接修改当前 0001。

**Architecture:** 领域新增原子部分更新，HTTP 使用可选能力保持旧 Service 兼容。头像独立列参与资料返回、生命周期和当前结构预检。

**Tech Stack:** Go 1.26.5、PostgreSQL 17、pgx、sqlc 1.31.1、Chi。

**Spec:** [设计](../../../openspec/changes/archive/2026-10-21-add-user-avatar-url/design.md)及该 change 的全部 specs。

## Global Constraints

- 本次仅新增头像，不新增 username、上传、代理或第三方同步。
- 不新增 0002；只改当前初始头像列与当前哈希，保持历史标签、原 SQL、冻结源。
- GOWORK=off，sqlc generate；无手改生成代码、Force、旧数据转换或产品库操作。
- 头像最长 2048 字节、绝对 HTTPS、空字符串清空、nil 不更新，具体地址拒绝条件见规格。
- 用户已确认短设计与实施范围，按 AGENTS 默认子代理执行；使用当前任务分支，不提交、推送或归档。

## Review Focus

- 两字段任一非法不能留下部分写入或账号时间变化。
- 仅更新一字段不能覆盖同时提交的另一字段。
- 新原子更新等待账号锁后必须复核 ACTIVE，不能恢复已匿名化的头像。
- 旧 Service 只更新显示名继续可用，含头像但无能力必须整次 503。
- 旧同号资料 schema 拒绝且数据/catalog/version/dirty 不变；历史固定 SQL 不变。

### Task 1: 实现头像垂直能力

**Files:**
- Modify: migrations/0001_init.up.sql、migrations/profile_attributes_baseline.go、internal/migrationcheck/main.go、user/query.sql 及 sqlc 生成文件。
- Modify/Create: user/service_me.go、user/service_profile.go、user/service_profiles.go、user/admin.go、httpapi/enduser/me.go、handler.go、response.go、httpapi/admin/response.go、internal/profileattributes 保留字。
- Tests: user/service_avatar_test.go、httpapi/enduser/me_test.go、httpapi/admin/users_test.go、migrations/avatar_test.go、profile_attributes_test.go 及相关精确字段测试、scripts/required-tests.txt。
- Documentation（主代理配合）: README.md、docs/openapi.md、docs/go-integration.md、docs/compatibility.md、docs/migrations.md、AGENTS.md。

**Interfaces:**
- Produces: user.ProfilePatch{DisplayName *string, AvatarURL *string}；(*Service).UpdateProfile(context.Context,string,ProfilePatch)(Me,error)。
- Produces: Me/AdminUser/PublicProfile.AvatarURL string；HTTP avatar_url string；可选 enduser.ProfileUpdater，不增加必需 Service 方法。
- Retains: UpdateDisplayName 的现有调用兼容和行为；不重构认证流程。

- [x] 写行为测试，先运行证实缺失头像能力导致失败。HTTP 可先用 JSON 请求观察现有 NO_FIELDS/未知字段；迁移可断言安装结果头像列存在。
- [x] 领域先完整校验，withDomainTx 内锁最新账号并验证 ACTIVE，仅更新选定字段。复用显示名校验。非法 ID/未知账号/空 patch 分别正确处理。
- [x] 生成 SQL，补齐两面响应和公开资料隐藏、清理 NULL、扩展顶层保留字；新增头像能力缺失 503，保持旧 Service 编译兼容。
- [x] 更新初始 SQL 和当前哈希，预检拒绝缺列、错误类型、非空和默认值漂移。旧资料初始等价夹具显式追加头像后比较当前 catalog，增加原无头像 v1 独立拒绝证据。
- [x] PostgreSQL 测试覆盖设置/清除/省略/null、2048/2049 边界、非法 HTTPS、原子拒绝、并发字段保存、状态复核、冷静期保留和恢复、匿名化回滚、旧结构 dirty 优先。运行相关套件直到通过，记录 TDD 实际结果。
- [x] 更新本任务文档和必需测试清单，独立审查本任务并处理实质问题。

### Task 2: 全量验证和交付

**Files:** openspec/changes/archive/2026-10-21-add-user-avatar-url/verification.md、tasks.md。

**Interfaces:** 消费 Task 1 的全部变更与测试。

- [x] 运行 `GOWORK=off go build ./...`、`go vet ./...`、`go test -count=1 ./...`；用本任务 PostgreSQL 17 执行真实领域、迁移、并发验证。
- [x] 运行 sqlc 生成一致性、迁移历史和 `openspec validate --all --strict`。race 若 Windows 工具链不足使用 Linux 镜像，明确列出任何缺验。
- [x] 全分支独立代码审查，修复后定向复验，将结果和限制统一记录 verification.md，再更新 tasks。保留变更供用户审阅，不自动归档或提交。
