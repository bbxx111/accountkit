# v0.3.0 发布准备验收

日期：2026-10-19。状态：v0.3.0 已发布，固定版本远端消费通过，已于2026-10-20归档。用户确认版本并授权继续发布；[发布说明](../../../../docs/releases/v0.3.0.md)、[发布门禁](../../../../docs/release-checklist.md)。

## 范围与快照

任务分支 chore/prepare-next-release，运行时代码基线 c3bdc8d788ce6a088f3a0b4da866280916c7ae9b；原始证据位于 .test-output/prepare-v0.3-release/。本次仅维护发布文档和工件，不修改运行时代码、SQL、生成代码、源快照或历史 tag。

## 实际验证

- 远端 develop 在准备开始时为上述基线，main 为 4368eb70c579160f7802332354e7fefede55a34e；当次只读查询未发现 v0.3.0 tag。
- 使用 OpenSpec 1.11.0、sqlc 1.31.1、Go 1.26.5 linux/amd64、PostgreSQL 17/Redis 7 和 PostgreSQL 客户端 17.11。完整 Git 历史及 v0.1.0/v0.2.0 标签复制到隔离容器，checkout 到上述精确快照，未用浅克隆或跳过历史检查。
- 专用资源标签为 accountkit.task=release-v030-20261019，普通测试库、恢复源库、恢复目标库相互独立。恢复脚本核对源/目标实际不同且均为空；未连接既有业务容器。环境探测时登录 shell 未继承 Go PATH，正式验证脚本显式提供 /usr/local/go/bin 后执行，未修改门禁以绕过该环境问题。

| 检查 | 本次结果 |
|---|---|
| bash scripts/check-generated.sh | Windows Git Bash / sqlc 1.31.1，退出 0，生成结果无差异 |
| bash scripts/verify.sh | Linux，退出 0；包含迁移历史、独立 build、vet、全库 race、必需测试门禁及真实恢复 |
| 全库 race 与必需测试 | 41 个测试包、2047 个通过事件、0 失败；630/630 项库必需测试满足；测试级只有 TestRecoveryFixture 按常规套件规则 skip |
| 恢复演练 | pg_dump/pg_restore 成功，seed、verify、source-unchanged 三阶段各实际通过，无 skip；认证数据、流程、密文解密、access/refresh 和源库未改变检查通过 |
| bash scripts/verify-accountsvc.sh | Linux，退出 0；5 个测试包、417 个通过事件、0 失败、0 测试级 skip；23/23 项服务必需测试满足，保留原 race、实际子进程与 5 分钟时限 |
| OpenSpec | validate --all --strict：23 项通过、0 失败；本次为 skip_specs 的文档维护，不修改主规格 |
| 文档与差异 | 本地链接目标和 git diff --check 通过；运行时代码、测试、依赖、迁移和验证脚本相对受测 c3bdc8d 无差异，隔离副本受测后工作区干净 |

测试计数包含子测试事件。常规套件的 TestRecoveryFixture skip 已由恢复脚本三阶段补充实际执行；不能将普通套件 skip 当作恢复未测，也不能把准备快照通过解释为正式 main 候选已验收。

