## 1. 迁移与基线（直接修改 0001）

- [x] 1.1 在当前 `0001_init.up.sql` 末尾新增 `purged_user` 表与 `idx_purged_user_purge_time`（定义见 design D1），并在 `0001_init.down.sql` 中最先 `DROP TABLE IF EXISTS purged_user`。验证：新空 schema Up 后为 version=1/dirty=false，表结构与 design 一致，重复 Up 无变化。
- [x] 1.2 新增 `migrations/purged_user_baseline.go`（只读预检）并接入 `checkBaseline`。验证：数据库测试用 `git show v0.6.0:migrations/0001_init.up.sql` 安装旧结构后 Up 被拒绝（`ErrIncompatibleBaseline`），catalog、数据与 version/dirty 不变；缺列、类型错误、缺约束的结构同样被拒绝；dirty 优先。
- [x] 1.3 `internal/migrationcheck`：`publishedInitialReleases` 增加 v0.6.0 pin（提交 `ec6802cd221db3ce107e79bac92174bc35a0ba7d`，使用原 `initialFrozen` 的两个哈希），`initialFrozen` 更新为新文件的哈希；补充 `published_initial_test` 断言。验证：`bash scripts/check-migrations.sh` 与 migrationcheck 测试通过。
- [x] 1.4 AGENTS.md“迁移安全”新增本轮限定授权条目，并同步 `docs/migrations.md` 的当前初始基线说明。验证：内容与 migration-safety 增量规格一致。

## 2. 库：写入与读取

- [x] 2.1 `user/query.sql` 新增 `InsertPurgedUser`、`ListPurgedUsersAfter`、`DeletePurgedUsersBefore`（分批）；执行 `sqlc generate`。验证：`bash scripts/check-generated.sh` 通过，生成差异只包含这三个查询。
- [x] 2.2 在 `purgeOne` 事务末尾（宿主 Anonymizer 之后）先 `LOCK TABLE purged_user IN SHARE ROW EXCLUSIVE MODE`，再插入记录。验证：数据库测试覆盖清理成功写入一条记录、Anonymizer 失败时整体回滚且无记录、记录不含身份数据。
- [x] 2.3 实现 `Service.ListPurgedUsers(ctx, after, limit)`。验证：数据库测试覆盖从头读取、续读、追平后返回等价游标、limit 边界（0 与 1001 被拒绝）。
- [x] 2.4 并发正确性：并发执行多个清理，同时用游标循环拉取。验证：数据库测试确认最终读到全部记录，序号顺序与提交顺序一致，没有遗漏。

## 3. 保留期

- [x] 3.1 增加 `Config.PurgedUserRetention` 与环境变量 `PURGED_USER_RETENTION`：0 表示长期保留，大于 0 时不得小于 720h。验证：config 测试覆盖默认值、合法值、过短值被拒绝（错误指出变量名）。
- [x] 3.2 保留期大于 0 时注册维护任务 `cleanup_purged_users`，分批删除过期记录。验证：数据库测试确认只删除超出保留期的记录，未配置时不删除。

## 4. accountsvc 路由

- [x] 4.1 把内省 handler 的客户端校验抽成内部共享函数；新增 `ACCOUNTSVC_PURGE_FEED_CLIENTS` 解析，拒绝与内省列表重复的秘密，migrate 不解析。验证：config 测试与内省回归测试。
- [x] 4.2 新增 handler 与路由 `GET /v1/purgedUsers`：Basic 鉴权（401 带 challenge、AIP 错误体）、未配置时返回 503 `PURGE_FEED_NOT_CONFIGURED`、`page_token` 与 `page_size` 校验、`no-store`、`next_page_token` 始终返回。验证：handler 单元测试；内省凭据访问返回 401。
- [ ] 4.3 进程级集成测试：账号注销并到期清理后，用拉取凭据读到该 user_id，续拉为空，游标稳定。验证：`verify-accountsvc.sh`。

## 5. 文档

- [ ] 5.1 更新以下文档：
  - `docs/accountsvc.md`：已清理用户拉取契约、凭据、保留期、业务方幂等清理与补拉要求；
  - `docs/gateway-integration.md`：内部入口阻断；
  - `docs/compatibility.md`：0001 调整与旧库拒绝；
  - README：库 API 与保留期配置；
  - `.env.example`、Compose 透传。

  验证：链接检查；文档与规格一致。

## 6. 验证与记录

- [ ] 6.1 运行 `go build`、`go vet`、`go test ./...`（连接真实数据库）、`check-generated.sh`、`check-migrations.sh`、`openspec validate --all --strict`。
- [ ] 6.2 在 Linux 镜像中运行 `verify.sh` 与 `verify-accountsvc.sh`，含恢复演练；新增的必需测试同步写入 required-tests 列表；由一个独立审查代理复核整条分支。
- [ ] 6.3 编写 `verification.md`。
