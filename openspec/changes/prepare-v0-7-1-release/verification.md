# prepare-v0-7-1-release 验收记录

日期：2026-10-29。分支 `chore/prepare-v0-7-1-release`，基线 develop `2397058`（`allow-development-http-issuer` 合并提交）。执行方式：主会话顺序完成发布材料，独立审查与完整验证分别由审查子代理与 Linux 验证镜像完成。

## 范围盘点

- v0.7.0..develop 非合并提交：`ca1b511`（v0.7.0 发布补记）、`729523e`（v0.7.0 准备归档）、`b415072` / `1daa6da` / `debbb0c`（开发模式 HTTP issuer 实现、审查修复、归档）。
- `git diff v0.7.0..develop -- go.mod go.sum migrations` 为空；当前 `0001_init.up.sql` SHA256 `ef2d1f229fa15b660cc7ecb4f417615b76866c19494641c96faf25bab24188b6`，与 v0.7.0 相同。
- 代码改动限于 `internal/accountsvc/{oidcjwt,adminauth}`、`internal/accountsvc/config.go`、`run.go` 及其测试；公开 Go 包、`httpapi`、`tokens`、`user`、OpenAPI 操作集合均未变。

## 发布材料

`docs/releases/v0.7.1.md`、`docs/release-checklist.md`（当前待办与 v0.7.1 门禁）、README（两处版本表述）、`docs/features.md`、`docs/go-integration.md`、`docs/openapi.md`、`docs/compatibility.md`（新增首节）、`docs/openapi/{enduser,admin}.json`（`info.version` v0.7.1，描述注明与 v0.7.0 相同）。

## 静态检查（准备快照）

| 检查 | 结果 |
|---|---|
| `openspec validate --all --strict` | 41 passed, 0 failed |
| `bash scripts/check-generated.sh` | 通过（exit 0） |
| `bash scripts/check-migrations.sh` | 通过（历史不可变，版本递增） |
| `node scripts/check-openapi.cjs` | 通过（admin 11 操作；enduser 校验通过） |
| `git diff --check` | 通过 |
| 本地相对链接与锚点检查（README、发布说明、检查表、兼容、功能、Go 集成、OpenAPI、accountsvc 文档、本变更工件） | 新增链接全部可达；仅本记录在生成前报缺失，已有的 `migrations.md` 百分号编码锚点为既有写法 |

## 完整 Linux 验证（准备快照）

在 `tests/Dockerfile` 构建的 `akverify-linux` 镜像中执行，使用本任务创建的一次性 `postgres:17.4`（数据库 akverify / akverify_svc / akverify_src / akverify_dst）与 `redis:7-alpine` 容器，代码为 develop `2397058` 的工作树（仅叠加本变更的文档改动）；日志保留在被忽略的 `.test-output/v071prep/`，结束后容器、网络、卷与镜像已全部删除。

| 脚本 | 结果 |
|---|---|
| `scripts/verify.sh`（迁移历史、build、vet、race、必需测试门禁 1007 项、恢复 seed/verify/source-unchanged） | exit 0；顶层测试 1217 pass / 0 fail / 2 skip（`TestProfileAttributesUnreadableFile` 按 root 权限预期 skip、`TestRecoveryFixture` 普通套件预期 skip）；pg_dump/pg_restore 17.11 恢复演练通过 |
| `scripts/verify-accountsvc.sh`（服务 race、必需测试门禁 62 项、进程级集成） | exit 0；顶层测试 203 pass / 0 fail / 1 skip（`TestProfileAttributesUnreadableFile`） |

## 独立审查

审查子代理对准备快照（发布说明、检查表、README/docs 版本表述、OpenAPI、本变更工件）独立核对代码与 v0.7.0 先例，自行运行 `git diff v0.7.0..HEAD`、0001 哈希、`git diff --check`、`openspec validate --all --strict`、`check-openapi.cjs` 及读取 README/OpenAPI 的测试。结论：Critical 0、Important 0、Minor 7，可提交。处理：

1. `docs/compatibility.md` 新节与发布说明「运行与验证」缺「准备中」标记：已补。
2. 发布说明称本地 compose 可直接配置 HTTP issuer，但模板不含 `ACCOUNTSVC_ADMIN_*`：已改为只有服务签发方可经模板配置，管理员变量需自行加入。
3. production 下 HTTP `jwks_uri` 的后果表述模糊：已补「管理员 issuer 启动失败、服务签发方 Bearer 503」。
4. 检查表 v0.7.0 段落残留「发布变更归档另行执行」而归档已完成：已删去。
5. 验收记录中 strict 校验计数需在工件修改后复跑：已复跑（见下）。
6. 建议勾选 1.1–1.3 与门禁前两项：已勾选，并随 Linux 验证完成勾选第三项。
7. 版本语义观察（补丁 vs 次版本）：以 design.md 记录的用户选择为准，不改。

修复后复跑 `openspec validate --all --strict` 与 `git diff --check`，结果见提交时的静态检查表。

## 未验证项

- 真实 Keycloak 联调由接入方完成。
- 最终 main 候选以在线 CI 为准；合并 main、tag、Release 与远端消费待单独授权。
