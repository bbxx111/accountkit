# 发布准备核验记录

状态：v1.1.0 tag 与 GitHub Release 已发布，本地及最终候选 CI、两个独立远端 Go 消费验证通过。发布补记已合入 develop/main，本变更已归档；归档记录的集成单独留证。本轮未部署产品。准备阶段与后续发布进展分别记录如下。

## 已确认基线

- 当前已发布Go版本v1.0.0；tag对象 `f497dec1dd487f18cb52e5ee3df85f438bde1831`，解析提交 `e1b93da7d050bce6e94766412a81f5c11d994b1f`，与远端一致。
- 当前准备起点为develop `62aaa82010f6ba69ec32daa60f898a3a2db73a85`；发布准备分支 `chore/prepare-next-release`。
- 该起点的[Go CI](https://github.com/bbxx111/accountkit/actions/runs/43661311566)与[SDK CI](https://github.com/bbxx111/accountkit/actions/runs/43661311567)全部通过。已查询确认check-generated、Verify package and recovery、Verify accountsvc with real dependencies and process signals均成功；SDK单元/真实服务/嵌入策略/Git消费以及Android/iOS检查成功。
- 以上仅为起点证据。发布说明、版本路径或其他文件变更后产生新候选，最终main候选须重新取得精确SHA对应的线上门禁。
- 从v1.0.0到起点，migrations、go.mod、go.sum无差异；不需要生产数据库升级，冻结0001保持。
- 远端只有Go v1.0.0及两个SDK的v1.0.0 tag，无远端v0 tag。本地历史v0引用不作为远端发布状态依据，未进行删除。
- main当前远端 `e6408f6a7909f3de7931f73c8c1b8573a59001cb`；仅计算的merge-tree预检无冲突，没有执行真实合并。

## 兼容性决策输入

- 独立公开源码审计确认：新增user.ResolveIdentityInput、user.ResolvedIdentity与Service.ResolveIdentity；已有公开Go符号未见删除或签名改变。消费者Authorization解析收紧，包含新增16KiB边界，不能将所有被拒绝的旧输入笼统称作语法非法。源码兼容不等于所有运行契约兼容。
- 相对正式v1.0.0，accountsvc移除旧逐接口客户端配置，新增统一注册权限，服务Bearer强制机器sub绑定与scope交集，构成服务接入契约变化。
- azp profile改名default发生在本轮未发布开发过程中，不能列作v1.0.0已发布配置的迁移项。
- 两SDK已有独立v1.0.0版本；相对其发布提交，包内仅README变动，未见运行时代码变化，不自动升SDK版本。

## 版本决定前完成的准备

- 两份独立只读审计已完成，原始报告为 `.test-output/next-release/public-api-audit.md` 与 `service-contract-audit.md`。
- 兼容指南明确区分正式v1.0.0与下一候选；补齐三项服务能力、Basic=P/Bearer=P∩scope、机器主体、profile、认证头边界、管理员依赖故障与身份解析的升级摘要。
- 服务手册纠正默认三项能力及第三realm说明，明确双方式示例只授两项权限而非完整权限目录；不改变运行代码。
- accountsvc上线清单补齐三项内部入口、主体/scope配置及独立OIDC可用性验证；身份解析补入候选能力对照。独立文案复核通过，按反馈明确16KiB仅对消费者入口属于新增边界（管理员和服务v1.0.0已有上限）。
- 版本号未定前不改README/OpenAPI的正式版本注记，不生成版本号占位的正式Release说明，不把基线CI标为最终候选通过。

该阶段文档检查：OpenSpec全库strict 46/46通过（含本发布准备草案，不表示当时设计/任务已完成）；OpenAPI源码检查通过；git diff --check通过。该阶段未改Go/SQL/依赖，没有为文档修正重复执行运行测试；下方另记版本确认后的发布准备验证。

## 已确认版本决策

用户选择保留v1。按新增公开能力准备v1.1.0候选，保留module/import及HTTP路由，不另建服务版本轴。accountsvc旧配置与Bearer授权要求仍是明确的破坏性接入变化，发布说明必须列明，不能因保留v1而声称所有运行契约兼容；这不构成未来任意破坏v1承诺的授权。

## 发布边界

真实提供方、产品网关、SMTP实际送达与目标环境恢复属于产品上线验收，不能由本地或CI夹具替代；库发布与产品部署按既有release-checklist分别执行。当前工作只进行发布准备，tag/Release、main集成和推送需按用户后续明确授权执行。

## v1.1.0 候选文档与元数据

- 已完成候选发布说明、迁移/回退说明，以及 README、功能、Go 集成、OpenAPI、服务和 SDK 指南。三份 OpenAPI 仅修改 info 元数据，路径、DTO、安全定义保持；模块路径、Go 依赖、生产迁移和两包 SDK 清单版本均未变化。
- SDK 1.0.0 对 v1.0.0 的 verified 行保留，新增 v1.1.0 pending 行。两包 `check-sdk-release --metadata-only` 通过仅证明元数据有效，不表示新候选服务兼容完成，也不替代正式 SDK 发布门禁。
- 独立任务审查发现并已修复 invalid_token 挑战说明：单个可识别 Bearer 的语法/长度拒绝也可能附带此属性，不要求已调用 token verifier；复审通过。任务 2 首次文档替换未命中，父任务检查实际 diff 后要求补齐，最终逐文件确认；未接受初次完成声明。
- `node scripts/check-openapi.cjs` 通过（24 个消费者、11 个管理操作）；`check-sdk-coverage.mjs` 覆盖 36 个操作（含 1 个服务操作）；覆盖检查器测试 13/13 通过。`openspec validate --all --strict` 46/46 通过；`git diff --check` 通过。
- 文档各本地链接与相关锚点已核对；预定 v1.1.0 Release 链接仅为候选说明，没有据此声称远端对象已存在。原始任务报告在 `.test-output/next-release/`。

## 本地发布准备快照

验证起点为准备分支提交 `e82f0038d76b57e741cbe0d23f7f4b403eca2282` 加本轮候选文档/元数据工作区。源码和完整 Git 历史复制到一次性 Linux 容器的原生文件系统；文件清单保存在 `.test-output/next-release/snapshot-files.sha256`，清单自身 SHA256 为 `a80d26a3bfa143f47379a158eb06faf270067440363220442dc696748cfb56e7`。验证过程中后续仅修正文档措辞与验收记录，不改变 Go、SQL、配置实现、测试或依赖。

环境为 Go 1.26.5、PostgreSQL 17.11 客户端、PG17 与 Redis7 专用实例、普通测试库和两个不同的空恢复库；`GOWORK=off`、`CGO_ENABLED=1`。验证镜像 `accountkit-verify:local` 的 ID 为 `sha256:0e30ae4ca529a017176b0fa8ac6a60f5b4d89ce9732826899e300d9b7ea0a885`。宿主 sqlc 1.31.1 `check-generated.sh` 已通过。

首次辅助快照在文件清单阶段误把日志目录符号链接当作文件，测试未开始即退出；已调整清单生成顺序并重新创建验证容器，没有放宽任何测试。

- `bash scripts/verify.sh` 已通过：完整迁移历史、`go build ./...`、`go vet ./...`、`go test -race -json -count=1 ./...` 与 1057 项必需测试门禁。race 输出有 56 个包、1274 项顶层测试通过，零失败；三个顶层 skip 的补验见下文。
- 真实 `pg_dump` / `pg_restore` 恢复 seed、verify、source-unchanged 三阶段各强制执行 `TestRecoveryFixture` 并通过，未出现 skip；证据保留 `.test-output/recovery.q2HKHX/`，包括合成数据备份、状态与三份 JSONL。
- 独立 `bash scripts/verify-accountsvc.sh` 通过：服务构建/vet、11 个包和 265 项顶层测试、128 项服务必需门禁均成功，零失败。该轮额外设置 `ACCOUNTSVC_TEST_RACE=1`，被测服务进程也启用 race；真实 SIGTERM、OIDC/SMTP 替身、PostgreSQL/Redis 与传输模式用例实际执行。原始日志为 `.test-output/accountsvc-tests.jsonl` 和 `.test-output/next-release/verify-accountsvc.log`。
- 同一 Linux 镜像编译带 race 的 accountsvc 测试二进制，以 `runuser -u nobody` 运行 `TestProfileAttributesUnreadableFile`，实际 PASS、无 skip。日志 `.test-output/next-release/nonroot.log`。辅助验证容器最终退出码 0。

原始库日志为 `.test-output/tests.jsonl` 和 `.test-output/next-release/verify.log`。普通套件的 `TestRecoveryFixture` 按设计 skip，已由恢复三阶段补验；Linux 镜像缺 Docker CLI 的 `TestConfigServiceClientComposeExamples` 已在宿主真实 Docker 环境重新运行通过（16 个子场景，日志 `compose-examples.log`）；root 能读取权限为 000 的文件，`TestProfileAttributesUnreadableFile` 已由上述非 root 运行补验。Windows 特有权限语义未验证。

最终文件哈希复核显示，验证快照之后仅服务手册/候选说明的挑战措辞、OpenAPI 指南的版本句、计划、tasks 和本验收记录变化；Go/SQL/依赖/测试源码及三份 OpenAPI JSON 一致。纯文档修正已独立复审通过，不据此重复整套运行测试。

## 最终审查与交付

- 整体审查覆盖准备分支的已提交与本轮候选差异，确认范围仅文档/元数据，无运行实现或冻结迁移变更。发现一处 P3：OpenAPI 指南遗漏旧版本标记 `identity-resolution`；已改为 v1.1.0 候选，针对性复核判定 ADDRESSED，无剩余 findings。审查同时重统计实际 JSONL、抽查补验/恢复日志与快照哈希，确认本记录相符。原始报告 `.test-output/next-release/final-review.md`。
- 任务专用 PostgreSQL、Redis、验证容器及 `accountkit-release-1115` 网络已清理；仅保留 ignored 测试日志、合成恢复备份与既有缓存，未操作产品资源。
- 准备工件在 `chore/prepare-next-release` 本地提交；未推送、未合并 develop/main、未创建 tag/Release，OpenSpec 变更未归档。四项准备任务完成；后续最终候选 CI、发布与远端消费按 tasks 的 Workflow follow-up 执行。

本轮未执行最终 main SHA 的在线 CI、正式 tag 创建/消费或 SDK 平台全套验证。SDK v1.1.0 兼容保持 pending；发布前仍须取得最终候选的 Go/SDK CI，发布后执行两个独立远端 Go 消费核验。真实提供方与产品部署仍是另行验收范围。

## 2026-11-15 发布执行进展

以上“未推送/未发布”等描述保留准备阶段事实。本节记录用户随后授权继续发布的实际进展。

- 准备分支经独立 merge commit 合入并推送 develop `0c134d0c2b0a1492c640e7da6442c15fe695d225`，再合入并推送 main `6eef5e349ab17582a5c25b8fa5236372b988a618`。远端 SHA 与本地一致，两分支文件树相同。
- 最终候选 `6eef5e349ab17582a5c25b8fa5236372b988a618` 的 [Go CI](https://github.com/bbxx111/accountkit/actions/runs/43682397743) 和 [SDK CI](https://github.com/bbxx111/accountkit/actions/runs/43682397757) 已全部成功。逐项核对生成检查、库/恢复、accountsvc 真实依赖/进程信号，以及 SDK 单元/真实服务/嵌入策略/Git 消费、Android 和 iOS；所有 job 与实际执行的步骤均 success，head_sha 精确匹配。iOS 的 Java 安装、Android 的 Xcode 选择按平台条件 skipped，不属于缺少必需验证。
- 在上述门禁通过、远端同名 tag 不存在后，创建并推送 annotated `v1.1.0`。tag 对象 `14b53f3542f8b67a2b42eb5db55eaa8d8bebeaca`，解析提交 `6eef5e349ab17582a5c25b8fa5236372b988a618`；本地与远端两项均一致，未移动已有 tag。
- tag 推送时 GitHub CLI 未登录、浏览器连接不可用，因此先完成可独立执行的远端消费验证，并保持 Release/归档未完成状态。用户随后完成 `gh auth login`，未向代理提供或由代理提取凭据。
- CI 原始证据在 `.test-output/next-release/final-candidate-ci.json` 和 `final-candidate-jobs.json`；独立远端 Go 宿主位于 `.test-output/next-release/consumers/root` 和 `consumers/direct`，各含 `verification.json`。
- 两个独立 module 均在 Go 1.26.5 windows/amd64、`GOWORK=off`、无 replace 下执行远端 `go get github.com/bbxx111/accountkit@v1.1.0`、`go build ./...`、`go run .`，全部通过。分别编译根门面的 `EndUserHandler` 与直接 `httpapi/enduser.New` / `Router`，并对 `user.ResolveIdentityInput`、`user.ResolvedIdentity` 和 `(*user.Service).ResolveIdentity` 做公开类型/签名编译检查；未连接产品数据库。
- 两者均报告版本 v1.1.0、无 Replace，模块 Sum 为 `h1:Iix6PXFhBNkp0y4LqI6jR9wpPKSai4xdnj1QFuUj85E=`。下载的 0001 up/down SHA256 分别为 `ef2d1f229fa15b660cc7ecb4f417615b76866c19494641c96faf25bab24188b6` / `812541721471025056edd1f655d876b2f4d938fffd8f4f7ee2cfa9a97ff490d5`，与冻结值一致。
- 首次模块代理下载返回 HTTP/2 `INTERNAL_ERROR`，另一个进程等待共享模块缓存锁；停止该次等待后，按原代理与校验设置重试成功。未关闭校验、未添加本地 replace、未改 tag 或运行代码。此为下载阶段故障，不计作构建或业务测试失败。

## GitHub Release 与发布补记

- GitHub Release 创建于 `2026-11-15T08:37:37Z`：[v1.1.0](https://github.com/bbxx111/accountkit/releases/tag/v1.1.0)，非 draft、非 prerelease，targetCommitish 为固定候选 `6eef5e349ab17582a5c25b8fa5236372b988a618`。
- 首次创建命令被自动审批拒绝，原因是准备说明仍含“候选、尚未发布”。该命令未执行；修正为正式 Go 版本说明、补齐实际 CI/tag/消费证据并独立审查 PASS 后成功创建。发布说明源仍为 `docs/releases/v1.1.0.md`，没有另维护 Release 内容；tag 内的历史候选文档不修改。
- 发布补记更新 README、功能/兼容/Go/OpenAPI/服务/SDK 指南与三份 OpenAPI info 描述。SDK 清单仍为 1.0.0，兼容矩阵保留 v1.0.0 verified 行，并依据最终候选完整 SDK CI 将 v1.1.0 行改为 verified；没有发布新 SDK tag。
- 本轮只修改文档和元数据。OpenAPI 24/11 操作、SDK 36 操作覆盖以及两包完整 `check-sdk-release` 检查通过；运行代码、SQL、依赖、SDK 清单和 HTTP 定义未变，未重复完整运行套件。后续记录补记集成和归档校验。

## Publication Integration

发布补记 `a8bd53b9f9a7ce24cdd693e4b78f34066f854377`（含此前证据提交 `3900728`）经 [PR #29](https://github.com/bbxx111/accountkit/pull/29) 的独立 merge commit 合入 develop `0fbc977e343d7ad31db3a7a93b69625c0ce636bf`；再以独立 merge commit 合入 main `e2eb83fdb23bb50c16dcc1e04de9cc8fa3b22712`，均已推送，文件树一致。发布补记只修改文档/元数据，独立审查通过；OpenSpec 46/46、OpenAPI/SDK 元数据与 222 个本地/tag 文件链接核对通过。正式版本 CI 指固定 tag 对应的 `6eef5e3`；补记分支随后触发的 CI 不是本版本发布门禁的替代证据。

## Archive Verification

- 2026-11-15 在 `docs/archive-v1-1-0-release` 分支使用已安装的 OpenSpec CLI 归档。CLI root 为本仓库；proposal/design/tasks 均 done，specs 声明 skipped、existingOutputPaths 为空，任务 4/4 完成，无目标目录冲突。归档 advisory 仅包含中文工件要求，没有额外操作指引。
- 执行 `openspec archive prepare-next-release --yes --skip-specs --json`，保留默认校验；目标为 `openspec/changes/archive/2026-11-15-prepare-next-release`，未修改主规格。移动后立即对照原始清单确认五份文件（含 `.openspec.yaml`）逐字节一致，再补记本节及任务完成状态。
- 同步修复 SDK 兼容矩阵、客户端指南、发布检查表、正式发布说明及计划中的五处引用文件；未改写历史 tag。原始归档输入哈希清单保存在 `.test-output/next-release/archive-inputs.json`。
- 归档后 `openspec validate --all --strict` 为 45/45 主规格通过，`openspec list --json` 返回空活动列表；8 份变动 Markdown 的 43 个本地/tag 文件链接检查通过，旧活动引用残留为零。OpenAPI 契约除既有 info 描述外保持，24/11 操作、SDK 36 操作覆盖、两包完整发布元数据及 `git diff --check` 均通过。只移动工件和维护文档引用，未重复运行库/数据库/race 套件。
