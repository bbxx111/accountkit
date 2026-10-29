# prepare-v0-7-2-release 验收记录

日期：2026-10-29。分支 `chore/prepare-v0-7-2-release`，基线 develop `61c6b9c`（`add-admin-reveal-no-store` 归档合并提交）。执行方式：主会话顺序完成发布材料，独立审查由审查子代理完成，完整验证在 Linux 验证镜像中执行。版本号由本变更按补丁递增选定，用户在修复合入后指示继续发布准备，未单独点名版本号。

## 范围盘点

- v0.7.1..develop 合并提交：`21d9fe8`（`:reveal` 响应 no-store）、`df42c46`（`caller_cancel` 用例容忍）、`6b5c22d`（接入方真实环境验收补记）、`61c6b9c`（修复变更归档）；此外为 v0.7.1 的发布补记与归档。
- `git diff v0.7.1..develop -- go.mod go.sum migrations` 为空；当前 `0001_init.up.sql` SHA256 `ef2d1f229fa15b660cc7ecb4f417615b76866c19494641c96faf25bab24188b6`，与 v0.7.1 相同。
- 生产代码改动仅 `httpapi/admin/identities.go`（处理器入口设置 `Cache-Control: no-store`）；`internal/accountsvc/gateway_integration_test.go` 为测试改动。公开 Go 包签名、路由、字段、错误码、`tokens`、`user` 均未变；OpenAPI 管理契约增加 `adminRevealIdentity` 200 响应头声明（已随修复变更合入）。

## 发布材料

`docs/releases/v0.7.2.md`、`docs/release-checklist.md`（当前待办与 v0.7.2 门禁；v0.7.1 待办段落与 v0.6.0–v0.7.1 门禁末项删去已过时的“归档另行执行”）、README（两处版本表述）、`docs/features.md`、`docs/go-integration.md`、`docs/openapi.md`、`docs/compatibility.md`（新增首节）、`docs/openapi/{enduser,admin}.json`（`info.version` v0.7.2，描述注明与 v0.7.1 相同及管理契约的响应头声明）。

## 静态检查（准备快照）

| 检查 | 结果 |
|---|---|
| `openspec validate --all --strict` | 41 passed, 0 failed |
| `bash scripts/check-generated.sh` | 通过（exit 0） |
| `bash scripts/check-migrations.sh` | 通过（历史不可变，版本递增） |
| `node scripts/check-openapi.cjs` | 通过（enduser 24 操作、admin 11 操作，含 `adminRevealIdentity` 200 `Cache-Control` 常量断言） |
| `git diff --check` | 通过 |
| 本地相对链接与锚点检查（README、发布说明、检查表、兼容、功能、Go 集成、OpenAPI、本变更工件） | 新增链接全部可达；本记录生成前仅其自身报缺失，已有的 `migrations.md` 百分号编码锚点为既有写法 |

## 完整 Linux 验证（准备快照）

在 `tests/Dockerfile` 构建的 `akverify-linux` 镜像中执行，使用本任务创建的一次性 `postgres:17.4`（数据库 akverify / akverify_svc / akverify_src / akverify_dst）与 `redis:7-alpine` 容器，代码为 develop `61c6b9c` 的工作树叠加本变更的文档改动（审查修复前的快照；审查后只改动文档与工件）；日志保留在被忽略的 `.test-output/v072prep/`，结束后容器、网络、卷与镜像已全部删除。

| 脚本 | 结果 |
|---|---|
| `scripts/verify.sh`（迁移历史、build、vet、race、必需测试门禁 1007 项、恢复 seed/verify/source-unchanged） | exit 0；顶层测试 1217 pass / 0 fail / 2 skip（`TestProfileAttributesUnreadableFile` 按 root 权限预期 skip、`TestRecoveryFixture` 普通套件预期 skip）；pg_dump/pg_restore 17.11 恢复演练通过 |
| `scripts/verify-accountsvc.sh`（服务 race、必需测试门禁 62 项、进程级集成） | exit 0；顶层测试 203 pass / 0 fail / 1 skip（`TestProfileAttributesUnreadableFile`） |
| `go test -race -count=20 -run TestServiceIntegrationGatewayBoundary ./internal/accountsvc/`（同一镜像与真实 PostgreSQL/Redis，日志 `.test-output/v072prep/caller-cancel-race20.log`） | ok（151s），20 次全部通过；20 次中容忍分支未触发（无 `client-side response race observed` 日志），即本轮未复现竞争，容忍分支的触发证据仍是 CI run 40809097973 第一次尝试的失败日志 |

## 独立审查

审查子代理对准备快照（发布说明、检查表、README/docs 版本表述、兼容新节、OpenAPI、本变更工件）独立核对代码、路由与 v0.7.1 先例，自行运行 `git diff v0.7.1..develop`、0001 哈希、`git diff --check`、`openspec validate --all --strict`、`check-openapi.cjs`、链接/锚点脚本及 `gh run view 40809097973`。结论：Critical 0、Important 2、Minor 5，暂不建议直接提交。处理：

1. Important：发布说明、兼容指南与 README 称 `:reveal` 的 403/503 等错误响应也带 no-store，但 `RequireRole` 403、宿主 verifier 401/403 与 `Unconfigured` 503 都在处理器之前写出（`httpapi/admin/handler.go` 的 `r.With(su)` 与 `Unconfigured`），不经过处理器。已改为“处理器写出的所有响应（200 及 400/401/404/500）”，并写明前置中间件响应不含明文也不带该头。
2. Important：发布说明称 `caller_cancel` 调整“连跑 20 次通过”但无可追溯日志。已改写为 CI 事实（#43、#102 与 40809097973 第一次尝试）并引用本记录；本次在验证镜像中重新执行 20 次 race 运行并保留日志（见上表）。
3. Minor：`docs/compatibility.md` 新节与发布说明 OpenAPI 句缺“候选”标记：已补。
4. Minor：版本号来源未写明：状态行、design.md 与本记录已写明由本变更按补丁递增选定、用户指示继续发布准备。
5. Minor：tasks 1.1–1.3 与门禁前两项事实上已完成：已勾选；2.1/2.2 与门禁第三项随本记录完成勾选。
6. Minor：v0.7.0/v0.7.1 门禁末项残留“归档另行明确执行”：已改为“发布准备变更已归档”（v0.6.0 同句一并修正，三者均已归档）。
7. Minor：README 第 95 行句式：已拆句。

修复后复跑 `openspec validate --all --strict`（41 passed）与 `git diff --check`（通过）。

## 发布与消费（分别授权后执行）

待执行：合并 develop/main、最终候选在线 CI、tag、GitHub Release、远端消费与补记。

## 未验证项

- 真实网关对 `Cache-Control` 的透传未验证；接入方（Kong，无缓存插件）确认无需额外处理。
- 最终 main 候选以在线 CI 为准；合并 main、tag、Release 与远端消费待单独授权。
