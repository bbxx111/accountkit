# v0.6.0 发布准备验收

日期：2026-10-24。用户确认版本号 v0.6.0，要求归档 `configurable-provider-profile-backfill` 并开始发布准备。当前结论：v0.6.0 已发布。首个 main 候选 CI 因 v0.5.0 初始定义未固定而失败，修复后最终候选 `ec6802c` 在线 CI 全部通过；annotated tag 与 GitHub Release 已核验，两个独立宿主从远端无 replace 消费通过。产品部署与本变更归档另行执行。

## 准备范围

- develop `0871422`（含 `configurable-provider-profile-backfill` 归档合并）之上，分支 `docs/prepare-v0-6-0-release` 只修改文档、两份 OpenAPI 的 `info.version`/`info.description` 与本变更工件；准备提交 `0d26f3a` 相对 develop 无 Go、SQL、生成代码或脚本改动。
- 盘点 v0.5.0..develop：八项已归档变更，见 [v0.6.0 发布说明](../../../../docs/releases/v0.6.0.md)。go.mod/go.sum 无变化；当前 `migrations/0001_init.up.sql` SHA256 为 `4c6b565e336aba88db5f6bf219407b026a7c1b438057b61470377005ed2509c5`，v0.5.0 为 `491068d7577ca33a94dc9d3f74b02acdbc95662aa954c693b63a3fd7b196b12c`。

## 静态检查（通过）

- `openspec validate --all --strict`：37 项通过、0 失败。
- `bash scripts/check-generated.sh` 退出 0；`bash scripts/check-migrations.sh`：迁移历史不可变、版本严格递增。
- `node scripts/check-openapi.cjs`：消费者 24 个操作、管理面 11 个操作，引用、路径参数、响应 DTO 与会话上下文检查通过；`go test ./httpapi/...` 通过。
- `git diff --check` 通过；变更文件中的本地 Markdown 链接均存在（本文件创建后，发布说明与检查表对它的引用不再悬空）。

## 完整本地验证（准备快照 `0d26f3a`，通过）

在 `tests/Dockerfile` 构建的 Linux 验证镜像 `accountkit-verify:latest` 中执行，PostgreSQL 17 与 Redis 7 使用四个一次性数据库（`accountkit_v060prep_test/_svc/_src/_dst`）和 Redis DB 12；运行结束后已删除这四个数据库并清空 DB 12（运行前为空）。原始日志在被忽略的 `.test-output/v060prep/`。

- `bash scripts/verify.sh` 退出 0：迁移历史检查、独立构建、vet、race；44 个包全部通过，1080 个顶层测试通过、0 失败、2 个预期 skip（`TestRecoveryFixture` 在普通套件中按规则 skip，`TestProfileAttributesUnreadableFile` 以 root 运行时 skip）；必需测试门禁（`scripts/required-tests.txt` 948 项）通过；pg_dump/pg_restore 17.11 恢复演练 seed/verify/source-unchanged 通过。
- `bash scripts/verify-accountsvc.sh` 退出 0：5 个包全部通过，105 个顶层测试通过、0 失败、1 个预期 skip（`TestProfileAttributesUnreadableFile`）；包含服务进程 race、传输模式与网关边界集成用例。
- 准备快照之后的提交只修改发布说明、OpenAPI 描述文本与本记录；修改后重新运行 `check-openapi.cjs` 与 `go test ./httpapi/enduser` 通过。

## 首个 main 候选 CI 失败与修复

