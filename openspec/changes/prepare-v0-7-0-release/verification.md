# v0.7.0 发布准备验收

日期：2026-10-28。用户确认版本号 v0.7.0 并授权发布准备（不含合并 main、推送、tag 与 Release）。当前结论：准备快照的静态检查、完整本地验证与独立审查全部通过；合并 develop/main、推送、tag、Release 与远端消费尚未执行，等待逐项授权。

## 准备范围

- develop `7e3a524`（含 `add-accountsvc-email-templates` 归档与 CI 去重）之上，分支 `docs/prepare-v0-7-0-release`（准备提交 `49f0f2c`，本记录的补记提交除外）只修改文档、两份 OpenAPI 的 `info.version`/`info.description` 与本变更工件；相对 develop 无 Go、SQL、生成代码或脚本改动。
- 盘点 v0.6.0..develop：六项已归档变更与四项维护，见 [v0.7.0 发布说明](../../../docs/releases/v0.7.0.md)。go.mod/go.sum 无变化；当前 `migrations/0001_init.up.sql` SHA256 为 `ef2d1f229fa15b660cc7ecb4f417615b76866c19494641c96faf25bab24188b6`，v0.6.0 为 `4c6b565e336aba88db5f6bf219407b026a7c1b438057b61470377005ed2509c5`（已由 `add-purged-user-feed` 作为独立 pin 固定）。

## 静态检查（通过）

- `openspec validate --all --strict`：41 项通过、0 失败。
- `bash scripts/check-generated.sh` 退出 0；`bash scripts/check-migrations.sh`：迁移历史不可变、版本严格递增。
- `node scripts/check-openapi.cjs`：消费者 24 个操作、管理面 11 个操作，引用、路径参数、响应 DTO 与会话上下文检查通过；`go test ./httpapi/...` 通过。
- `git diff --check` 通过；变更文件中的 151 个本地 Markdown 链接均存在（本文件创建后，发布说明与检查表对它的引用不再悬空）。

## 完整本地验证（通过）

准备快照为提交 `49f0f2c`。在 `tests/Dockerfile` 构建的 Linux 验证镜像 `akverify-linux`（golang:1.26.5 + postgresql-client-17）中执行，PostgreSQL 17.4 容器 `akverify-pg`（数据库 `akverify`、`akverify_svc`、`akverify_src`、`akverify_dst`）与 Redis 7 容器 `akverify-redis`（DB 0/1）均为本任务创建的一次性资源，运行结束后已删除容器、网络、构建缓存卷与镜像。原始日志在被忽略的 `.test-output/v070prep/`。

- `bash scripts/verify.sh` 退出 0：迁移历史检查、独立构建、vet、race；52 个有测试的包全部通过，1208 个顶层测试通过、0 失败、2 个预期 skip（`TestRecoveryFixture` 在普通套件中按规则 skip，`TestProfileAttributesUnreadableFile` 以 root 运行时 skip）；必需测试门禁（`scripts/required-tests.txt` 1007 项）通过；pg_dump/pg_restore 17.11 恢复演练 seed/verify/source-unchanged 通过。
- `bash scripts/verify-accountsvc.sh` 退出 0：10 个有测试的包全部通过，194 个顶层测试通过、0 失败、1 个预期 skip（`TestProfileAttributesUnreadableFile`）；必需门禁（`scripts/accountsvc-required-tests.txt` 62 项）通过；包含服务进程 race、传输模式与网关边界集成用例。
- 验证运行期间只应用了独立审查的文档文本修正（README、`docs/accountsvc.md`、`docs/features.md`、发布说明），无 Go、SQL、脚本或生成代码改动；修正后重新运行读取 README 与 OpenAPI 的测试包（`httpapi/enduser`、`internal/migrationcheck`）通过。

## 独立审查

发布说明、版本表述与 OpenSpec 工件经独立审查子代理核对代码、SQL、迁移 pin、配置变量名与错误码：Critical 0、Important 3、Minor 6。已处理：

- 兼容说明节数由“前六节”改为“标注随 v0.7.0 发布的前五节”（第六节随 v0.6.0 发布）。
- README 导语与“最新版本”段同步为 v0.7.0（准备中），保留 v0.6.0 为已发布最新版本。
- `ACCOUNTKIT_RECOVERY_*` 的措辞改为“不得出现在 accountsvc 进程环境中，否则同样被拒绝”，与 `rejectLegacyPrefix` 无白名单的实现一致。
- 补充遗漏的公开符号（`tokens.ParsePrivateKey`/`Algorithm`/`TokenType`/`Signer.JWKS`、`user.ErrPurgedUserCursorAhead`/`MaxPurgedUserPage`/`Deps.PurgedUserRetention`、`aliyun` 包导出面、`smtp.NewWithRenderer` 的品牌校验错误路径）；删除 `LegacyChineseRenderer` 不存在的“位置式字面量”迁移动作；`SMS_ALIYUN_ENDPOINT` 改为“只接受 HTTPS，非 production 仅额外允许回环地址 HTTP”（发布说明与 `docs/accountsvc.md` 同步）；WARN 列表注明邮件模板与签发方不可用另有 WARN；`docs/features.md` 的 HTTP 契约行补充 JWKS 与已清理用户拉取不在库契约内。

审查确认一致：0001 哈希与 v0.6.0 pin、发布说明提到的全部公开 Go 符号与维护任务顺序、accountsvc 变量名/路由/错误码/服务 Bearer 规则、与兼容说明前五节无矛盾、OpenAPI 操作数 24/11 不变且 diff 仅为描述文本、go.mod 无变化。修改后重新运行 `git diff --check`、`go test ./httpapi/enduser ./internal/migrationcheck`（读取 README 与 OpenAPI 的测试）通过。

## 未验证范围

- 阿里云短信未用真实凭据与已审核模板发送；邮件布局未在真实邮件客户端核对显示效果；服务 Bearer 认证未与真实 IdP 联调；产品部署、实际服务商、网关/监控与目标环境验收由集成方负责。
- 本地验证使用本任务创建的一次性容器与数据库，不等同于 CI 环境；正式门禁以最终 main 候选的在线 CI 为准。
