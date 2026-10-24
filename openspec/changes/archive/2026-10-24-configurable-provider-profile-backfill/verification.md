# Verification

记录 `configurable-provider-profile-backfill` 在分支 `feat/configurable-provider-profile-backfill` 上的实际检查结果（2026-10-24）。原始输出保存在被忽略的 `.test-output/fast-backfill.jsonl`、`.test-output/tests.jsonl` 与 `.test-output/verify-backfill-20261024.log`。

## 执行方式

按任务 1–4 顺序实施并逐任务复核：任务 1（配置项）与任务 2（统一补填逻辑）首次复核即通过；任务 3（文档）经一轮措辞修正后通过。任务 4 为本记录对应的集成验证。

测试依赖为本机 Docker 中的 PostgreSQL 17.5 与 Redis 7。快速验证使用一次性数据库 `accountkit_backfill_20261024` 与 Redis DB 13；完整验证使用专为本次创建的 `accountkit_verify_backfill_20261024`（集成）、`accountkit_recsrc_backfill_20261024` / `accountkit_recdst_backfill_20261024`（恢复源/目标，均为空库）和开始前为空的 Redis DB 12。

## 已运行检查

- `GOWORK=off go build ./...`、`go vet ./...`：通过。
- `GOWORK=off go test -count=1 -json ./...`（设置 `SERVER_TEST_DB_DSN` 与 `ACCOUNTSVC_TEST_REDIS_URL`）：退出码 0。44 个包通过，1066 个顶层测试通过，0 fail，14 skip。
  - skip：13 个 `internal/accountsvc` 的 `TestServiceIntegration*` / `TestServiceProcessSIGTERM`（需 `scripts/verify-accountsvc.sh` 的服务环境，本变更未触及）以及预期 skip 的 `TestRecoveryFixture`。
  - `user` 包（真实 PostgreSQL）与根包 HTTP 契约测试均实际执行，包含新增的 5 个 `user` 补填测试与 `TestWeiboHTTPProfileBackfillDisabled`，无 skip。
- `openspec validate --all --strict`：37 项全部通过。
- 本变更无 SQL/sqlc 改动，未重新生成代码。

## 完整验证

使用 `tests/Dockerfile` 构建的 Linux 验证镜像（golang:1.26.5、PostgreSQL 17.11 客户端），容器加入数据库所在 Docker 网络并按容器名连接，运行 `bash scripts/verify.sh`：**退出码 0**。

- 迁移历史校验、build、vet 通过。
- `go test -race`：44 个包通过，1079 个顶层测试通过，0 fail。
- 必需测试门禁（`internal/testgate`）通过。
- pg_dump / pg_restore 恢复演练（seed、verify、source-unchanged 三阶段）通过。
- 两个 skip：预期的 `TestRecoveryFixture`；`TestProfileAttributesUnreadableFile` 因容器以 root 运行可读取 0000 权限文件而自行 skip，不在必需列表中。
- 首次启动容器时因 Git Bash 路径转换使工作目录无效（docker 报错、退出码 125，测试未开始）；设置 `MSYS_NO_PATHCONV=1` 后重跑，代码与依赖均未改动。
- 结束后三个一次性数据库已删除，Redis DB 12 为空。

## 测试变更

删除 6 个与“按 ALIPAY/QQ/WEIBO 补填集合区分”的旧规则绑定的测试，由配置驱动的统一规则测试取代：

| 已删除 | 原因 |
|---|---|
| `TestAlipayExceptionDoesNotBackfillOtherProviders`、`TestQQExceptionDoesNotBackfillOtherProviders`、`TestWeiboRefillDoesNotBackfillOtherProviders` | 断言“集合外提供方登录不补填”，规则改为按 `PROVIDER_PROFILE_BACKFILL` 判定 |
| `TestAlipayRepeatBindingDoesNotBackfill`、`TestQQRepeatBindingDoesNotBackfill`、`TestWeiboRepeatBindingDoesNotBackfill` | 断言重复绑定不补填，改为按配置判定 |

替代测试：`user/service_profile_backfill_test.go` 的 6 个测试，与函数名一一对应：

- `TestProviderProfileBackfillOnSignIn`：默认、开启、关闭三种模式下已有身份再次登录的补填判定，覆盖各提供方；关闭时仍更新 `provider_meta.profile`。
- `TestProviderProfileBackfillKeepsNonEmpty`：非空显示名与头像不被覆盖。
- `TestProviderProfileBackfillOnBinding`：绑定到已有账号（含重复绑定）的补填判定。
- `TestProviderProfileBackfillOnCompletionLink`：登录补全关联到已有账号（新身份与已拥有证明两条路径）的补填判定。
- `TestProviderProfileBackfillDisabledStillFillsNewAccounts`：关闭时直接注册与补全创建的新账号仍被填充。
- `TestProviderProfileBackfillRollbackAfterSessionFailure`：GOOGLE 在补填后会话创建失败，资料与 `provider_meta` 随事务回滚（最终审查新增）。

另有根包 `TestWeiboHTTPProfileBackfillDisabled`（证明配置从 `Config` 经装配传到服务）。

`scripts/required-tests.txt`：移除 6 个已删除条目，新增 6 个 `user` 测试与 `TestWeiboHTTPProfileBackfillDisabled`；门禁通过，未放宽任何 skip 规则。

## 审查结论

- 任务 1、任务 2：首次复核通过。
- 任务 3：经一轮文档措辞修正后通过。

## 最终审查

整分支最终审查要求的修复已完成：

- 主规格 `user-basic-profile` “内置头像资料”与 `oidc-sign-in` “OIDC 资料声明映射”与新规则冲突，已在本变更增加对应 MODIFIED 增量（保留全部场景），proposal 补充 `oidc-sign-in`；`openspec validate configurable-provider-profile-backfill --strict` 与 `openspec validate --all --strict` 通过。
- 补充 `TestProviderProfileBackfillRollbackAfterSessionFailure` 并加入 `scripts/required-tests.txt`，满足 tasks 2.3 对非 ALIPAY/QQ/WEIBO 提供方的原子性要求。
- 小项：`new-identity` 子测试增加 `provider_meta` 断言；还原 `docs/compatibility.md` 的历史措辞；修正 `displayNameFromProvider` 注释；清理微博 HTTP 测试未使用的赋值。
- 重新运行：新回滚测试（-v，PASS 非 SKIP）、`go test -run TestProviderProfileBackfill ./user/`、`TestWeiboHTTPProfileBackfillDisabled`、`go vet ./...`、gofmt 与上述 openspec 校验。

## 未验证项

- 不涉及真实第三方平台联调：补填行为与提供方无关，仅以合成提供方和微博 HTTP 模拟接口验证，不代表真实平台返回的显示名/头像字段行为。
- `internal/accountsvc` 服务级集成与 SIGTERM 用例在快速验证中 skip；未单独运行 `scripts/verify-accountsvc.sh`。