- 准备分支经 `--no-ff` 合入 develop（`ea14828`）、develop 合入 main（`08c4a4a`）并推送。develop [CI](https://github.com/bbxx111/accountkit/actions/runs/40091175248) 通过；main [CI](https://github.com/bbxx111/accountkit/actions/runs/40091175160) 在 `verify.sh` 的迁移历史检查失败：`release initial migration changed or missing: refs/tags/v0.5.0: 0001_init.up.sql`。未创建 tag 或 Release。
- 原因：`add-identity-provider-registry` 与 `add-google-sign-in` 按授权修改了当前 0001，但未像 v0.4.0 那样把 v0.5.0 已发布初始定义独立固定。v0.5.0 tag 提交 `102315c` 只在 main 上，develop 与准备分支上 `tag --merged HEAD` 不包含它，因此本地准备检查与 develop CI 均未暴露。
- 修复（`fix/migrationcheck-pin-v0-5-0`）：`publishedInitialReleases` 增加 v0.5.0 精确提交 `102315c3069273f7a8c3ad253c1f4ae01255f8c6` 与已发布哈希（up `491068d7…`，down `2f26c7dc…`），新增 `TestPublishedV050Pinned`、`TestCurrentInitialDiffersFromPublishedV050`（先失败后通过）；AGENTS.md 与迁移手册的固定基线列表补 v0.5.0。不改 tag、历史 SQL、冻结源或当前 initialFrozen，不放宽其他检查；移动/删除 v0.5.0 tag 仍失败。
- 验证：`go test ./internal/migrationcheck/` 与 `go vet` 通过；修复分支 `check-migrations.sh` 通过；在 main 上复现失败（exit 1），把修复试合并到 main 的临时工作树后 `check-migrations.sh` 通过。最终判定以新 main 候选的在线 CI 为准。

## 发布与远端消费（2026-10-24）

- 修复经 `--no-ff` 合入 develop（`55b1946386170193b5fc632a14511898da966781`），develop 合入 main（`ec6802cd221db3ce107e79bac92174bc35a0ba7d`）并推送；main 树与 develop 一致，本地 `check-migrations.sh` 在 main 上通过。
- 最终候选 [CI run 40093942831](https://github.com/bbxx111/accountkit/actions/runs/40093942831)：head_sha 精确为 `ec6802c…`，结论 success；check-generated、Linux 镜像构建、`verify.sh`（含迁移历史、race、必需门禁与恢复演练）、`verify-accountsvc.sh` 各步骤均成功。
- 创建前远端无 v0.6.0；annotated tag `v0.6.0`（对象 `8009c5dcc4622deec6655b1080d254eae41f782d`，消息 `Release v0.6.0`）指向 `ec6802c…`，`git ls-remote` 核验；历史 tag 未移动。
- [GitHub Release v0.6.0](https://github.com/bbxx111/accountkit/releases/tag/v0.6.0) 于 2026-10-24T12:38:27Z 发布，非 draft/prerelease，为 latest；正文取自发布说明，状态改为已发布，相对链接固定到 `ec6802c` 的 blob 地址。
- 远端消费：`.test-output/release-v060/` 下两个独立宿主（`facade`、`enduser`），`GOWORK=off`、`GOPROXY=https://goproxy.cn,direct`、`GOSUMDB=sum.golang.org`、独立 GOMODCACHE，`go get github.com/bbxx111/accountkit@v0.6.0`，无 replace；均 `go mod tidy`、`go vet`、构建并运行通过。
  - `facade`：自定义 `idp.Provider` 装入 `user.Deps.Providers`；`accountkit.Config` 的 Google/QQ/微博与 `ProviderProfileBackfill` 配置；`IdentityKind` 为字符串 API 名，`ParseIdentityKind` 接受格式合法的未知类型、拒绝非法格式；`IdentityCapabilities`、`IdpCredential`、`HandleFacebookDataDeletion` 与 `ProviderDeletionStatus` 编译通过。
  - `enduser`：直接 `httpapi/enduser` 装配，`POST /users:signInWithIdp` 旧 `wechat` 成员返回 400 `MALFORMED_BODY`，缺少 credential 返回 400 `CREDENTIAL_INCOMPLETE`。
  - 两者 `go list -m -json` 版本为 v0.6.0、无 Replace；模块内 `migrations/0001_init.up.sql` SHA256 为 `4c6b565e…`，与当前固定哈希一致。日志见 `consumption.log`。

## 独立审查

发布说明与版本表述经独立审查：Critical 0、Important 3、Minor 5。已处理：

- 两份 OpenAPI `info.description` 仍写“已发布 v0.5.0”，已改为 v0.6.0（准备中）。
- 本记录缺失导致的悬空链接，随本文件创建解决。
- 补充 Facebook 数据删除的行为与结果（`DELETED`/`SCHEDULED`/`BLOCKED`）及 accountsvc 不提供回调路由。
- 补充 `ParseIdentityKind` 放宽、credential 未知字段错误码变化、显式 Enabled 非内置类型须登记、审计 SYSTEM 操作者放宽；去除把 Google 视为 v0.5.0 既有能力的措辞；回退描述与迁移手册对齐（旧二进制预检可能放行，随后失败）。

未修改：兼容说明中三处已标注“已被取代”的历史补填段落与头像 HTTPS 表述，审查结论为仅提示。

## 未验证范围

- Google、OIDC、Facebook、微信小程序、支付宝、QQ、微博均未与真实平台、SDK 或真机联调；产品部署、实际服务商、网关/监控与目标环境验收由集成方负责。
- 本地验证使用共享开发 PostgreSQL 容器中的一次性数据库，不等同于 CI 环境。
