## Archive

2026-11-04，按用户“归档已完成的变更”的单独请求，将本变更归档至当前目录；6/6 项任务已完成，无增量规格，主规格无需同步。下文“发布准备变更不自动归档”等描述保留发布结项时点；本次请求完成后已归档。发布候选、CI、tag、Release 与远端消费证据保持原记录，文中的 `.test-output/` 路径均相对仓库根目录。

## Scope

2026-11-01，按用户“按步骤进行发布”授权执行两个 SDK 首版发布。Go v1.0.0 版本不变，未涉及 ai-food 或产品部署。

## Preparation

- 功能变更 43/43 已完成；三个新增规格同步至主规格，归档位置为 ../2026-11-01-add-account-client-sdks/。
- 实现证据见[功能验收记录](../2026-11-01-add-account-client-sdks/verification.md)。发布准备只调整规格、证据引用和文档，不更改 SDK/Go 运行时实现。
- 候选包清单与 sdk/flutter/v1.0.0、sdk/admin-ts/v1.0.0 元数据检查通过，服务兼容基线 v1.0.0。
- OpenSpec 全量 strict 45 项通过；OpenAPI 35 操作、SDK 覆盖、两个发布元数据入口、差异空白与归档/当前文档的本地相对文件链接检查通过。三个新主规格保留 delta 的 Purpose 与所有 Requirement 正文。
- 独立发布审查及限定复核为 Approve：修复 Release 正文相对链接，补充归档记录历史时点；无遗留实质问题。
- 功能基线 develop 792bd6b4c1adc7a08cc14028d68f0bb1f51846d8 的 [Go CI](https://github.com/bbxx111/accountkit/actions/runs/41265770842) 与 [SDK CI](https://github.com/bbxx111/accountkit/actions/runs/41265770843) 全部成功。
- 发布准备 a8a6040e7ea91dd0db2072b70f5c1e9a42b63e66 的 [Go CI](https://github.com/bbxx111/accountkit/actions/runs/41266298748) 与 [SDK CI](https://github.com/bbxx111/accountkit/actions/runs/41266298695) 四项全部成功；[PR #21](https://github.com/bbxx111/accountkit/pull/21) 以 5888ea005db02f3671cc021efd4179644327cf53 合入 develop。与 Go v1.0.0 对比，Go 文件变化仅新增 sdks/testsupport/fixture，无生产 Go、go.mod/go.sum、OpenAPI 或迁移变更。

## Candidate and Release

[PR #22](https://github.com/bbxx111/accountkit/pull/22) 以双亲 merge commit **1a6c60c41a330d568e8795c11c640197ce25c0e6** 合入 main，树内容与发布准备 a8a6040 完全一致。最终候选 [Go CI](https://github.com/bbxx111/accountkit/actions/runs/41267072571) 与 [SDK CI](https://github.com/bbxx111/accountkit/actions/runs/41267072598) 四项全部成功，head SHA 精确一致；发布前再次通过 GitHub API 核对。Go 包含 sqlc、tests/Dockerfile 镜像内独立构建/vet/race、迁移历史、必需测试、恢复 seed/verify/source-unchanged 和 accountsvc 真实依赖/进程信号验证。

最终候选 SDK 三项 job 已成功。下载的候选 artifact 包含 24 项脚本测试通过、55 项 TypeScript 单元测试通过、真实服务与嵌入式策略验证、独立 Git 消费，以及 Android app-debug.apk / iOS Runner.app（14.3MB）构建成功记录。保存于 `.test-output/client-sdks-release/candidate-sdk-artifacts/`；该原生证据使用候选源码的本地固定 Git 快照，尚不代表正式远端 tag 消费。

确认远端不存在同名 SDK tag 后，创建 annotated tags 并用 `git push --atomic` 一次推送。远端解析均为候选 **1a6c60c41a330d568e8795c11c640197ce25c0e6**：

| SDK tag | annotated tag 对象 | Release |
| --- | --- | --- |
| sdk/flutter/v1.0.0 | 16c7aa884fd05615af86e418b6d0d6b3e47ce0a6 | [Flutter SDK 1.0.0](https://github.com/bbxx111/accountkit/releases/tag/sdk/flutter/v1.0.0) |
| sdk/admin-ts/v1.0.0 | d3d8dc5870a821275b3bfa6106aeb28aeb9514a4 | [Admin TypeScript SDK 1.0.0](https://github.com/bbxx111/accountkit/releases/tag/sdk/admin-ts/v1.0.0) |

两个 Release 使用候选提交中的同一份发布说明并指定 `--verify-tag --latest=false`。原 Go v1.0.0 tag 对象 f497dec1dd487f18cb52e5ee3df85f438bde1831、解析提交 e1b93da7d050bce6e94766412a81f5c11d994b1f 均未变化，API 确认仓库 Latest 仍为 v1.0.0。

## Remote Consumption

2026-11-01 在 Windows 独立临时消费目录执行以下命令，两者退出码均为 0，入口完成后自动清理其临时应用。不使用本地快照、path/workspace override 或浮动分支。

- `node scripts/verify-sdk-consumers.mjs --sdk admin-ts --git-url https://github.com/bbxx111/accountkit.git --ref sdk/admin-ts/v1.0.0`：安装、冻结安装、tsc、实际 HTTP 客户端行为测试和 Vite 生产构建通过；日志 `remote-admin-ts.log`。
- `node scripts/verify-sdk-consumers.mjs --sdk flutter --git-url https://github.com/bbxx111/accountkit.git --ref sdk/flutter/v1.0.0 --platform none`：pub get、冻结安装、analyze 与 scope 行为测试通过；日志 `remote-flutter.log`。Flutter 镜像沿用已验证配置，Git 源为实际 GitHub 仓库。

日志均位于 `.test-output/client-sdks-release/`，开头明确记录 `remote Git` 和对应 tag。两个 tag 经远端解析均匹配上述候选 SHA；本次远端消费不重复原生构建，原生证据来自同一候选 CI。

## Published Documentation

已完成当前文档的已发布状态补记，正式 tag 保留原候选文档不移动。补记提交 ecc3c1c 通过 [PR #23](https://github.com/bbxx111/accountkit/pull/23) 以 5ba535dbf135d3a4954e24af3e10f58c61be338a 合入 develop，再通过 [PR #24](https://github.com/bbxx111/accountkit/pull/24) 以 e56a62a4791ae875ffd85cec9d66c88054f09209 合入 main，均保留 merge commit。此后的分支 HEAD 与已发布 tag SHA 分开追溯，不把候选 CI 当作补记提交的 CI。两个 Release 正文也使用同一份已发布状态说明补记。

补记 OpenSpec strict 45 项、两个候选元数据及差异检查通过，独立证据与文档审查为 Approve，无遗留问题；仅 8 个 Markdown 文件变化，不重复运行源码功能测试。

实际集成完成后任务更新为 6/6；本次结项只补充 OpenSpec 任务状态及上述已发生的合并证据。发布准备变更不自动归档，功能变更已归档。发布后的文档 CI 与发布候选 CI 分开记录；当前发布结论依据精确 tag 候选的成功 CI 和真实远端消费。

## Boundaries

无真实提供方授权、iOS 真机/签名或产品上线验证；最终候选 iOS 无签名编译与远端 Flutter platform none 消费分别记录。原始输出保留 ignored .test-output/client-sdks-release/。

## Archive Verification

- 2026-11-04 使用已安装的 OpenSpec 1.13.0，在 `docs/archive-completed-releases` 分支执行归档；本变更 6/6、同批 Go 发布变更 7/7 完成，工件齐全，无增量规格及归档目录冲突。CLI 使用 `archive --yes --skip-specs --json`，保留默认归档校验。
- 归档前 `openspec validate --all --strict`：45 项通过、0 失败；归档后同一入口：43 项主规格通过、0 失败。减少的两项为已归档变更；`openspec list --json` 返回空活动列表。
- 自行复核本批差异：11 个归档文件齐全，两份 `.openspec.yaml` 的 Git 内容哈希保持；16 份受影响 Markdown 的 84 个本地链接及 13 个仓库 blob 目标核对通过，旧活动路径残留为 0，`git diff --check` 与归档 Markdown 空白检查通过。固定 tag 链接按本地 Git 对象核对，develop/main 链接按当前工作树目标核对，不宣称远端链接已更新。
- 原始输出及文档核对结果保存在仓库根目录的 `.test-output/archive-completed-releases/`。本次仅移动工件、修复引用及补记归档状态，未修改主规格、SDK/Go/SQL/依赖或版本对象；未重新运行构建、功能/数据库/race/恢复测试，未查询在线 CI，未提交、推送或合并本次归档。

## Integration Review

2026-11-04，用户在归档完成后明确授权“提交，合并，推送”；本轮按仓库默认流程通过 PR merge commit 合入 develop。上节“未提交、推送或合并”描述归档检查阶段的历史状态。

提交前独立审查结论为 Approve，无 Critical/Important/Minor 问题；审查确认 11 个工件及两份元数据完整保留，历史发布证据未篡改，仅预期文档与归档状态变化，复跑 84 个本地链接、13 个仓库文件目标及差异空白检查通过。提交前全量严格校验为 43/43 主规格通过；本次 PR 的在线 CI 与历史发布候选 CI 分别追溯。
