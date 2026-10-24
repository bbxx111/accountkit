# v0.6.0 发布准备验收

日期：2026-10-24。用户确认版本号 v0.6.0，要求归档 `configurable-provider-profile-backfill` 并开始发布准备。当前结论：发布材料已完成并经独立审查，准备快照的静态检查与完整本地验证全部通过；合并 develop/main、最终候选 CI、tag、Release 与远端消费尚未执行，须分别取得明确授权。

## 准备范围

- develop `0871422`（含 `configurable-provider-profile-backfill` 归档合并）之上，分支 `docs/prepare-v0-6-0-release` 只修改文档、两份 OpenAPI 的 `info.version`/`info.description` 与本变更工件；准备提交 `0d26f3a` 相对 develop 无 Go、SQL、生成代码或脚本改动。
- 盘点 v0.5.0..develop：八项已归档变更，见 [v0.6.0 发布说明](../../../docs/releases/v0.6.0.md)。go.mod/go.sum 无变化；当前 `migrations/0001_init.up.sql` SHA256 为 `4c6b565e336aba88db5f6bf219407b026a7c1b438057b61470377005ed2509c5`，v0.5.0 为 `491068d7577ca33a94dc9d3f74b02acdbc95662aa954c693b63a3fd7b196b12c`。

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

## 独立审查

发布说明与版本表述经独立审查：Critical 0、Important 3、Minor 5。已处理：

- 两份 OpenAPI `info.description` 仍写“已发布 v0.5.0”，已改为 v0.6.0（准备中）。
- 本记录缺失导致的悬空链接，随本文件创建解决。
- 补充 Facebook 数据删除的行为与结果（`DELETED`/`SCHEDULED`/`BLOCKED`）及 accountsvc 不提供回调路由。
- 补充 `ParseIdentityKind` 放宽、credential 未知字段错误码变化、显式 Enabled 非内置类型须登记、审计 SYSTEM 操作者放宽；去除把 Google 视为 v0.5.0 既有能力的措辞；回退描述与迁移手册对齐（旧二进制预检可能放行，随后失败）。

未修改：兼容说明中三处已标注“已被取代”的历史补填段落与头像 HTTPS 表述，审查结论为仅提示。

## 未验证范围

- 最终 main 候选的在线 CI、tag、GitHub Release 与两个独立宿主的远端消费尚未执行。
- Google、OIDC、Facebook、微信小程序、支付宝、QQ、微博均未与真实平台、SDK 或真机联调；产品部署、实际服务商、网关/监控与目标环境验收由集成方负责。
- 本地验证使用共享开发 PostgreSQL 容器中的一次性数据库，不等同于 CI 环境。
