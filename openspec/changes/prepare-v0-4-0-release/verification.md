# v0.4.0 发布准备验收

日期：2026-10-20。状态：准备阶段，尚未提交、合并、推送或发布。[发布说明](../../../docs/releases/v0.4.0.md)、[发布门禁](../../../docs/release-checklist.md#v040-发布门禁)。

## 范围与快照

任务分支 `docs/prepare-v0-4-0-release`，位于被忽略的独立 worktree `.test-output/worktrees/prepare-v0-4-0-release`；主工作区当时检出 main，未改动。运行时代码基线为本地 develop 的 `2d55d8e12f859b3598b62bdab73e79e87d244eb5`。本次只新增发布说明，更新发布检查表、兼容说明和本变更工件，不修改运行时代码、SQL、生成代码、源快照或历史 tag。原始证据位于 ignored `.test-output/prepare-v0.4-release/`。

准备开始时的引用状态：

- 本地 develop `2d55d8e`，比远端 develop `924c4f0` 多出 `harden-database-runtime` 的实现、归档与合并三个提交，尚未推送。
- main 与远端 main 均为 `0e78b0e`；远端只有 v0.1.0/v0.2.0/v0.3.0，`ls-remote` 未发现 v0.4.0。
- 数据库设计评审 `0157ffb` 按用户要求未建 change，此前只有不含 SHA 的本地日志；`harden-database-runtime` 的完整 Linux 验证以 `924c4f0` 为基线，覆盖了评审代码，但未执行 `verify-accountsvc.sh`。本次对包含两者的精确快照重新执行库与服务两套门禁。

## 盘点结论

v0.3.0 之后的运行时变更为会话认证来源与客户端描述（三个已归档变更）、数据库设计评审和数据库运行契约修复。存在以下破坏性变化，建议版本号为 v0.4.0：

- 旧 v0.3.0 数据库不能直接接管，且 v0.3.0 的 Migrate 可能接受新结构；
- 解绑/换绑按会话来源撤销，直接 Go `UnbindIdentity` 须传完整 Principal；
- `New` 拒绝 `MaxConns < 2` 的连接池；
- 公开结构新增字段，影响位置式复合字面量；
- 开发中曾出现的 known/initial 字段及快照枚举已删除（从未发布）。

`internal/migrationcheck` 无需改动：v0.4.0 tag 的 0001 与当前固定哈希一致，按普通已发布历史检查。

## 实际验证

使用 OpenSpec 1.14.3、sqlc 1.31.1；Linux 验证镜像 `accountkit-verify:local`，内含 Go 1.26.5 linux/amd64、PostgreSQL 客户端 17.11、git 2.47.3，配合专用 `postgres:17` 与 `redis:7` 容器。资源均带标签 `accountkit.task=release-v040-20261020`。普通测试库、恢复源库、恢复目标库为同一专用实例中的三个新建空库。

完整 Git 历史与全部 tag 以 bundle 复制进容器后 checkout 到上述快照；environment.txt 记录 HEAD 精确匹配、非浅克隆，tag 为 v0.1.0/v0.2.0/v0.3.0。

| 检查 | 本次结果 |
|---|---|
| `bash scripts/check-generated.sh` | Windows Git Bash / sqlc 1.31.1，退出 0，生成结果无差异 |
| `bash scripts/verify.sh` | Linux，退出 0；迁移历史、独立 build、vet、全库 race、必需测试门禁及恢复演练均通过 |
| 全库 race 与必需测试 | 42 个测试包通过、0 失败（另 8 个无测试包）；2402 个测试通过事件、0 失败；唯一测试级 skip 为普通套件按规则跳过的 TestRecoveryFixture；719 行必需清单门禁通过 |
| 恢复演练 | pg_dump/pg_restore 17.11；seed、verify、source-unchanged 三阶段的 TestRecoveryFixture 均 PASS。覆盖版本、认证数据、必有会话来源、单份 JSON 客户端描述、登录补全流程快照、解密、access/refresh 及源库未变 |
| `bash scripts/verify-accountsvc.sh` | Linux，退出 0，`ACCOUNTSVC_TEST_RACE=1`；5 个测试包、419 个测试通过事件、0 失败、0 skip；服务必需清单门禁通过 |
| 受测工作区 | 两套脚本执行后，容器内受测检出无已跟踪文件改动 |
| OpenSpec | `openspec validate --all --strict`：25 项通过、0 失败；本变更 `skip_specs`，规划工件齐全 |
| 差异 | `git diff --check` 通过；相对受测快照只改动 docs/releases/v0.4.0.md、docs/release-checklist.md、docs/compatibility.md 和本变更工件 |

测试计数包含子测试事件。普通套件的 TestRecoveryFixture skip 已由恢复脚本三阶段实际执行补足。准备快照通过不代表最终 main 候选或在线 CI 已通过。

## 独立审查

独立只读审查对照 `v0.3.0..2d55d8e` 的代码、go.mod、四个已归档变更、数据库设计评审提交及长期文档。未发现 Critical；2 项 Important、6 项 Minor 已全部处理，处理前均已复核证据：

1. Important：发布说明误写“本版本不新增依赖”。`git diff v0.3.0..HEAD -- go.mod` 确认新增直接依赖 httpsfv、useragent，puddle 由间接改为直接；已如实列出。
2. Important：遗漏新增撤销原因 `IDENTITY_UNBOUND`（`enum.RevokeIdentityUnbound`=9）。已在发布说明和兼容说明补写，包括审计消费方与回退时对存量值的影响。
3. Minor：补写 HTTP 解绑在会话失效时返回 401 `TOKEN_INVALID`，当前会话查询存储故障返回 503 `DEPENDENCY_UNAVAILABLE`。
4. Minor：说明 `ReplaceIdentity` 在 v0.3.0 已有同等复核，本版本只改当前会话的保留条件。
5. Minor：列出新增公开 API，并注明 sqlc 生成包不是稳定接口；引用的标识符已逐一 grep 确认存在。
6. Minor：点名被取代或移除的五个索引，提示自行按 request_id、flow_id 查询审计的影响。
7. Minor：检查表门禁第 4 项改为先 `--no-ff` 合入 develop 并推送，再合入 main。

审查确认路由、scope、错误码、请求头、管理面会话 DTO、连接池校验、READ COMMITTED 范围、advisory lock、导入加锁与按来源撤销均与代码一致，accountsvc 无配置变化；开发期中间态未被描述为已发布内容；链接和锚点全部可解析；检查表和任务没有夸大完成度。报告保存在[审查记录](../../../.test-output/prepare-v0.4-release/review.md)。审查只读执行，不替代测试。

## 原始证据与资源清理

- [环境](../../../.test-output/prepare-v0.4-release/raw/environment.txt)、[受测提交](../../../.test-output/prepare-v0.4-release/verified-commit.txt)、[远端 tag](../../../.test-output/prepare-v0.4-release/remote-tags.txt)、[生成检查](../../../.test-output/prepare-v0.4-release/generated.log)、[OpenSpec 检查](../../../.test-output/prepare-v0.4-release/openspec.log)。
- [库完整日志](../../../.test-output/prepare-v0.4-release/raw/library.log)、[库测试事件](../../../.test-output/prepare-v0.4-release/raw/tests.jsonl)、[服务完整日志](../../../.test-output/prepare-v0.4-release/raw/accountsvc.log)、[服务测试事件](../../../.test-output/prepare-v0.4-release/raw/accountsvc-tests.jsonl)、[恢复材料](../../../.test-output/prepare-v0.4-release/raw/recovery.s4zcO1/)。

清理前按任务标签列出资源，确认 runner 退出 0、证据文件存在后，仅删除本次的三个容器（runner、PostgreSQL、Redis）及其匿名卷、隔离网络和模块缓存卷；既有 postgres/redis 容器保持运行。清单见[资源记录](../../../.test-output/prepare-v0.4-release/cleaned-resources.txt)。

## 未完成与边界

材料尚未提交、合并或推送，本地 develop 的三个提交也未推送；尚未确定 main 最终候选，未核验在线 CI。未创建 v0.4.0 tag/Release，未验证远端固定版本消费。长期文档中的“开发中”标记、README 发布入口和 OpenAPI `info.version` 留待正式发布记录阶段更新。未发布镜像/二进制，未部署产品，本变更未归档。

真实服务商、产品网关、监控平台及目标环境恢复由集成方另行验收；微信/Apple 任务 8.5 仍未验证。旧数据库需要保留数据时仍须单独制定和验证迁移方案，本轮恢复演练不等于提供旧库原地升级能力。