基线[在线 CI #39198167304](https://github.com/bbxx111/accountkit/actions/runs/39198167304)初次读取为 in_progress，随后已完成且 conclusion=success。已核对全部步骤成功，包括生成一致性、完整库与恢复、严格服务门禁，head_sha 与 c3bdc8d 完全一致。该 CI 证明准备前已推送的代码基线，不包含本轮尚未提交的发布文档。

## 独立审查

review_v030_release 对照 v0.2.0..c3bdc8d 最终变更、公开源码、主规格和兼容指南审查，未发现需要修复的实质问题。确认旧库拒绝、登录返回、审计 API/游标、发送器别名、指标配置及集成方责任准确，准备、最终候选与正式发布状态明确区分。审查者只读核验，未将源码审查冒充测试执行。

## 原始证据与资源清理

- [工具环境](../../../../.test-output/prepare-v0.3-release/environment.txt)、[受测提交](../../../../.test-output/prepare-v0.3-release/verified-commit.txt)、[生成检查](../../../../.test-output/prepare-v0.3-release/generated.log)、[OpenSpec 检查](../../../../.test-output/prepare-v0.3-release/openspec.log)。
- [库完整日志](../../../../.test-output/prepare-v0.3-release/library.log)、[库事件汇总](../../../../.test-output/prepare-v0.3-release/library-summary.json)、[服务完整日志](../../../../.test-output/prepare-v0.3-release/accountsvc.log)、[服务事件汇总](../../../../.test-output/prepare-v0.3-release/accountsvc-summary.json)。
- [全部测试事件及合成恢复材料](../../../../.test-output/prepare-v0.3-release/raw/)、[在线 CI](../../../../.test-output/prepare-v0.3-release/base-ci-latest.json)、[CI 步骤](../../../../.test-output/prepare-v0.3-release/base-ci-jobs.json)、[独立审查](../../../../.test-output/prepare-v0.3-release/review.md)。

完整日志、JSON 事件、恢复材料及汇总已复制到主工作区的 ignored 目录。清理前逐项核验名称、任务标签、runner 退出 0 和证据文件存在，仅删除本次三个容器、匿名卷及隔离网络；既有 postgres/redis 保持运行。清理记录见[资源清单](../../../../.test-output/prepare-v0.3-release/cleaned-resources.json)。

## 未完成与边界

准备阶段结束时，材料尚未提交、合并或推送，尚未确定 main 最终发布提交，也未核验其 CI。正式发布前须包含全部发布文件并重新核对候选门禁；准备阶段未创建 v0.3.0 tag/Release，未验证 @v0.3.0 固定版本远端消费。未发布镜像/二进制或部署产品，本变更未归档。

真实服务商、产品网关、监控平台及目标环境恢复由集成方另行验收；微信/Apple 任务 8.5 仍未验证。旧数据库需要保留数据时仍须单独制定和验证迁移方案，本轮恢复演练不等于提供旧库原地升级能力。

## 正式发布阶段（2026-10-19）

用户在准备结果及下一步“提交、合并至 main，核对最终候选 CI，再创建 tag/Release”说明后明确要求继续。本次据此提交准备文档，以独立 merge commit 集成并推送，最终 main 候选 CI 成功后才发布 v0.3.0，随后验证远端固定版本消费。实际 SHA、CI、tag/Release 与消费结果继续补记；不改写历史 tag，不自动归档或部署产品。

准备提交为 290fb42，首轮 develop/main 合并分别为 45ef80e / e898864，均已推送。发布前交叉检查长期文档时发现 docs/features.md 仍保留 accountsvc 尚无指标端点的旧描述，已按实际可选 GET /metrics 配置修正，并在 README 补充 v0.3.0 发布入口及初始结构有限授权的历史边界。该修正仅涉及文档，运行时保持；最终发布候选须包含此次修正并核对其自己的 CI，不以首轮候选结果替代。

### 最终发布与消费结果

| 项目 | 实际结果 |
|---|---|
| 最终文档修正 | 7fe6290，独立只读复审通过；76 个本地链接目标及 23 项 OpenSpec 严格校验通过 |
| 最终 develop 集成 | d07bae6，以独立 merge commit 合入 |
| 最终 main 发布 SHA | ba2d0412764c659beabdbd0d3f0f0f9bb9007657，以独立 merge commit 合入；文件树与最终文档任务分支一致，运行时仍与本地完整验证基线相同 |
| 最终候选 CI | [verify #39203841497](https://github.com/bbxx111/accountkit/actions/runs/39203841497)，head_sha 精确匹配，全部步骤 success，含生成、完整库/恢复与严格服务门禁 |
| 固定版本 | annotated v0.3.0，远端 tag 对象 b1764c61fbffc4d4e4b9f1fc8ef73e3bdb427f70，解引用为上述 main 发布 SHA |
| 标签后迁移历史 | GOWORK=off go run ./internal/migrationcheck 退出 0；新正式 tag 纳入检查，v0.1.0/v0.2.0 对象与解引用提交前后逐项一致 |
| GitHub Release | [v0.3.0](https://github.com/bbxx111/accountkit/releases/tag/v0.3.0)，已发布，非 draft、非 prerelease；复用本版本说明，正文相对链接转换为固定提交链接 |
| 根门面独立宿主 | 公共远端 go get @v0.3.0、go mod tidy、GOWORK=off go build 均退出 0；编译身份策略、token/pending、补全调用、根 HTTP 门面、外部审计 Reader/Recorder 与两个指标适配器；本地执行审计映射器和指标构造/关闭成功 |
| 直接 enduser 独立宿主 | 远端 go get、tidy、build 均退出 0；Service、IdentityReplacer、SignInCompletionService 和 New/Router 契约编译，空入口执行退出 0 |
| 消费来源 | 两个 go list -m -json 均 Version=v0.3.0、无 Replace，GOWORK=off；使用本轮独立模块缓存从公共代理/远端获取，不使用本地源码替换 |

根宿主涉及认证和数据库的方法只做编译契约核验；运行检查仅构造本地审计/指标组件，不表示真实服务商或产品环境联调通过。首轮候选 CI #39203421635 亦成功，但发布依据始终为包含文档修正的最终 #39203841497。

原始材料：[候选元数据](../../../../.test-output/prepare-v0.3-release/candidate.json)、[最终 CI](../../../../.test-output/prepare-v0.3-release/main-ci-final.json)、[CI 步骤](../../../../.test-output/prepare-v0.3-release/main-ci-final-jobs.json)、[远端 tag](../../../../.test-output/prepare-v0.3-release/remote-tag.txt)、[标签后迁移检查](../../../../.test-output/prepare-v0.3-release/tag-migrationcheck.log)、[Release 创建结果](../../../../.test-output/prepare-v0.3-release/release-result.json)、[独立宿主源码和日志](../../../../.test-output/prepare-v0.3-release/hosts/)、[模块下载来源](../../../../.test-output/prepare-v0.3-release/download.json)。材料保持 ignored，不提交缓存、二进制或合成数据。

### 发布记录维护

在 docs/record-v0.3.0-publication 分支补记上述事实，并更新 README、功能/集成/观测指南及 OpenAPI info 版本状态。OpenAPI 的路径、请求/响应结构保持，现有契约检查仍为消费者 21、管理员 11 个操作通过。AGENTS 与迁移手册明确当前 0001 已随 v0.3.0 发布，之前的有限授权不再适用于修改它；未改任何 SQL、固定哈希、源快照或 tag。

本轮记录检查：175 个本地链接目标存在，git diff --check 通过，OpenSpec 严格校验 23 项通过；逐份比较 OpenAPI JSON，除 info.version/description 外全部内容一致。发布后的记录维护仅涉及文档，未重复运行全库测试，最终发布候选 CI 和远端消费证据如上。

发布记录作为后续文档提交合入 develop/main；不移动已验证的 v0.3.0 tag。Release 正文同步最终说明并使用记录提交的固定链接，产品部署与真实环境验收继续由集成方负责。本变更 8/8 任务完成，归档仍待单独明确请求。

## 归档（2026-10-20）

用户明确要求归档prepare-v0-3-0-release。归档前工件proposal/design/tasks均done，specs按skip_specs=true跳过，8/8任务完成；没有delta规格需要同步。

通过OpenSpec archive --yes --skip-specs移入openspec/changes/archive/2026-10-20-prepare-v0-3-0-release/，保留.openspec.yaml与本记录。修复README、发布说明、发布检查表指向本变更的链接，以及本记录指向长期文档和ignored原始材料的相对路径。此前“未归档/待明确请求”的段落保留为历史阶段记录，本节为最新归档状态。

本轮仅维护归档与文档引用，不改变运行时代码、SQL、主规格、正式tag或远端Release，不重复发布、不运行部署或重新声明历史CI状态。归档在docs/archive-v0-3-0-release分支执行，不直接向develop提交，不推送远端。

归档检查：openspec validate --all --strict共24项通过、0失败，openspec list --json返回空活动清单；本轮涉及文档的本地文件链接检查、8/8任务及归档标记检查、git diff --check均通过。纯文档变更未重跑Go/数据库测试。
