# 已清理用户拉取 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 账号清理事务内写入持久的已清理记录，accountsvc 以独立凭据提供按游标分页的拉取接口。数据库变更直接修改当前 0001（用户明确授权）。

**Architecture:**
- 当前 `0001` 新增 `purged_user(user_id PK, seq IDENTITY UNIQUE, purge_time)`。
- `purgeOne` 在事务末尾加表锁（SHARE ROW EXCLUSIVE）后插入记录。
- `user.Service.ListPurgedUsers` 按 `seq` 键集读取。
- 可选保留期维护任务。
- accountsvc 新增 `/v1/purgedUsers` handler：抽取内省的 Basic 客户端校验共用；未配置时返回 503。
- 迁移预检拒绝缺表的旧 v1；v0.6.0 pin；新的 `initialFrozen`。

**Tech Stack:** Go 1.26、pgx/v5、sqlc 1.31.1、golang-migrate、chi。

**Spec:** `openspec/changes/archive/2026-10-27-add-purged-user-feed/`（proposal.md、design.md、specs/、tasks.md）

## Global Constraints

- 表定义逐字遵循 design D1；down 中最先删除 `purged_user`。
- v0.6.0 pin：提交 `ec6802cd221db3ce107e79bac92174bc35a0ba7d`。哈希使用原 `initialFrozen`：up 为 `4c6b565e…09c5`，down 为 `2f26c7dc…1f9b`。
- 旧结构只能从 git 历史（`git show <commit>:migrations/0001_init.up.sql`）安装，不得从今天的 SQL 推导。
- 拉取接口的约定：
  - `page_size` 默认 100，范围 1–1000；
  - `page_token` 为 base64url 编码的 `v1:<seq>`；
  - 非法输入返回 400 `INVALID_ARGUMENT` / `INVALID_PAGE_TOKEN`（`page_size` 非法时返回 `INVALID_ARGUMENT`）；
  - 未配置时返回 503 `UNAVAILABLE` / `PURGE_FEED_NOT_CONFIGURED`；
  - 401 带 `WWW-Authenticate: Basic realm="accountsvc-purge-feed"`；
  - 响应设置 `Cache-Control: no-store`。
- 保留期：0 表示不清理；大于 0 时不得小于 720h，变量为 `PURGED_USER_RETENTION`。
- 不 Force、不改版本、不清库；所有测试资源一次性使用。

## Review Focus

- 并发清理与持续拉取同时进行时没有遗漏（Task 2 并发测试）。
- Anonymizer 失败时不留下记录（Task 2）。
- 旧 v0.6.0 结构被无写拒绝，dirty 优先（Task 1）。
- 内省凭据不能访问拉取接口，同一秘密不得同时出现在两个列表中（Task 4）。
- 追平时游标稳定，重复拉取结果幂等（Task 2 与 Task 4）。

---

### Task 1: 迁移与基线（tasks 1.1–1.4）

**Files:** `migrations/0001_init.{up,down}.sql`；新建 `migrations/purged_user_baseline.go` 与 `migrations/purged_user_test.go`；修改 `migrations/baseline.go`、`internal/migrationcheck/main.go` 与 `published_initial_test.go`、AGENTS.md、`docs/migrations.md`；同时调整受影响的历史目录比较测试，在“历史结构 + 已授权 DDL”的组合中加入 `purgedUserDDL`。

- [ ] 先写测试：
  - 新安装后 `purged_user` 结构与 design 完全一致；
  - 用 v0.6.0 tag 的 SQL 安装旧结构后执行 Up，被拒绝且目录不变；
  - 结构不完整（缺唯一约束、seq 不是 identity）同样被拒绝；
  - dirty 优先。
- [ ] 修改 SQL，实现 `checkPurgedUserBaseline`（使用物化的目录 CTE，与既有预检风格一致），接入 `checkBaseline`。
- [ ] 更新 pin 与 `initialFrozen`，记录 AGENTS.md 授权，同步迁移手册。
- [ ] 运行 `go test ./migrations/ ./internal/migrationcheck/`（连接真实数据库）与 `check-migrations.sh`，然后提交。

### Task 2: 写入与读取（tasks 2.1–2.4）

**Files:** `user/query.sql`；sqlc 生成代码；`user/service_purge.go`；新建 `user/purged_users.go` 与 `user/service_purged_users_test.go`。

**Interfaces:**
- `type PurgedUser struct{ UserID string; PurgeTime time.Time }`
- `func (s *Service) ListPurgedUsers(ctx context.Context, after int64, limit int) ([]PurgedUser, int64, error)`
- `const MaxPurgedUserPage = 1000`

查询：

```sql
-- name: InsertPurgedUser :exec
INSERT INTO purged_user (user_id) VALUES ($1);

-- name: LockPurgedUserForInsert :exec
LOCK TABLE purged_user IN SHARE ROW EXCLUSIVE MODE;

-- name: ListPurgedUsersAfter :many
SELECT user_id, seq, purge_time FROM purged_user WHERE seq > @after ORDER BY seq LIMIT @page_size;

-- name: DeletePurgedUsersBefore :execrows
DELETE FROM purged_user WHERE user_id IN (
  SELECT user_id FROM purged_user WHERE purge_time < @before ORDER BY purge_time LIMIT @batch_size);
```

- [ ] 测试：
  - 清理后存在一条记录；
  - Anonymizer 失败时不存在记录；
  - 分页、续拉、追平与 limit 边界；
  - 并发：8 个账号并发清理，同时循环拉取，最终集合等于全部账号，并且返回的 seq 严格递增。
- [ ] 实现，然后提交。

### Task 3: 保留期（tasks 3.1–3.2）

**Files:** `config.go`、`accountkit.go`（维护任务注册）、`user/purged_users.go`（`CleanupPurgedUsers`）及相应测试。

- [ ] 测试并实现，然后提交。

### Task 4: accountsvc 路由（tasks 4.1–4.3）

**Files:**
- 新建 `internal/accountsvc/basicclients/`：从 introspection 抽出 `Clients` 的解析与 `Authorized(r)`；
- 修改 `introspection/handler.go`，改为使用该包；
- 新建 `internal/accountsvc/purgefeed/handler.go` 及测试；
- 修改 `config.go`、`run.go`、`http.go`；
- 新建 `purge_feed_integration_test.go`。

- [ ] 测试并实现，然后提交。

### Task 5: 文档（task 5.1）；Task 6: 验证（tasks 6.1–6.3）

与上一个变更相同的流程：依次执行完整验证、独立审查、编写 `verification.md`，最后清理 Docker 资源。
