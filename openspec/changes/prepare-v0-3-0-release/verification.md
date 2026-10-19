# v0.3.0 发布准备验收

日期：2026-10-19。状态：发布准备完成，已获继续发布授权，正式发布进行中。用户确认按 v0.3.0 准备；[发布说明](../../../docs/releases/v0.3.0.md)、[发布门禁](../../../docs/release-checklist.md)。

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

- [工具环境](../../../.test-output/prepare-v0.3-release/environment.txt)、[受测提交](../../../.test-output/prepare-v0.3-release/verified-commit.txt)、[生成检查](../../../.test-output/prepare-v0.3-release/generated.log)、[OpenSpec 检查](../../../.test-output/prepare-v0.3-release/openspec.log)。
- [库完整日志](../../../.test-output/prepare-v0.3-release/library.log)、[库事件汇总](../../../.test-output/prepare-v0.3-release/library-summary.json)、[服务完整日志](../../../.test-output/prepare-v0.3-release/accountsvc.log)、[服务事件汇总](../../../.test-output/prepare-v0.3-release/accountsvc-summary.json)。
- [全部测试事件及合成恢复材料](../../../.test-output/prepare-v0.3-release/raw/)、[在线 CI](../../../.test-output/prepare-v0.3-release/base-ci-latest.json)、[CI 步骤](../../../.test-output/prepare-v0.3-release/base-ci-jobs.json)、[独立审查](../../../.test-output/prepare-v0.3-release/review.md)。

完整日志、JSON 事件、恢复材料及汇总已复制到主工作区的 ignored 目录。清理前逐项核验名称、任务标签、runner 退出 0 和证据文件存在，仅删除本次三个容器、匿名卷及隔离网络；既有 postgres/redis 保持运行。清理记录见[资源清单](../../../.test-output/prepare-v0.3-release/cleaned-resources.json)。

## 未完成与边界

准备阶段结束时，材料尚未提交、合并或推送，尚未确定 main 最终发布提交，也未核验其 CI。正式发布前须包含全部发布文件并重新核对候选门禁；准备阶段未创建 v0.3.0 tag/Release，未验证 @v0.3.0 固定版本远端消费。未发布镜像/二进制或部署产品，本变更未归档。

真实服务商、产品网关、监控平台及目标环境恢复由集成方另行验收；微信/Apple 任务 8.5 仍未验证。旧数据库需要保留数据时仍须单独制定和验证迁移方案，本轮恢复演练不等于提供旧库原地升级能力。

## 正式发布阶段（2026-10-19）

用户在准备结果及下一步“提交、合并至 main，核对最终候选 CI，再创建 tag/Release”说明后明确要求继续。本次据此提交准备文档，以独立 merge commit 集成并推送，最终 main 候选 CI 成功后才发布 v0.3.0，随后验证远端固定版本消费。实际 SHA、CI、tag/Release 与消费结果继续补记；不改写历史 tag，不自动归档或部署产品。
