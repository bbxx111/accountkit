# Verification

本文记录 `add-purged-user-feed` 在分支 `feat/purged-user-feed` 上的实际检查（2026-10-27）。原始输出保存在被忽略的 `.test-output/verify/feed-*`、`.test-output/tests.jsonl`、`.test-output/accountsvc-tests.jsonl` 中。

## 执行方式

- 按[实现计划](../../../../docs/superpowers/plans/2026-10-27-add-purged-user-feed.md)在本会话内顺序实施，未使用逐任务子代理：迁移 → 写入/读取 → 保留期 → accountsvc 路由之间依赖紧密。
- 每组都先写失败测试，再写实现（TDD）。
- 全部实现完成后，由一个独立审查代理复核整条分支。
- 依赖使用本任务专门创建的一次性 Docker 资源：PostgreSQL 17.4 容器 `accountkit-verify-pg-feed`（合成凭据，只绑定回环端口；库 `accountkit_strict`、`accountkit_recsrc`、`accountkit_recdst`）与 Redis 7 容器 `accountkit-verify-redis-feed`。Linux 验证镜像由 `tests/Dockerfile` 构建。结束后容器、网络、卷与镜像均已删除。

## 授权与迁移

经用户明确要求“直接改 0001、不新增 0002”，当前 `0001` 新增 `purged_user`，down 先删除该表。迁移历史的固定哈希如下：

| 对象 | up | down |
|---|---|---|
| v0.6.0 pin（`ec6802cd221db3ce107e79bac92174bc35a0ba7d`） | `4c6b565e…09c5` | `2f26c7dc…1f9b` |
| 当前 `initialFrozen` | `ef2d1f22…88b6` | `81254172…90d5` |

AGENTS.md 与 `docs/migrations.md` 记录了本轮限定授权。

`TestPurgedUserMissingV060Rejected` 用 `git show <v0.6.0 提交>` 安装旧结构，验证被无写拒绝且 dirty 优先。漂移拒绝覆盖以下 11 种结构：

- 缺唯一约束；
- 非 identity、BY DEFAULT；
- 缺 CHECK；
- 可空；
- 类型不符；
- 缺索引；
- 多列；
- CACHE、INCREMENT、CYCLE 漂移。

## 已运行检查

| 检查 | 结果 |
|---|---|
| `go build ./...`、`go vet ./...` | 通过 |
| `go test -count=1 -json ./...`（Windows，连接真实数据库） | 1118 通过；2 个失败是本变更预期影响的既有断言（维护任务数 6→7、默认 schema 表清单需加入 `purged_user`），已修正并复跑通过 |
| `bash scripts/check-generated.sh`（sqlc v1.31.1） | 通过 |
| `bash scripts/check-migrations.sh` | 通过 |
| `openspec validate --all --strict` | 通过 |
| `bash scripts/verify.sh`（Linux 镜像，最终提交 `6f3531b`） | 退出码 0：race 下 1138 通过、无数据竞争；必需测试门禁通过；pg_dump/pg_restore 恢复演练三阶段通过。2 个 skip 均为预期：`TestRecoveryFixture`；`TestProfileAttributesUnreadableFile`（容器以 root 运行，无法构造不可读文件） |
| `bash scripts/verify-accountsvc.sh`（Linux 镜像，最终提交） | 退出码 0：race 下 127 通过 |

`TestServiceIntegrationPurgedUserFeed` 在两套门禁中均实际执行并通过，验证了以下行为：

- 内省凭据访问拉取接口返回 401；
- 真实进程注销账号后，冷静期结束、维护任务完成清理，拉取接口返回该 user_id；
- 追平后游标稳定，重复拉取结果相同；
- 内省对该账号返回 inactive；
- 进程输出不含凭据。

并发正确性由以下两项测试覆盖：

- `TestPurgedUserFeedConcurrentPurgeNoGaps`：4 个并发 purge 与持续拉取，结果无遗漏、无重复；
- `TestPurgeWaitsForPurgedUserLock`：确定性测试。**已做变异验证**：移除表锁后该测试失败，恢复后通过。

## 审查结论

独立审查代理复核整条分支，无 Critical 问题。它从 PostgreSQL 语义层面确认了“无漏读”论证成立：READ COMMITTED 下，SHARE ROW EXCLUSIVE 表锁自冲突，且先提交可见、后释放锁。审查还确认无死锁环、保留期清理不影响游标、预检与 DDL 一致、pin 与哈希正确。

处理结果（提交 `6facb83`、`6f3531b`）：

**Important**

1. `audit/db/models.go` 的 sqlc 产物未提交（audit 目标共享 migrations schema），导致 `check-generated` 在 HEAD 失败。已提交。
2. 新旧版本混跑风险：旧二进制的预检会接受新结构，但其 purge 不写已清理记录，导致对应账号永久缺席拉取清单。已在迁移手册与兼容说明中明确警告，要求部署方阻止混跑和回退。
3. 游标在数据库恢复或重新部署后可能回退。已实现检测：`ListPurgedUsers` 对超过 `pg_sequence_last_value` 的游标返回 `ErrPurgedUserCursorAhead`，HTTP 为 400 `INVALID_PAGE_TOKEN`，业务方据此从头重拉。新增测试 `TestListPurgedUsersRejectsCursorAheadOfFeed` 与 `TestPurgeFeedCursorAheadAfterRestore`。design D8 记录了这项检测在“追平后才拉取”时的局限。

**Minor**

1. 预检增加序号选项校验。
2. 原并发测试在无锁时也可能通过，因此补充了确定性锁测试，并让并发测试遇到 purge 错误时失败。
3. 更新以下过时内容：AGENTS.md 与迁移手册的 pin 列表、`RunMaintenanceOnce` 的任务顺序注释、`docs/openapi.md` 的范围说明、design D6，以及游标文案（游标不透明、不防伪造）。
4. 必需测试补充 v0.6.0 pin 测试与新增的 user 测试。purgefeed、basicclients 沿用 introspection 子包惯例，不列入必需测试。
5. `cleanup_purged_users` 的指标归入 `other`，是有意取舍：指标规范固定了六个任务标签。已写入 design 与 `docs/metrics.md`。

## 未验证项

- **跨 PostgreSQL 大版本**：未在 PostgreSQL 18 上验证。审查指出，既有预检的约束计数风格在 PG18 可能需要调整；这是既有问题，不在本变更范围内，目标版本为 PG17。
